package filesystem

import (
	"context"
	"image/color"
	"os"
	"path/filepath"
	"testing"
)

func TestRenameMovesCachedThumbnail(t *testing.T) {
	svc, root := testService(t)
	writeThumbnailTestImage(t, filepath.Join(root, "before.jpg"), color.RGBA{R: 255, A: 255})
	cacheThumbnail(t, svc, "/Files/before.jpg")
	oldKey := onlyCachedThumbnailKey(t, svc)

	if err := svc.Rename(context.Background(), "/Files/before.jpg", "/Files/after.jpg"); err != nil {
		t.Fatal(err)
	}
	assertCachedThumbnailMoved(t, svc, oldKey, "/Files/after.jpg")
}

func TestMoveDirectoryRegeneratesThumbnailsOnDemand(t *testing.T) {
	svc, root := testService(t)
	if err := os.MkdirAll(filepath.Join(root, "source", "nested"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, "destination"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeThumbnailTestImage(t, filepath.Join(root, "source", "nested", "photo.jpg"), color.RGBA{G: 255, A: 255})
	cacheThumbnail(t, svc, "/Files/source/nested/photo.jpg")
	oldKey := onlyCachedThumbnailKey(t, svc)

	if err := svc.CopyMove(context.Background(), "move", []string{"/Files/source"}, "/Files/destination", nil); err != nil {
		t.Fatal(err)
	}
	if onlyCachedThumbnailKey(t, svc) != oldKey {
		t.Fatal("move unexpectedly scanned or rewrote thumbnail cache entries")
	}
	cacheThumbnail(t, svc, "/Files/destination/source/nested/photo.jpg")
	cache := svc.thumbnailCache.(*memoryThumbnailCache)
	cache.mu.Lock()
	defer cache.mu.Unlock()
	if len(cache.items) != 2 {
		t.Fatalf("destination thumbnail was not regenerated on demand: %d entries", len(cache.items))
	}
}

func TestDeleteDirectoryDeletesCachedThumbnails(t *testing.T) {
	svc, root := testService(t)
	if err := os.Mkdir(filepath.Join(root, "source"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeThumbnailTestImage(t, filepath.Join(root, "source", "photo.jpg"), color.RGBA{B: 255, A: 255})
	cacheThumbnail(t, svc, "/Files/source/photo.jpg")

	if err := svc.Delete(context.Background(), "/Files/source"); err != nil {
		t.Fatal(err)
	}
	cache := svc.thumbnailCache.(*memoryThumbnailCache)
	cache.mu.Lock()
	defer cache.mu.Unlock()
	if len(cache.items) != 0 {
		t.Fatalf("cache still contains %d entries", len(cache.items))
	}
}

func cacheThumbnail(t *testing.T, svc *Service, virtualPath string) {
	t.Helper()
	if _, err := svc.Thumbnail(context.Background(), virtualPath, managedThumbnailDimension, managedThumbnailDimension); err != nil {
		t.Fatal(err)
	}
}

func onlyCachedThumbnailKey(t *testing.T, svc *Service) string {
	t.Helper()
	cache := svc.thumbnailCache.(*memoryThumbnailCache)
	cache.mu.Lock()
	defer cache.mu.Unlock()
	if len(cache.items) != 1 {
		t.Fatalf("cache contains %d entries, want 1", len(cache.items))
	}
	for key := range cache.items {
		return key
	}
	return ""
}

func assertCachedThumbnailMoved(t *testing.T, svc *Service, oldKey, virtualPath string) {
	t.Helper()
	source, file, err := svc.inspectThumbnailSource(context.Background(), virtualPath, managedThumbnailDimension, managedThumbnailDimension)
	if err != nil {
		t.Fatal(err)
	}
	_ = file.Close()
	cache := svc.thumbnailCache.(*memoryThumbnailCache)
	cache.mu.Lock()
	defer cache.mu.Unlock()
	if _, ok := cache.items[oldKey]; ok {
		t.Fatal("old thumbnail cache key still exists")
	}
	if _, ok := cache.items[source.key]; !ok {
		t.Fatal("thumbnail was not moved to the new cache key")
	}
	if len(cache.items) != 1 {
		t.Fatalf("cache contains %d entries, want 1", len(cache.items))
	}
}
