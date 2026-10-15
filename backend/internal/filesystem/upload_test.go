package filesystem

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestUploadPreservesTrailingFilenameSpace(t *testing.T) {
	root := t.TempDir()
	resolver, err := NewResolver([]Root{{Name: "Files", Path: root}})
	if err != nil {
		t.Fatal(err)
	}
	manager := NewUploadManager(resolver, t.TempDir())
	session, err := manager.Init(context.Background(), "/Files", "report ", 4, 1)
	if err != nil {
		t.Fatal(err)
	}
	if _, complete, err := manager.Append(context.Background(), session.ID, 0, bytes.NewBufferString("data"), 4); err != nil || !complete {
		t.Fatalf("complete=%v err=%v", complete, err)
	}
	if data, err := os.ReadFile(filepath.Join(root, "report ")); err != nil || string(data) != "data" {
		t.Fatalf("uploaded file: %q %v", data, err)
	}
	if _, err := os.Stat(filepath.Join(root, "report")); !os.IsNotExist(err) {
		t.Fatalf("filename was trimmed: %v", err)
	}
}

func TestUploadManagerResumesAndCompletes(t *testing.T) {
	root := t.TempDir()
	resolver, err := NewResolver([]Root{{Name: "Files", Path: root}})
	if err != nil {
		t.Fatal(err)
	}
	manager := NewUploadManager(resolver, filepath.Join(t.TempDir(), "uploads"))
	ctx := context.Background()

	session, err := manager.Init(ctx, "/Files", "movie.bin", 10, 1234)
	if err != nil {
		t.Fatal(err)
	}
	if _, complete, err := manager.Append(ctx, session.ID, 0, bytes.NewReader([]byte("1234")), 4); err != nil || complete {
		t.Fatalf("first chunk complete=%v err=%v", complete, err)
	}
	resumed, err := manager.Init(ctx, "/Files", "movie.bin", 10, 1234)
	if err != nil {
		t.Fatal(err)
	}
	if resumed.Offset != 4 {
		t.Fatalf("offset=%d want 4", resumed.Offset)
	}
	final, complete, err := manager.Append(ctx, resumed.ID, 4, bytes.NewReader([]byte("567890")), 6)
	if err != nil || !complete {
		t.Fatalf("final complete=%v err=%v", complete, err)
	}
	if final.Offset != 10 {
		t.Fatalf("offset=%d want 10", final.Offset)
	}
	data, err := os.ReadFile(filepath.Join(root, "movie.bin"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "1234567890" {
		t.Fatalf("content=%q", data)
	}
}

func TestUploadOffsetMismatchReportsExpected(t *testing.T) {
	root := t.TempDir()
	resolver, _ := NewResolver([]Root{{Name: "Files", Path: root}})
	manager := NewUploadManager(resolver, filepath.Join(t.TempDir(), "uploads"))
	session, err := manager.Init(context.Background(), "/Files", "x.bin", 2, 1)
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = manager.Append(context.Background(), session.ID, 1, bytes.NewReader([]byte("x")), 1)
	var offsetErr *UploadOffsetError
	if !errors.As(err, &offsetErr) || offsetErr.Expected != 0 {
		t.Fatalf("err=%v expected offset error", err)
	}
}
