package filesystem

import (
	"context"
	"errors"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"sort"
	"testing"
	"time"
)

func testService(t *testing.T) (*Service, string) {
	t.Helper()
	root := t.TempDir()
	resolver, err := NewResolver([]Root{{Name: "Files", Path: root}})
	if err != nil {
		t.Fatal(err)
	}
	service := NewService(resolver, 1024*1024)
	t.Cleanup(service.Close)
	return service, root
}

func TestListReadsFilesystemDirectly(t *testing.T) {
	svc, root := testService(t)
	if err := os.WriteFile(filepath.Join(root, "hello.txt"), []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, "folder"), 0o755); err != nil {
		t.Fatal(err)
	}

	entries, err := svc.List(context.Background(), "/Files")
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Fatalf("got %d entries, want 2", len(entries))
	}
	if !entries[0].IsDir || entries[0].Name != "folder" {
		t.Fatalf("directories should sort first, got %#v", entries[0])
	}
	if entries[1].Name != "hello.txt" || entries[1].IsDir {
		t.Fatalf("unexpected file entry %#v", entries[1])
	}
}

func TestListNeverExposesExtractionStagingDirectories(t *testing.T) {
	svc, root := testService(t)
	if err := os.Mkdir(filepath.Join(root, ".webfile-extract-operation"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, ".ordinary-hidden"), 0o700); err != nil {
		t.Fatal(err)
	}
	entries, err := svc.List(context.Background(), "/Files")
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name != ".ordinary-hidden" {
		t.Fatalf("entries=%v", entries)
	}
}

func TestListOmitsRootExcludedNames(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"lost+found", "visible"} {
		if err := os.Mkdir(filepath.Join(root, name), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	nested := filepath.Join(root, "visible")
	if err := os.Mkdir(filepath.Join(nested, "lost+found"), 0o755); err != nil {
		t.Fatal(err)
	}
	resolver, err := NewResolver([]Root{{Name: "Files", Path: root, ExcludedNames: []string{"lost+found"}}})
	if err != nil {
		t.Fatal(err)
	}
	service := NewService(resolver, 1024*1024)
	t.Cleanup(service.Close)

	entries, err := service.List(context.Background(), "/Files")
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name != "visible" {
		t.Fatalf("entries=%v", entries)
	}
	entries, err = service.List(context.Background(), "/Files/visible")
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("nested entries=%v", entries)
	}
}

func TestListReportsDirectoryNavigationAccess(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "accessible"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, "restricted"), 0o000); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "file.txt"), []byte("file"), 0o644); err != nil {
		t.Fatal(err)
	}
	resolver, err := NewResolver([]Root{{Name: "Files", Path: root}})
	if err != nil {
		t.Fatal(err)
	}
	service := NewService(resolver, 1024*1024)
	t.Cleanup(service.Close)

	entries, err := service.List(context.Background(), "/Files")
	if err != nil {
		t.Fatal(err)
	}
	byName := make(map[string]Entry, len(entries))
	for _, entry := range entries {
		byName[entry.Name] = entry
	}
	if value := byName["accessible"].CanNavigate; value == nil || !*value {
		t.Fatalf("accessible directory navigation = %v", value)
	}
	if value := byName["restricted"].CanNavigate; value == nil || *value {
		t.Fatalf("restricted directory navigation = %v", value)
	}
	if value := byName["file.txt"].CanNavigate; value != nil {
		t.Fatalf("file navigation = %v, want nil", *value)
	}
}

func TestListSortsMixedCaseNamesUsingHostCollation(t *testing.T) {
	svc, root := testService(t)
	want := []string{"z", "A", "a", "Z"}
	for _, name := range want {
		if err := os.WriteFile(filepath.Join(root, name), []byte(name), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	sort.Slice(want, func(i, j int) bool {
		left := hostCollationKey(want[i])
		right := hostCollationKey(want[j])
		if left == right {
			return want[i] < want[j]
		}
		return left < right
	})

	entries, err := svc.List(context.Background(), "/Files")
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != len(want) {
		t.Fatalf("got %d entries, want %d", len(entries), len(want))
	}
	for index, name := range want {
		if entries[index].Name != name {
			t.Fatalf("entry %d is %q, want %q", index, entries[index].Name, name)
		}
	}
}

func TestNewDocumentIsCreatedOnlyOnSave(t *testing.T) {
	svc, root := testService(t)
	path := "/Files/LICENSE"
	if _, err := os.Stat(filepath.Join(root, "LICENSE")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("file should not exist before save: %v", err)
	}

	written, err := svc.WriteText(context.Background(), path, []byte("terms"), "", true)
	if err != nil {
		t.Fatal(err)
	}
	if written.ETag == "" {
		t.Fatal("expected an etag")
	}
	data, err := os.ReadFile(filepath.Join(root, "LICENSE"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "terms" {
		t.Fatalf("got %q", data)
	}
}

func TestWriteTextThroughSymlinkPreservesLinkAndUpdatesTarget(t *testing.T) {
	svc, root := testService(t)
	target := filepath.Join(root, "target.txt")
	link := filepath.Join(root, "link.txt")
	if err := os.WriteFile(target, []byte("before"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("target.txt", link); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.WriteText(context.Background(), "/Files/link.txt", []byte("after"), "", false); err != nil {
		t.Fatal(err)
	}
	info, err := os.Lstat(link)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		t.Fatal("save replaced the symbolic link")
	}
	content, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "after" {
		t.Fatalf("target content=%q", content)
	}
}

func TestDetectContentTypeRecognisesExtensionlessText(t *testing.T) {
	svc, root := testService(t)
	if err := os.WriteFile(filepath.Join(root, "LICENSE"), []byte("Terms and conditions\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	result, err := svc.DetectContentType(context.Background(), "/Files/LICENSE")
	if err != nil {
		t.Fatal(err)
	}
	if result.ContentType != "text/plain; charset=utf-8" {
		t.Fatalf("got %q", result.ContentType)
	}
}

func TestDetectContentTypeDoesNotTreatExtensionlessBinaryAsText(t *testing.T) {
	svc, root := testService(t)
	if err := os.WriteFile(filepath.Join(root, "payload"), []byte{0, 1, 2, 3, 4}, 0o644); err != nil {
		t.Fatal(err)
	}

	result, err := svc.DetectContentType(context.Background(), "/Files/payload")
	if err != nil {
		t.Fatal(err)
	}
	if result.ContentType != "application/octet-stream" {
		t.Fatalf("got %q", result.ContentType)
	}
}

func TestDetectContentTypeRecognisesExtensionlessImage(t *testing.T) {
	svc, root := testService(t)
	file, err := os.Create(filepath.Join(root, "picture"))
	if err != nil {
		t.Fatal(err)
	}
	picture := image.NewRGBA(image.Rect(0, 0, 1, 1))
	picture.Set(0, 0, color.RGBA{R: 1, G: 2, B: 3, A: 255})
	if err := png.Encode(file, picture); err != nil {
		_ = file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}

	result, err := svc.DetectContentType(context.Background(), "/Files/picture")
	if err != nil {
		t.Fatal(err)
	}
	if result.ContentType != "image/png" {
		t.Fatalf("got %q", result.ContentType)
	}
}

func TestDetectContentTypeDoesNotTrustMisleadingImageExtension(t *testing.T) {
	svc, root := testService(t)
	if err := os.WriteFile(filepath.Join(root, "not-an-image.jpg"), []byte("plain text"), 0o644); err != nil {
		t.Fatal(err)
	}

	result, err := svc.DetectContentType(context.Background(), "/Files/not-an-image.jpg")
	if err != nil {
		t.Fatal(err)
	}
	if result.ContentType != "text/plain; charset=utf-8" {
		t.Fatalf("got %q", result.ContentType)
	}
}

func TestCreateOnlyConflictsIfTargetAppears(t *testing.T) {
	svc, root := testService(t)
	if err := os.WriteFile(filepath.Join(root, "notes.txt"), []byte("other writer"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := svc.WriteText(context.Background(), "/Files/notes.txt", []byte("mine"), "", true)
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("got %v, want ErrConflict", err)
	}
}

func TestConfiguredRootCannotBeDeletedOrRenamed(t *testing.T) {
	svc, _ := testService(t)
	if err := svc.Delete(context.Background(), "/Files"); err == nil {
		t.Fatal("expected root deletion to be refused")
	}
	if err := svc.Rename(context.Background(), "/Files", "/Files2"); err == nil {
		t.Fatal("expected root rename to be refused")
	}
}

func TestSymlinkCannotEscapeRoot(t *testing.T) {
	svc, root := testService(t)
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, "outside")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	_, err := svc.List(context.Background(), "/Files/outside")
	if !errors.Is(err, ErrOutsideRoot) {
		t.Fatalf("got %v, want ErrOutsideRoot", err)
	}
}

func TestDeletingSymlinkDoesNotDeleteTarget(t *testing.T) {
	svc, root := testService(t)
	target := filepath.Join(root, "target")
	if err := os.Mkdir(target, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(target, "keep.txt"), []byte("keep"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(root, "link")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if err := svc.Delete(context.Background(), "/Files/link"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(target, "keep.txt")); err != nil {
		t.Fatalf("target content was removed: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(root, "link")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("symlink still exists: %v", err)
	}
}

func TestDetailsWithFolderSizeCalculatesCachesAndRefreshes(t *testing.T) {
	svc, root := testService(t)
	folder := filepath.Join(root, "folder")
	if err := os.Mkdir(folder, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(folder, "first.txt"), []byte("first"), 0o644); err != nil {
		t.Fatal(err)
	}

	initial, err := svc.DetailsWithFolderSize(context.Background(), "/Files/folder", true)
	if err != nil {
		t.Fatal(err)
	}
	if initial.SizeStatus != "calculating" || !initial.SizeRefreshing {
		t.Fatalf("unexpected initial status: %#v", initial)
	}

	ready := waitForFolderSize(t, svc, "/Files/folder", 5)
	if ready.SizeRefreshing {
		t.Fatalf("completed result should not be refreshing: %#v", ready)
	}

	if err := os.WriteFile(filepath.Join(folder, "second.txt"), []byte("second"), 0o644); err != nil {
		t.Fatal(err)
	}
	cached, err := svc.DetailsWithFolderSize(context.Background(), "/Files/folder", true)
	if err != nil {
		t.Fatal(err)
	}
	if cached.Size != 5 || cached.SizeStatus != "ready" || !cached.SizeRefreshing {
		t.Fatalf("expected cached value during refresh: %#v", cached)
	}
	waitForFolderSize(t, svc, "/Files/folder", 11)
}

func TestDetailsWithFolderSizeExpiresOldCache(t *testing.T) {
	svc, root := testService(t)
	folder := filepath.Join(root, "folder")
	if err := os.Mkdir(folder, 0o755); err != nil {
		t.Fatal(err)
	}
	svc.folderSizeCache[folder] = folderSizeCacheEntry{size: 99, calculatedAt: time.Now().Add(-folderSizeCacheTTL)}

	details, err := svc.DetailsWithFolderSize(context.Background(), "/Files/folder", false)
	if err != nil {
		t.Fatal(err)
	}
	if details.SizeStatus != "calculating" || details.Size != 0 {
		t.Fatalf("expired cache should be recalculated: %#v", details)
	}
}

func TestListIncludesOnlyAlreadyCachedFolderSizes(t *testing.T) {
	svc, root := testService(t)
	folder := filepath.Join(root, "folder")
	if err := os.Mkdir(folder, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(folder, "content.txt"), []byte("content"), 0o644); err != nil {
		t.Fatal(err)
	}

	uncached, err := svc.List(context.Background(), "/Files")
	if err != nil {
		t.Fatal(err)
	}
	if uncached[0].SizeStatus != "" {
		t.Fatalf("listing should not calculate an uncached size: %#v", uncached[0])
	}
	if svc.folderSizePending[folder] {
		t.Fatal("listing unexpectedly started a folder size calculation")
	}

	svc.folderSizeCache[folder] = folderSizeCacheEntry{size: 7, calculatedAt: time.Now()}
	cached, err := svc.List(context.Background(), "/Files")
	if err != nil {
		t.Fatal(err)
	}
	if cached[0].Size != 7 || cached[0].SizeStatus != "ready" {
		t.Fatalf("cached folder size was not included: %#v", cached[0])
	}
}

func waitForFolderSize(t *testing.T, svc *Service, path string, expected int64) Details {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		details, err := svc.DetailsWithFolderSize(context.Background(), path, false)
		if err != nil {
			t.Fatal(err)
		}
		if details.SizeStatus == "ready" && details.Size == expected && !details.SizeRefreshing {
			return details
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("folder size did not become %d", expected)
	return Details{}
}
