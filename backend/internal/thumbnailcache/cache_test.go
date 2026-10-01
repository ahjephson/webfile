package thumbnailcache

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"testing"
)

func TestStorePersistsAcrossInstancesWithOneAtomicFile(t *testing.T) {
	dir := t.TempDir()
	first, err := Open(dir, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	want := Item{Data: []byte("thumbnail-data"), ContentType: "image/webp"}
	key := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	if err := first.Put(context.Background(), key, want); err != nil {
		t.Fatal(err)
	}
	second, err := Open(dir, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	got, ok, err := second.Get(context.Background(), key)
	if err != nil || !ok {
		t.Fatalf("get persisted thumbnail: ok=%v err=%v", ok, err)
	}
	if string(got.Data) != string(want.Data) || got.ContentType != want.ContentType {
		t.Fatalf("got %#v, want %#v", got, want)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || filepath.Ext(entries[0].Name()) != ".thumb" {
		t.Fatalf("cache files = %v", entries)
	}
}

func TestUnixClientRequiresMatchingPeerCapability(t *testing.T) {
	store, err := Open(t.TempDir(), 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	authorizer := NewAuthorizer()
	token, err := authorizer.Issue(uint32(os.Geteuid()))
	if err != nil {
		t.Fatal(err)
	}
	socket := filepath.Join(t.TempDir(), "cache.sock")
	listener, err := Listen(socket)
	if err != nil {
		t.Fatal(err)
	}
	server := &http.Server{Handler: authorizer.Handler(store.Handler()), ConnContext: ConnContext}
	go server.Serve(listener)
	t.Cleanup(func() { _ = server.Close() })

	key := "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	client := NewClient(socket, token)
	if err := client.Put(context.Background(), key, Item{Data: []byte("image"), ContentType: "image/jpeg"}); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := client.Get(context.Background(), key); err != nil || !ok {
		t.Fatalf("authorized get: ok=%v err=%v", ok, err)
	}
	if _, _, err := NewClient(socket, "wrong").Get(context.Background(), key); err == nil {
		t.Fatal("wrong cache capability was accepted")
	}
}
