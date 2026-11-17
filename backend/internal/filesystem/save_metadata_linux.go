//go:build linux

package filesystem

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
	"webfile/internal/rootedio"
)

func hasMultipleLinks(info fs.FileInfo) bool {
	stat, ok := info.Sys().(*syscall.Stat_t)
	return ok && stat.Nlink > 1
}

func lockEditorDirectory(ctx context.Context, disk *rootedio.FS, path string) (func(), error) {
	directory, err := disk.Open(filepath.Dir(path))
	if err != nil {
		return nil, err
	}
	if err := lockEditorFile(ctx, directory); err != nil {
		_ = directory.Close()
		return nil, err
	}
	// A directory lock stays valid when an atomic save replaces the file inode,
	// and is shared by normal and elevated workers using the same directory.
	return func() { _ = directory.Close() }, nil
}

func lockEditorFile(ctx context.Context, file *os.File) error {
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		err := unix.Flock(int(file.Fd()), unix.LOCK_EX|unix.LOCK_NB)
		if err == nil {
			return nil
		}
		if !errors.Is(err, unix.EWOULDBLOCK) && !errors.Is(err, unix.EINTR) {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func writeLinkedFile(file *os.File, content []byte) error {
	if _, err := file.WriteAt(content, 0); err != nil {
		return err
	}
	if err := file.Truncate(int64(len(content))); err != nil {
		return err
	}
	return file.Sync()
}

func copyReplacementMetadata(disk *rootedio.FS, source string, target *os.File, info fs.FileInfo) error {
	if err := target.Chmod(info.Mode().Perm()); err != nil {
		return err
	}
	if stat, ok := info.Sys().(*syscall.Stat_t); ok {
		if err := unix.Fchown(int(target.Fd()), int(stat.Uid), int(stat.Gid)); err != nil && !errors.Is(err, unix.EPERM) {
			return err
		}
	}

	original, err := disk.OpenFile(source, unix.O_PATH, 0)
	if err != nil {
		return err
	}
	defer original.Close()
	originalPath := fmt.Sprintf("/proc/self/fd/%d", original.Fd())
	size, err := unix.Listxattr(originalPath, nil)
	if errors.Is(err, unix.ENOTSUP) {
		return nil
	}
	if err != nil || size == 0 {
		return err
	}
	names := make([]byte, size)
	size, err = unix.Listxattr(originalPath, names)
	if err != nil {
		return err
	}
	for _, name := range strings.Split(strings.TrimRight(string(names[:size]), "\x00"), "\x00") {
		if !strings.HasPrefix(name, "user.") && name != "system.posix_acl_access" {
			continue
		}
		valueSize, err := unix.Getxattr(originalPath, name, nil)
		if err != nil {
			return err
		}
		value := make([]byte, valueSize)
		valueSize, err = unix.Getxattr(originalPath, name, value)
		if err != nil {
			return err
		}
		if err := unix.Fsetxattr(int(target.Fd()), name, value[:valueSize], 0); err != nil {
			return err
		}
	}
	return nil
}
