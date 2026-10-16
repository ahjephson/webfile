package filesystem

import (
	"context"
	"errors"
	"fmt"
	"image/color"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"webfile/internal/thumbnailcache"
)

type indexedTestCache struct {
	*thumbnailcache.Store
	gets          atomic.Int64
	puts          atomic.Int64
	locationCalls atomic.Int64
	fail          bool
	block         bool
	beforeApply   func()
	beforeGet     func()
}

func (c *indexedTestCache) Get(ctx context.Context, key string) (thumbnailcache.Item, bool, error) {
	c.gets.Add(1)
	if c.beforeGet != nil {
		callback := c.beforeGet
		c.beforeGet = nil
		callback()
	}
	return c.Store.Get(ctx, key)
}
func (c *indexedTestCache) Put(ctx context.Context, key string, item thumbnailcache.Item) error {
	c.puts.Add(1)
	return c.Store.Put(ctx, key, item)
}
func (c *indexedTestCache) Locations(ctx context.Context, request thumbnailcache.LocationRequest) (thumbnailcache.LocationResponse, error) {
	c.locationCalls.Add(1)
	if c.block {
		<-ctx.Done()
		return thumbnailcache.LocationResponse{}, ctx.Err()
	}
	if c.fail {
		return thumbnailcache.LocationResponse{}, errors.New("index unavailable")
	}
	if request.Action == "apply" && c.beforeApply != nil {
		callback := c.beforeApply
		c.beforeApply = nil
		callback()
	}
	return c.Store.Locations(ctx, request)
}

func indexedService(t *testing.T) (*Service, string, *indexedTestCache) {
	t.Helper()
	root := t.TempDir()
	resolver, err := NewResolver([]Root{{Name: "Files", Path: root}})
	if err != nil {
		t.Fatal(err)
	}
	store, err := thumbnailcache.Open(t.TempDir(), 16<<20)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	cache := &indexedTestCache{Store: store}
	service := NewServiceWithThumbnailOptions(resolver, 1024, ThumbnailOptions{Cache: cache})
	t.Cleanup(service.Close)
	return service, root, cache
}

func indexedNode(t *testing.T, service *Service, virtual string) int64 {
	t.Helper()
	tickets := service.captureThumbnailChanges(context.Background(), []string{virtual})
	if len(tickets) != 1 {
		t.Fatal("missing cache snapshot")
	}
	return tickets[0].Node
}

func TestIndexedFolderRenameAndMoveReuseThumbnailsWithoutScan(t *testing.T) {
	service, root, cache := indexedService(t)
	if err := os.MkdirAll(filepath.Join(root, "before", "nested"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, "destination"), 0700); err != nil {
		t.Fatal(err)
	}
	writeThumbnailTestImage(t, filepath.Join(root, "before", "nested", "photo.jpg"), color.RGBA{R: 255, A: 255})
	for index := 0; index < 200; index++ {
		if err := os.WriteFile(filepath.Join(root, "before", fmt.Sprintf("text-%d", index)), []byte("text"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	cacheThumbnail(t, service, "/Files/before/nested/photo.jpg")
	node := indexedNode(t, service, "/Files/before/nested/photo.jpg")
	if indexedNode(t, service, "/Files/before/text-0") != 0 {
		t.Fatal("uncached file entered thumbnail index")
	}
	for iteration := 0; iteration < 6; iteration++ {
		from, to := "/Files/before", "/Files/after"
		if iteration%2 != 0 {
			from, to = to, from
		}
		gets, calls := cache.gets.Load(), cache.locationCalls.Load()
		if err := service.Rename(context.Background(), from, to); err != nil {
			t.Fatal(err)
		}
		if cache.gets.Load() != gets {
			t.Fatal("rename inspected descendant thumbnails")
		}
		if cache.locationCalls.Load()-calls != 2 {
			t.Fatal("rename did more than capture/apply cache calls")
		}
		if indexedNode(t, service, to+"/nested/photo.jpg") != node {
			t.Fatal("rename did not reparent cached descendants")
		}
		beforeLookup := cache.locationCalls.Load()
		cacheThumbnail(t, service, to+"/nested/photo.jpg")
		if cache.locationCalls.Load()-beforeLookup != 1 {
			t.Fatal("warm thumbnail repeated its reference publication")
		}
	}
	transfer := service.NewTransfer("move", []string{"/Files/before"}, "/Files/destination")
	if err := transfer.Run(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	if indexedNode(t, service, "/Files/destination/before/nested/photo.jpg") != node {
		t.Fatal("background move lost cached subtree")
	}
	cacheThumbnail(t, service, "/Files/destination/before/nested/photo.jpg")
	if err := service.CopyMove(context.Background(), "move", []string{"/Files/destination/before"}, "/Files", nil); err != nil {
		t.Fatal(err)
	}
	if indexedNode(t, service, "/Files/before/nested/photo.jpg") != node {
		t.Fatal("legacy move lost cached subtree")
	}
	cacheThumbnail(t, service, "/Files/before/nested/photo.jpg")
	if cache.puts.Load() != 1 {
		t.Fatalf("folder operations regenerated %d thumbnails", cache.puts.Load()-1)
	}
}

func TestIndexedDeleteCancellationAndResume(t *testing.T) {
	service, root, cache := indexedService(t)
	if err := os.Mkdir(filepath.Join(root, "folder"), 0700); err != nil {
		t.Fatal(err)
	}
	for index := 0; index < 200; index++ {
		if err := os.WriteFile(filepath.Join(root, "folder", fmt.Sprintf("text-%d", index)), nil, 0600); err != nil {
			t.Fatal(err)
		}
	}
	writeThumbnailTestImage(t, filepath.Join(root, "folder", "photo.jpg"), color.RGBA{R: 255, A: 255})
	cacheThumbnail(t, service, "/Files/folder/photo.jpg")
	node := indexedNode(t, service, "/Files/folder/photo.jpg")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	err := service.DeleteWithProgress(ctx, "/Files/folder", func(count int64) {
		if count == 1 {
			cancel()
		}
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("delete: %v", err)
	}
	if indexedNode(t, service, "/Files/folder/photo.jpg") != node {
		t.Fatal("partial delete detached surviving references")
	}
	if err := service.Delete(context.Background(), "/Files/folder"); err != nil {
		t.Fatal(err)
	}
	gets := cache.gets.Load()
	if _, err := service.Thumbnail(context.Background(), "/Files/folder/photo.jpg", 320, 320); !os.IsNotExist(err) {
		t.Fatalf("deleted source: %v", err)
	}
	if cache.gets.Load() != gets {
		t.Fatal("deleted source reached cache before authorization")
	}
	if indexedNode(t, service, "/Files/folder/photo.jpg") != 0 {
		t.Fatal("successful delete left subtree attached")
	}
}

func TestIndexedDeleteRecreateDuringDelayedCleanup(t *testing.T) {
	service, root, cache := indexedService(t)
	folder := filepath.Join(root, "folder")
	if err := os.Mkdir(folder, 0700); err != nil {
		t.Fatal(err)
	}
	writeThumbnailTestImage(t, filepath.Join(folder, "photo.jpg"), color.RGBA{R: 255, A: 255})
	cacheThumbnail(t, service, "/Files/folder/photo.jpg")
	cache.beforeApply = func() {
		if err := os.Mkdir(folder, 0700); err != nil {
			t.Fatal(err)
		}
		writeThumbnailTestImage(t, filepath.Join(folder, "photo.jpg"), color.RGBA{B: 255, A: 255})
		cacheThumbnail(t, service, "/Files/folder/photo.jpg")
	}
	if err := service.Delete(context.Background(), "/Files/folder"); err != nil {
		t.Fatal(err)
	}
	if indexedNode(t, service, "/Files/folder/photo.jpg") == 0 {
		t.Fatal("delayed delete erased recreated location")
	}
	cacheThumbnail(t, service, "/Files/folder/photo.jpg")
	if cache.puts.Load() != 2 {
		t.Fatal("recreated thumbnail was removed")
	}
}

func TestIndexFailureDoesNotBlockRenameMoveOrDelete(t *testing.T) {
	service, root, cache := indexedService(t)
	if err := os.Mkdir(filepath.Join(root, "folder"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, "destination"), 0700); err != nil {
		t.Fatal(err)
	}
	writeThumbnailTestImage(t, filepath.Join(root, "folder", "photo.jpg"), color.RGBA{R: 255, A: 255})
	cacheThumbnail(t, service, "/Files/folder/photo.jpg")
	cache.fail = true
	if err := service.Rename(context.Background(), "/Files/folder", "/Files/renamed"); err != nil {
		t.Fatal(err)
	}
	if err := service.NewTransfer("move", []string{"/Files/renamed"}, "/Files/destination").Run(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	cacheThumbnail(t, service, "/Files/destination/renamed/photo.jpg")
	if err := service.Delete(context.Background(), "/Files/destination/renamed"); err != nil {
		t.Fatal(err)
	}
}

func TestIndexedCopyUsesNewSourceVersion(t *testing.T) {
	service, root, cache := indexedService(t)
	writeThumbnailTestImage(t, filepath.Join(root, "photo.jpg"), color.RGBA{R: 255, A: 255})
	if err := os.Mkdir(filepath.Join(root, "destination"), 0700); err != nil {
		t.Fatal(err)
	}
	before, err := service.Thumbnail(context.Background(), "/Files/photo.jpg", 320, 320)
	if err != nil {
		t.Fatal(err)
	}
	if err := service.NewTransfer("copy", []string{"/Files/photo.jpg"}, "/Files/destination").Run(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	after, err := service.Thumbnail(context.Background(), "/Files/destination/photo.jpg", 320, 320)
	if err != nil {
		t.Fatal(err)
	}
	if before.ETag == after.ETag || cache.puts.Load() != 2 {
		t.Fatal("copy reused original inode's source version")
	}
	if indexedNode(t, service, "/Files/photo.jpg") == 0 || indexedNode(t, service, "/Files/destination/photo.jpg") == 0 {
		t.Fatal("copy removed original reference")
	}
}

func TestIndexedMergeSkipAndReplacementKeepCorrectReferences(t *testing.T) {
	for _, choice := range []string{"skip", "replace"} {
		t.Run(choice, func(t *testing.T) {
			service, root, cache := indexedService(t)
			for _, folder := range []string{"from/folder", "to/folder"} {
				if err := os.MkdirAll(filepath.Join(root, folder), 0700); err != nil {
					t.Fatal(err)
				}
			}
			writeThumbnailTestImage(t, filepath.Join(root, "from", "folder", "photo.jpg"), color.RGBA{R: 255, A: 255})
			writeThumbnailTestImage(t, filepath.Join(root, "to", "folder", "photo.jpg"), color.RGBA{B: 255, A: 255})
			cacheThumbnail(t, service, "/Files/from/folder/photo.jpg")
			cacheThumbnail(t, service, "/Files/to/folder/photo.jpg")
			sourceNode := indexedNode(t, service, "/Files/from/folder/photo.jpg")
			transfer := service.NewTransfer("move", []string{"/Files/from/folder"}, "/Files/to")
			var conflict *TransferConflict
			if err := transfer.Run(context.Background(), nil); !errors.As(err, &conflict) {
				t.Fatalf("folder conflict: %v", err)
			}
			if err := transfer.Decide(ConflictDecision{Revision: conflict.Revision, Choice: "merge"}); err != nil {
				t.Fatal(err)
			}
			if err := transfer.Run(context.Background(), nil); !errors.As(err, &conflict) {
				t.Fatalf("file conflict: %v", err)
			}
			if err := transfer.Decide(ConflictDecision{Revision: conflict.Revision, Choice: choice}); err != nil {
				t.Fatal(err)
			}
			if err := transfer.Run(context.Background(), nil); err != nil {
				t.Fatal(err)
			}
			if choice == "skip" {
				if indexedNode(t, service, "/Files/from/folder/photo.jpg") != sourceNode {
					t.Fatal("skipped source lost references")
				}
				cacheThumbnail(t, service, "/Files/from/folder/photo.jpg")
				cacheThumbnail(t, service, "/Files/to/folder/photo.jpg")
				if cache.puts.Load() != 2 {
					t.Fatal("skipped merge invalidated unchanged thumbnails")
				}
			} else {
				cacheThumbnail(t, service, "/Files/to/folder/photo.jpg")
				if cache.puts.Load() != 3 {
					t.Fatal("replacement did not regenerate changed source version")
				}
				if indexedNode(t, service, "/Files/from/folder/photo.jpg") != 0 {
					t.Fatal("fully moved merged folder remains attached")
				}
			}
		})
	}
}

func TestIndexedSourceOperationsRespectCacheDeadline(t *testing.T) {
	service, root, cache := indexedService(t)
	if err := os.Mkdir(filepath.Join(root, "folder"), 0700); err != nil {
		t.Fatal(err)
	}
	writeThumbnailTestImage(t, filepath.Join(root, "folder", "photo.jpg"), color.RGBA{R: 255, A: 255})
	cacheThumbnail(t, service, "/Files/folder/photo.jpg")
	cache.block = true
	start := time.Now()
	if err := service.Delete(context.Background(), "/Files/folder"); err != nil {
		t.Fatal(err)
	}
	if time.Since(start) > time.Second {
		t.Fatal("delete blocked on thumbnail cleanup")
	}
}

func TestIndexedImageChangedDuringGenerationIsNotPublished(t *testing.T) {
	service, root, cache := indexedService(t)
	path := filepath.Join(root, "photo.jpg")
	writeThumbnailTestImage(t, path, color.RGBA{R: 255, A: 255})
	cache.beforeGet = func() { writeThumbnailTestImage(t, path, color.RGBA{B: 255, A: 255}) }
	if _, err := service.Thumbnail(context.Background(), "/Files/photo.jpg", 320, 320); err == nil {
		t.Fatal("changed source was published under old key")
	}
	if cache.puts.Load() != 0 {
		t.Fatal("changed image entered blob cache")
	}
	cacheThumbnail(t, service, "/Files/photo.jpg")
	if cache.puts.Load() != 1 {
		t.Fatal("current version did not regenerate")
	}
}

func TestIndexedMovesBetweenRootsAndFilesystems(t *testing.T) {
	for _, crossDevice := range []bool{false, true} {
		t.Run(fmt.Sprintf("cross-device=%v", crossDevice), func(t *testing.T) {
			service, from, cache := indexedService(t)
			to := t.TempDir()
			if crossDevice {
				var err error
				to, err = os.MkdirTemp("/dev/shm", "webfile-index-move-")
				if err != nil {
					t.Skip("tmpfs unavailable")
				}
				t.Cleanup(func() { _ = os.RemoveAll(to) })
			}
			resolver, err := NewResolver([]Root{{Name: "From", Path: from}, {Name: "To", Path: to}})
			if err != nil {
				t.Fatal(err)
			}
			service.resolver = resolver
			if err := os.Mkdir(filepath.Join(from, "folder"), 0700); err != nil {
				t.Fatal(err)
			}
			writeThumbnailTestImage(t, filepath.Join(from, "folder", "photo.jpg"), color.RGBA{R: 255, A: 255})
			cacheThumbnail(t, service, "/From/folder/photo.jpg")
			if err := service.NewTransfer("move", []string{"/From/folder"}, "/To").Run(context.Background(), nil); err != nil {
				t.Fatal(err)
			}
			cacheThumbnail(t, service, "/To/folder/photo.jpg")
			want := int64(1)
			if crossDevice {
				want = 2
			}
			if cache.puts.Load() != want {
				t.Fatalf("generated thumbnails=%d want=%d", cache.puts.Load(), want)
			}
			if indexedNode(t, service, "/From/folder/photo.jpg") != 0 || indexedNode(t, service, "/To/folder/photo.jpg") == 0 {
				t.Fatal("cross-root references were not retired/created")
			}
		})
	}
}

func TestIndexedRenameCancellationWhileCapturingLeavesSource(t *testing.T) {
	service, root, cache := indexedService(t)
	if err := os.Mkdir(filepath.Join(root, "folder"), 0700); err != nil {
		t.Fatal(err)
	}
	cache.block = true
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	if err := service.Rename(ctx, "/Files/folder", "/Files/renamed"); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("rename after cancellation: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "folder")); err != nil {
		t.Fatal("cancelled rename removed source")
	}
}
