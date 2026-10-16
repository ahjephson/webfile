package filesystem

import (
	"context"
	"image/color"
	"os"
	"path/filepath"
	"testing"
)

func TestRenameFileRegeneratesChangedVersion(t *testing.T) {
	svc, root := testService(t)
	writeThumbnailTestImage(t, filepath.Join(root, "before.jpg"), color.RGBA{R: 255, A: 255})
	cacheThumbnail(t, svc, "/Files/before.jpg")
	oldKey := onlyCachedThumbnailKey(t, svc)

	if err := svc.Rename(context.Background(), "/Files/before.jpg", "/Files/after.jpg"); err != nil {
		t.Fatal(err)
	}
	thumbnail, err := svc.Thumbnail(context.Background(), "/Files/after.jpg", 320, 320)
	if err != nil {
		t.Fatal(err)
	}
	if thumbnail.ETag == `"`+oldKey[:32]+`"` {
		t.Fatal("file rename reused a source version with changed ctime")
	}
}

func TestMoveDirectoryReusesUnchangedDescendantThumbnails(t *testing.T) {
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
	if len(cache.items) != 1 {
		t.Fatalf("unchanged destination thumbnail was regenerated: %d entries", len(cache.items))
	}
}

func TestDeleteDirectoryDoesNotScanThumbnailCache(t *testing.T) {
	svc, root := testService(t)
	if err := os.Mkdir(filepath.Join(root, "source"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeThumbnailTestImage(t, filepath.Join(root, "source", "photo.jpg"), color.RGBA{B: 255, A: 255})
	cacheThumbnail(t, svc, "/Files/source/photo.jpg")
	cache := &untouchedTransferCache{}
	svc.thumbnailCache = cache

	if err := svc.Delete(context.Background(), "/Files/source"); err != nil {
		t.Fatal(err)
	}
	if cache.calls != 0 {
		t.Fatalf("deletion made %d thumbnail cache calls", cache.calls)
	}
	if _, err := svc.Thumbnail(context.Background(), "/Files/source/photo.jpg", 320, 320); !os.IsNotExist(err) {
		t.Fatalf("deleted source was not rejected before cache access: %v", err)
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
