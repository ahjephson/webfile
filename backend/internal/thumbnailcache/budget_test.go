package thumbnailcache

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestWarmCachePutDoesNotRescanAllBlobs(t *testing.T) {
	dir := t.TempDir()
	for i := 0; i < 2000; i++ {
		if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("%064x.thumb", i)), []byte("image/png\nx"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	store, err := Open(dir, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	key := fmt.Sprintf("%064x", 3000)
	allocations := testing.AllocsPerRun(3, func() {
		if err := store.Put(context.Background(), key, Item{Data: []byte("new"), ContentType: "image/png"}); err != nil {
			t.Fatal(err)
		}
	})
	if allocations > 150 {
		t.Fatalf("warm Put allocated %.0f times; blob count must not determine write cost", allocations)
	}
}

func TestStoreBudgetTracksRestartReadsReplacementsMovesAndDeletes(t *testing.T) {
	dir := t.TempDir()
	key := func(i int) string { return fmt.Sprintf("%064x", i) }
	item := Item{Data: []byte("1234567890"), ContentType: "image/png"} // 20 bytes encoded.
	put := func(s *Store, i int) {
		t.Helper()
		if err := s.Put(context.Background(), key(i), item); err != nil {
			t.Fatal(err)
		}
	}
	store, err := Open(dir, 60)
	if err != nil {
		t.Fatal(err)
	}
	put(store, 1)
	put(store, 2)
	put(store, 3)
	store, err = Open(dir, 60)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok, err := store.Get(context.Background(), key(1)); err != nil || !ok {
		t.Fatalf("read: %v, %v", ok, err)
	}
	put(store, 4)
	if _, ok, _ := store.Get(context.Background(), key(2)); ok {
		t.Fatal("least recently used blob survived eviction")
	}
	if err := store.Move(context.Background(), key(3), key(1)); err != nil {
		t.Fatal(err)
	}
	if err := store.Delete(context.Background(), key(4)); err != nil {
		t.Fatal(err)
	}
	put(store, 5)
	put(store, 5)
	put(store, 6)
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var bytes int64
	for _, entry := range entries {
		info, err := entry.Info()
		if err != nil {
			t.Fatal(err)
		}
		bytes += info.Size()
	}
	if bytes != 60 {
		t.Fatalf("wrong disk budget after mutations: %d", bytes)
	}
}

func BenchmarkWarmCachePut(b *testing.B) {
	for _, count := range []int{0, 10000} {
		b.Run(fmt.Sprint(count), func(b *testing.B) {
			dir := b.TempDir()
			for i := 0; i < count; i++ {
				if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("%064x.thumb", i)), []byte("image/png\nx"), 0600); err != nil {
					b.Fatal(err)
				}
			}
			store, err := Open(dir, 1<<30)
			if err != nil {
				b.Fatal(err)
			}
			key := fmt.Sprintf("%064x", count+1)
			item := Item{Data: make([]byte, 8192), ContentType: "image/png"}
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if err := store.Put(context.Background(), key, item); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
