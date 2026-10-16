//go:build linux

package filesystem

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

type gatedUploadCopyContext struct {
	context.Context
	started, release chan struct{}
	once             sync.Once
}

func (c *gatedUploadCopyContext) Value(key any) any {
	if _, copying := key.(copyChunkSizeContextKey); copying {
		c.once.Do(func() { close(c.started); <-c.release })
	}
	return c.Context.Value(key)
}

func TestCrossDiskUploadPublicationAllowsOtherUploadsAndCancellation(t *testing.T) {
	destination, err := os.MkdirTemp("/dev/shm", "webfile-upload-test-")
	if err != nil {
		t.Skip(err)
	}
	defer os.RemoveAll(destination)
	resolver, err := NewResolver([]Root{{Name: "Files", Path: destination}})
	if err != nil {
		t.Fatal(err)
	}
	manager := NewUploadManager(resolver, t.TempDir())
	upload, err := manager.Init(context.Background(), "/Files", "one.bin", 1, 1)
	if err != nil {
		t.Fatal(err)
	}
	gate := &gatedUploadCopyContext{Context: context.Background(), started: make(chan struct{}), release: make(chan struct{})}
	var release sync.Once
	defer release.Do(func() { close(gate.release) })
	appended := make(chan error, 1)
	go func() {
		_, _, err := manager.Append(gate, upload.ID, 0, bytes.NewReader([]byte("x")), 1)
		appended <- err
	}()
	select {
	case <-gate.started:
	case <-time.After(time.Second):
		t.Fatal("cross-disk publication did not start")
	}
	initialized := make(chan error, 1)
	go func() { _, err := manager.Init(context.Background(), "/Files", "two.bin", 1, 2); initialized <- err }()
	select {
	case err := <-initialized:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("publication blocked an independent upload")
	}
	cancelled := make(chan error, 1)
	go func() { cancelled <- manager.Cancel(upload.ID) }()
	select {
	case err := <-cancelled:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("publication blocked cancellation")
	}
	release.Do(func() { close(gate.release) })
	if err := <-appended; !errors.Is(err, context.Canceled) {
		t.Fatalf("append: %v", err)
	}
	if _, err := os.Stat(filepath.Join(destination, "one.bin")); !os.IsNotExist(err) {
		t.Fatalf("cancelled upload published: %v", err)
	}
}
