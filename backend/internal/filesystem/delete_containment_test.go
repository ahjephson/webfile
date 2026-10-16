//go:build linux

package filesystem

import (
	"context"
	"image/color"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func TestDeleteRemainsConfinedWhenParentChangesAfterValidation(t *testing.T) {
	for _, directory := range []bool{false, true} {
		name := "file"
		if directory {
			name = "directory"
		}
		t.Run(name, func(t *testing.T) {
			service, root := testService(t)
			outside := t.TempDir()
			slot := filepath.Join(root, "slot")
			parked := filepath.Join(root, "parked")
			relative := "photo.png"
			if directory {
				relative = filepath.Join("tree", relative)
			}
			for _, parent := range []string{slot, outside} {
				if err := os.MkdirAll(filepath.Dir(filepath.Join(parent, relative)), 0o700); err != nil {
					t.Fatal(err)
				}
				writeThumbnailTestImage(t, filepath.Join(parent, relative), color.RGBA{R: 255, A: 255})
			}
			var once sync.Once
			swap := func() {
				once.Do(func() {
					if err := os.Rename(slot, parked); err != nil {
						t.Fatal(err)
					}
					if err := os.Symlink(outside, slot); err != nil {
						t.Fatal(err)
					}
				})
			}
			target := "/Files/slot/photo.png"
			if directory {
				target = "/Files/slot/tree"
			}
			err := service.DeleteWithProgress(context.Background(), target, func(int64) { swap() })
			if _, statErr := os.Stat(filepath.Join(outside, relative)); statErr != nil {
				t.Fatalf("delete reached outside the root: %v (delete result: %v)", statErr, err)
			}
			if err == nil {
				t.Fatal("parent replacement should have rejected deletion")
			}
			if _, err := os.Stat(filepath.Join(parked, relative)); err != nil {
				t.Fatalf("original file unexpectedly changed: %v", err)
			}
		})
	}
}
