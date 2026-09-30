package filesystem

import (
	"archive/zip"
	"context"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

func (s *Service) WriteFolderZip(ctx context.Context, virtualPath string, out io.Writer) error {
	_, abs, err := s.resolver.ResolveExisting(virtualPath)
	if err != nil {
		return err
	}
	rootInfo, err := os.Stat(abs)
	if err != nil {
		return err
	}
	if !rootInfo.IsDir() {
		return ErrNotDirectory
	}
	base := filepath.Base(abs)
	zw := zip.NewWriter(out)

	err = filepath.WalkDir(abs, func(current string, de fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		info, err := os.Lstat(current)
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return errors.New("folder download does not follow symbolic links")
		}
		if !info.IsDir() && !info.Mode().IsRegular() {
			return errors.New("folder download contains an unsupported special file")
		}
		rel, err := filepath.Rel(abs, current)
		if err != nil {
			return err
		}
		name := base
		if rel != "." {
			name += "/" + filepath.ToSlash(rel)
		}
		if info.IsDir() {
			name = strings.TrimSuffix(name, "/") + "/"
		}
		header, err := zip.FileInfoHeader(info)
		if err != nil {
			return err
		}
		header.Name = name
		if info.IsDir() {
			_, err = zw.CreateHeader(header)
			return err
		}
		header.Method = zip.Deflate
		writer, err := zw.CreateHeader(header)
		if err != nil {
			return err
		}
		f, err := os.Open(current)
		if err != nil {
			return err
		}
		_, copyErr := io.Copy(writer, f)
		closeErr := f.Close()
		if copyErr != nil {
			return copyErr
		}
		return closeErr
	})
	if err != nil {
		return err
	}
	return zw.Close()
}
