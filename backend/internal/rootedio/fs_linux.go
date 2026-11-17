// Package rootedio confines canonical absolute paths to pinned filesystem roots.
// Callers resolve intentional symlinks first; traversal never follows a symlink
// substituted after that resolution, including links to another allowed root.
package rootedio

import (
	"crypto/rand"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/sys/unix"
)

type FS struct {
	mu    sync.Mutex
	roots map[string]*os.File
}

func New(paths ...string) *FS {
	disk := &FS{roots: make(map[string]*os.File)}
	for _, path := range paths {
		path = filepath.Clean(path)
		disk.roots[path] = nil
		_, _ = disk.anchor(path)
	}
	return disk
}

func (d *FS) Close() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	var result error
	for path, root := range d.roots {
		if root != nil {
			result = errors.Join(result, root.Close())
		}
		delete(d.roots, path)
	}
	return result
}

func (d *FS) anchor(path string) (*os.File, error) {
	if root := d.roots[path]; root != nil {
		return root, nil
	}
	fd, err := unix.Open("/", unix.O_PATH|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	for _, part := range strings.Split(strings.TrimPrefix(path, "/"), "/") {
		if part == "" {
			continue
		}
		next, openErr := unix.Openat(fd, part, unix.O_PATH|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
		unix.Close(fd)
		if openErr != nil {
			return nil, openErr
		}
		fd = next
	}
	file := os.NewFile(uintptr(fd), path)
	d.roots[path] = file
	return file, nil
}

func (d *FS) parent(path string) (*os.File, string, error) {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return nil, "", fs.ErrPermission
	}
	d.mu.Lock()
	selected := ""
	for root := range d.roots {
		if path == root || strings.HasPrefix(path, strings.TrimSuffix(root, "/")+"/") {
			if len(root) > len(selected) {
				selected = root
			}
		}
	}
	if selected == "" {
		d.mu.Unlock()
		return nil, "", fs.ErrPermission
	}
	root, err := d.anchor(selected)
	var fd int
	if err == nil {
		fd, err = unix.Openat(int(root.Fd()), ".", unix.O_PATH|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	}
	d.mu.Unlock()
	if err != nil {
		return nil, "", err
	}
	relative, err := filepath.Rel(selected, path)
	if err != nil {
		unix.Close(fd)
		return nil, "", err
	}
	for _, part := range strings.Split(filepath.Dir(relative), "/") {
		if part == "." {
			continue
		}
		next, openErr := unix.Openat(fd, part, unix.O_PATH|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
		unix.Close(fd)
		if openErr != nil {
			return nil, "", openErr
		}
		fd = next
	}
	return os.NewFile(uintptr(fd), filepath.Dir(path)), filepath.Base(relative), nil
}

func (d *FS) OpenFile(path string, flags int, mode fs.FileMode) (file *os.File, result error) {
	defer func() {
		if result != nil {
			result = &os.PathError{Op: "open", Path: path, Err: result}
		}
	}()
	parent, name, err := d.parent(path)
	if err != nil {
		return nil, err
	}
	defer parent.Close()
	fd, err := unix.Openat(int(parent.Fd()), name, flags|unix.O_NOFOLLOW|unix.O_CLOEXEC|unix.O_NONBLOCK, uint32(mode.Perm()))
	if err != nil {
		return nil, err
	}
	return os.NewFile(uintptr(fd), path), nil
}

func (d *FS) Open(path string) (*os.File, error) {
	return d.OpenFile(path, os.O_RDONLY, 0)
}

func (d *FS) OpenRoot(path string) (*os.Root, error) {
	file, err := d.OpenFile(path, os.O_RDONLY|unix.O_DIRECTORY, 0)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	return os.OpenRoot("/proc/self/fd/" + strconv.FormatUint(uint64(file.Fd()), 10))
}

func (d *FS) Lstat(path string) (fs.FileInfo, error) {
	file, err := d.OpenFile(path, unix.O_PATH, 0)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	return file.Stat()
}

func (d *FS) Stat(path string) (fs.FileInfo, error) {
	info, err := d.Lstat(path)
	if err == nil && info.Mode()&fs.ModeSymlink != 0 {
		return nil, unix.ELOOP
	}
	return info, err
}

func (d *FS) ReadDir(path string) ([]fs.DirEntry, error) {
	file, err := d.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	entries, err := file.ReadDir(-1)
	if err != nil {
		return nil, err
	}
	safe := make([]fs.DirEntry, 0, len(entries))
	for _, entry := range entries {
		info, err := d.Lstat(filepath.Join(path, entry.Name()))
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, err
		}
		safe = append(safe, fs.FileInfoToDirEntry(info))
	}
	entries = safe
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	return entries, err
}

func (d *FS) Mkdir(path string, mode fs.FileMode) error {
	parent, name, err := d.parent(path)
	if err != nil {
		return err
	}
	defer parent.Close()
	return unix.Mkdirat(int(parent.Fd()), name, uint32(mode.Perm()))
}

func (d *FS) MkdirAll(path string, mode fs.FileMode) error {
	if info, err := d.Stat(path); err == nil {
		if info.IsDir() {
			return nil
		}
		return unix.ENOTDIR
	} else if !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	if err := d.MkdirAll(filepath.Dir(path), mode); err != nil {
		return err
	}
	if err := d.Mkdir(path, mode); err != nil && !errors.Is(err, fs.ErrExist) {
		return err
	}
	info, err := d.Stat(path)
	if err == nil && !info.IsDir() {
		return unix.ENOTDIR
	}
	return err
}

func temporaryName(pattern string) string {
	index := strings.LastIndex(pattern, "*")
	if index < 0 {
		return pattern + rand.Text()
	}
	return pattern[:index] + rand.Text() + pattern[index+1:]
}

func (d *FS) CreateTemp(dir, pattern string) (*os.File, error) {
	for attempts := 0; attempts < 100; attempts++ {
		name := filepath.Join(dir, temporaryName(pattern))
		file, err := d.OpenFile(name, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0600)
		if errors.Is(err, fs.ErrExist) {
			continue
		}
		return file, err
	}
	return nil, fs.ErrExist
}

func (d *FS) MkdirTemp(dir, pattern string) (string, error) {
	for attempts := 0; attempts < 100; attempts++ {
		name := filepath.Join(dir, temporaryName(pattern))
		err := d.Mkdir(name, 0700)
		if errors.Is(err, fs.ErrExist) {
			continue
		}
		return name, err
	}
	return "", fs.ErrExist
}

func (d *FS) Remove(path string) error {
	parent, name, err := d.parent(path)
	if err != nil {
		return err
	}
	defer parent.Close()
	err = unix.Unlinkat(int(parent.Fd()), name, 0)
	if errors.Is(err, unix.EISDIR) || errors.Is(err, unix.EPERM) {
		err = unix.Unlinkat(int(parent.Fd()), name, unix.AT_REMOVEDIR)
	}
	return err
}

func (d *FS) RemoveAll(path string) error {
	info, err := d.Lstat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if info.IsDir() {
		entries, err := d.ReadDir(path)
		if err != nil {
			return err
		}
		for _, entry := range entries {
			if err := d.RemoveAll(filepath.Join(path, entry.Name())); err != nil {
				return err
			}
		}
	}
	return d.Remove(path)
}

func (d *FS) Chmod(path string, mode fs.FileMode) error {
	file, err := d.OpenFile(path, unix.O_PATH, 0)
	if err != nil {
		return err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return err
	}
	if info.Mode()&fs.ModeSymlink != 0 {
		return unix.ELOOP
	}
	// chmod follows this process-owned descriptor, never a mutable user pathname.
	// Unlike fchmodat2, this also works on supported kernels older than Linux 6.5.
	return os.Chmod("/proc/self/fd/"+strconv.FormatUint(uint64(file.Fd()), 10), mode)
}

func (d *FS) Lchown(path string, uid, gid int) error {
	parent, name, err := d.parent(path)
	if err != nil {
		return err
	}
	defer parent.Close()
	return unix.Fchownat(int(parent.Fd()), name, uid, gid, unix.AT_SYMLINK_NOFOLLOW)
}

func (d *FS) Chtimes(path string, at, mt time.Time) error {
	parent, name, err := d.parent(path)
	if err != nil {
		return err
	}
	defer parent.Close()
	times := []unix.Timespec{unix.NsecToTimespec(at.UnixNano()), unix.NsecToTimespec(mt.UnixNano())}
	return unix.UtimesNanoAt(int(parent.Fd()), name, times, unix.AT_SYMLINK_NOFOLLOW)
}

func (d *FS) Readlink(path string) (string, error) {
	parent, name, err := d.parent(path)
	if err != nil {
		return "", err
	}
	defer parent.Close()
	buffer := make([]byte, 4096)
	n, err := unix.Readlinkat(int(parent.Fd()), name, buffer)
	if n == len(buffer) {
		return "", unix.ENAMETOOLONG
	}
	return string(buffer[:max(0, n)]), err
}

func (d *FS) Symlink(target, path string) error {
	parent, name, err := d.parent(path)
	if err != nil {
		return err
	}
	defer parent.Close()
	return unix.Symlinkat(target, int(parent.Fd()), name)
}

func (d *FS) pair(source, target string, operation func(int, string, int, string) error) (result error) {
	defer func() {
		if result != nil {
			result = &os.LinkError{Op: "publish", Old: source, New: target, Err: result}
		}
	}()
	from, name, err := d.parent(source)
	if err != nil {
		return err
	}
	defer from.Close()
	to, dest, err := d.parent(target)
	if err != nil {
		return err
	}
	defer to.Close()
	return operation(int(from.Fd()), name, int(to.Fd()), dest)
}

func (d *FS) Link(source, target string) error {
	return d.pair(source, target, func(a int, b string, c int, e string) error { return unix.Linkat(a, b, c, e, 0) })
}

func (d *FS) Rename(source, target string) error {
	return d.pair(source, target, unix.Renameat)
}

func (d *FS) RenameFlags(source, target string, flags uint) error {
	return d.pair(source, target, func(a int, b string, c int, e string) error { return unix.Renameat2(a, b, c, e, flags) })
}

func (d *FS) WalkDir(root string, walk fs.WalkDirFunc) error {
	info, err := d.Lstat(root)
	var entry fs.DirEntry
	if err == nil {
		entry = fs.FileInfoToDirEntry(info)
	}
	var visit func(string, fs.DirEntry, error) error
	visit = func(path string, entry fs.DirEntry, failure error) error {
		if err := walk(path, entry, failure); err != nil || failure != nil || !entry.IsDir() {
			if err == fs.SkipDir && entry != nil && entry.IsDir() {
				return nil
			}
			return err
		}
		entries, err := d.ReadDir(path)
		if err != nil {
			return walk(path, entry, err)
		}
		for _, child := range entries {
			if err := visit(filepath.Join(path, child.Name()), child, nil); err != nil {
				if err == fs.SkipDir {
					break
				}
				return err
			}
		}
		return nil
	}
	err = visit(root, entry, err)
	if err == fs.SkipAll || err == fs.SkipDir {
		return nil
	}
	return err
}
