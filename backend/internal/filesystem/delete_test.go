//go:build linux

package filesystem

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestDeleteStreamsAllChildrenAndDoesNotFollowLinks(t *testing.T) {
	service, root := testService(t)
	for _, directory := range []string{"tree/a", "tree/b"} {
		if err := os.MkdirAll(filepath.Join(root, directory), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	for index := 0; index < 600; index++ {
		directory := "a"
		if index%2 == 0 {
			directory = "b"
		}
		if err := os.WriteFile(filepath.Join(root, "tree", directory, fmt.Sprint(index)), []byte("delete without reading"), 0); err != nil {
			t.Fatal(err)
		}
	}
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "keep.txt"), []byte("outside"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "tree", "link")); err != nil {
		t.Fatal(err)
	}
	var previous int64 = -1
	err := service.DeleteWithProgress(context.Background(), "/Files/tree", func(deleted int64) {
		if deleted != previous+1 {
			t.Errorf("non-monotonic deletion count: %d after %d", deleted, previous)
		}
		previous = deleted
	})
	if err != nil {
		t.Fatal(err)
	}
	if previous != 604 {
		t.Fatalf("removed %d entries, want 604", previous)
	}
	if _, err := os.Stat(filepath.Join(root, "tree")); !os.IsNotExist(err) {
		t.Fatalf("tree remains: %v", err)
	}
	if data, err := os.ReadFile(filepath.Join(outside, "keep.txt")); err != nil || string(data) != "outside" {
		t.Fatalf("followed outside link: %q %v", data, err)
	}
}

func TestDeleteCancellationStopsWithinLargeFolderAndCanResume(t *testing.T) {
	service, root := testService(t)
	tree := filepath.Join(root, "tree")
	if err := os.Mkdir(tree, 0o700); err != nil {
		t.Fatal(err)
	}
	for index := 0; index < 1000; index++ {
		if err := os.WriteFile(filepath.Join(tree, fmt.Sprint(index)), []byte("keep until deleted"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var removed int64
	err := service.DeleteWithProgress(ctx, "/Files/tree", func(count int64) {
		removed = count
		if count == 5 {
			cancel()
		}
	})
	if !errors.Is(err, context.Canceled) || removed != 5 {
		t.Fatalf("cancellation result: %d %v", removed, err)
	}
	entries, err := os.ReadDir(tree)
	if err != nil || len(entries) != 995 {
		t.Fatalf("cancellation removed extra entries: %d %v", len(entries), err)
	}
	if err := service.DeleteWithProgress(context.Background(), "/Files/tree", func(count int64) { removed = count }); err != nil {
		t.Fatal(err)
	}
	if removed != 996 {
		t.Fatalf("resume removed %d entries, want 996", removed)
	}
}

func TestDeleteCancelledBeforeStartDoesNotChangeSource(t *testing.T) {
	service, root := testService(t)
	file := filepath.Join(root, "keep.txt")
	if err := os.WriteFile(file, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := service.DeleteWithProgress(ctx, "/Files/keep.txt", nil); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(file); err != nil || string(data) != "keep" {
		t.Fatalf("cancelled delete changed source: %q %v", data, err)
	}
}
