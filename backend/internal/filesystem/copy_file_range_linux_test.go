//go:build linux

package filesystem

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestCopyFileRangePlatformCopiesRegularFile(t *testing.T) {
	content := []byte("copy-file-range")
	directory := t.TempDir()
	sourcePath := filepath.Join(directory, "source")
	if err := os.WriteFile(sourcePath, content, 0o600); err != nil {
		t.Fatal(err)
	}
	source, err := os.Open(sourcePath)
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	destinationPath := filepath.Join(directory, "destination")
	destination, err := os.Create(destinationPath)
	if err != nil {
		t.Fatal(err)
	}

	var bytesDone int64
	complete, err := copyFileRangePlatform(context.Background(), source, destination, func(delta int64) {
		bytesDone += delta
	})
	if err != nil {
		t.Fatal(err)
	}
	if !complete {
		t.Skip("copy_file_range is not supported by the test filesystem")
	}
	if err := destination.Close(); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(destinationPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(content) {
		t.Fatalf("copied %q, want %q", got, content)
	}
	if bytesDone != int64(len(content)) {
		t.Fatalf("bytes done = %d, want %d", bytesDone, len(content))
	}
}
