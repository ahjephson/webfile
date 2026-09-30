package filesystem

import (
	"archive/zip"
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"
)

func TestFolderDownloadProducesZip(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "folder", "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "folder", "sub", "a.txt"), []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	resolver, _ := NewResolver([]Root{{Name: "Files", Path: root}})
	service := NewService(resolver, 1024)
	var buf bytes.Buffer
	if err := service.WriteFolderZip(context.Background(), "/Files/folder", &buf); err != nil {
		t.Fatal(err)
	}
	zr, err := zip.NewReader(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	if err != nil {
		t.Fatal(err)
	}
	var got string
	for _, f := range zr.File {
		if f.Name != "folder/sub/a.txt" {
			continue
		}
		r, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		data, err := io.ReadAll(r)
		_ = r.Close()
		if err != nil {
			t.Fatal(err)
		}
		got = string(data)
	}
	if got != "hello" {
		t.Fatalf("got %q", got)
	}
}
