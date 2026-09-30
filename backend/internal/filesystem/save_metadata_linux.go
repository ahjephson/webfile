//go:build linux

package filesystem

import (
	"errors"
	"io/fs"
	"os"
	"strings"
	"syscall"

	"golang.org/x/sys/unix"
)

func hasMultipleLinks(info fs.FileInfo) bool {
	stat, ok := info.Sys().(*syscall.Stat_t)
	return ok && stat.Nlink > 1
}

func writeLinkedFile(path string, content []byte) error {
	file, err := os.OpenFile(path, os.O_WRONLY, 0)
	if err != nil {
		return err
	}
	if _, err := file.WriteAt(content, 0); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Truncate(int64(len(content))); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return err
	}
	return file.Close()
}

func copyReplacementMetadata(source string, target *os.File, info fs.FileInfo) error {
	if err := target.Chmod(info.Mode().Perm()); err != nil {
		return err
	}
	if stat, ok := info.Sys().(*syscall.Stat_t); ok {
		if err := unix.Fchown(int(target.Fd()), int(stat.Uid), int(stat.Gid)); err != nil && !errors.Is(err, unix.EPERM) {
			return err
		}
	}

	size, err := unix.Listxattr(source, nil)
	if errors.Is(err, unix.ENOTSUP) {
		return nil
	}
	if err != nil || size == 0 {
		return err
	}
	names := make([]byte, size)
	size, err = unix.Listxattr(source, names)
	if err != nil {
		return err
	}
	for _, name := range strings.Split(strings.TrimRight(string(names[:size]), "\x00"), "\x00") {
		if !strings.HasPrefix(name, "user.") && name != "system.posix_acl_access" {
			continue
		}
		valueSize, err := unix.Getxattr(source, name, nil)
		if err != nil {
			return err
		}
		value := make([]byte, valueSize)
		valueSize, err = unix.Getxattr(source, name, value)
		if err != nil {
			return err
		}
		if err := unix.Fsetxattr(int(target.Fd()), name, value[:valueSize], 0); err != nil {
			return err
		}
	}
	return nil
}
