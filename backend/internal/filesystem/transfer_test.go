package filesystem

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
)

func containsPhase(progress []TransferProgress, phase string) bool {
	for _, update := range progress {
		if update.Phase == phase {
			return true
		}
	}
	return false
}

func TestCopyFileDataUsesCompletedFastCopy(t *testing.T) {
	sourcePath := filepath.Join(t.TempDir(), "source")
	if err := os.WriteFile(sourcePath, []byte("fast-copy"), 0o600); err != nil {
		t.Fatal(err)
	}
	source, err := os.Open(sourcePath)
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	destination, err := os.CreateTemp(t.TempDir(), "destination")
	if err != nil {
		t.Fatal(err)
	}
	destinationPath := destination.Name()

	var bytesDone int64
	fastCopy := func(_ context.Context, source, destination *os.File, progress func(int64)) (bool, error) {
		written, err := io.Copy(destination, source)
		progress(written)
		return true, err
	}
	if err := copyFileData(context.Background(), source, destination, func(phase string, delta int64) {
		if phase != transferPhaseCopying {
			t.Fatalf("phase = %q", phase)
		}
		bytesDone += delta
	}, fastCopy); err != nil {
		t.Fatal(err)
	}
	if bytesDone != int64(len("fast-copy")) {
		t.Fatalf("bytes done = %d", bytesDone)
	}
	if err := destination.Close(); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(destinationPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "fast-copy" {
		t.Fatalf("copied %q", got)
	}
}

func TestCopyFileDataResumesBufferedFallbackAfterPartialFastCopy(t *testing.T) {
	content := []byte("partial-fast-copy-then-buffered")
	sourcePath := filepath.Join(t.TempDir(), "source")
	if err := os.WriteFile(sourcePath, content, 0o600); err != nil {
		t.Fatal(err)
	}
	source, err := os.Open(sourcePath)
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	destinationPath := filepath.Join(t.TempDir(), "destination")
	destination, err := os.Create(destinationPath)
	if err != nil {
		t.Fatal(err)
	}

	var bytesDone int64
	fastCopy := func(_ context.Context, source, destination *os.File, progress func(int64)) (bool, error) {
		written, err := io.CopyN(destination, source, 7)
		progress(written)
		return false, err
	}
	if err := copyFileData(context.Background(), source, destination, func(_ string, delta int64) {
		bytesDone += delta
	}, fastCopy); err != nil {
		t.Fatal(err)
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

func TestCopyMoveCopiesDirectoryTree(t *testing.T) {
	root := t.TempDir()
	resolver, err := NewResolver([]Root{{Name: "Files", Path: root}})
	if err != nil {
		t.Fatal(err)
	}
	svc := NewService(resolver, 1<<20)

	if err := os.MkdirAll(filepath.Join(root, "src", "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "src", "sub", "hello.txt"), []byte("hello"), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, "dest"), 0o755); err != nil {
		t.Fatal(err)
	}

	var updates []TransferProgress
	if err := svc.CopyMove(context.Background(), "copy", []string{"/Files/src"}, "/Files/dest", func(p TransferProgress) { updates = append(updates, p) }); err != nil {
		t.Fatal(err)
	}
	last := updates[len(updates)-1]
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
	for _, phase := range []string{transferPhaseScanning, transferPhaseCopying, transferPhaseFlushing, transferPhasePublishing, transferPhaseCompleted} {
		if !containsPhase(updates, phase) {
			t.Fatalf("missing %q phase in %#v", phase, updates)
		}
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
