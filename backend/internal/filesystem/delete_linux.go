//go:build linux

package filesystem

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"webfile/internal/thumbnailcache"

	"golang.org/x/sys/unix"
)

// CountDeleteEntries counts files, links and directories without reading file
// contents or inspecting thumbnails. The result is a snapshot: other programs
// may change the tree before or during deletion. No child symlinks are followed.
func (s *Service) CountDeleteEntries(ctx context.Context, virtualPath string) (int64, error) {
	if IsConfiguredRootPath(virtualPath) {
		return 0, errors.New("configured roots cannot be deleted")
	}
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	configured, absolute, err := s.resolver.ResolveEntry(virtualPath)
	if err != nil {
		return 0, err
	}
	root, err := os.OpenRoot(configured.Path)
	if err != nil {
		return 0, err
	}
	defer root.Close()
	relative, err := filepath.Rel(configured.Path, absolute)
	if err != nil {
		return 0, err
	}
	if relative == "." {
		return 0, ErrInvalidPath
	}
	parent, err := root.OpenFile(filepath.Dir(relative), os.O_RDONLY|unix.O_DIRECTORY, 0)
	if err != nil {
		return 0, err
	}
	defer parent.Close()
	return countDeleteEntryAt(ctx, parent, filepath.Base(relative))
}

func countDeleteEntryAt(ctx context.Context, parent *os.File, name string) (int64, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	fd, err := unix.Openat(int(parent.Fd()), name, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if errors.Is(err, unix.ENOENT) {
		return 0, nil
	}
	if errors.Is(err, unix.ENOTDIR) || errors.Is(err, unix.ELOOP) {
		return 1, nil // A file or link, including a link to a directory.
	}
	if err != nil {
		return 0, &os.PathError{Op: "openat", Path: name, Err: err}
	}
	directory := os.NewFile(uintptr(fd), name)
	defer directory.Close()
	total := int64(1)
	for {
		if err := ctx.Err(); err != nil {
			return 0, err
		}
		entries, readErr := directory.ReadDir(128)
		for _, entry := range entries {
			if err := ctx.Err(); err != nil {
				return 0, err
			}
			if entry.IsDir() {
				count, err := countDeleteEntryAt(ctx, directory, entry.Name())
				if err != nil {
					return 0, err
				}
				total += count
			} else {
				total++
			}
		}
		if errors.Is(readErr, io.EOF) {
			return total, nil
		}
		if readErr != nil {
			return 0, readErr
		}
	}
}

// DeleteWithProgress streams directory entries without reading file contents
// or inspecting thumbnails. Callers may count first. The callback receives the
// number of entries actually removed, including directories, starting at zero.
func (s *Service) DeleteWithProgress(ctx context.Context, virtualPath string, report func(int64)) error {
	if IsConfiguredRootPath(virtualPath) {
		return errors.New("configured roots cannot be deleted")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	configured, absolute, err := s.resolver.ResolveEntry(virtualPath)
	if err != nil {
		return err
	}
	root, err := os.OpenRoot(configured.Path)
	if err != nil {
		return err
	}
	defer root.Close()
	relative, err := filepath.Rel(configured.Path, absolute)
	if err != nil {
		return err
	}
	if relative == "." {
		return ErrInvalidPath
	}
	s.videoQueue.cancelPath(ctx, CleanVirtual(virtualPath))
	if report != nil {
		report(0)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	// Resolve the parent beneath the open root after cancellation/progress work,
	// then keep every descendant operation relative to an open directory fd.
	parent, err := root.OpenFile(filepath.Dir(relative), os.O_RDONLY|unix.O_DIRECTORY, 0)
	if err != nil {
		return err
	}
	defer parent.Close()
	var deleted int64
	removed := func() {
		deleted++
		if report != nil {
			report(deleted)
		}
	}
	tickets := s.captureThumbnailChanges(ctx, []string{virtualPath})
	if err := deleteEntryAt(ctx, parent, filepath.Base(relative), removed); err != nil {
		return err
	}
	if len(tickets) == 1 {
		s.applyThumbnailChanges([]thumbnailcache.LocationChange{{Ticket: tickets[0], Purge: true}})
	}
	return nil
}

func deleteEntryAt(ctx context.Context, parent *os.File, name string, removed func()) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	// Unlink regular files and symlinks directly; no file-content or stat read
	// is needed. Never follow a child symlink, including one swapped in mid-walk.
	err := unix.Unlinkat(int(parent.Fd()), name, 0)
	if err == nil {
		removed()
		return nil
	}
	if errors.Is(err, unix.ENOENT) {
		return nil
	}
	if !errors.Is(err, unix.EISDIR) && !errors.Is(err, unix.EPERM) {
		return &os.PathError{Op: "unlinkat", Path: name, Err: err}
	}
	fd, err := unix.Openat(int(parent.Fd()), name, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if errors.Is(err, unix.ENOENT) {
		return nil
	}
	if errors.Is(err, unix.ENOTDIR) || errors.Is(err, unix.ELOOP) {
		// A directory may have become a file or symlink since unlinkat.
		if err := ctx.Err(); err != nil {
			return err
		}
		err = unix.Unlinkat(int(parent.Fd()), name, 0)
		if err == nil {
			removed()
			return nil
		}
		if errors.Is(err, unix.ENOENT) {
			return nil
		}
	}
	if err != nil {
		return &os.PathError{Op: "openat", Path: name, Err: err}
	}
	directory := os.NewFile(uintptr(fd), name)
	defer directory.Close()
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		names, readErr := directory.Readdirnames(128)
		for _, child := range names {
			if err := deleteEntryAt(ctx, directory, child, removed); err != nil {
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
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := unix.Unlinkat(int(parent.Fd()), name, unix.AT_REMOVEDIR); err != nil {
		if errors.Is(err, unix.ENOENT) {
			return nil
		}
		return &os.PathError{Op: "unlinkat", Path: name, Err: err}
	}
	removed()
	return nil
}
