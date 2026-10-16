package filesystem

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestCancelledFolderSizeRequestsLeaveNoQueuedScans(t *testing.T) {
	service, root := testService(t)
	service.folderSizeSlots <- struct{}{}
	service.folderSizeSlots <- struct{}{}
	defer func() { <-service.folderSizeSlots; <-service.folderSizeSlots }()
	for index := 0; index < 40; index++ {
		name := fmt.Sprintf("folder-%d", index)
		if err := os.Mkdir(filepath.Join(root, name), 0700); err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithCancel(context.Background())
		_, err := service.DetailsWithFolderSize(ctx, "/Files/"+name, true)
		cancel()
		if err != nil {
			t.Fatal(err)
		}
	}
	deadline := time.Now().Add(time.Second)
	for {
		service.folderSizeMu.Lock()
		pending := len(service.folderSizePending)
		service.folderSizeMu.Unlock()
		if pending == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("%d abandoned scans remain queued", pending)
		}
		time.Sleep(time.Millisecond)
	}
}

func TestFolderSizeLeasesKeepOtherViewersAliveAndBoundTheQueue(t *testing.T) {
	service, root := testService(t)
	service.folderSizeSlots <- struct{}{}
	service.folderSizeSlots <- struct{}{}
	defer func() { <-service.folderSizeSlots; <-service.folderSizeSlots }()
	if err := os.Mkdir(filepath.Join(root, "shared"), 0700); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	if _, err := service.DetailsWithFolderSizeLease(ctx, "/Files/shared", true, "first"); err != nil {
		t.Fatal(err)
	}
	if _, err := service.DetailsWithFolderSizeLease(ctx, "/Files/shared", true, "second"); err != nil {
		t.Fatal(err)
	}
	cancel() // Finishing the short HTTP request must not cancel a leased scan.
	service.CancelFolderSizeLease("first")
	service.folderSizeMu.Lock()
	job := service.folderSizePending[filepath.Join(root, "shared")]
	alive := job != nil && job.ctx.Err() == nil && len(job.leases) == 1
	service.folderSizeMu.Unlock()
	if !alive {
		t.Fatal("closing one viewer cancelled another viewer's scan")
	}
	for index := 0; index < 40; index++ {
		name := fmt.Sprintf("queued-%d", index)
		if err := os.Mkdir(filepath.Join(root, name), 0700); err != nil {
			t.Fatal(err)
		}
		_, _ = service.DetailsWithFolderSizeLease(context.Background(), "/Files/"+name, true, name)
	}
	service.folderSizeMu.Lock()
	count := len(service.folderSizePending)
	service.folderSizeMu.Unlock()
	if count > maxFolderSizeJobs {
		t.Fatalf("unbounded queue: %d", count)
	}
	service.Close() // Shutdown cancels queued work even though both IO slots are occupied.
}

func TestFolderSizesCarryScanTimeAndInvalidateAfterNestedWrites(t *testing.T) {
	s, root := testService(t)
	if err := os.MkdirAll(filepath.Join(root, "parent", "nested"), 0700); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(root, "parent", "nested", "file.txt")
	if err := os.WriteFile(file, []byte("old"), 0600); err != nil {
		t.Fatal(err)
	}
	before := time.Now()
	ready := waitForFolderSize(t, s, "/Files/parent", 3)
	if ready.SizeCalculatedAt == nil || ready.SizeCalculatedAt.Before(before) {
		t.Fatalf("missing scan time: %#v", ready)
	}
	unchanged, err := s.DetailsWithFolderSize(context.Background(), "/Files/parent", false)
	if err != nil || unchanged.SizeRefreshing || unchanged.SizeCalculatedAt == nil || !unchanged.SizeCalculatedAt.Equal(*ready.SizeCalculatedAt) {
		t.Fatalf("unchanged folder was scanned again: %#v, %v", unchanged, err)
	}
	listed, err := s.List(context.Background(), "/Files")
	if err != nil || len(listed) != 1 || listed[0].SizeCalculatedAt == nil || !listed[0].SizeCalculatedAt.Equal(*ready.SizeCalculatedAt) {
		t.Fatalf("listing lost timestamp: %#v, %v", listed, err)
	}
	if _, err := s.WriteText(context.Background(), "/Files/parent/nested/file.txt", []byte("updated content"), "", false); err != nil {
		t.Fatal(err)
	}
	listed, err = s.List(context.Background(), "/Files")
	if err != nil || listed[0].SizeStatus == "ready" {
		t.Fatalf("nested write left cached ancestor: %#v, %v", listed, err)
	}
	updated := waitForFolderSize(t, s, "/Files/parent", 15)
	if updated.SizeCalculatedAt == nil || !updated.SizeCalculatedAt.After(*ready.SizeCalculatedAt) {
		t.Fatalf("scan time did not advance: %#v", updated)
	}
	// External edits to existing descendants do not change the parent's mtime.
	// The freshness limit must still force a scan, including configured roots.
	s.folderSizeMu.Lock()
	cache := s.folderSizeCache[filepath.Join(root, "parent")]
	cache.calculatedAt = time.Now().Add(-folderSizeCacheTTL)
	s.folderSizeCache[filepath.Join(root, "parent")] = cache
	s.folderSizeMu.Unlock()
	if err := os.WriteFile(file, []byte("external"), 0600); err != nil {
		t.Fatal(err)
	}
	waitForFolderSize(t, s, "/Files/parent", 8)
	waitForFolderSize(t, s, "/Files", 8)
	s.InvalidateFolderSizes("/Files")
	s.folderSizeMu.Lock()
	cache = s.folderSizeCache[root]
	s.folderSizeMu.Unlock()
	if !cache.dirty {
		t.Fatal("configured root size was not invalidated")
	}
}

func TestListingRescansPreviouslyScannedFoldersAfterExternalChange(t *testing.T) {
	s, root := testService(t)
	folder := filepath.Join(root, "folder")
	if err := os.Mkdir(folder, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(folder, "old"), []byte("old"), 0600); err != nil {
		t.Fatal(err)
	}
	waitForFolderSize(t, s, "/Files/folder", 3)
	if err := os.WriteFile(filepath.Join(folder, "new"), []byte("new file"), 0600); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(time.Second)
	for {
		entries, err := s.List(context.Background(), "/Files")
		if err != nil {
			t.Fatal(err)
		}
		if entries[0].SizeStatus == "ready" && entries[0].Size == 11 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("listing kept obsolete folder size: %#v", entries)
		}
		time.Sleep(time.Millisecond)
	}
}
