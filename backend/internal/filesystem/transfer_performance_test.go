package filesystem

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"webfile/internal/thumbnailcache"
)

type untouchedTransferCache struct{ calls int }

func (c *untouchedTransferCache) Get(context.Context, string) (thumbnailcache.Item, bool, error) {
	c.calls++
	return thumbnailcache.Item{}, false, errors.New("transfer must not read thumbnail cache")
}
func (c *untouchedTransferCache) Put(context.Context, string, thumbnailcache.Item) error {
	c.calls++
	return nil
}
func (c *untouchedTransferCache) Move(context.Context, string, string) error { c.calls++; return nil }
func (c *untouchedTransferCache) Delete(context.Context, string) error       { c.calls++; return nil }

func TestMovesAcrossRootsOnOneFilesystemRenameWithoutThumbnailWork(t *testing.T) {
	for _, legacy := range []bool{false, true} {
		for _, directory := range []bool{false, true} {
			name := "file"
			if directory {
				name = "directory"
			}
			if legacy {
				name += " legacy"
			}
			t.Run(name, func(t *testing.T) {
				base := t.TempDir()
				from, to := filepath.Join(base, "from"), filepath.Join(base, "to")
				for _, path := range []string{from, to} {
					if err := os.Mkdir(path, 0o700); err != nil {
						t.Fatal(err)
					}
				}
				source := filepath.Join(from, "item")
				if directory {
					if err := os.Mkdir(source, 0o700); err != nil {
						t.Fatal(err)
					}
					if err := os.WriteFile(filepath.Join(source, "file"), []byte("contents"), 0o600); err != nil {
						t.Fatal(err)
					}
				} else if err := os.WriteFile(source, []byte("contents"), 0o600); err != nil {
					t.Fatal(err)
				}
				before, err := os.Stat(source)
				if err != nil {
					t.Fatal(err)
				}
				resolver, err := NewResolver([]Root{{Name: "From", Path: from}, {Name: "To", Path: to}})
				if err != nil {
					t.Fatal(err)
				}
				cache := &untouchedTransferCache{}
				service := NewServiceWithThumbnailOptions(resolver, 1024, ThumbnailOptions{Cache: cache})
				defer service.Close()
				var scanning string
				report := func(p TransferProgress) {
					if p.Phase == "scanning" && scanning == "" {
						scanning = p.Message
					}
				}
				if legacy {
					err = service.CopyMove(context.Background(), "move", []string{"/From/item"}, "/To", report)
				} else {
					err = service.NewTransfer("move", []string{"/From/item"}, "/To").Run(context.Background(), report)
				}
				if err != nil {
					t.Fatal(err)
				}
				after, err := os.Stat(filepath.Join(to, "item"))
				if err != nil || !os.SameFile(before, after) {
					t.Fatalf("move copied instead of renaming: %v", err)
				}
				if _, err := os.Stat(source); !os.IsNotExist(err) {
					t.Fatalf("source remains: %v", err)
				}
				if cache.calls != 0 {
					t.Fatalf("move made %d thumbnail cache calls", cache.calls)
				}
				if scanning != "Scanning" {
					t.Fatalf("scanning message = %q", scanning)
				}
			})
		}
	}
}

func TestCrossRootRenameDoesNotBypassSourceExclusions(t *testing.T) {
	base := t.TempDir()
	from, to := filepath.Join(base, "from"), filepath.Join(base, "to")
	if err := os.MkdirAll(filepath.Join(from, "folder"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(to, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"visible.txt", "private.txt"} {
		if err := os.WriteFile(filepath.Join(from, "folder", name), []byte(name), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	resolver, err := NewResolver([]Root{{Name: "From", Path: from, ExcludedNames: []string{"private.txt"}}, {Name: "To", Path: to}})
	if err != nil {
		t.Fatal(err)
	}
	service := NewService(resolver, 1024)
	defer service.Close()
	if err := service.NewTransfer("move", []string{"/From/folder"}, "/To").Run(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(from, "folder", "private.txt")); err != nil {
		t.Fatal("excluded source was moved", err)
	}
	if _, err := os.Stat(filepath.Join(to, "folder", "private.txt")); !os.IsNotExist(err) {
		t.Fatal("excluded file appeared at destination", err)
	}
	if data, err := os.ReadFile(filepath.Join(to, "folder", "visible.txt")); err != nil || string(data) != "visible.txt" {
		t.Fatalf("visible file was not moved: %q %v", data, err)
	}
}

func TestCrossFilesystemMoveCancellationRetainsSource(t *testing.T) {
	from, err := os.MkdirTemp("/dev/shm", "webfile-move-cancel-")
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
	if err := os.WriteFile(filepath.Join(from, "file.txt"), []byte("keep original"), 0o600); err != nil {
		t.Fatal(err)
	}
	resolver, err := NewResolver([]Root{{Name: "From", Path: from}, {Name: "To", Path: to}})
	if err != nil {
		t.Fatal(err)
	}
	service := NewService(resolver, 1024)
	defer service.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	err = service.NewTransfer("move", []string{"/From/file.txt"}, "/To").Run(ctx, func(p TransferProgress) {
		if p.Phase == "flushing" {
			cancel()
		}
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("move result = %v", err)
	}
	if data, err := os.ReadFile(filepath.Join(from, "file.txt")); err != nil || string(data) != "keep original" {
		t.Fatalf("source changed: %q %v", data, err)
	}
	entries, err := os.ReadDir(to)
	if err != nil || len(entries) != 0 {
		t.Fatalf("cancelled move left output: %v %v", entries, err)
	}
}
