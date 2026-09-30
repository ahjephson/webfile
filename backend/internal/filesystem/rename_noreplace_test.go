package filesystem

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

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
