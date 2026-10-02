//go:build !linux

package filesystem

import (
	"errors"
	"io"
	"os"
)

func canNavigateDirectory(path string) bool {
	directory, err := os.Open(path)
	if err != nil {
		return false
	}
	defer directory.Close()
	_, err = directory.Readdirnames(1)
	return err == nil || errors.Is(err, io.EOF)
}

func canReadFile(path string) bool {
	file, err := os.Open(path)
	if err != nil {
		return false
	}
	_ = file.Close()
	return true
}
