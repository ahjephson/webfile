package archive

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"webfile/internal/filesystem"
)

func TestExtractionRejectsExcludedEntriesBeforePublishing(t *testing.T) {
	for _, tc := range []struct {
		name, destination, relative, excluded string
		existing                              bool
	}{
		{"existing file", "/Files", "protected.txt", "/protected.txt", true},
		{"nested subtree", "/Files", "private/blocked.txt", "/private", true},
		{"new file", "/Files", "blocked.txt", "/blocked.txt", false},
		{"new destination", "/Files/new", "blocked.txt", "/new/blocked.txt", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			writeTestFile(t, filepath.Join(root, "input.zip"), "archive")
			if tc.existing {
				if err := os.MkdirAll(filepath.Join(root, filepath.Dir(tc.relative)), 0700); err != nil {
					t.Fatal(err)
				}
				writeTestFile(t, filepath.Join(root, tc.relative), "original")
			}
			resolver := testResolver(t, filesystem.Root{Name: "Files", Path: root, ExcludedPaths: []string{tc.excluded}})
			tool := writeTool(t, "#!/bin/sh\nset -eu\nmkdir -p \"$1/"+filepath.Dir(tc.relative)+"\"\nprintf safe > \"$1/a-safe.txt\"\nprintf replacement > \"$1/"+tc.relative+"\"\n")
			extractor := New(Config{Executable: tool, ExtractArgs: []string{"{destination}"}}, resolver)
			_, err := extractor.ExtractInteractiveWithOptions(context.Background(), "/Files/input.zip", tc.destination, nil, nil, ExtractOptions{ExistingFiles: ExistingFilesOverwrite})
			if err == nil {
				t.Fatal("extraction accepted an excluded destination")
			}
			destination := root
			if tc.destination == "/Files/new" {
				destination = filepath.Join(root, "new")
			}
			if _, err := os.Stat(filepath.Join(destination, "a-safe.txt")); !os.IsNotExist(err) {
				t.Fatalf("partial publication: %v", err)
			}
			if tc.existing {
				data, err := os.ReadFile(filepath.Join(root, tc.relative))
				if err != nil || string(data) != "original" {
					t.Fatalf("excluded file changed: %q %v", data, err)
				}
			} else if _, err := os.Stat(filepath.Join(destination, tc.relative)); !os.IsNotExist(err) {
				t.Fatalf("excluded file created: %v", err)
			}
			assertNoStaging(t, root)
		})
	}
}
