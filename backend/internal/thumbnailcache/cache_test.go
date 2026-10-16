package thumbnailcache

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestCapabilitiesForConcurrentElevatedWorkersAreIndependent(t *testing.T) {
	authorizer := NewAuthorizer()
	first, err := authorizer.Issue(0)
	if err != nil {
		t.Fatal(err)
	}
	second, err := authorizer.Issue(0)
	if err != nil {
		t.Fatal(err)
	}
	handler := authorizer.Handler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) }))
	check := func(token string, uid uint32, want int) {
		t.Helper()
		request := httptest.NewRequest(http.MethodGet, "/cache/test", nil)
		request = request.WithContext(context.WithValue(request.Context(), peerUIDContextKey{}, uid))
		request.Header.Set("Authorization", "Bearer "+token)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != want {
			t.Fatalf("uid=%d status=%d want=%d", uid, response.Code, want)
		}
	}
	check(first, 0, http.StatusNoContent)
	check(second, 0, http.StatusNoContent)
	check(first, 1000, http.StatusUnauthorized)
	check("unissued", 0, http.StatusUnauthorized)
	authorizer.Revoke(first)
	check(first, 0, http.StatusUnauthorized)
	check(second, 0, http.StatusNoContent)
}

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
	t.Cleanup(func() { _ = store.Close() })
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
	movedKey := "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"
	if err := client.Move(context.Background(), key, movedKey); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := client.Get(context.Background(), key); err != nil || ok {
		t.Fatalf("old key after move: ok=%v err=%v", ok, err)
	}
	if _, ok, err := client.Get(context.Background(), movedKey); err != nil || !ok {
		t.Fatalf("new key after move: ok=%v err=%v", ok, err)
	}
	if err := client.Delete(context.Background(), movedKey); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := client.Get(context.Background(), movedKey); err != nil || ok {
		t.Fatalf("key after delete: ok=%v err=%v", ok, err)
	}
	if _, _, err := NewClient(socket, "wrong").Get(context.Background(), key); err == nil {
		t.Fatal("wrong cache capability was accepted")
	}
	location := testLocation("folder", "image.jpg")
	scopedToken, err := authorizer.IssueScoped(uint32(os.Geteuid()), "unix-test:normal", []string{location.Root})
	if err != nil {
		t.Fatal(err)
	}
	scopedClient := NewClient(socket, scopedToken)
	result, err := scopedClient.Locations(context.Background(), LocationRequest{Action: "begin", Paths: []Location{location}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := scopedClient.Locations(context.Background(), LocationRequest{Action: "link", Ticket: result.Tickets[0], Key: movedKey, Variant: HashName("320")}); err != nil {
		t.Fatal(err)
	}
	result, err = scopedClient.Locations(context.Background(), LocationRequest{Action: "capture", Paths: []Location{location}})
	if err != nil || result.Tickets[0].Node == 0 {
		t.Fatalf("scoped Unix location: %#v %v", result, err)
	}
	if _, err := client.Locations(context.Background(), LocationRequest{Action: "begin", Paths: []Location{location}}); err == nil {
		t.Fatal("unscoped capability accepted location mutations")
	}
}
