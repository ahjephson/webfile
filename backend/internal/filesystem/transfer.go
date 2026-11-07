package filesystem

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
	"syscall"
	"time"
	"webfile/internal/taskcontrol"
)

type TransferProgress struct {
	Phase      string
	BytesDone  int64
	BytesTotal int64
	ItemsDone  int64
	ItemsTotal int64
	Message    string
}

type progressFunc func(TransferProgress)

const (
	transferPhaseScanning       = "scanning"
	transferPhaseCopying        = "copying"
	transferPhaseFlushing       = "flushing"
	transferPhasePublishing     = "publishing"
	transferPhaseRemovingSource = "removing_source"
	transferPhaseCompleted      = "completed"
)

type measuredEntry struct {
	VirtualPath string
	Root        Root
	Absolute    string
	Bytes       int64
	Items       int64
}

// CopyMove copies or moves one or more filesystem entries into an existing
// destination directory. The filesystem remains authoritative: no catalogue or
// metadata database participates in the operation.
func (s *Service) CopyMove(ctx context.Context, kind string, sources []string, destinationDir string, report progressFunc) error {
	if kind != "copy" && kind != "move" {
		return fmt.Errorf("unsupported transfer kind %q", kind)
	}
	if len(sources) == 0 {
		return errors.New("at least one source is required")
	}
	capturedThumbnails := s.captureThumbnailTransfer(ctx, kind, sources, destinationDir)
	defer s.finishThumbnailTransfer(capturedThumbnails)
	if report != nil {
		report(TransferProgress{Phase: transferPhaseScanning, Message: "Scanning"})
	}

	destRoot, destAbs, err := s.resolver.ResolveExisting(destinationDir)
	if err != nil {
		return err
	}
	destInfo, err := os.Stat(destAbs)
	if err != nil {
		return err
	}
	if !destInfo.IsDir() {
		return ErrNotDirectory
	}

	measured := make([]measuredEntry, 0, len(sources))
	var bytesTotal, itemsTotal int64
	seenDestinations := map[string]struct{}{}
	for _, source := range sources {
		if IsConfiguredRootPath(source) {
			return errors.New("configured roots cannot be copied or moved")
		}
		root, abs, err := s.resolver.ResolveEntry(source)
		if err != nil {
			return err
		}
		if kind == "move" {
			if err := root.validateMoveSource(ctx, abs); err != nil {
				return err
			}
		}
		bytes, items, err := measureTree(ctx, abs)
		if err != nil {
			return err
		}
		target := filepath.Join(destAbs, filepath.Base(abs))
		if _, duplicate := seenDestinations[target]; duplicate {
			return ErrConflict
		}
		seenDestinations[target] = struct{}{}
		if _, err := os.Lstat(target); err == nil {
			return ErrConflict
		} else if !errors.Is(err, fs.ErrNotExist) {
			return err
		}
		if isSameOrDescendant(abs, destAbs) {
			return fmt.Errorf("destination cannot be inside source %s", source)
		}
		measured = append(measured, measuredEntry{
			VirtualPath: CleanVirtual(source),
			Root:        root,
			Absolute:    abs,
			Bytes:       bytes,
			Items:       items,
		})
		bytesTotal += bytes
		itemsTotal += items
	}

	var bytesDone, itemsDone int64
	emit := func(phase, message string) {
		if report != nil {
			report(TransferProgress{
				Phase:      phase,
				BytesDone:  bytesDone,
				BytesTotal: bytesTotal,
				ItemsDone:  itemsDone,
				ItemsTotal: itemsTotal,
				Message:    message,
			})
		}
	}
	emit(transferPhaseScanning, "Scan complete")

	for _, source := range measured {
		if err := taskcontrol.Wait(ctx); err != nil {
			return err
		}
		target := filepath.Join(destAbs, filepath.Base(source.Absolute))
		message := fmt.Sprintf("%s %s", strings.ToUpper(kind[:1])+kind[1:], path.Base(source.VirtualPath))

		if kind == "move" {
			s.videoQueue.cancelPath(ctx, source.VirtualPath)
			if err := taskcontrol.Wait(ctx); err != nil {
				return err
			}
		}
		if kind == "move" && canRenameTransferTree(source.Root, destRoot) {
			info, err := os.Lstat(source.Absolute)
			if err != nil {
				return err
			}
			if err := moveReviewedItem(source.Root, destRoot, source.Absolute, target, info); err == nil {
				bytesDone += source.Bytes
				itemsDone += source.Items
				emit(transferPhaseCompleted, message)
				continue
			} else if errors.Is(err, ErrConflict) {
				return err
			} else if !errors.Is(err, syscall.EXDEV) && !errors.Is(err, errRenameNoReplaceUnsupported) {
				return err
			}
		}

		startBytes, startItems := bytesDone, itemsDone
		err := copyTree(ctx, source.Absolute, target, func(phase string, deltaBytes, deltaItems int64) {
			bytesDone += deltaBytes
			itemsDone += deltaItems
			emit(phase, message)
		})
		if err != nil {
			bytesDone, itemsDone = startBytes, startItems
			return err
		}
		if kind == "move" {
			emit(transferPhaseRemovingSource, message)
			if err := os.RemoveAll(source.Absolute); err != nil {
				return fmt.Errorf("copied but could not remove source %s: %w", source.VirtualPath, err)
			}
		}
	}

	emit(transferPhaseCompleted, "Complete")
	return nil
}

func measureTree(ctx context.Context, source string) (int64, int64, error) {
	var bytes, items int64
	err := filepath.WalkDir(source, func(current string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := taskcontrol.Wait(ctx); err != nil {
			return err
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		items++
		if info.Mode().IsRegular() {
			bytes += info.Size()
			return nil
		}
		if info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return nil
		}
		return fmt.Errorf("unsupported special file: %s", current)
	})
	return bytes, items, err
}

func copyTree(ctx context.Context, source, destination string, progress func(phase string, deltaBytes, deltaItems int64)) (err error) {
	if _, statErr := os.Lstat(destination); statErr == nil {
		return ErrConflict
	} else if !errors.Is(statErr, fs.ErrNotExist) {
		return statErr
	}

	created := false
	defer func() {
		if err != nil && created {
			_ = os.RemoveAll(destination)
		}
	}()

	type dirTime struct {
		path string
		mode fs.FileMode
		at   time.Time
		mt   time.Time
	}
	var dirs []dirTime

	err = filepath.WalkDir(source, func(current string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		rel, relErr := filepath.Rel(source, current)
		if relErr != nil {
			return relErr
		}
		target := destination
		if rel != "." {
			target = filepath.Join(destination, rel)
		}
		info, infoErr := os.Lstat(current)
		if infoErr != nil {
			return infoErr
		}

		switch {
		case info.IsDir():
			if makeErr := os.Mkdir(target, info.Mode().Perm()); makeErr != nil {
				if errors.Is(makeErr, fs.ErrExist) {
					return ErrConflict
				}
				return makeErr
			}
			if rel == "." {
				created = true
			}
			dirs = append(dirs, dirTime{path: target, mode: info.Mode().Perm(), at: info.ModTime(), mt: info.ModTime()})
			progress(transferPhaseCopying, 0, 1)
			return nil

		case info.Mode()&os.ModeSymlink != 0:
			link, readErr := os.Readlink(current)
			if readErr != nil {
				return readErr
			}
			if makeErr := os.Symlink(link, target); makeErr != nil {
				if errors.Is(makeErr, fs.ErrExist) {
					return ErrConflict
				}
				return makeErr
			}
			if rel == "." {
				created = true
			}
			progress(transferPhaseCopying, 0, 1)
			return nil

		case info.Mode().IsRegular():
			if copyErr := copyFileAtomic(ctx, current, target, info.Mode().Perm(), info.ModTime(), func(phase string, deltaBytes int64) {
				progress(phase, deltaBytes, 0)
			}); copyErr != nil {
				return copyErr
			}
			if rel == "." {
				created = true
			}
			progress(transferPhasePublishing, 0, 1)
			return nil

		default:
			return fmt.Errorf("unsupported special file: %s", current)
		}
	})
	if err != nil {
		return err
	}

	// Creating children changes directory mtimes. Restore directory metadata
	// from the bottom up after the tree is complete.
	progress(transferPhasePublishing, 0, 0)
	for i := len(dirs) - 1; i >= 0; i-- {
		_ = os.Chmod(dirs[i].path, dirs[i].mode)
		_ = os.Chtimes(dirs[i].path, dirs[i].at, dirs[i].mt)
	}
	return nil
}

func copyFileAtomic(ctx context.Context, source, destination string, mode fs.FileMode, modified time.Time, progress func(phase string, deltaBytes int64)) error {
	in, err := os.Open(source)
	if err != nil {
		return err
	}
	defer in.Close()

	if err := os.MkdirAll(filepath.Dir(destination), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(destination), ".webfile-copy-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err := tmp.Chmod(mode); err != nil {
		_ = tmp.Close()
		return err
	}

	if progress != nil {
		// Start timing before the first read/write, which can itself block.
		progress(transferPhaseCopying, 0)
	}
	if err := copyFileData(ctx, in, tmp, progress, copyFileRangePlatform); err != nil {
		_ = tmp.Close()
		return err
	}
	if progress != nil {
		progress(transferPhaseFlushing, 0)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}

	if progress != nil {
		progress(transferPhasePublishing, 0)
	}
	if err := taskcontrol.Wait(ctx); err != nil {
		return err
	}
	// The temporary file already lives in the destination directory. An atomic
	// no-replace rename publishes it without exposing partial output and works
	// on filesystems that support renameat2 even when hard links are unavailable.
	// The platform fallback retains exclusive-create semantics where necessary.
	if err := renameNoReplaceContext(ctx, tmpName, destination); err != nil {
		return err
	}
	_ = os.Chtimes(destination, modified, modified)
	return nil
}

type rangeCopyFunc func(context.Context, *os.File, *os.File, func(int64)) (bool, error)

func copyFileData(ctx context.Context, source, destination *os.File, progress func(string, int64), fastCopy rangeCopyFunc) error {
	reportBytes := func(n int64) {
		if progress != nil {
			progress(transferPhaseCopying, n)
		}
	}
	complete, err := fastCopy(ctx, source, destination, reportBytes)
	if err != nil {
		return err
	}
	if complete {
		return nil
	}

	// The fast path may have made partial progress before falling back. Start
	// range scheduling at the current offset; the final Sync covers both paths.
	writeback, err := newCopyWriteback(destination)
	if err != nil {
		return err
	}
	buf := make([]byte, 1024*1024)
	for {
		if err := taskcontrol.Wait(ctx); err != nil {
			return err
		}
		n, readErr := source.Read(buf)
		if n > 0 {
			written, writeErr := destination.Write(buf[:n])
			if written > 0 {
				reportBytes(int64(written))
			}
			if writeErr != nil {
				return writeErr
			}
			if written != n {
				return io.ErrShortWrite
			}
			if err := writeback(ctx, int64(written)); err != nil {
				return err
			}
		}
		if errors.Is(readErr, io.EOF) {
			break
		}
		if readErr != nil {
			return readErr
		}
	}
	return nil
}

func isSameOrDescendant(source, candidate string) bool {
	rel, err := filepath.Rel(source, candidate)
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator)))
}
