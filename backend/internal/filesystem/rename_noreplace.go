package filesystem

import (
	"context"
	"errors"
	"io"
	"io/fs"
	"os"
	"webfile/internal/rootedio"
)

var ErrRenameNoReplaceUnsupported = errors.New("atomic no-replace rename is not supported by this filesystem")

var errRenameNoReplaceUnsupported = ErrRenameNoReplaceUnsupported

func RenameNoReplace(disk *rootedio.FS, source, destination string) error {
	return renameNoReplace(disk, source, destination)
}

func renameNoReplaceFallback(disk *rootedio.FS, source, destination string) error {
	return renameNoReplaceFallbackContext(disk, context.Background(), source, destination)
}

func renameNoReplaceFallbackContext(disk *rootedio.FS, ctx context.Context, source, destination string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	info, err := disk.Lstat(source)
	if err != nil {
		return err
	}
	if info.IsDir() {
		return ErrRenameNoReplaceUnsupported
	}
	if err := disk.Link(source, destination); err != nil {
		if errors.Is(err, fs.ErrExist) {
			return ErrConflict
		}
		return copyNoReplaceContext(disk, ctx, source, destination, info)
	}
	if err := disk.Remove(source); err != nil {
		_ = disk.Remove(destination)
		return err
	}
	return nil
}

func copyNoReplace(disk *rootedio.FS, source, destination string, info fs.FileInfo) (result error) {
	return copyNoReplaceContext(disk, context.Background(), source, destination, info)
}

func copyNoReplaceContext(disk *rootedio.FS, ctx context.Context, source, destination string, info fs.FileInfo) (result error) {
	if err := ctx.Err(); err != nil {
		return err
	}
	input, err := disk.Open(source)
	if err != nil {
		return err
	}
	defer input.Close()
	output, err := disk.OpenFile(destination, os.O_WRONLY|os.O_CREATE|os.O_EXCL, info.Mode().Perm())
	if err != nil {
		if errors.Is(err, fs.ErrExist) {
			return ErrConflict
		}
		return err
	}
	removeDestination := true
	defer func() {
		_ = output.Close()
		if removeDestination {
			_ = disk.Remove(destination)
		}
	}()
	if _, err := io.Copy(output, &uploadContextReader{ctx: ctx, reader: input}); err != nil {
		return err
	}
	if err := output.Sync(); err != nil {
		return err
	}
	if err := output.Close(); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := disk.Chtimes(destination, info.ModTime(), info.ModTime()); err != nil {
		return err
	}
	if err := disk.Remove(source); err != nil {
		return err
	}
	removeDestination = false
	return nil
}
