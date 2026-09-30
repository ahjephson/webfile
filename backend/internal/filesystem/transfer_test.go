package filesystem

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestCopyMoveCopiesDirectoryTree(t *testing.T) {
	root := t.TempDir()
	resolver, err := NewResolver([]Root{{Name: "Files", Path: root}})
	if err != nil {
		t.Fatal(err)
	}
	svc := NewServiceWithTrash(resolver, 1<<20, filepath.Join(t.TempDir(), "trash"))

	if err := os.MkdirAll(filepath.Join(root, "src", "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "src", "sub", "hello.txt"), []byte("hello"), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, "dest"), 0o755); err != nil {
		t.Fatal(err)
	}

	var last TransferProgress
	if err := svc.CopyMove(context.Background(), "copy", []string{"/Files/src"}, "/Files/dest", func(p TransferProgress) { last = p }); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(root, "dest", "src", "sub", "hello.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "hello" {
		t.Fatalf("got %q", data)
	}
	if last.BytesDone != last.BytesTotal || last.ItemsDone != last.ItemsTotal {
		t.Fatalf("incomplete progress %#v", last)
	}
	if _, err := os.Stat(filepath.Join(root, "src", "sub", "hello.txt")); err != nil {
		t.Fatalf("copy removed source: %v", err)
	}
}

func TestCopyMoveMovesSameRoot(t *testing.T) {
	svc, root := testService(t)
	if err := os.Mkdir(filepath.Join(root, "dest"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "move.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := svc.CopyMove(context.Background(), "move", []string{"/Files/move.txt"}, "/Files/dest", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "move.txt")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("source still exists: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "dest", "move.txt")); err != nil {
		t.Fatal(err)
	}
}

func TestCopyMoveRefusesExistingDestination(t *testing.T) {
	svc, root := testService(t)
	if err := os.Mkdir(filepath.Join(root, "dest"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "same.txt"), []byte("source"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "dest", "same.txt"), []byte("dest"), 0o644); err != nil {
		t.Fatal(err)
	}
	err := svc.CopyMove(context.Background(), "copy", []string{"/Files/same.txt"}, "/Files/dest", nil)
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("got %v, want conflict", err)
	}
}

func TestTrashRestoreAndPurge(t *testing.T) {
	root := t.TempDir()
	resolver, err := NewResolver([]Root{{Name: "Files", Path: root}})
	if err != nil {
		t.Fatal(err)
	}
	trashDir := filepath.Join(t.TempDir(), "trash")
	svc := NewServiceWithTrash(resolver, 1<<20, trashDir)

	original := filepath.Join(root, "notes.txt")
	if err := os.WriteFile(original, []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := svc.Trash(context.Background(), []string{"/Files/notes.txt"}, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(original); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("source still exists: %v", err)
	}

	items, err := svc.ListTrash(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].OriginalPath != "/Files/notes.txt" {
		t.Fatalf("unexpected trash: %#v", items)
	}

	if err := svc.RestoreTrash(context.Background(), items[0].ID); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(original)
	if err != nil || string(data) != "hello" {
		t.Fatalf("restore failed: %v %q", err, data)
	}

	if err := svc.Trash(context.Background(), []string{"/Files/notes.txt"}, nil); err != nil {
		t.Fatal(err)
	}
	items, err = svc.ListTrash(context.Background())
	if err != nil || len(items) != 1 {
		t.Fatalf("trash list: %v %#v", err, items)
	}
	if err := svc.PurgeTrash(context.Background(), items[0].ID); err != nil {
		t.Fatal(err)
	}
	items, err = svc.ListTrash(context.Background())
	if err != nil || len(items) != 0 {
		t.Fatalf("purge failed: %v %#v", err, items)
	}
}
