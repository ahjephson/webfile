package filesystem

import (
	"context"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"
)

type SearchRequest struct {
	Path          string `json:"path"`
	Query         string `json:"query"`
	Wildcard      bool   `json:"wildcard"`
	CaseSensitive bool   `json:"case_sensitive"`
	Kind          string `json:"kind"`
	ShowHidden    bool   `json:"show_hidden"`
}
type SearchUpdate struct {
	Entry     *Entry `json:"entry,omitempty"`
	Scanned   int    `json:"scanned"`
	Skipped   int    `json:"skipped"`
	Truncated bool   `json:"truncated"`
	Done      bool   `json:"done"`
	Error     string `json:"error,omitempty"`
}

func (r SearchRequest) Validate() error {
	if strings.TrimSpace(r.Query) == "" || len(r.Query) > 256 {
		return fmt.Errorf("search requires a filename query of 1–256 characters")
	}
	if r.Kind != "" && r.Kind != "all" && r.Kind != "files" && r.Kind != "folders" {
		return fmt.Errorf("invalid search kind")
	}
	if r.Wildcard {
		if _, err := path.Match(r.Query, ""); err != nil {
			return fmt.Errorf("invalid wildcard pattern")
		}
	}
	return nil
}

func (s *Service) Search(ctx context.Context, request SearchRequest, emit func(SearchUpdate) error) error {
	if err := request.Validate(); err != nil {
		return err
	}
	select {
	case s.searchSlots <- struct{}{}:
		defer func() { <-s.searchSlots }()
	default:
		return fmt.Errorf("another search is running; cancel it before starting a new one")
	}
	root, abs, err := s.resolver.ResolveExisting(request.Path)
	if err != nil {
		return err
	}
	if info, err := os.Lstat(abs); err != nil {
		return err
	} else if !info.IsDir() {
		return ErrNotDirectory
	}
	fs, err := os.OpenRoot(abs)
	if err != nil {
		return err
	}
	defer fs.Close()
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	query := request.Query
	if !request.CaseSensitive {
		query = strings.ToLower(query)
	}
	excluded := makeNameSet(root.ExcludedNames)
	update := SearchUpdate{}
	matches := 0
	last := time.Now()
	var walk func(string, int) error
	walk = func(rel string, depth int) error {
		if depth > 64 {
			update.Truncated = true
			return nil
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		f, err := fs.Open(rel)
		if err != nil {
			update.Skipped++
			return nil
		}
		defer f.Close()
		opened, e := f.Stat()
		linked, e2 := fs.Lstat(rel)
		if e != nil || e2 != nil || !linked.IsDir() || !os.SameFile(opened, linked) {
			update.Skipped++
			return nil
		}
		for {
			batch, readErr := f.ReadDir(128)
			if readErr != nil && readErr != io.EOF {
				update.Skipped++
				return nil
			}
			for _, item := range batch {
				if err := ctx.Err(); err != nil {
					return err
				}
				if update.Scanned >= 100000 || matches >= 1000 {
					update.Truncated = true
					return nil
				}
				name := item.Name()
				child := filepath.Join(rel, name)
				absolute := filepath.Join(abs, child)
				if _, hidden := excluded[name]; hidden || strings.HasPrefix(name, ".webfile-") || isExcludedPath(root, absolute) || (!request.ShowHidden && strings.HasPrefix(name, ".")) {
					continue
				}
				info, e := fs.Lstat(child)
				if e != nil {
					update.Skipped++
					continue
				}
				if !info.IsDir() && !info.Mode().IsRegular() {
					continue
				}
				update.Scanned++
				readable := false
				if info.IsDir() {
					readable = canNavigateDirectory(absolute)
				} else {
					readable = canReadFile(absolute)
				}
				if !readable {
					update.Skipped++
					continue
				}
				value := name
				if !request.CaseSensitive {
					value = strings.ToLower(value)
				}
				matched := strings.Contains(value, query)
				if request.Wildcard {
					matched, _ = path.Match(query, value)
				}
				if matched && (request.Kind == "" || request.Kind == "all" || request.Kind == "folders" && info.IsDir() || request.Kind == "files" && !info.IsDir()) {
					entry := toEntry(path.Join(CleanVirtual(request.Path), filepath.ToSlash(filepath.Dir(child))), name, info)
					update.Entry = &entry
					matches++
					if err := emit(update); err != nil {
						return err
					}
					update.Entry = nil
					last = time.Now()
				} else if time.Since(last) > 100*time.Millisecond {
					if err := emit(update); err != nil {
						return err
					}
					last = time.Now()
				}
				if info.IsDir() {
					if err := walk(child, depth+1); err != nil {
						return err
					}
					if update.Truncated {
						return nil
					}
				}
			}
			if readErr == io.EOF {
				return nil
			}
		}
	}
	err = walk(".", 0)
	if ctx.Err() != nil && ctx.Err() != context.Canceled {
		update.Truncated = true
	}
	if err != nil && err != context.DeadlineExceeded {
		return err
	}
	update.Done = true
	return emit(update)
}
