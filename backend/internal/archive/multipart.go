package archive

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"

	"webfile/internal/filesystem"

	"golang.org/x/sys/unix"
)

var multipartSuffix = regexp.MustCompile(`(?i)\.(7z|zip)\.([0-9]{3}|[1-9][0-9]{3,})$`)

func IsMultipart(name string) bool { return multipartSuffix.MatchString(name) }

// FirstVolume also provides a stable identity when several parts are selected.
func FirstVolume(name string) string {
	match := multipartSuffix.FindStringSubmatchIndex(name)
	if match == nil {
		return name
	}
	return name[:match[4]] + "001"
}

type multipartInput struct {
	directory string
	name      string
	files     []*os.File
	paths     []string
	infos     []os.FileInfo
}

func (m *multipartInput) close() {
	if m == nil {
		return
	}
	for _, file := range m.files {
		_ = file.Close()
	}
	_ = os.RemoveAll(m.directory)
}

// Only validated, pinned regular files are exposed to 7-Zip. The private
// directory preserves volume names without copying potentially large archives
// or letting the tool discover unvalidated siblings in the source directory.
func (e *Extractor) prepareMultipart(ctx context.Context, root filesystem.Root, absolute string) (_ *multipartInput, err error) {
	if !multipartSuffix.MatchString(absolute) {
		return nil, nil
	}
	first := FirstVolume(absolute)
	prefix := first[:len(first)-len(filepath.Ext(first))]
	width := len(filepath.Ext(first)) - 1
	entries, err := e.resolver.IO().ReadDir(filepath.Dir(first))
	if err != nil {
		return nil, err
	}
	var names []string
	for _, entry := range entries {
		candidate := filepath.Join(filepath.Dir(first), entry.Name())
		if multipartSuffix.MatchString(candidate) && FirstVolume(candidate) == first {
			names = append(names, candidate)
		}
	}
	sort.Slice(names, func(i, j int) bool {
		a, _ := strconv.Atoi(filepath.Ext(names[i])[1:])
		b, _ := strconv.Atoi(filepath.Ext(names[j])[1:])
		return a < b
	})
	if len(names) == 0 {
		return nil, os.ErrNotExist
	}
	m := &multipartInput{}
	defer func() {
		if err != nil {
			m.close()
		}
	}()
	m.directory, err = os.MkdirTemp("", "webfile-volumes-")
	if err != nil {
		return nil, err
	}
	for index, name := range names {
		if err = ctx.Err(); err != nil {
			return nil, err
		}
		if name != fmt.Sprintf("%s.%0*d", prefix, width, index+1) {
			return nil, errors.New("archive volume is missing")
		}
		if err = root.ValidatePath(name); err != nil {
			return nil, err
		}
		var file *os.File
		file, err = e.resolver.IO().OpenFile(name, os.O_RDONLY|unix.O_NONBLOCK, 0)
		if err != nil {
			return nil, err
		}
		m.files = append(m.files, file)
		var info os.FileInfo
		info, err = file.Stat()
		if err != nil {
			return nil, err
		}
		if !info.Mode().IsRegular() {
			return nil, errors.New("archive volume is not a regular file")
		}
		m.paths = append(m.paths, name)
		m.infos = append(m.infos, info)
		// fd 3 is the primary archive; fd 4 remains the extraction destination.
		fd := 3
		if index > 0 {
			fd = index + 4
		}
		err = os.Symlink(fmt.Sprintf("/proc/self/fd/%d", fd), filepath.Join(m.directory, filepath.Base(name)))
		if err != nil {
			return nil, err
		}
	}
	m.name = filepath.Join(m.directory, filepath.Base(first))
	return m, nil
}

func (m *multipartInput) extraFiles(destination *os.File) []*os.File {
	files := []*os.File{m.files[0], destination}
	return append(files, m.files[1:]...)
}

func (e *Extractor) deleteMultipart(m *multipartInput, result ExtractResult) ExtractResult {
	for index, name := range m.paths {
		info, err := e.resolver.IO().Lstat(name)
		original := m.infos[index]
		if err != nil || !info.Mode().IsRegular() || !os.SameFile(original, info) || info.Size() != original.Size() || !info.ModTime().Equal(original.ModTime()) {
			result.ArchiveRetained = "archive kept because a volume changed or is unavailable"
			return result
		}
	}
	for _, name := range m.paths {
		if err := e.resolver.IO().Remove(name); err != nil {
			result.ArchiveRetained = "some archive volumes could not be deleted"
			return result
		}
	}
	result.ArchiveDeleted = true
	return result
}
