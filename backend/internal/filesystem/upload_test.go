package filesystem

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

type enteredUploadReader struct {
	io.ReadCloser
	entered chan struct{}
}

type gatedUploadCloser struct {
	*enteredUploadReader
	closing chan struct{}
	release chan struct{}
}

func (r *gatedUploadCloser) Close() error {
	close(r.closing)
	<-r.release
	return r.ReadCloser.Close()
}

func TestCancelledUploadFinishesBodyCleanupBeforeReturning(t *testing.T) {
	resolver, err := NewResolver([]Root{{Name: "Files", Path: t.TempDir()}})
	if err != nil {
		t.Fatal(err)
	}
	manager := NewUploadManager(resolver, t.TempDir())
	upload, err := manager.Init(context.Background(), "/Files", "slow.bin", 4, 1)
	if err != nil {
		t.Fatal(err)
	}
	reader, writer := io.Pipe()
	entered, closing, release := make(chan struct{}), make(chan struct{}), make(chan struct{})
	finished := make(chan error, 1)
	var workers sync.WaitGroup
	var released sync.Once
	workers.Add(1)
	go func() {
		defer workers.Done()
		body := &gatedUploadCloser{&enteredUploadReader{reader, entered}, closing, release}
		_, _, err := manager.Append(context.Background(), upload.ID, 0, body, 4)
		finished <- err
	}()
	t.Cleanup(func() {
		released.Do(func() { close(release) })
		_ = writer.Close()
		_ = reader.Close()
		workers.Wait()
	})
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("upload never started reading")
	}
	if err := manager.Cancel(upload.ID); err != nil {
		t.Fatal(err)
	}
	select {
	case <-closing:
	case <-time.After(time.Second):
		t.Fatal("cancellation did not start body cleanup")
	}
	_ = writer.CloseWithError(io.ErrUnexpectedEOF)
	select {
	case err := <-finished:
		t.Fatalf("append returned before body cleanup finished: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	// Waiting for a slow Close must also release the shared manager lock.
	independent := make(chan error, 1)
	workers.Add(1)
	go func() {
		defer workers.Done()
		_, err := manager.Init(context.Background(), "/Files", "independent.bin", 1, 2)
		independent <- err
	}()
	select {
	case err := <-independent:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("body cleanup blocked another upload")
	}
	released.Do(func() { close(release) })
	select {
	case err := <-finished:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("append returned %v; want cancellation", err)
		}
	case <-time.After(time.Second):
		t.Fatal("append did not return after body cleanup")
	}
}

func (r *enteredUploadReader) Read(p []byte) (int, error) {
	select {
	case <-r.entered:
	default:
		close(r.entered)
	}
	return r.ReadCloser.Read(p)
}

func TestStalledUploadCanBeCancelledWithoutBlockingOtherUploads(t *testing.T) {
	root := t.TempDir()
	resolver, err := NewResolver([]Root{{Name: "Files", Path: root}})
	if err != nil {
		t.Fatal(err)
	}
	manager := NewUploadManager(resolver, t.TempDir())
	upload, err := manager.Init(context.Background(), "/Files", "slow.bin", 4, 1)
	if err != nil {
		t.Fatal(err)
	}
	reader, writer := io.Pipe()
	entered := make(chan struct{})
	finished := make(chan error, 1)
	var workers sync.WaitGroup
	workers.Add(1)
	go func() {
		defer workers.Done()
		_, _, err := manager.Append(context.Background(), upload.ID, 0, &enteredUploadReader{reader, entered}, 4)
		finished <- err
	}()
	t.Cleanup(func() { _ = writer.Close(); _ = reader.Close(); workers.Wait() })
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("upload never started reading")
	}
	independent := make(chan error, 1)
	workers.Add(1)
	go func() {
		defer workers.Done()
		_, err := manager.Init(context.Background(), "/Files", "independent.bin", 4, 2)
		independent <- err
	}()
	select {
	case err := <-independent:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("stalled body blocked independent upload initialization")
	}
	cancelled := make(chan error, 1)
	workers.Add(1)
	go func() { defer workers.Done(); cancelled <- manager.Cancel(upload.ID) }()
	select {
	case err := <-cancelled:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("stalled body blocked cancellation")
	}
	select {
	case err := <-finished:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("append error = %v; want cancellation", err)
		}
	case <-time.After(time.Second):
		t.Fatal("cancelled upload kept reading its body")
	}
	if _, err := os.Stat(filepath.Join(root, "slow.bin")); !os.IsNotExist(err) {
		t.Fatalf("cancelled upload was published: %v", err)
	}
	if _, err := os.Stat(manager.recordPath(upload.ID)); !os.IsNotExist(err) {
		t.Fatalf("cancelled metadata was recreated: %v", err)
	}
}

func TestUploadPreservesTrailingFilenameSpace(t *testing.T) {
	root := t.TempDir()
	resolver, err := NewResolver([]Root{{Name: "Files", Path: root}})
	if err != nil {
		t.Fatal(err)
	}
	manager := NewUploadManager(resolver, t.TempDir())
	session, err := manager.Init(context.Background(), "/Files", "report ", 4, 1)
	if err != nil {
		t.Fatal(err)
	}
	if _, complete, err := manager.Append(context.Background(), session.ID, 0, bytes.NewBufferString("data"), 4); err != nil || !complete {
		t.Fatalf("complete=%v err=%v", complete, err)
	}
	if data, err := os.ReadFile(filepath.Join(root, "report ")); err != nil || string(data) != "data" {
		t.Fatalf("uploaded file: %q %v", data, err)
	}
	if _, err := os.Stat(filepath.Join(root, "report")); !os.IsNotExist(err) {
		t.Fatalf("filename was trimmed: %v", err)
	}
}

func TestUploadManagerResumesAndCompletes(t *testing.T) {
	root := t.TempDir()
	resolver, err := NewResolver([]Root{{Name: "Files", Path: root}})
	if err != nil {
		t.Fatal(err)
	}
	manager := NewUploadManager(resolver, filepath.Join(t.TempDir(), "uploads"))
	ctx := context.Background()

	session, err := manager.Init(ctx, "/Files", "movie.bin", 10, 1234)
	if err != nil {
		t.Fatal(err)
	}
	if _, complete, err := manager.Append(ctx, session.ID, 0, bytes.NewReader([]byte("1234")), 4); err != nil || complete {
		t.Fatalf("first chunk complete=%v err=%v", complete, err)
	}
	resumed, err := manager.Init(ctx, "/Files", "movie.bin", 10, 1234)
	if err != nil {
		t.Fatal(err)
	}
	if resumed.Offset != 4 {
		t.Fatalf("offset=%d want 4", resumed.Offset)
	}
	final, complete, err := manager.Append(ctx, resumed.ID, 4, bytes.NewReader([]byte("567890")), 6)
	if err != nil || !complete {
		t.Fatalf("final complete=%v err=%v", complete, err)
	}
	if final.Offset != 10 {
		t.Fatalf("offset=%d want 10", final.Offset)
	}
	data, err := os.ReadFile(filepath.Join(root, "movie.bin"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "1234567890" {
		t.Fatalf("content=%q", data)
	}
}

func TestUploadOffsetMismatchReportsExpected(t *testing.T) {
	root := t.TempDir()
	resolver, _ := NewResolver([]Root{{Name: "Files", Path: root}})
	manager := NewUploadManager(resolver, filepath.Join(t.TempDir(), "uploads"))
	session, err := manager.Init(context.Background(), "/Files", "x.bin", 2, 1)
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = manager.Append(context.Background(), session.ID, 1, bytes.NewReader([]byte("x")), 1)
	var offsetErr *UploadOffsetError
	if !errors.As(err, &offsetErr) || offsetErr.Expected != 0 {
		t.Fatalf("err=%v expected offset error", err)
	}
}
