package archive

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"webfile/internal/filesystem"
)

func TestDeleteArchiveOnlyAfterCompleteExtraction(t *testing.T) {
	for _, name := range []string{"enabled", "disabled", "skipped", "failed", "cancelled", "replaced", "modified", "symlink", "delete denied"} {
		t.Run(name, func(t *testing.T) {
			if name == "delete denied" && os.Geteuid() == 0 {
				t.Skip("requires filesystem permission enforcement")
			}
			root := t.TempDir()
			sourceDir := filepath.Join(root, "source")
			if err := os.Mkdir(sourceDir, 0o700); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = os.Chmod(sourceDir, 0o700) })
			source := filepath.Join(sourceDir, "sample.zip")
			writeTestFile(t, source, "archive")
			if name == "symlink" {
				if err := os.Rename(source, filepath.Join(sourceDir, "target.zip")); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink("target.zip", source); err != nil {
					t.Fatal(err)
				}
			}
			destination := filepath.Join(root, "out")
			if err := os.Mkdir(destination, 0o700); err != nil {
				t.Fatal(err)
			}
			if name == "skipped" {
				writeTestFile(t, filepath.Join(destination, "file.txt"), "existing")
			}
			script := "#!/bin/sh\nset -eu\nprintf extracted > \"$1/file.txt\"\n"
			if name == "failed" {
				script = "#!/bin/sh\nexit 9\n"
			}
			target := New(Config{Executable: writeTool(t, script), ExtractArgs: []string{"{destination}"}}, testResolver(t, filesystem.Root{Name: "Files", Path: root}))
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			changed := false
			result, err := target.ExtractInteractiveWithOptions(ctx, "/Files/source/sample.zip", "/Files/out", nil, func(progress Progress) {
				if progress.Phase != "publishing" || changed {
					return
				}
				changed = true
				switch name {
				case "cancelled":
					cancel()
				case "replaced":
					replacement := filepath.Join(sourceDir, "replacement")
					writeTestFile(t, replacement, "replacement archive")
					if err := os.Rename(replacement, source); err != nil {
						t.Fatal(err)
					}
				case "modified":
					writeTestFile(t, source, "changed")
					later := time.Now().Add(time.Hour)
					if err := os.Chtimes(source, later, later); err != nil {
						t.Fatal(err)
					}
				case "delete denied":
					if err := os.Chmod(sourceDir, 0o500); err != nil {
						t.Fatal(err)
					}
				}
			}, ExtractOptions{ExistingFiles: ExistingFilesSkip, DeleteArchive: name != "disabled"})
			if name == "failed" || name == "cancelled" {
				if err == nil {
					t.Fatal("expected unsuccessful extraction")
				}
				if name == "cancelled" && !errors.Is(err, context.Canceled) {
					t.Fatalf("error=%v", err)
				}
			} else if err != nil {
				t.Fatal(err)
			}
			_, statErr := os.Lstat(source)
			if name == "enabled" {
				if !errors.Is(statErr, os.ErrNotExist) || !result.ArchiveDeleted {
					t.Fatalf("source not deleted: result=%+v error=%v", result, statErr)
				}
			} else {
				if statErr != nil || result.ArchiveDeleted {
					t.Fatalf("source not retained: result=%+v error=%v", result, statErr)
				}
			}
			if name != "failed" && name != "cancelled" {
				content, err := os.ReadFile(filepath.Join(destination, "file.txt"))
				if err != nil {
					t.Fatal(err)
				}
				want := "extracted"
				if name == "skipped" {
					want = "existing"
				}
				if string(content) != want {
					t.Fatalf("extracted content=%q", content)
				}
				if name != "enabled" && name != "disabled" && result.ArchiveRetained == "" {
					t.Fatalf("missing retention reason: %+v", result)
				}
			}
			if name == "symlink" {
				if _, err := os.Stat(filepath.Join(sourceDir, "target.zip")); err != nil {
					t.Fatal(err)
				}
			}
			assertNoStaging(t, destination)
		})
	}
}
