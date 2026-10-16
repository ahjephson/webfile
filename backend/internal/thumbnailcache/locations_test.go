package thumbnailcache

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func testLocation(names ...string) Location {
	location := Location{Root: HashName("configured-root")}
	for _, name := range names {
		location.Names = append(location.Names, HashName(name))
	}
	return location
}

func newLocationStore(t *testing.T) *Store {
	t.Helper()
	store, err := Open(t.TempDir(), 16<<20)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

func locationRequest(t *testing.T, store *Store, request LocationRequest) LocationResponse {
	t.Helper()
	response, err := store.Locations(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	return response
}

func captureLocation(t *testing.T, store *Store, path Location) LocationTicket {
	t.Helper()
	return locationRequest(t, store, LocationRequest{Action: "capture", Paths: []Location{path}}).Tickets[0]
}

func linkLocation(t *testing.T, store *Store, path Location, key string) LocationTicket {
	t.Helper()
	if err := store.Put(context.Background(), key, Item{Data: []byte("image"), ContentType: "image/jpeg"}); err != nil {
		t.Fatal(err)
	}
	ticket := locationRequest(t, store, LocationRequest{Action: "begin", Paths: []Location{path}}).Tickets[0]
	locationRequest(t, store, LocationRequest{Action: "link", Ticket: ticket, Key: key, Variant: HashName("320x320")})
	return captureLocation(t, store, path)
}

func cleanLocations(t *testing.T, store *Store) {
	t.Helper()
	index, err := store.locationIndex()
	if err != nil {
		t.Fatal(err)
	}
	for iteration := 0; iteration < 12; iteration++ {
		if err := index.cleanBatch(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
}

func TestLocationDeleteRecreateAndDelayedPublication(t *testing.T) {
	store := newLocationStore(t)
	path := testLocation("folder_%*", "photo.jpg")
	oldKey, newKey := HashName("old"), HashName("new")
	linkLocation(t, store, path, oldKey)
	folder := testLocation("folder_%*")
	old := captureLocation(t, store, folder)
	inflight := locationRequest(t, store, LocationRequest{Action: "begin", Paths: []Location{path}}).Tickets[0]
	change := LocationChange{Ticket: old, Purge: true}
	locationRequest(t, store, LocationRequest{Action: "apply", Changes: []LocationChange{change}})
	newTicket := linkLocation(t, store, path, newKey)
	// Both repeated cleanup and a late old generation must leave the new node.
	locationRequest(t, store, LocationRequest{Action: "apply", Changes: []LocationChange{change}})
	if _, err := store.Locations(context.Background(), LocationRequest{Action: "link", Ticket: inflight, Key: oldKey, Variant: HashName("320x320")}); err == nil {
		t.Fatal("old publication was accepted after delete/recreate")
	}
	cleanLocations(t, store)
	if current := captureLocation(t, store, path); current.Node != newTicket.Node || current.Node == old.Node {
		t.Fatalf("recreated node changed: %#v", current)
	}
	if _, ok, err := store.Get(context.Background(), oldKey); err != nil || ok {
		t.Fatalf("old blob remains: %v %v", ok, err)
	}
	if _, ok, err := store.Get(context.Background(), newKey); err != nil || !ok {
		t.Fatalf("new blob lost: %v %v", ok, err)
	}
}

func TestLocationConcurrentNewReferencePreventsOldCleanup(t *testing.T) {
	store := newLocationStore(t)
	path := testLocation("folder", "image.jpg")
	linkLocation(t, store, path, HashName("old"))
	old := captureLocation(t, store, testLocation("folder"))
	newKey := HashName("new")
	newTicket := linkLocation(t, store, path, newKey)
	locationRequest(t, store, LocationRequest{Action: "apply", Changes: []LocationChange{{Ticket: old, Purge: true}}})
	cleanLocations(t, store)
	if captureLocation(t, store, path).Node != newTicket.Node {
		t.Fatal("old cleanup removed a newer reference")
	}
	if _, ok, err := store.Get(context.Background(), newKey); err != nil || !ok {
		t.Fatalf("new blob lost: %v %v", ok, err)
	}
}

func TestLocationFolderMovesAndBatchedMutations(t *testing.T) {
	store := newLocationStore(t)
	key := HashName("image")
	initial := linkLocation(t, store, testLocation("before", "nested", "image.jpg"), key)
	for iteration := 0; iteration < 12; iteration++ {
		from, to := testLocation("before"), testLocation("after")
		if iteration%2 != 0 {
			from, to = to, from
		}
		ticket := captureLocation(t, store, from)
		locationRequest(t, store, LocationRequest{Action: "apply", Changes: []LocationChange{{Ticket: ticket, Destination: &to}}})
		child := Location{Root: to.Root, Names: append(append([]string(nil), to.Names...), HashName("nested"), HashName("image.jpg"))}
		if captureLocation(t, store, child).Node != initial.Node {
			t.Fatal("folder move replaced or lost cached descendants")
		}
		if captureLocation(t, store, from).Node != 0 {
			t.Fatal("old folder still attached")
		}
	}
	linkLocation(t, store, testLocation("other", "image.jpg"), key)
	tickets := locationRequest(t, store, LocationRequest{Action: "capture", Paths: []Location{testLocation("before"), testLocation("other")}}).Tickets
	locationRequest(t, store, LocationRequest{Action: "apply", Changes: []LocationChange{{Ticket: tickets[0], Purge: true}, {Ticket: tickets[1], Purge: true}}})
	cleanLocations(t, store)
	if _, ok, err := store.Get(context.Background(), key); err != nil || ok {
		t.Fatalf("batch cleanup failed: %v %v", ok, err)
	}
}

func TestLocationSharedBlobSurvivesAnotherNamespaceDelete(t *testing.T) {
	store := newLocationStore(t)
	first, second := testLocation("folder", "image.jpg"), testLocation("folder", "image.jpg")
	second.Root = HashName("another owner")
	key := HashName("shared image")
	one := linkLocation(t, store, first, key)
	linkLocation(t, store, second, key)
	locationRequest(t, store, LocationRequest{Action: "apply", Changes: []LocationChange{{Ticket: one, Purge: true}}})
	cleanLocations(t, store)
	if _, ok, err := store.Get(context.Background(), key); err != nil || !ok {
		t.Fatalf("shared blob deleted: %v %v", ok, err)
	}
	if captureLocation(t, store, second).Node == 0 {
		t.Fatal("other namespace reference deleted")
	}
}

func TestLocationPersistenceBoundsAndCorruptionRecovery(t *testing.T) {
	dir := t.TempDir()
	store, err := Open(dir, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	path, key := testLocation("folder", "image.jpg"), HashName("persistent")
	ticket := linkLocation(t, store, path, key)
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err = Open(dir, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if current := captureLocation(t, store, path); current.Node != ticket.Node || current.Epoch != ticket.Epoch {
		t.Fatal("index did not survive restart")
	}
	index, err := store.locationIndex()
	if err != nil {
		t.Fatal(err)
	}
	index.mu.Lock()
	index.limit = 4
	index.mu.Unlock()
	for iteration := 0; iteration < 12; iteration++ {
		linkLocation(t, store, testLocation(fmt.Sprintf("folder-%d", iteration), "image.jpg"), key)
	}
	index.mu.Lock()
	var count int64
	err = index.db.QueryRow("SELECT COUNT(*) FROM nodes").Scan(&count)
	index.mu.Unlock()
	if err != nil || count > 4 {
		t.Fatalf("index bound not enforced: %d %v", count, err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "locations.sqlite"), []byte("corrupt disposable index"), 0600); err != nil {
		t.Fatal(err)
	}
	recovered, err := Open(dir, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	defer recovered.Close()
	linkLocation(t, recovered, path, key)
	if _, ok, err := recovered.Get(context.Background(), key); err != nil || !ok {
		t.Fatalf("corrupt index affected blobs: %v %v", ok, err)
	}
}

func TestLocationCapabilityOwnershipAndRotation(t *testing.T) {
	store := newLocationStore(t)
	authorizer := NewAuthorizer()
	root := testLocation("image.jpg").Root
	issue := func(owner string) string {
		token, err := authorizer.IssueScoped(1000, owner, []string{root})
		if err != nil {
			t.Fatal(err)
		}
		return token
	}
	first, second, elevated := issue("1000:alice:normal"), issue("1001:bob:normal"), issue("1000:alice:elevated")
	handler := authorizer.Handler(store.Handler())
	request := func(token string, value LocationRequest, want int) LocationResponse {
		data, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		r := httptest.NewRequest(http.MethodPost, "/cache/locations", bytes.NewReader(data))
		r = r.WithContext(context.WithValue(r.Context(), peerUIDContextKey{}, uint32(1000)))
		r.Header.Set("Authorization", "Bearer "+token)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code != want {
			t.Fatalf("status=%d want=%d body=%s", w.Code, want, w.Body.String())
		}
		var result LocationResponse
		if want == 200 {
			if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
				t.Fatal(err)
			}
		}
		return result
	}
	path := testLocation("image.jpg")
	begin := request(first, LocationRequest{Action: "begin", Paths: []Location{path}}, 200).Tickets[0]
	request(first, LocationRequest{Action: "link", Ticket: begin, Key: HashName("image"), Variant: HashName("320")}, 200)
	for _, token := range []string{second, elevated} {
		got := request(token, LocationRequest{Action: "capture", Paths: []Location{path}}, 200)
		if got.Tickets[0].Node != 0 {
			t.Fatal("another authority can see directory references")
		}
		request(token, LocationRequest{Action: "apply", Changes: []LocationChange{{Ticket: begin, Purge: true}}}, 200)
	}
	request(first, LocationRequest{Action: "begin", Paths: []Location{{Root: HashName("unconfigured"), Names: path.Names}}}, 400)
	authorizer.Revoke(first)
	request(first, LocationRequest{Action: "capture", Paths: []Location{path}}, 401)
	rotated := issue("1000:alice:normal")
	if got := request(rotated, LocationRequest{Action: "capture", Paths: []Location{path}}, 200); got.Tickets[0].Node == 0 {
		t.Fatal("capability rotation lost stable namespace")
	}
	authorizer = NewAuthorizer()
	handler = authorizer.Handler(store.Handler())
	restarted := issue("1000:alice:normal")
	request(rotated, LocationRequest{Action: "capture", Paths: []Location{path}}, 401)
	if got := request(restarted, LocationRequest{Action: "capture", Paths: []Location{path}}, 200); got.Tickets[0].Node == 0 {
		t.Fatal("broker restart lost stable namespace")
	}
}

func TestLocationCrossRootRetirementPreservesReusableBlob(t *testing.T) {
	store := newLocationStore(t)
	key := HashName("shared move image")
	path := testLocation("folder", "image.jpg")
	linkLocation(t, store, path, key)
	from := captureLocation(t, store, testLocation("folder"))
	destination := testLocation("moved")
	destination.Root = HashName("other configured root")
	locationRequest(t, store, LocationRequest{Action: "apply", Changes: []LocationChange{{Ticket: from, Destination: &destination}}})
	cleanLocations(t, store)
	if _, ok, err := store.Get(context.Background(), key); err != nil || !ok {
		t.Fatalf("cross-root move discarded reusable blob: %v %v", ok, err)
	}
	if captureLocation(t, store, path).Node != 0 {
		t.Fatal("source references remain attached")
	}
}

func TestLocationMoveRejectsDescendantCycle(t *testing.T) {
	store := newLocationStore(t)
	linkLocation(t, store, testLocation("folder", "photo.jpg"), HashName("image"))
	ticket := captureLocation(t, store, testLocation("folder"))
	destination := testLocation("folder", "nested", "moved")
	if _, err := store.Locations(context.Background(), LocationRequest{Action: "apply", Changes: []LocationChange{{Ticket: ticket, Destination: &destination}}}); err == nil {
		t.Fatal("directory index accepted a cycle")
	}
	if captureLocation(t, store, testLocation("folder")).Node != ticket.Node {
		t.Fatal("rejected move changed index")
	}
}

func TestLocationCleanupResumesAfterRestart(t *testing.T) {
	dir := t.TempDir()
	store, err := Open(dir, 16<<20)
	if err != nil {
		t.Fatal(err)
	}
	key := HashName("shared cached image")
	for index := 0; index < 100; index++ {
		linkLocation(t, store, testLocation("folder", fmt.Sprintf("image-%d.jpg", index)), key)
	}
	ticket := captureLocation(t, store, testLocation("folder"))
	locationRequest(t, store, LocationRequest{Action: "apply", Changes: []LocationChange{{Ticket: ticket, Purge: true}}})
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(dir, 16<<20)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	cleanLocations(t, reopened)
	if captureLocation(t, reopened, testLocation("folder")).Node != 0 {
		t.Fatal("detached folder reappeared after restart")
	}
	if _, ok, err := reopened.Get(context.Background(), key); err != nil || ok {
		t.Fatalf("interrupted cleanup left unreferenced blob: %v %v", ok, err)
	}
}

func BenchmarkLocationTrackingOverhead(b *testing.B) {
	for _, tracked := range []bool{false, true} {
		b.Run(fmt.Sprintf("tracking=%v", tracked), func(b *testing.B) {
			store, err := Open(b.TempDir(), 16<<20)
			if err != nil {
				b.Fatal(err)
			}
			defer store.Close()
			key := HashName("existing image")
			if err := store.Put(context.Background(), key, Item{Data: []byte("image"), ContentType: "image/jpeg"}); err != nil {
				b.Fatal(err)
			}
			path := testLocation("folder", "image.jpg")
			b.ResetTimer()
			for iteration := 0; iteration < b.N; iteration++ {
				if _, ok, err := store.Get(context.Background(), key); err != nil || !ok {
					b.Fatal(err)
				}
				if tracked {
					result, err := store.Locations(context.Background(), LocationRequest{Action: "begin", Paths: []Location{path}, Key: key, Variant: HashName("320")})
					if err != nil {
						b.Fatal(err)
					}
					if !result.Tickets[0].Linked {
						if _, err := store.Locations(context.Background(), LocationRequest{Action: "link", Ticket: result.Tickets[0], Key: key, Variant: HashName("320")}); err != nil {
							b.Fatal(err)
						}
					}
				}
			}
		})
	}
}

func BenchmarkLocationPublicationOverhead(b *testing.B) {
	for _, tracked := range []bool{false, true} {
		b.Run(fmt.Sprintf("tracking=%v", tracked), func(b *testing.B) {
			store, err := Open(b.TempDir(), 16<<20)
			if err != nil {
				b.Fatal(err)
			}
			defer store.Close()
			key := HashName("generated image")
			b.ResetTimer()
			for iteration := 0; iteration < b.N; iteration++ {
				if err := store.Put(context.Background(), key, Item{Data: []byte("image"), ContentType: "image/jpeg"}); err != nil {
					b.Fatal(err)
				}
				if tracked {
					path := testLocation("folder", fmt.Sprintf("image-%d.jpg", iteration))
					result, err := store.Locations(context.Background(), LocationRequest{Action: "begin", Paths: []Location{path}})
					if err != nil {
						b.Fatal(err)
					}
					if _, err := store.Locations(context.Background(), LocationRequest{Action: "link", Ticket: result.Tickets[0], Key: key, Variant: HashName("320")}); err != nil {
						b.Fatal(err)
					}
				}
			}
		})
	}
}
