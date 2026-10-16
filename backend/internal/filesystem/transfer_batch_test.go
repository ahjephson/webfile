package filesystem

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"testing"
)

func crossFilesystemSmallFiles(t *testing.T) (*Service, string, string, []string) {
	t.Helper()
	from, err := os.MkdirTemp("/dev/shm", "webfile-batch-test-")
	if err != nil {
		t.Skipf("tmpfs unavailable: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(from) })
	to := t.TempDir()
	fromInfo, err := os.Stat(from)
	if err != nil {
		t.Fatal(err)
	}
	toInfo, err := os.Stat(to)
	if err != nil {
		t.Fatal(err)
	}
	if fromInfo.Sys().(*syscall.Stat_t).Dev == toInfo.Sys().(*syscall.Stat_t).Dev {
		t.Skip("different filesystems required")
	}
	var sources []string
	for index := 0; index < 8; index++ {
		name := fmt.Sprintf("file-%d.txt", index)
		if err := os.WriteFile(filepath.Join(from, name), []byte("original "+name), 0o600); err != nil {
			t.Fatal(err)
		}
		sources = append(sources, "/From/"+name)
	}
	resolver, err := NewResolver([]Root{{Name: "From", Path: from}, {Name: "To", Path: to}})
	if err != nil {
		t.Fatal(err)
	}
	service := NewService(resolver, 1024)
	t.Cleanup(service.Close)
	return service, from, to, sources
}

func TestSmallCopyBatchCancellationDrainsWorkersAndKeepsSources(t *testing.T) {
	service, from, to, sources := crossFilesystemSmallFiles(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	err := service.NewTransfer("move", sources, "/To").Run(ctx, func(p TransferProgress) {
		if p.Phase == "flushing" {
			cancel()
		}
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("move result=%v", err)
	}
	for _, source := range sources {
		name := filepath.Base(source)
		if data, err := os.ReadFile(filepath.Join(from, name)); err != nil || string(data) != "original "+name {
			t.Fatalf("source changed: %q %v", data, err)
		}
	}
	entries, err := os.ReadDir(to)
	if err != nil || len(entries) != 0 {
		t.Fatalf("workers retained output after cancellation: %v %v", entries, err)
	}
}

func TestSmallCopyBatchReviewsConcurrentDestinationAndResumes(t *testing.T) {
	service, from, to, sources := crossFilesystemSmallFiles(t)
	transfer := service.NewTransfer("move", sources, "/To")
	var once sync.Once
	var injected error
	err := transfer.Run(context.Background(), func(p TransferProgress) {
		if p.Phase == "copying" {
			once.Do(func() {
				injected = os.WriteFile(filepath.Join(to, "file-0.txt"), []byte("concurrent destination"), 0o600)
			})
		}
	})
	if injected != nil {
		t.Fatal(injected)
	}
	var conflict *TransferConflict
	if !errors.As(err, &conflict) || conflict.Source.Path != "/From/file-0.txt" {
		t.Fatalf("expected file conflict: %v %#v", err, conflict)
	}
	if data, err := os.ReadFile(filepath.Join(to, "file-0.txt")); err != nil || string(data) != "concurrent destination" {
		t.Fatalf("concurrent destination overwritten: %q %v", data, err)
	}
	if data, err := os.ReadFile(filepath.Join(from, "file-0.txt")); err != nil || string(data) != "original file-0.txt" {
		t.Fatalf("conflicting source removed: %q %v", data, err)
	}
	if err := transfer.Decide(ConflictDecision{Revision: conflict.Revision, Choice: "keep_both"}); err != nil {
		t.Fatal(err)
	}
	var final TransferProgress
	if err := transfer.Run(context.Background(), func(p TransferProgress) { final = p }); err != nil {
		t.Fatal(err)
	}
	if final.ItemsDone != 8 || final.ItemsTotal != 8 || final.BytesDone != final.BytesTotal {
		t.Fatalf("progress double counted batch retries: %#v", final)
	}
	for index := 0; index < 8; index++ {
		name := fmt.Sprintf("file-%d.txt", index)
		target := name
		if index == 0 {
			target = "file-0 (copy).txt"
		}
		if data, err := os.ReadFile(filepath.Join(to, target)); err != nil || string(data) != "original "+name {
			t.Fatalf("completed item missing: %q %v", data, err)
		}
		if _, err := os.Stat(filepath.Join(from, name)); !os.IsNotExist(err) {
			t.Fatalf("completed move retained source: %v", err)
		}
	}
}
