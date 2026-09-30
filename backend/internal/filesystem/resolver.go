package filesystem

import (
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"
)

var (
	ErrInvalidPath = errors.New("invalid path")
	ErrOutsideRoot = errors.New("path escapes configured root")
)

type Root struct {
	Name string
	Path string
}

type Resolver struct {
	roots     map[string]Root
	rootOrder []Root
}

func NewResolver(roots []Root) (*Resolver, error) {
	r := &Resolver{roots: make(map[string]Root, len(roots)), rootOrder: make([]Root, 0, len(roots))}
	for _, root := range roots {
		root.Path = filepath.Clean(root.Path)
		if resolved, err := filepath.EvalSymlinks(root.Path); err == nil {
			root.Path = resolved
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
	return path.Clean("/" + strings.TrimSpace(v))
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
	return root, resolved, nil
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
	return root, filepath.Join(resolvedParent, filepath.Base(candidate)), nil
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
	return root, candidate, nil
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
