package thumbnailcache

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"time"

	_ "modernc.org/sqlite"
)

// Location contains only opaque hashes. The broker never receives source paths.
type Location struct {
	Root  string   `json:"root"`
	Names []string `json:"names"`
}

type LocationTicket struct {
	Location Location `json:"location"`
	Epoch    string   `json:"epoch"`
	Serial   string   `json:"serial,omitempty"`
	Node     int64    `json:"node,omitempty"`
	Linked   bool     `json:"linked,omitempty"`
}

type LocationChange struct {
	Ticket      LocationTicket `json:"ticket"`
	Destination *Location      `json:"destination,omitempty"`
	Purge       bool           `json:"purge,omitempty"`
}

type LocationRequest struct {
	Action  string           `json:"action"`
	Paths   []Location       `json:"paths,omitempty"`
	Ticket  LocationTicket   `json:"ticket,omitempty"`
	Key     string           `json:"key,omitempty"`
	Variant string           `json:"variant,omitempty"`
	Changes []LocationChange `json:"changes,omitempty"`
}

type LocationResponse struct {
	Tickets []LocationTicket `json:"tickets,omitempty"`
}

// LocationCache is optional: failure of derived metadata never blocks file IO.
type LocationCache interface {
	Locations(context.Context, LocationRequest) (LocationResponse, error)
}

func HashName(name string) string {
	sum := sha256.Sum256([]byte(name))
	return hex.EncodeToString(sum[:])
}

// RootKey is calculated from configuration, before symlink resolution, in both
// broker and worker. Config changes produce a new cache-only namespace.
func RootKey(name, path string, excludedNames, excludedPaths []string) string {
	data, _ := json.Marshal([]any{name, filepath.Clean(path), excludedNames, excludedPaths})
	return HashName(string(data))
}

type namespaceContextKey struct{}

func locationNamespace(ctx context.Context, root string) (string, error) {
	if err := validateKey(root); err != nil {
		return "", err
	}
	if scopes, scoped := ctx.Value(namespaceContextKey{}).(map[string]string); scoped {
		if namespace, ok := scopes[root]; ok {
			return namespace, nil
		}
		return "", errors.New("thumbnail location namespace is not authorized")
	}
	// Direct in-process stores are useful in development and tests. The HTTP
	// endpoint requires an explicitly scoped capability below.
	return root, nil
}

type locationIndex struct {
	mu    sync.Mutex
	db    *sql.DB
	stop  chan struct{}
	done  chan struct{}
	limit int64
	nodes int64
	store *Store
}

func (s *Store) locationIndex() (*locationIndex, error) {
	s.indexMu.Lock()
	defer s.indexMu.Unlock()
	if s.indexClosed {
		return nil, errors.New("thumbnail cache is closed")
	}
	if s.index != nil || s.indexErr != nil {
		return s.index, s.indexErr
	}
	path := filepath.Join(s.dir, "locations.sqlite")
	open := func() (*sql.DB, error) {
		file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
		if err != nil {
			return nil, err
		}
		_ = file.Close()
		db, err := sql.Open("sqlite", path)
		if err != nil {
			return nil, err
		}
		db.SetMaxOpenConns(1)
		_, err = db.Exec(`PRAGMA journal_mode=WAL; PRAGMA synchronous=NORMAL;
		PRAGMA foreign_keys=ON; PRAGMA busy_timeout=100; PRAGMA journal_size_limit=4194304;
		PRAGMA max_page_count=16384;
		CREATE TABLE IF NOT EXISTS namespaces (root TEXT PRIMARY KEY, epoch TEXT NOT NULL, serial TEXT NOT NULL);
		CREATE TABLE IF NOT EXISTS nodes (id INTEGER PRIMARY KEY AUTOINCREMENT, root TEXT NOT NULL, parent INTEGER NOT NULL, name TEXT NOT NULL, dead INTEGER NOT NULL DEFAULT 0, purge INTEGER NOT NULL DEFAULT 0);
		CREATE UNIQUE INDEX IF NOT EXISTS live_location ON nodes(root,parent,name) WHERE dead=0;
		CREATE INDEX IF NOT EXISTS children ON nodes(parent);
		CREATE INDEX IF NOT EXISTS cleanup ON nodes(dead) WHERE dead=1;
		CREATE TABLE IF NOT EXISTS refs (node INTEGER NOT NULL REFERENCES nodes(id) ON DELETE CASCADE, variant TEXT NOT NULL, key TEXT NOT NULL, PRIMARY KEY(node,variant));
		CREATE INDEX IF NOT EXISTS blob_refs ON refs(key);`)
		if err != nil {
			_ = db.Close()
			return nil, err
		}
		_ = os.Chmod(path, 0600)
		return db, nil
	}
	db, err := open()
	if err != nil {
		// Only disposable index files are removed; image blobs remain readable.
		for _, suffix := range []string{"", "-wal", "-shm"} {
			_ = os.Remove(path + suffix)
		}
		db, err = open()
	}
	if err != nil {
		s.indexErr = err
		return nil, err
	}
	index := &locationIndex{db: db, stop: make(chan struct{}), done: make(chan struct{}), store: s, limit: min(100000, max(1024, s.maxBytes/256))}
	if err := db.QueryRow("SELECT COUNT(*) FROM nodes").Scan(&index.nodes); err != nil {
		_ = db.Close()
		s.indexErr = err
		return nil, err
	}
	s.index = index
	go index.runCleanup()
	return index, nil
}

func (s *Store) Close() error {
	s.indexMu.Lock()
	if s.indexClosed {
		s.indexMu.Unlock()
		return nil
	}
	s.indexClosed = true
	index := s.index
	s.indexMu.Unlock()
	if index == nil {
		return nil
	}
	close(index.stop)
	<-index.done
	index.mu.Lock()
	defer index.mu.Unlock()
	return index.db.Close()
}

func randomRevision() string {
	var data [16]byte
	if _, err := rand.Read(data[:]); err != nil {
		panic("cannot generate cache revision: " + err.Error())
	}
	return hex.EncodeToString(data[:])
}

func validateLocation(location Location) error {
	if err := validateKey(location.Root); err != nil {
		return err
	}
	if len(location.Names) == 0 || len(location.Names) > 128 {
		return errors.New("invalid thumbnail location depth")
	}
	for _, name := range location.Names {
		if err := validateKey(name); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) Locations(ctx context.Context, request LocationRequest) (LocationResponse, error) {
	if len(request.Paths) > 2000 || len(request.Changes) > 2000 {
		return LocationResponse{}, errors.New("too many thumbnail locations")
	}
	locations := append([]Location(nil), request.Paths...)
	if request.Action == "link" {
		locations = append(locations, request.Ticket.Location)
	}
	for _, change := range request.Changes {
		locations = append(locations, change.Ticket.Location)
		if change.Destination != nil {
			locations = append(locations, *change.Destination)
			from, to := change.Ticket.Location, *change.Destination
			if from.Root == to.Root && len(to.Names) >= len(from.Names) && slices.Equal(to.Names[:len(from.Names)], from.Names) {
				return LocationResponse{}, errors.New("thumbnail directory move would create a cycle")
			}
		}
	}
	for _, location := range locations {
		if err := validateLocation(location); err != nil {
			return LocationResponse{}, err
		}
		if _, err := locationNamespace(ctx, location.Root); err != nil {
			return LocationResponse{}, err
		}
	}
	if request.Action == "link" || (request.Action == "begin" && (request.Key != "" || request.Variant != "")) {
		if err := validateKey(request.Key); err != nil {
			return LocationResponse{}, err
		}
		if err := validateKey(request.Variant); err != nil {
			return LocationResponse{}, err
		}
	}
	index, err := s.locationIndex()
	if err != nil {
		return LocationResponse{}, err
	}
	index.mu.Lock()
	defer index.mu.Unlock()
	initialNodes := index.nodes
	committed := false
	defer func() {
		if !committed {
			index.nodes = initialNodes
		}
	}()
	tx, err := index.db.BeginTx(ctx, nil)
	if err != nil {
		return LocationResponse{}, err
	}
	defer tx.Rollback()
	var response LocationResponse
	switch request.Action {
	case "begin", "capture":
		for _, location := range request.Paths {
			namespace, _ := locationNamespace(ctx, location.Root)
			var epoch, serial string
			err := tx.QueryRowContext(ctx, "SELECT epoch,serial FROM namespaces WHERE root=?", namespace).Scan(&epoch, &serial)
			if errors.Is(err, sql.ErrNoRows) && request.Action == "begin" {
				epoch, serial = randomRevision(), randomRevision()
				_, err = tx.ExecContext(ctx, "INSERT INTO namespaces(root,epoch,serial) VALUES(?,?,?)", namespace, epoch, serial)
			}
			if errors.Is(err, sql.ErrNoRows) {
				response.Tickets = append(response.Tickets, LocationTicket{Location: location})
				continue
			}
			if err != nil {
				return response, err
			}
			node, err := index.findNode(ctx, tx, namespace, location.Names, false)
			if err != nil {
				return response, err
			}
			linked := false
			if node != 0 && request.Action == "begin" && request.Key != "" {
				var key string
				err := tx.QueryRowContext(ctx, "SELECT key FROM refs WHERE node=? AND variant=?", node, request.Variant).Scan(&key)
				if err != nil && !errors.Is(err, sql.ErrNoRows) {
					return response, err
				}
				linked = key == request.Key
			}
			response.Tickets = append(response.Tickets, LocationTicket{Location: location, Epoch: epoch, Serial: serial, Node: node, Linked: linked})
		}
	case "link":
		ticket := request.Ticket
		namespace, _ := locationNamespace(ctx, ticket.Location.Root)
		var epoch string
		if err := tx.QueryRowContext(ctx, "SELECT epoch FROM namespaces WHERE root=?", namespace).Scan(&epoch); err != nil {
			return response, err
		}
		if epoch != ticket.Epoch {
			return response, errors.New("thumbnail location generation expired")
		}
		node, err := index.findNode(ctx, tx, namespace, ticket.Location.Names, true)
		if err != nil {
			return response, err
		}
		var existing string
		_ = tx.QueryRowContext(ctx, "SELECT key FROM refs WHERE node=? AND variant=?", node, request.Variant).Scan(&existing)
		if existing != request.Key {
			if existing == "" {
				var variants int
				if err := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM refs WHERE node=?", node).Scan(&variants); err != nil {
					return response, err
				}
				if variants >= 4 {
					if _, err := tx.ExecContext(ctx, "DELETE FROM refs WHERE node=?", node); err != nil {
						return response, err
					}
				}
			}
			if _, err := tx.ExecContext(ctx, "INSERT INTO refs(node,variant,key) VALUES(?,?,?) ON CONFLICT(node,variant) DO UPDATE SET key=excluded.key", node, request.Variant, request.Key); err != nil {
				return response, err
			}
			if _, err := tx.ExecContext(ctx, "UPDATE namespaces SET serial=? WHERE root=?", randomRevision(), namespace); err != nil {
				return response, err
			}
		}
	case "apply":
		// Validate against one pre-operation revision per namespace, then apply
		// the batch atomically. Concurrent publication causes a conservative miss.
		type revision struct{ epoch, serial string }
		before := map[string]revision{}
		matchedRoots := map[string]bool{}
		for _, change := range request.Changes {
			ticket := change.Ticket
			if ticket.Epoch == "" {
				continue
			}
			namespace, _ := locationNamespace(ctx, ticket.Location.Root)
			current, ok := before[namespace]
			if !ok {
				err := tx.QueryRowContext(ctx, "SELECT epoch,serial FROM namespaces WHERE root=?", namespace).Scan(&current.epoch, &current.serial)
				if errors.Is(err, sql.ErrNoRows) {
					continue
				}
				if err != nil {
					return response, err
				}
				before[namespace] = current
			}
			if current.epoch != ticket.Epoch {
				continue
			}
			matchedRoots[namespace] = true
			if current.serial == ticket.Serial && ticket.Node != 0 {
				found, err := index.findNode(ctx, tx, namespace, ticket.Location.Names, false)
				if err != nil {
					return response, err
				}
				if found == ticket.Node {
					moved := false
					if destination := change.Destination; destination != nil && destination.Root == ticket.Location.Root {
						existing, err := index.findNode(ctx, tx, namespace, destination.Names, false)
						if err != nil {
							return response, err
						}
						if existing == 0 {
							parent, err := index.findNode(ctx, tx, namespace, destination.Names[:len(destination.Names)-1], true)
							if err != nil {
								return response, err
							}
							_, err = tx.ExecContext(ctx, "UPDATE nodes SET parent=?,name=? WHERE id=? AND root=? AND dead=0", parent, destination.Names[len(destination.Names)-1], ticket.Node, namespace)
							if err != nil {
								return response, err
							}
							moved = true
						}
					}
					if !moved {
						if _, err := tx.ExecContext(ctx, "UPDATE nodes SET dead=1,purge=? WHERE id=? AND root=?", change.Purge, ticket.Node, namespace); err != nil {
							return response, err
						}
					}
				}
			}
		}
		for namespace := range before {
			// Even a skipped mutation invalidates in-flight old generation tickets.
			if matchedRoots[namespace] {
				if _, err := tx.ExecContext(ctx, "UPDATE namespaces SET epoch=?,serial=? WHERE root=?", randomRevision(), randomRevision(), namespace); err != nil {
					return response, err
				}
			}
		}
	default:
		return response, errors.New("invalid thumbnail location action")
	}
	// Cache-only metadata has a separate hard bound; resetting it is safe.
	var namespaces int64
	if err := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM namespaces").Scan(&namespaces); err != nil {
		return response, err
	}
	if index.nodes > index.limit || namespaces > 256 {
		if _, err := tx.ExecContext(ctx, "DELETE FROM refs; DELETE FROM nodes; DELETE FROM namespaces;"); err != nil {
			return response, err
		}
		index.nodes = 0
	}
	err = tx.Commit()
	committed = err == nil
	return response, err
}

func (index *locationIndex) findNode(ctx context.Context, tx *sql.Tx, root string, names []string, create bool) (int64, error) {
	var parent int64
	for _, name := range names {
		var node int64
		err := tx.QueryRowContext(ctx, "SELECT id FROM nodes WHERE root=? AND parent=? AND name=? AND dead=0", root, parent, name).Scan(&node)
		if errors.Is(err, sql.ErrNoRows) {
			if !create {
				return 0, nil
			}
			result, err := tx.ExecContext(ctx, "INSERT INTO nodes(root,parent,name) VALUES(?,?,?)", root, parent, name)
			if err != nil {
				return 0, err
			}
			node, err = result.LastInsertId()
			if err != nil {
				return 0, err
			}
			index.nodes++
		} else if err != nil {
			return 0, err
		}
		parent = node
	}
	return parent, nil
}

func (index *locationIndex) runCleanup() {
	defer close(index.done)
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-index.stop:
			return
		case <-ticker.C:
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			_ = index.cleanBatch(ctx)
			cancel()
		}
	}
}

func (index *locationIndex) cleanBatch(ctx context.Context) error {
	index.mu.Lock()
	defer index.mu.Unlock()
	tx, err := index.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, "SELECT id,purge FROM nodes WHERE dead=1 LIMIT 32")
	if err != nil {
		return err
	}
	type deadNode struct {
		id    int64
		purge bool
	}
	var nodes []deadNode
	for rows.Next() {
		var node deadNode
		if err := rows.Scan(&node.id, &node.purge); err != nil {
			rows.Close()
			return err
		}
		nodes = append(nodes, node)
	}
	readErr := rows.Err()
	rows.Close()
	if readErr != nil {
		return readErr
	}
	var keys []string
	for _, node := range nodes {
		if node.purge {
			rows, err := tx.QueryContext(ctx, "SELECT key FROM refs WHERE node=?", node.id)
			if err != nil {
				return err
			}
			for rows.Next() {
				var key string
				if err := rows.Scan(&key); err != nil {
					rows.Close()
					return err
				}
				keys = append(keys, key)
			}
			readErr := rows.Err()
			rows.Close()
			if readErr != nil {
				return readErr
			}
		}
		if _, err := tx.ExecContext(ctx, "UPDATE nodes SET dead=1,purge=? WHERE parent=?", node.purge, node.id); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, "DELETE FROM nodes WHERE id=?", node.id); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	index.nodes -= int64(len(nodes))
	for _, key := range keys {
		var count int
		if err := index.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM refs WHERE key=?", key).Scan(&count); err != nil {
			return err
		}
		if count == 0 {
			_ = index.store.Delete(ctx, key)
		}
	}
	return nil
}

func (s *Store) locationHandler(w http.ResponseWriter, r *http.Request) {
	if _, scoped := r.Context().Value(namespaceContextKey{}).(map[string]string); !scoped {
		http.Error(w, "scoped cache capability required", http.StatusForbidden)
		return
	}
	defer r.Body.Close()
	var request LocationRequest
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		http.Error(w, "invalid thumbnail location request", http.StatusBadRequest)
		return
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		http.Error(w, "invalid thumbnail location request", http.StatusBadRequest)
		return
	}
	response, err := s.Locations(r.Context(), request)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(response)
}

func (c *Client) Locations(ctx context.Context, request LocationRequest) (LocationResponse, error) {
	data, err := json.Marshal(request)
	if err != nil {
		return LocationResponse{}, err
	}
	req, err := c.request(ctx, http.MethodPost, "locations", bytes.NewReader(data))
	if err != nil {
		return LocationResponse{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	response, err := c.http.Do(req)
	if err != nil {
		return LocationResponse{}, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return LocationResponse{}, fmt.Errorf("thumbnail locations: %s", response.Status)
	}
	var result LocationResponse
	err = json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&result)
	return result, err
}
