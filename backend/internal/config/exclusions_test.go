package config

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"webfile/internal/filesystem"
)

func TestExactExclusionsPreserveFilenameWhitespace(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{" secret ", "secret", "private ", "private", " "} {
		if err := os.WriteFile(filepath.Join(root, name), []byte("data"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	cfg := Default()
	cfg.Roots = []Root{{Name: "Files", Path: root, ExcludedNames: []string{" secret ", " "}, ExcludedPaths: []string{"/private "}}}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	resolver, err := filesystem.NewResolver([]filesystem.Root{{Name: "Files", Path: root, ExcludedNames: cfg.Roots[0].ExcludedNames, ExcludedPaths: cfg.Roots[0].ExcludedPaths}})
	if err != nil {
		t.Fatal(err)
	}
	service := filesystem.NewService(resolver, 1024)
	defer service.Close()
	entries, err := service.List(context.Background(), "/Files")
	if err != nil {
		t.Fatal(err)
	}
	visible := map[string]bool{}
	for _, entry := range entries {
		visible[entry.Name] = true
	}
	if len(visible) != 2 || !visible["secret"] || !visible["private"] {
		t.Fatalf("incorrect exact exclusions: %v", visible)
	}
	if _, _, err := resolver.ResolveExisting("/Files/private "); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("excluded path accessible: %v", err)
	}
	if _, _, err := resolver.ResolveExisting("/Files/private"); err != nil {
		t.Fatalf("unexcluded path rejected: %v", err)
	}
}
