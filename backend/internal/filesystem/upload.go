package filesystem

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

type UploadSession struct {
	ID             string    `json:"id"`
	Path           string    `json:"path"`
	Name           string    `json:"name"`
	Size           int64     `json:"size"`
	Offset         int64     `json:"offset"`
	UpdatedAt      time.Time `json:"updated_at"`
	OperationID    string    `json:"operation_id"`
	SourceModified int64     `json:"source_modified,omitempty"`
}

type uploadRecord struct {
	UploadSession
	TempName string `json:"temp_name"`
}

type UploadManager struct {
	mu        sync.Mutex
	active    map[string]context.CancelFunc
	resolver  *Resolver
	baseDir   string
	nextPrune time.Time
}

func NewUploadManager(resolver *Resolver, baseDir string) *UploadManager {
	return &UploadManager{resolver: resolver, baseDir: baseDir, active: make(map[string]context.CancelFunc)}
}

func (m *UploadManager) Init(ctx context.Context, virtualDir, filename string, size, sourceModified int64) (UploadSession, error) {
	if err := ctx.Err(); err != nil {
		return UploadSession{}, err
	}
	filename = filepath.Base(filename)
	if filename == "" || filename == "." || filename == string(filepath.Separator) {
		return UploadSession{}, ErrInvalidPath
	}
	if size < 0 {
		return UploadSession{}, errors.New("upload size cannot be negative")
	}
	virtualPath := VirtualJoin(virtualDir, filename)
	_, destination, err := m.resolver.ResolveForCreate(virtualPath)
	if err != nil {
		return UploadSession{}, err
	}
	if _, err := os.Lstat(destination); err == nil {
		return UploadSession{}, ErrConflict
	} else if !errors.Is(err, fs.ErrNotExist) {
		return UploadSession{}, err
	}
	if err := m.ensureDir(); err != nil {
		return UploadSession{}, err
	}
	if err := m.pruneIfDue(time.Now()); err != nil {
		return UploadSession{}, err
	}

	id := uploadID(virtualPath, size, sourceModified)
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.active[id] != nil {
		return UploadSession{}, ErrConflict
	}
	if rec, err := m.readRecordLocked(id); err == nil {
		if rec.Path != virtualPath || rec.Size != size || rec.SourceModified != sourceModified {
			return UploadSession{}, ErrConflict
		}
		st, statErr := os.Stat(filepath.Join(m.baseDir, rec.TempName))
		if statErr == nil {
			if st.Size() > rec.Size {
				return UploadSession{}, errors.New("staged upload is larger than declared size")
			}
			rec.Offset = st.Size()
			rec.UpdatedAt = time.Now().UTC()
			_ = m.writeRecordLocked(rec)
			return rec.UploadSession, nil
		}
	}

	tmp, err := os.CreateTemp(m.baseDir, id+"-*.part")
	if err != nil {
		return UploadSession{}, err
	}
	name := filepath.Base(tmp.Name())
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmp.Name())
		return UploadSession{}, err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmp.Name())
		return UploadSession{}, err
	}
	rec := uploadRecord{UploadSession: UploadSession{
		ID:             id,
		Path:           virtualPath,
		Name:           filename,
		Size:           size,
		Offset:         0,
		UpdatedAt:      time.Now().UTC(),
		OperationID:    "upload-" + id,
		SourceModified: sourceModified,
	}, TempName: name}
	if err := m.writeRecordLocked(rec); err != nil {
		_ = os.Remove(filepath.Join(m.baseDir, name))
		return UploadSession{}, err
	}
	return rec.UploadSession, nil
}

func (m *UploadManager) Append(ctx context.Context, id string, offset int64, src io.Reader, length int64) (UploadSession, bool, error) {
	if err := ctx.Err(); err != nil {
		return UploadSession{}, false, err
	}
	if length < 0 {
		return UploadSession{}, false, errors.New("content length is required")
	}
	var cancel context.CancelFunc
	var stopClose func() bool
	var bodyClosed chan struct{}
	m.mu.Lock()
	defer func() {
		m.mu.Unlock()
		if stopClose != nil {
			stopped := stopClose()
			cancel()
			if !stopped {
				// Close may access the HTTP response controller. Finish it
				// before the handler returns, without blocking other uploads.
				<-bodyClosed
			}
		}
	}()
	if m.active[id] != nil {
		return UploadSession{}, false, ErrConflict
	}
	rec, err := m.readRecordLocked(id)
	if err != nil {
		return UploadSession{}, false, err
	}
	tempPath := filepath.Join(m.baseDir, rec.TempName)
	st, err := os.Stat(tempPath)
	if err != nil {
		return UploadSession{}, false, err
	}
	current := st.Size()
	if offset != current {
		rec.Offset = current
		return rec.UploadSession, false, &UploadOffsetError{Expected: current}
	}
	if current+length > rec.Size {
		return UploadSession{}, false, errors.New("chunk exceeds declared upload size")
	}
	f, err := os.OpenFile(tempPath, os.O_WRONLY, 0)
	if err != nil {
		return UploadSession{}, false, err
	}
	if _, err := f.Seek(current, io.SeekStart); err != nil {
		_ = f.Close()
		return UploadSession{}, false, err
	}
	ctx, cancel = context.WithCancel(ctx)
	m.active[id] = cancel
	defer delete(m.active, id)
	bodyClosed = make(chan struct{})
	stopClose = context.AfterFunc(ctx, func() {
		defer close(bodyClosed)
		if closer, ok := src.(io.Closer); ok {
			_ = closer.Close()
		}
	})
	// Keep one writer per upload, but do not hold the manager lock while
	// reading a network body. Cancel can remove staging and cancel this read.
	m.mu.Unlock()
	written, err := func() (int64, error) {
		defer m.mu.Lock()
		defer f.Close()
		written, err := io.CopyN(f, &uploadContextReader{ctx: ctx, reader: src}, length)
		if err != nil {
			return written, err
		}
		if err := f.Sync(); err != nil {
			return written, err
		}
		return written, f.Close()
	}()
	if ctx.Err() != nil {
		return UploadSession{}, false, ctx.Err()
	}
	if err != nil {
		return UploadSession{}, false, err
	}
	rec.Offset = current + written
	rec.UpdatedAt = time.Now().UTC()
	complete := rec.Offset == rec.Size
	if complete {
		_, destination, err := m.resolver.ResolveForCreate(rec.Path)
		if err != nil {
			return UploadSession{}, false, err
		}
		if err := os.Chmod(tempPath, 0o644); err != nil {
			return UploadSession{}, false, err
		}
		if err := renameNoReplace(tempPath, destination); err != nil {
			if errors.Is(err, ErrConflict) {
				return UploadSession{}, false, ErrConflict
			}
			if !errors.Is(err, syscall.EXDEV) && !errors.Is(err, errRenameNoReplaceUnsupported) {
				return UploadSession{}, false, err
			}
			// The active entry still excludes another writer for this upload.
			// Publication can take minutes across disks; cancellation and other
			// uploads must be able to acquire the manager lock during that IO.
			m.mu.Unlock()
			copyErr := copyFileAtomic(ctx, tempPath, destination, 0o644, time.Now(), nil)
			m.mu.Lock()
			if err := ctx.Err(); err != nil {
				return UploadSession{}, false, err
			}
			if copyErr != nil {
				return UploadSession{}, false, copyErr
			}
			_ = os.Remove(tempPath)
		}
		_ = os.Remove(m.recordPath(id))
	} else if err := m.writeRecordLocked(rec); err != nil {
		return UploadSession{}, false, err
	}
	return rec.UploadSession, complete, nil
}

func (m *UploadManager) Cancel(id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	rec, err := m.readRecordLocked(id)
	if err != nil {
		return err
	}
	if cancel := m.active[id]; cancel != nil {
		cancel()
	}
	_ = os.Remove(filepath.Join(m.baseDir, rec.TempName))
	return os.Remove(m.recordPath(id))
}

func (m *UploadManager) Active(id string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.active[id] != nil
}

type uploadContextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r *uploadContextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(p)
}

func (m *UploadManager) ensureDir() error {
	return os.MkdirAll(m.baseDir, 0o700)
}

func (m *UploadManager) recordPath(id string) string {
	return filepath.Join(m.baseDir, id+".json")
}

func (m *UploadManager) readRecordLocked(id string) (uploadRecord, error) {
	if id == "" || strings.ContainsAny(id, `/\\`) {
		return uploadRecord{}, ErrInvalidPath
	}
	data, err := os.ReadFile(m.recordPath(id))
	if err != nil {
		return uploadRecord{}, err
	}
	var rec uploadRecord
	if err := json.Unmarshal(data, &rec); err != nil {
		return uploadRecord{}, err
	}
	if rec.ID != id || filepath.Base(rec.TempName) != rec.TempName || !strings.HasPrefix(rec.TempName, id+"-") || !strings.HasSuffix(rec.TempName, ".part") {
		return uploadRecord{}, ErrInvalidPath
	}
	return rec, nil
}

func (m *UploadManager) writeRecordLocked(rec uploadRecord) error {
	data, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(m.baseDir, ".upload-meta-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer os.Remove(name)
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(name, m.recordPath(rec.ID))
}

type UploadOffsetError struct {
	Expected int64
}

func (e *UploadOffsetError) Error() string {
	return fmt.Sprintf("upload offset mismatch; expected %d", e.Expected)
}

func uploadID(path string, size, sourceModified int64) string {
	sum := sha256.Sum256([]byte(path + "\x00" + strconv.FormatInt(size, 10) + "\x00" + strconv.FormatInt(sourceModified, 10)))
	return hex.EncodeToString(sum[:16])
}
