//go:build linux

package filesystem

import (
	"context"
	"errors"
	"io/fs"
	"webfile/internal/rootedio"

	"golang.org/x/sys/unix"
)

func renameNoReplace(disk *rootedio.FS, source, destination string) error {
	return renameNoReplaceContext(disk, context.Background(), source, destination)
}

func renameNoReplaceContext(disk *rootedio.FS, ctx context.Context, source, destination string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	err := disk.RenameFlags(source, destination, unix.RENAME_NOREPLACE)
	if err == nil {
		return nil
	}
	if errors.Is(err, fs.ErrExist) || errors.Is(err, unix.ENOTEMPTY) {
		return ErrConflict
	}
	if errors.Is(err, unix.ENOSYS) || errors.Is(err, unix.EINVAL) || errors.Is(err, unix.EOPNOTSUPP) {
		return renameNoReplaceFallbackContext(disk, ctx, source, destination)
	}
	return err
}
