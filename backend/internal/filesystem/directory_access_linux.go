//go:build linux

package filesystem

import "golang.org/x/sys/unix"

func canNavigateDirectory(path string) bool {
	return unix.Access(path, unix.R_OK|unix.X_OK) == nil
}

func canReadFile(path string) bool {
	return unix.Access(path, unix.R_OK) == nil
}
