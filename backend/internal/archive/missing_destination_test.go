package archive

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"webfile/internal/filesystem"
)

func TestExtractCreatesMissingDestinationParents(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, filepath.Join(root, "sample.zip"), "archive")
	tool := writeTool(t, "#!/bin/sh\nset -eu\nprintf ok > \"$1/file.txt\"\n")
	target := New(Config{Executable: tool, ExtractArgs: []string{"{destination}"}}, testResolver(t, filesystem.Root{Name: "Files", Path: root}))
	count, err := target.Extract(context.Background(), "/Files/sample.zip", "/Files/new/parent/output", "")
	if err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("count=%d", count)
	}
	content, err := os.ReadFile(filepath.Join(root, "new/parent/output/file.txt"))
	if err != nil || string(content) != "ok" {
		t.Fatalf("content=%q, error=%v", content, err)
	}
	assertNoStaging(t, filepath.Join(root, "new/parent"))
}

func TestMissingDestinationCannotCreateOutsideRootOrInExcludedPath(t *testing.T) {
	for _, mode := range []string{"outside symlink", "excluded", "excluded symlink", "file parent"} {
		t.Run(mode, func(t *testing.T) {
			root, outside := t.TempDir(), t.TempDir()
			config := filesystem.Root{Name: "Files", Path: root, ExcludedPaths: []string{"/private"}}
			writeTestFile(t, filepath.Join(root, "sample.zip"), "archive")
			destination := "/Files/link/new/parent/output"
			switch mode {
			case "outside symlink":
				if err := os.Symlink(outside, filepath.Join(root, "link")); err != nil {
					t.Fatal(err)
				}
			case "excluded":
				destination = "/Files/private/new/parent/output"
			case "excluded symlink":
				if err := os.Mkdir(filepath.Join(root, "private"), 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(filepath.Join(root, "private"), filepath.Join(root, "link")); err != nil {
					t.Fatal(err)
				}
			case "file parent":
				writeTestFile(t, filepath.Join(root, "link"), "file")
			}
			target := New(Config{Executable: writeTool(t, "#!/bin/sh\nexit 99\n"), ExtractArgs: []string{"{destination}"}}, testResolver(t, config))
			if _, err := target.Extract(context.Background(), "/Files/sample.zip", destination, ""); err == nil {
				t.Fatal("accepted invalid destination")
			}
			for _, candidate := range []string{filepath.Join(outside, "new"), filepath.Join(root, "private/new")} {
				if _, err := os.Lstat(candidate); !os.IsNotExist(err) {
					t.Fatalf("created forbidden path %s: %v", candidate, err)
				}
			}
		})
	}
}
