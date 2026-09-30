package filesystem

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"
)

type TrashItem struct {
	ID           string    `json:"id"`
	Name         string    `json:"name"`
	OriginalPath string    `json:"original_path"`
	DeletedAt    time.Time `json:"deleted_at"`
	IsDir        bool      `json:"is_dir"`
	Size         int64     `json:"size"`
}

type trashRecord struct {
	TrashItem
	StoredName string `json:"stored_name"`
}

func (s *Service) trashBase() (string, error) {
	if s.trashDir != "" {
		return s.trashDir, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".local", "share", "webfile", "trash"), nil
}

func (s *Service) ensureTrashDirs() (filesDir, infoDir string, err error) {
	base, err := s.trashBase()
	if err != nil {
		return "", "", err
	}
	filesDir = filepath.Join(base, "files")
	infoDir = filepath.Join(base, "info")
	if err := os.MkdirAll(filesDir, 0o700); err != nil {
		return "", "", err
	}
	if err := os.MkdirAll(infoDir, 0o700); err != nil {
		return "", "", err
	}
	return filesDir, infoDir, nil
}

func (s *Service) Trash(ctx context.Context, sources []string, report progressFunc) error {
	if len(sources) == 0 {
		return errors.New("at least one source is required")
	}
	filesDir, infoDir, err := s.ensureTrashDirs()
	if err != nil {
		return err
	}

	type sourceInfo struct {
		virtual  string
		absolute string
		bytes    int64
		items    int64
		isDir    bool
	}
	measured := make([]sourceInfo, 0, len(sources))
	var bytesTotal, itemsTotal int64
	for _, source := range sources {
		if IsConfiguredRootPath(source) {
			return errors.New("configured roots cannot be moved to trash")
		}
		_, abs, err := s.resolver.ResolveEntry(source)
		if err != nil {
			return err
		}
		info, err := os.Lstat(abs)
		if err != nil {
			return err
		}
		bytes, items, err := measureTree(ctx, abs)
		if err != nil {
			return err
		}
		measured = append(measured, sourceInfo{virtual: CleanVirtual(source), absolute: abs, bytes: bytes, items: items, isDir: info.IsDir()})
		bytesTotal += bytes
		itemsTotal += items
	}

	var bytesDone, itemsDone int64
	emit := func(message string) {
		if report != nil {
			report(TransferProgress{BytesDone: bytesDone, BytesTotal: bytesTotal, ItemsDone: itemsDone, ItemsTotal: itemsTotal, Message: message})
		}
	}
	emit("Preparing Recycle Bin")

	for _, source := range measured {
		if err := ctx.Err(); err != nil {
			return err
		}
		id := trashID()
		storedName := id + "-" + filepath.Base(source.absolute)
		trashPath := filepath.Join(filesDir, storedName)
		item := TrashItem{
			ID:           id,
			Name:         filepath.Base(source.absolute),
			OriginalPath: source.virtual,
			DeletedAt:    time.Now(),
			IsDir:        source.isDir,
			Size:         source.bytes,
		}

		startBytes, startItems := bytesDone, itemsDone
		if err := renameNoReplace(source.absolute, trashPath); err == nil {
			bytesDone += source.bytes
			itemsDone += source.items
			emit("Moving " + item.Name + " to Recycle Bin")
		} else if errors.Is(err, syscall.EXDEV) || errors.Is(err, errRenameNoReplaceUnsupported) {
			if err := copyTree(ctx, source.absolute, trashPath, func(deltaBytes, deltaItems int64) {
				bytesDone += deltaBytes
				itemsDone += deltaItems
				emit("Moving " + item.Name + " to Recycle Bin")
			}); err != nil {
				bytesDone, itemsDone = startBytes, startItems
				return err
			}
			if err := os.RemoveAll(source.absolute); err != nil {
				return fmt.Errorf("copied %s to trash but could not remove source: %w", source.virtual, err)
			}
		} else {
			return err
		}

		if err := writeTrashInfo(infoDir, trashRecord{TrashItem: item, StoredName: storedName}); err != nil {
			// Avoid orphaning data without restore metadata. Best effort to put
			// the entry back where it came from before returning the error.
			if restoreErr := movePhysical(ctx, trashPath, source.absolute); restoreErr == nil {
				bytesDone, itemsDone = startBytes, startItems
			}
			return err
		}
	}
	emit("Moved to Recycle Bin")
	return nil
}

func (s *Service) ListTrash(ctx context.Context) ([]TrashItem, error) {
	filesDir, infoDir, err := s.ensureTrashDirs()
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(infoDir)
	if err != nil {
		return nil, err
	}
	items := make([]TrashItem, 0, len(entries))
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(infoDir, entry.Name()))
		if err != nil {
			continue
		}
		var record trashRecord
		if err := json.Unmarshal(data, &record); err != nil || record.ID == "" || record.StoredName == "" {
			continue
		}
		item := record.TrashItem
		stored := filepath.Join(filesDir, record.StoredName)
		info, err := os.Lstat(stored)
		if err != nil {
			continue
		}
		item.IsDir = info.IsDir()
		if !item.IsDir {
			item.Size = info.Size()
		}
		items = append(items, item)
	}
	sort.Slice(items, func(i, j int) bool { return items[i].DeletedAt.After(items[j].DeletedAt) })
	return items, nil
}

func (s *Service) RestoreTrash(ctx context.Context, id string) error {
	filesDir, infoDir, err := s.ensureTrashDirs()
	if err != nil {
		return err
	}
	record, err := readTrashInfo(infoDir, id)
	if err != nil {
		return err
	}
	_, destination, err := s.resolver.ResolveForCreate(record.OriginalPath)
	if err != nil {
		return err
	}
	source := filepath.Join(filesDir, record.StoredName)
	if err := movePhysical(ctx, source, destination); err != nil {
		return err
	}
	if err := os.Remove(filepath.Join(infoDir, id+".json")); err != nil {
		return err
	}
	return nil
}

func (s *Service) PurgeTrash(ctx context.Context, id string) error {
	filesDir, infoDir, err := s.ensureTrashDirs()
	if err != nil {
		return err
	}
	record, err := readTrashInfo(infoDir, id)
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := os.RemoveAll(filepath.Join(filesDir, record.StoredName)); err != nil {
		return err
	}
	return os.Remove(filepath.Join(infoDir, id+".json"))
}

func writeTrashInfo(infoDir string, record trashRecord) error {
	data, err := json.Marshal(record)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(infoDir, ".trash-info-*")
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
	return os.Rename(name, filepath.Join(infoDir, record.ID+".json"))
}

func readTrashInfo(infoDir, id string) (trashRecord, error) {
	if id == "" || strings.ContainsAny(id, `/\\`) {
		return trashRecord{}, ErrInvalidPath
	}
	data, err := os.ReadFile(filepath.Join(infoDir, id+".json"))
	if err != nil {
		return trashRecord{}, err
	}
	var record trashRecord
	if err := json.Unmarshal(data, &record); err != nil {
		return trashRecord{}, err
	}
	if record.ID != id || record.StoredName == "" {
		return trashRecord{}, ErrInvalidPath
	}
	return record, nil
}

func movePhysical(ctx context.Context, source, destination string) error {
	if err := renameNoReplace(source, destination); err == nil {
		return nil
	} else if errors.Is(err, ErrConflict) {
		return err
	} else if !errors.Is(err, syscall.EXDEV) && !errors.Is(err, errRenameNoReplaceUnsupported) {
		return err
	}
	if err := copyTree(ctx, source, destination, func(int64, int64) {}); err != nil {
		return err
	}
	return os.RemoveAll(source)
}

func trashID() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err == nil {
		return hex.EncodeToString(b[:])
	}
	return fmt.Sprintf("%d", time.Now().UnixNano())
}
