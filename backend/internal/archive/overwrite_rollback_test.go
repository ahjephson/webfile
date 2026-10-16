package archive

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestOverwritePublicationRollsBackEarlierReplacements(t *testing.T) {
	for _, cancelAt := range []int64{0, 2, 3} {
		name := "late-conflict"
		if cancelAt == 2 {
			name = "cancel"
		} else if cancelAt == 3 {
			name = "cancel-after-last-file"
		}
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			stage, destination := filepath.Join(root, "stage"), filepath.Join(root, "destination")
			for _, directory := range []string{stage, destination} {
				if err := os.Mkdir(directory, 0700); err != nil {
					t.Fatal(err)
				}
			}
			writeTestFile(t, filepath.Join(stage, "a.txt"), "new a")
			writeTestFile(t, filepath.Join(stage, "b.txt"), "new b")
			writeTestFile(t, filepath.Join(stage, "z.txt"), "new z")
			original := filepath.Join(destination, "a.txt")
			writeTestFile(t, original, "original a")
			if err := os.Chmod(original, 0640); err != nil {
				t.Fatal(err)
			}
			before, err := os.Stat(original)
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			_, err = publishStage(ctx, stage, destination, true, ExistingFilesOverwrite, func(progress Progress) {
				if progress.Phase == "publishing" {
					if cancelAt != 0 && progress.ItemsDone == cancelAt {
						cancel()
					} else if cancelAt == 0 && progress.ItemsDone == 2 {
						if err := os.Mkdir(filepath.Join(destination, "z.txt"), 0700); err != nil {
							t.Fatal(err)
						}
					}
				}
			})
			if err == nil {
				t.Fatal("expected publication failure")
			}
			if cancelAt != 0 && !errors.Is(err, context.Canceled) {
				t.Fatalf("cancellation error = %v", err)
			}
			data, err := os.ReadFile(original)
			if err != nil || string(data) != "original a" {
				t.Fatalf("original contents lost: %q %v", data, err)
			}
			after, err := os.Stat(original)
			if err != nil || !os.SameFile(before, after) || before.Mode() != after.Mode() || !before.ModTime().Equal(after.ModTime()) {
				t.Fatalf("original inode/metadata not restored: %v %v", after, err)
			}
			if _, err := os.Stat(filepath.Join(destination, "b.txt")); !os.IsNotExist(err) {
				t.Fatalf("new file not rolled back: %v", err)
			}
			if _, err := os.Stat(stage); !os.IsNotExist(err) {
				t.Fatalf("temporary stage retained after successful rollback: %v", err)
			}
		})
	}
}

func TestFailedOverwriteRollbackPreservesOriginalAndConcurrentChanges(t *testing.T) {
	for _, cancelRun := range []bool{false, true} {
		name := "conflict"
		if cancelRun {
			name = "cancel"
		}
		t.Run(name, func(t *testing.T) { testFailedOverwriteRollback(t, cancelRun) })
	}
}

func testFailedOverwriteRollback(t *testing.T, cancelRun bool) {
	root := t.TempDir()
	stage, destination := filepath.Join(root, "stage"), filepath.Join(root, "destination")
	for _, directory := range []string{stage, destination} {
		if err := os.Mkdir(directory, 0700); err != nil {
			t.Fatal(err)
		}
	}
	writeTestFile(t, filepath.Join(stage, "a.txt"), "new a")
	writeTestFile(t, filepath.Join(stage, "z.txt"), "new z")
	writeTestFile(t, filepath.Join(destination, "a.txt"), "original a")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	_, err := publishStage(ctx, stage, destination, true, ExistingFilesOverwrite, func(progress Progress) {
		if progress.Phase == "publishing" && progress.ItemsDone == 1 {
			writeTestFile(t, filepath.Join(destination, "a.txt"), "concurrent edit")
			if cancelRun {
				cancel()
			} else if err := os.Mkdir(filepath.Join(destination, "z.txt"), 0700); err != nil {
				t.Fatal(err)
			}
		}
	})
	if err == nil || !strings.Contains(err.Error(), stage) {
		t.Fatalf("recovery location not reported: %v", err)
	}
	var recovery *PublicationRecoveryError
	if !errors.As(err, &recovery) || recovery.Directory != stage {
		t.Fatalf("missing recovery error: %v", err)
	}
	if cancelRun && !errors.Is(err, context.Canceled) {
		t.Fatalf("lost cancellation cause: %v", err)
	}
	for path, expected := range map[string]string{filepath.Join(destination, "a.txt"): "concurrent edit", filepath.Join(stage, "a.txt"): "original a"} {
		data, err := os.ReadFile(path)
		if err != nil || string(data) != expected {
			t.Fatalf("data lost at %s: %q %v", path, data, err)
		}
	}
}
