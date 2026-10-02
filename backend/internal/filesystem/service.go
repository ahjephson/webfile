package filesystem

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
	"unicode/utf8"

	"webfile/internal/thumbnailcache"
)

var (
	ErrTooLarge     = errors.New("file is too large for the editor")
	ErrConflict     = errors.New("file changed or already exists")
	ErrNotDirectory = errors.New("path is not a directory")
)

type Entry struct {
	Name       string    `json:"name"`
	Path       string    `json:"path"`
	IsDir      bool      `json:"is_dir"`
	Size       int64     `json:"size"`
	SizeStatus string    `json:"size_status,omitempty"`
	Modified   time.Time `json:"modified"`
	Mode       string    `json:"mode"`
	UID        uint32    `json:"uid,omitempty"`
	GID        uint32    `json:"gid,omitempty"`
}

type FileContent struct {
	Path     string    `json:"path"`
	Content  string    `json:"content"`
	Modified time.Time `json:"modified"`
	ETag     string    `json:"etag"`
}

type FileType struct {
	Path        string `json:"path"`
	ContentType string `json:"content_type"`
}

type Service struct {
	resolver          *Resolver
	editorMaxBytes    int64
	thumbnailCache    thumbnailcache.Cache
	thumbnailSlots    chan struct{}
	thumbnailFlightMu sync.Mutex
	thumbnailFlights  map[string]*thumbnailFlight
	videoThumbnails   videoThumbnailGenerator
	videoQueue        *videoThumbnailQueue
	folderSizeMu      sync.Mutex
	folderSizeCache   map[string]folderSizeCacheEntry
	folderSizePending map[string]bool
	folderSizeSlots   chan struct{}
}

func NewService(resolver *Resolver, editorMaxBytes int64) *Service {
	return NewServiceWithThumbnailOptions(resolver, editorMaxBytes, ThumbnailOptions{})
}

func NewServiceWithThumbnailOptions(resolver *Resolver, editorMaxBytes int64, options ThumbnailOptions) *Service {
	options.applyDefaults()
	service := &Service{
		resolver:          resolver,
		editorMaxBytes:    editorMaxBytes,
		thumbnailCache:    options.Cache,
		thumbnailSlots:    make(chan struct{}, options.ImageWorkers),
		thumbnailFlights:  make(map[string]*thumbnailFlight),
		videoThumbnails:   videoThumbnailGenerator{ffmpeg: "ffmpeg", ffprobe: "ffprobe", threads: options.VideoFFmpegThreads},
		folderSizeCache:   make(map[string]folderSizeCacheEntry),
		folderSizePending: make(map[string]bool),
		folderSizeSlots:   make(chan struct{}, 2),
	}
	service.videoQueue = newVideoThumbnailQueue(service, options)
	return service
}

func (s *Service) Close() { s.videoQueue.close() }

func (s *Service) Resolver() *Resolver { return s.resolver }

func (s *Service) List(ctx context.Context, virtualPath string) ([]Entry, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	virtualPath = CleanVirtual(virtualPath)
	if virtualPath == "/" {
		roots := s.resolver.Roots()
		entries := make([]Entry, 0, len(roots))
		for _, root := range roots {
			st, err := os.Stat(root.Path)
			if err != nil {
				continue
			}
			entry := toEntry("/", root.Name, st)
			s.applyCachedFolderSize(&entry, root.Path)
			entries = append(entries, entry)
		}
		return entries, nil
	}

	_, abs, err := s.resolver.ResolveExisting(virtualPath)
	if err != nil {
		return nil, err
	}
	st, err := os.Stat(abs)
	if err != nil {
		return nil, err
	}
	if !st.IsDir() {
		return nil, ErrNotDirectory
	}

	dirEntries, err := os.ReadDir(abs)
	if err != nil {
		return nil, err
	}
	out := make([]Entry, 0, len(dirEntries))
	collationKeys := make(map[string]string, len(dirEntries))
	for _, de := range dirEntries {
		if strings.HasPrefix(de.Name(), ".webfile-extract-") {
			continue
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		info, err := de.Info()
		if err != nil {
			continue
		}
		entry := toEntry(virtualPath, de.Name(), info)
		if entry.IsDir {
			s.applyCachedFolderSize(&entry, filepath.Join(abs, de.Name()))
		}
		out = append(out, entry)
		collationKeys[entry.Name] = hostCollationKey(entry.Name)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].IsDir != out[j].IsDir {
			return out[i].IsDir
		}
		left := collationKeys[out[i].Name]
		right := collationKeys[out[j].Name]
		if left == right {
			return out[i].Name < out[j].Name
		}
		return left < right
	})
	return out, nil
}

func (s *Service) ReadText(ctx context.Context, virtualPath string) (FileContent, error) {
	if err := ctx.Err(); err != nil {
		return FileContent{}, err
	}
	_, abs, err := s.resolver.ResolveExisting(virtualPath)
	if err != nil {
		return FileContent{}, err
	}
	st, err := os.Stat(abs)
	if err != nil {
		return FileContent{}, err
	}
	if st.IsDir() {
		return FileContent{}, errors.New("cannot edit a directory")
	}
	if st.Size() > s.editorMaxBytes {
		return FileContent{}, ErrTooLarge
	}
	data, err := os.ReadFile(abs)
	if err != nil {
		return FileContent{}, err
	}
	return FileContent{
		Path:     CleanVirtual(virtualPath),
		Content:  string(data),
		Modified: st.ModTime(),
		ETag:     etag(st),
	}, nil
}

func (s *Service) DetectContentType(ctx context.Context, virtualPath string) (FileType, error) {
	if err := ctx.Err(); err != nil {
		return FileType{}, err
	}
	_, abs, err := s.resolver.ResolveExisting(virtualPath)
	if err != nil {
		return FileType{}, err
	}
	file, err := os.Open(abs)
	if err != nil {
		return FileType{}, err
	}
	defer file.Close()

	sample := make([]byte, 512)
	n, err := file.Read(sample)
	if err != nil && !errors.Is(err, io.EOF) {
		return FileType{}, err
	}
	sample = sample[:n]
	contentType := http.DetectContentType(sample)
	if looksLikeText(sample) && contentType == "application/octet-stream" {
		contentType = "text/plain; charset=utf-8"
	}
	if extensionType := mime.TypeByExtension(filepath.Ext(abs)); strings.HasPrefix(extensionType, "video/") && contentType == "application/octet-stream" {
		contentType = extensionType
	}
	return FileType{Path: CleanVirtual(virtualPath), ContentType: contentType}, nil
}

func looksLikeText(data []byte) bool {
	if len(data) == 0 {
		return true
	}
	if !utf8.Valid(data) {
		return false
	}
	for _, value := range data {
		if value == 0 || value < 0x09 || value > 0x0d && value < 0x20 {
			return false
		}
	}
	return true
}

func (s *Service) WriteText(ctx context.Context, virtualPath string, content []byte, ifMatch string, createOnly bool) (FileContent, error) {
	if int64(len(content)) > s.editorMaxBytes {
		return FileContent{}, ErrTooLarge
	}
	if err := ctx.Err(); err != nil {
		return FileContent{}, err
	}
	var abs string
	var err error
	if createOnly {
		_, abs, err = s.resolver.ResolveForCreate(virtualPath)
	} else {
		_, abs, err = s.resolver.ResolveExisting(virtualPath)
	}
	if err != nil {
		return FileContent{}, err
	}

	if createOnly {
		f, err := os.OpenFile(abs, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
		if err != nil {
			if errors.Is(err, fs.ErrExist) {
				return FileContent{}, ErrConflict
			}
			return FileContent{}, err
		}
		if _, err := f.Write(content); err != nil {
			_ = f.Close()
			_ = os.Remove(abs)
			return FileContent{}, err
		}
		if err := f.Close(); err != nil {
			return FileContent{}, err
		}
	} else {
		st, err := os.Stat(abs)
		if err != nil {
			return FileContent{}, err
		}
		if ifMatch != "" && ifMatch != etag(st) {
			return FileContent{}, ErrConflict
		}
		if hasMultipleLinks(st) {
			if err := writeLinkedFile(abs, content); err != nil {
				return FileContent{}, err
			}
			goto saved
		}
		tmp, err := os.CreateTemp(filepath.Dir(abs), ".webfile-save-*")
		if err != nil {
			return FileContent{}, err
		}
		tmpName := tmp.Name()
		defer os.Remove(tmpName)
		if err := copyReplacementMetadata(abs, tmp, st); err != nil {
			_ = tmp.Close()
			return FileContent{}, err
		}
		if _, err := tmp.Write(content); err != nil {
			_ = tmp.Close()
			return FileContent{}, err
		}
		if err := tmp.Sync(); err != nil {
			_ = tmp.Close()
			return FileContent{}, err
		}
		if err := tmp.Close(); err != nil {
			return FileContent{}, err
		}
		if err := os.Rename(tmpName, abs); err != nil {
			return FileContent{}, err
		}
	}

saved:
	st, err := os.Stat(abs)
	if err != nil {
		return FileContent{}, err
	}
	return FileContent{
		Path:     CleanVirtual(virtualPath),
		Content:  string(content),
		Modified: st.ModTime(),
		ETag:     etag(st),
	}, nil
}

func (s *Service) Mkdir(ctx context.Context, virtualPath string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	_, abs, err := s.resolver.ResolveForCreate(virtualPath)
	if err != nil {
		return err
	}
	return os.Mkdir(abs, 0o755)
}

func (s *Service) Rename(ctx context.Context, from, to string) error {
	if IsConfiguredRootPath(from) {
		return errors.New("configured roots cannot be renamed")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	fromRoot, fromAbs, err := s.resolver.ResolveEntry(from)
	if err != nil {
		return err
	}
	toRoot, toAbs, err := s.resolver.ResolveForCreate(to)
	if err != nil {
		return err
	}
	if fromRoot.Name != toRoot.Name {
		return errors.New("rename cannot cross configured roots; use the move operation")
	}
	return renameNoReplace(fromAbs, toAbs)
}

func (s *Service) Delete(ctx context.Context, virtualPath string) error {
	if IsConfiguredRootPath(virtualPath) {
		return errors.New("configured roots cannot be deleted")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	_, abs, err := s.resolver.ResolveEntry(virtualPath)
	if err != nil {
		return err
	}
	return os.RemoveAll(abs)
}

func (s *Service) OpenDownload(ctx context.Context, virtualPath string) (*os.File, os.FileInfo, error) {
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	_, abs, err := s.resolver.ResolveExisting(virtualPath)
	if err != nil {
		return nil, nil, err
	}
	f, err := os.Open(abs)
	if err != nil {
		return nil, nil, err
	}
	st, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return nil, nil, err
	}
	if st.IsDir() {
		_ = f.Close()
		return nil, nil, errors.New("path is a directory; use the streaming folder download")
	}
	return f, st, nil
}

func (s *Service) Upload(ctx context.Context, virtualDir, filename string, src io.Reader) (Entry, error) {
	if err := ctx.Err(); err != nil {
		return Entry{}, err
	}
	virtualPath := VirtualJoin(virtualDir, filepath.Base(filename))
	_, abs, err := s.resolver.ResolveForCreate(virtualPath)
	if err != nil {
		return Entry{}, err
	}
	if _, err := os.Lstat(abs); err == nil {
		return Entry{}, ErrConflict
	} else if !errors.Is(err, fs.ErrNotExist) {
		return Entry{}, err
	}

	tmp, err := os.CreateTemp(filepath.Dir(abs), ".webfile-upload-*")
	if err != nil {
		return Entry{}, err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if _, err := io.Copy(tmp, src); err != nil {
		_ = tmp.Close()
		return Entry{}, err
	}
	if err := tmp.Chmod(0o644); err != nil {
		_ = tmp.Close()
		return Entry{}, err
	}
	if err := tmp.Close(); err != nil {
		return Entry{}, err
	}
	if err := os.Link(tmpName, abs); err != nil {
		if errors.Is(err, fs.ErrExist) {
			return Entry{}, ErrConflict
		}
		return Entry{}, err
	}
	st, err := os.Stat(abs)
	if err != nil {
		return Entry{}, err
	}
	return toEntry(CleanVirtual(virtualDir), filepath.Base(filename), st), nil
}

func toEntry(parentVirtual, name string, st os.FileInfo) Entry {
	entry := Entry{
		Name:     name,
		Path:     VirtualJoin(parentVirtual, name),
		IsDir:    st.IsDir(),
		Size:     st.Size(),
		Modified: st.ModTime(),
		Mode:     st.Mode().String(),
	}
	if stat, ok := st.Sys().(*syscall.Stat_t); ok {
		entry.UID = stat.Uid
		entry.GID = stat.Gid
	}
	return entry
}

func etag(st os.FileInfo) string {
	return `"` + strconv.FormatInt(st.Size(), 16) + `-` + strconv.FormatInt(st.ModTime().UnixNano(), 16) + `"`
}

func FormatBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for x := n / unit; x >= unit; x /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTPE"[exp])
}
