package filesystem

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

type cancelDuringFallback struct {
	context.Context
	checks int
}

func (c *cancelDuringFallback) Err() error {
	c.checks++
	if c.checks > 4 {
		return context.Canceled
	}
	return nil
}

func TestCopyNoReplaceFallbackCancellationPreservesSourceAndRemovesPartial(t *testing.T) {
	dir := t.TempDir()
	source, destination := filepath.Join(dir, "source"), filepath.Join(dir, "destination")
	if err := os.WriteFile(source, make([]byte, 1<<20), 0600); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(source)
	if err != nil {
		t.Fatal(err)
	}
	ctx := &cancelDuringFallback{Context: context.Background()}
	if err := copyNoReplaceContext(ctx, source, destination, info); !errors.Is(err, context.Canceled) {
		t.Fatalf("expected cancellation: %v", err)
	}
	if _, err := os.Stat(source); err != nil {
		t.Fatalf("lost source: %v", err)
	}
	if _, err := os.Stat(destination); !os.IsNotExist(err) {
		t.Fatalf("partial destination retained: %v", err)
	}
}

func TestRenameNoReplacePreservesBothFilesOnConflict(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "source")
	destination := filepath.Join(dir, "destination")
	if err := os.WriteFile(source, []byte("source"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(destination, []byte("destination"), 0o644); err != nil {
		t.Fatal(err)
	}

	err := renameNoReplace(source, destination)

	if !errors.Is(err, ErrConflict) {
		t.Fatalf("got %v, want ErrConflict", err)
	}
	for path, expected := range map[string]string{source: "source", destination: "destination"} {
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			t.Fatal(readErr)
		}
		if string(data) != expected {
			t.Fatalf("%s contains %q", path, data)
		}
	}
}

func TestRenameNoReplaceMovesDirectoryWhenDestinationDoesNotExist(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "source")
	destination := filepath.Join(dir, "destination")
	if err := os.Mkdir(source, 0o755); err != nil {
		t.Fatal(err)
	}

	if err := renameNoReplace(source, destination); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(destination); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(source); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("source still exists: %v", err)
	}
}
