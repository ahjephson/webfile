package filesystem

import (
	"errors"
	"io"
	"io/fs"
	"os"
)

var ErrRenameNoReplaceUnsupported = errors.New("atomic no-replace rename is not supported by this filesystem")

var errRenameNoReplaceUnsupported = ErrRenameNoReplaceUnsupported

func RenameNoReplace(source, destination string) error {
	return renameNoReplace(source, destination)
}

func renameNoReplaceFallback(source, destination string) error {
	info, err := os.Lstat(source)
	if err != nil {
		return err
	}
	if info.IsDir() {
		return ErrRenameNoReplaceUnsupported
	}
	if err := os.Link(source, destination); err != nil {
		if errors.Is(err, fs.ErrExist) {
			return ErrConflict
		}
		return copyNoReplace(source, destination, info)
	}
	if err := os.Remove(source); err != nil {
		_ = os.Remove(destination)
		return err
	}
	return nil
}

func copyNoReplace(source, destination string, info fs.FileInfo) (result error) {
	input, err := os.Open(source)
	if err != nil {
		return err
	}
	defer input.Close()
	output, err := os.OpenFile(destination, os.O_WRONLY|os.O_CREATE|os.O_EXCL, info.Mode().Perm())
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
			_ = os.Remove(destination)
		}
	}()
	if _, err := io.Copy(output, input); err != nil {
		return err
	}
	if err := output.Sync(); err != nil {
		return err
	}
	if err := output.Close(); err != nil {
		return err
	}
	if err := os.Chtimes(destination, info.ModTime(), info.ModTime()); err != nil {
		return err
	}
	if err := os.Remove(source); err != nil {
		return err
	}
	removeDestination = false
	return nil
}
