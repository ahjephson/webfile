//go:build linux

package filesystem

import (
	"context"
	"errors"
	"io/fs"

	"golang.org/x/sys/unix"
)

func renameNoReplace(source, destination string) error {
	return renameNoReplaceContext(context.Background(), source, destination)
}

func renameNoReplaceContext(ctx context.Context, source, destination string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	err := unix.Renameat2(unix.AT_FDCWD, source, unix.AT_FDCWD, destination, unix.RENAME_NOREPLACE)
	if err == nil {
		return nil
	}
	if errors.Is(err, fs.ErrExist) || errors.Is(err, unix.ENOTEMPTY) {
		return ErrConflict
	}
	if errors.Is(err, unix.ENOSYS) || errors.Is(err, unix.EINVAL) || errors.Is(err, unix.EOPNOTSUPP) {
		return renameNoReplaceFallbackContext(ctx, source, destination)
	}
	return err
}
