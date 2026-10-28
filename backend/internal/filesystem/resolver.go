package filesystem

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"

	"webfile/internal/clienterror"
	"webfile/internal/thumbnailcache"
)

var (
	ErrInvalidPath = clienterror.New("invalid path", clienterror.InvalidPath)
	ErrOutsideRoot = clienterror.New("path escapes configured root", clienterror.InvalidPath)
)

type Root struct {
	Name          string
	Path          string
	ExcludedNames []string
	ExcludedPaths []string
	cacheID       string
}

type Resolver struct {
	roots     map[string]Root
	rootOrder []Root
}

func NewResolver(roots []Root) (*Resolver, error) {
	r := &Resolver{roots: make(map[string]Root, len(roots)), rootOrder: make([]Root, 0, len(roots))}
	for _, root := range roots {
		root.cacheID = thumbnailcache.RootKey(root.Name, root.Path, root.ExcludedNames, root.ExcludedPaths)
		root.Path = filepath.Clean(root.Path)
		if resolved, err := filepath.EvalSymlinks(root.Path); err == nil {
			root.Path = resolved
		}
		for index, excludedPath := range root.ExcludedPaths {
			root.ExcludedPaths[index] = path.Clean("/" + strings.TrimPrefix(excludedPath, "/"))
		}
		r.roots[root.Name] = root
		r.rootOrder = append(r.rootOrder, root)
	}
	return r, nil
}

func (r *Resolver) Roots() []Root {
	out := make([]Root, len(r.rootOrder))
	copy(out, r.rootOrder)
	return out
}

func CleanVirtual(v string) string {
	if v == "" {
		return "/"
	}
	return path.Clean("/" + v)
}

func SplitVirtual(v string) (rootName, rel string, err error) {
	v = CleanVirtual(v)
	if v == "/" {
		return "", "", nil
	}
	parts := strings.Split(strings.TrimPrefix(v, "/"), "/")
	if len(parts) == 0 || parts[0] == "" {
		return "", "", ErrInvalidPath
	}
	rootName = parts[0]
	if len(parts) > 1 {
		rel = path.Clean(strings.Join(parts[1:], "/"))
		if rel == "." {
			rel = ""
		}
	}
	return rootName, rel, nil
}

func IsConfiguredRootPath(v string) bool {
	name, rel, err := SplitVirtual(v)
	return err == nil && name != "" && rel == ""
}

func VirtualJoin(base, name string) string {
	return path.Clean(CleanVirtual(base) + "/" + name)
}

func (r *Resolver) ResolveExisting(v string) (Root, string, error) {
	root, candidate, err := r.resolveLexical(v)
	if err != nil {
		return Root{}, "", err
	}
	resolved, err := filepath.EvalSymlinks(candidate)
	if err != nil {
		return Root{}, "", err
	}
	if err := ensureWithin(root.Path, resolved); err != nil {
		return Root{}, "", err
	}
	if isExcludedPath(root, resolved) {
		return Root{}, "", os.ErrNotExist
	}
	return root, resolved, nil
}

// OpenExisting keeps path traversal anchored to the configured root even if a
// directory is replaced by a symlink after ResolveExisting returns.
func (r *Resolver) OpenExisting(v string) (*os.File, error) {
	root, absolute, err := r.ResolveExisting(v)
	if err != nil {
		return nil, err
	}
	fs, err := os.OpenRoot(root.Path)
	if err != nil {
		return nil, err
	}
	defer fs.Close()
	relative, err := filepath.Rel(root.Path, absolute)
	if err != nil {
		return nil, err
	}
	return fs.Open(relative)
}

// ValidatePath checks an absolute destination against the root and its excluded
// paths without requiring the destination or its parents to exist yet.
func (r Root) ValidatePath(candidate string) error {
	if err := ensureWithin(r.Path, candidate); err != nil {
		return err
	}
	if isExcludedPath(r, candidate) {
		return os.ErrNotExist
	}
	return nil
}

// Moving an ancestor would change the root-relative identity of an excluded
// path. Require users to move visible entries individually instead.
func (r Root) validateMoveSource(ctx context.Context, source string) error {
	if len(r.ExcludedPaths) == 0 {
		return ctx.Err()
	}
	root, err := os.OpenRoot(r.Path)
	if err != nil {
		return err
	}
	defer root.Close()
	relative, err := filepath.Rel(r.Path, source)
	if err != nil {
		return err
	}
	info, err := root.Lstat(relative)
	if err != nil || !info.IsDir() {
		return err
	}
	for _, excluded := range r.ExcludedPaths {
		if err := ctx.Err(); err != nil {
			return err
		}
		candidate := filepath.Join(r.Path, filepath.FromSlash(strings.TrimPrefix(excluded, "/")))
		if !isSameOrDescendant(source, candidate) {
			continue
		}
		relative, err := filepath.Rel(r.Path, candidate)
		if err != nil {
			return err
		}
		if _, err := root.Lstat(relative); err == nil {
			return fmt.Errorf("%w: directory contains an excluded path; move visible items individually", fs.ErrPermission)
		} else if !errors.Is(err, fs.ErrNotExist) {
			return err
		}
	}
	return nil
}

// ResolveEntry resolves a path without following the final path component.
// It is used for operations such as rename/delete where a symbolic link is the
// entry being manipulated, not the object it points at. Parent symlinks are
// still resolved and confined to the configured root.
func (r *Resolver) ResolveEntry(v string) (Root, string, error) {
	root, candidate, err := r.resolveLexical(v)
	if err != nil {
		return Root{}, "", err
	}
	parent := filepath.Dir(candidate)
	resolvedParent, err := filepath.EvalSymlinks(parent)
	if err != nil {
		return Root{}, "", err
	}
	if err := ensureWithin(root.Path, resolvedParent); err != nil {
		return Root{}, "", err
	}
	entry := filepath.Join(resolvedParent, filepath.Base(candidate))
	if isExcludedPath(root, entry) {
		return Root{}, "", os.ErrNotExist
	}
	if _, err := os.Lstat(entry); err != nil {
		return Root{}, "", err
	}
	return root, entry, nil
}

func (r *Resolver) ResolveForCreate(v string) (Root, string, error) {
	root, candidate, err := r.resolveLexical(v)
	if err != nil {
		return Root{}, "", err
	}
	parent := filepath.Dir(candidate)
	resolvedParent, err := filepath.EvalSymlinks(parent)
	if err != nil {
		return Root{}, "", err
	}
	if err := ensureWithin(root.Path, resolvedParent); err != nil {
		return Root{}, "", err
	}
	entry := filepath.Join(resolvedParent, filepath.Base(candidate))
	if isExcludedPath(root, entry) {
		return Root{}, "", os.ErrNotExist
	}
	return root, entry, nil
}

func (r *Resolver) resolveLexical(v string) (Root, string, error) {
	name, rel, err := SplitVirtual(v)
	if err != nil {
		return Root{}, "", err
	}
	if name == "" {
		return Root{}, "", ErrInvalidPath
	}
	root, ok := r.roots[name]
	if !ok {
		return Root{}, "", fmt.Errorf("unknown root %q", name)
	}
	candidate := filepath.Join(root.Path, filepath.FromSlash(rel))
	if err := ensureWithin(root.Path, candidate); err != nil {
		return Root{}, "", err
	}
	if isExcludedPath(root, candidate) {
		return Root{}, "", os.ErrNotExist
	}
	return root, candidate, nil
}

func isExcludedPath(root Root, candidate string) bool {
	relative, err := filepath.Rel(root.Path, candidate)
	if err != nil || relative == "." {
		return false
	}
	virtualRelative := "/" + filepath.ToSlash(relative)
	for _, excludedPath := range root.ExcludedPaths {
		if virtualRelative == excludedPath || strings.HasPrefix(virtualRelative, excludedPath+"/") {
			return true
		}
	}
	return false
}

func ensureWithin(root, candidate string) error {
	rel, err := filepath.Rel(root, candidate)
	if err != nil {
		return err
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		return ErrOutsideRoot
	}
	return nil
}
