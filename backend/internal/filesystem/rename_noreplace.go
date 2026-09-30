package filesystem

import (
	"errors"
	"io/fs"
	"os"
)

var errRenameNoReplaceUnsupported = errors.New("atomic no-replace rename is not supported by this filesystem")

func RenameNoReplace(source, destination string) error {
	return renameNoReplace(source, destination)
}

func renameNoReplaceFallback(source, destination string) error {
	info, err := os.Lstat(source)
	if err != nil {
		return err
	}
	if info.IsDir() {
		return errRenameNoReplaceUnsupported
	}
	if err := os.Link(source, destination); err != nil {
		if errors.Is(err, fs.ErrExist) {
			return ErrConflict
		}
		return err
	}
	if err := os.Remove(source); err != nil {
		_ = os.Remove(destination)
		return err
	}
	return nil
}
