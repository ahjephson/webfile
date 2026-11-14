// Standalone experiment only. This is not connected to WebFile's undo code.
package main

import (
	"database/sql"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"

	_ "modernc.org/sqlite"
)

const columns = "path,mode,device,inode,size,modified,changed"

type receipt struct {
	Path                                         string
	Mode, Device, Inode, Size, Modified, Changed int64
}

type measurement struct {
	Mode                  string  `json:"mode"`
	Go                    string  `json:"go"`
	SQLite                string  `json:"sqlite,omitempty"`
	Entries               int64   `json:"entries"`
	Seconds               float64 `json:"seconds"`
	EntriesPerSecond      float64 `json:"entries_per_second"`
	Batch                 int     `json:"batch"`
	CacheKiB              int     `json:"cache_kib"`
	DatabaseBytes         int64   `json:"database_bytes,omitempty"`
	AllocatedBytes        int64   `json:"allocated_bytes,omitempty"`
	PeakJournalFilesBytes int64   `json:"peak_journal_files_bytes,omitempty"`
	CheckpointSeconds     float64 `json:"checkpoint_seconds,omitempty"`
	Checksum              uint64  `json:"checksum,omitempty"`
}

func main() {
	mode := flag.String("mode", "", "fixture, record, validate, scan, or idle")
	fixture := flag.String("fixture", "", "isolated fixture directory")
	database := flag.String("db", "", "isolated journal database")
	count := flag.Int("count", 1000000, "regular files in fixture")
	batch := flag.Int("batch", 256, "maximum entries buffered; also transaction size")
	cache := flag.Int("cache-kib", 8192, "SQLite page-cache target")
	flag.Parse()
	if *batch < 1 || *batch > 1000 || *count < 1 || *cache < 1 {
		fail(errors.New("invalid benchmark configuration"))
	}
	result := measurement{Mode: *mode, Go: runtime.Version(), Batch: *batch, CacheKiB: *cache}
	start := time.Now()
	var err error
	switch *mode {
	case "idle":
	case "fixture":
		if *fixture == "" {
			fail(errors.New("fixture path required"))
		}
		err = createFixture(*fixture, *count)
		result.Entries = int64(*count) + 1
	case "record", "validate", "scan":
		if *database == "" || (*mode != "scan" && *fixture == "") {
			fail(errors.New("database and fixture paths required"))
		}
		var db *sql.DB
		db, err = openJournal(*database, *mode == "record", *cache)
		if err != nil {
			fail(err)
		}
		if err := db.QueryRow("SELECT sqlite_version()").Scan(&result.SQLite); err != nil {
			fail(err)
		}
		start = time.Now()
		switch *mode {
		case "record":
			err = record(db, *fixture, *database, *batch, &result)
		case "validate":
			err = validate(db, *fixture, *batch, &result)
		case "scan":
			err = scan(db, &result)
		}
		result.Seconds = time.Since(start).Seconds()
		if closeErr := db.Close(); err == nil {
			err = closeErr
		}
		result.DatabaseBytes, result.AllocatedBytes = journalSize(*database)
	default:
		err = errors.New("select a benchmark mode")
	}
	if err != nil {
		fail(err)
	}
	if result.Seconds == 0 {
		result.Seconds = time.Since(start).Seconds()
	}
	result.EntriesPerSecond = float64(result.Entries) / result.Seconds
	if err := json.NewEncoder(os.Stdout).Encode(result); err != nil {
		fail(err)
	}
}

func fail(err error) { fmt.Fprintln(os.Stderr, err); os.Exit(1) }

func createFixture(root string, count int) error {
	if err := os.Mkdir(root, 0700); err != nil {
		return err
	}
	for index := range count {
		name := fmt.Sprintf("holiday-2026-original-image-%09d.jpg", index)
		file, err := os.OpenFile(filepath.Join(root, name), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if err != nil {
			return err
		}
		if err := file.Close(); err != nil {
			return err
		}
	}
	dir, err := os.Open(root)
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}

func openJournal(name string, create bool, cache int) (*sql.DB, error) {
	if create {
		file, err := os.OpenFile(name, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0600)
		if err != nil {
			return nil, err
		}
		if err := file.Close(); err != nil {
			return nil, err
		}
	} else if _, err := os.Stat(name); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", name)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	settings := fmt.Sprintf(`PRAGMA journal_mode=WAL; PRAGMA synchronous=FULL;
PRAGMA cache_size=-%d; PRAGMA mmap_size=0; PRAGMA temp_store=FILE;
PRAGMA wal_autocheckpoint=1000; PRAGMA busy_timeout=5000;`, cache)
	if _, err := db.Exec(settings); err != nil {
		db.Close()
		return nil, err
	}
	if create {
		_, err = db.Exec(`CREATE TABLE entries (
path TEXT PRIMARY KEY, mode INTEGER NOT NULL, device INTEGER NOT NULL,
inode INTEGER NOT NULL, size INTEGER NOT NULL, modified INTEGER NOT NULL,
changed INTEGER NOT NULL) WITHOUT ROWID`)
		if err != nil {
			db.Close()
			return nil, err
		}
	}
	// Verify the settings rather than relying on silently accepted PRAGMAs.
	for name, expected := range map[string]int64{"synchronous": 2, "cache_size": -int64(cache), "mmap_size": 0, "temp_store": 1, "wal_autocheckpoint": 1000} {
		var actual int64
		if err := db.QueryRow("PRAGMA " + name).Scan(&actual); err != nil {
			db.Close()
			return nil, err
		}
		if actual != expected {
			db.Close()
			return nil, fmt.Errorf("%s=%d, expected %d", name, actual, expected)
		}
	}
	return db, nil
}

// Traversal never reads an entire directory or follows an entry symlink.
func walk(root string, batch int, emit func(receipt) error) error {
	fs, err := os.OpenRoot(root)
	if err != nil {
		return err
	}
	defer fs.Close()
	var visit func(string) error
	visit = func(relative string) error {
		info, err := fs.Lstat(relative)
		if err != nil {
			return err
		}
		stat := info.Sys().(*syscall.Stat_t)
		name := "/Files/destination/photos"
		if relative != "." {
			name += "/" + filepath.ToSlash(relative)
		}
		entry := receipt{name, int64(info.Mode()), int64(stat.Dev), int64(stat.Ino), info.Size(), info.ModTime().UnixNano(), stat.Ctim.Nano()}
		if err := emit(entry); err != nil {
			return err
		}
		if !info.IsDir() {
			return nil
		}
		dir, err := fs.OpenFile(relative, os.O_RDONLY|syscall.O_DIRECTORY|syscall.O_NOFOLLOW, 0)
		if err != nil {
			return err
		}
		defer dir.Close()
		for {
			children, readErr := dir.ReadDir(batch)
			for _, child := range children {
				if err := visit(filepath.Join(relative, child.Name())); err != nil {
					return err
				}
			}
			if errors.Is(readErr, io.EOF) {
				return nil
			}
			if readErr != nil {
				return readErr
			}
		}
	}
	return visit(".")
}

func record(db *sql.DB, fixture, name string, batch int, result *measurement) error {
	var tx *sql.Tx
	var statement *sql.Stmt
	inBatch := 0
	defer func() {
		if tx != nil {
			tx.Rollback()
		}
		if statement != nil {
			statement.Close()
		}
	}()
	commit := func() error {
		if tx == nil {
			return nil
		}
		if err := statement.Close(); err != nil {
			return err
		}
		statement = nil
		if err := tx.Commit(); err != nil {
			return err
		}
		tx = nil
		inBatch = 0
		logical, _ := journalSize(name)
		if logical > result.PeakJournalFilesBytes {
			result.PeakJournalFilesBytes = logical
		}
		return nil
	}
	err := walk(fixture, batch, func(entry receipt) error {
		if tx == nil {
			var err error
			tx, err = db.Begin()
			if err != nil {
				return err
			}
			statement, err = tx.Prepare("INSERT INTO entries(" + columns + ") VALUES(?,?,?,?,?,?,?)")
			if err != nil {
				return err
			}
		}
		if _, err := statement.Exec(entry.Path, entry.Mode, entry.Device, entry.Inode, entry.Size, entry.Modified, entry.Changed); err != nil {
			return err
		}
		result.Entries++
		inBatch++
		if inBatch == batch {
			return commit()
		}
		return nil
	})
	if err != nil {
		return err
	}
	if err := commit(); err != nil {
		return err
	}
	start := time.Now()
	var busy, pages, completed int
	if err := db.QueryRow("PRAGMA wal_checkpoint(TRUNCATE)").Scan(&busy, &pages, &completed); err != nil {
		return err
	}
	result.CheckpointSeconds = time.Since(start).Seconds()
	if busy != 0 {
		return errors.New("checkpoint did not complete")
	}
	return nil
}

func validate(db *sql.DB, fixture string, batch int, result *measurement) error {
	var expected int64
	if err := db.QueryRow("SELECT COUNT(*) FROM entries").Scan(&expected); err != nil {
		return err
	}
	placeholders := strings.TrimSuffix(strings.Repeat("?,", batch), ",")
	statement, err := db.Prepare("SELECT " + columns + " FROM entries WHERE path IN (" + placeholders + ")")
	if err != nil {
		return err
	}
	defer statement.Close()
	pending := make(map[string]receipt, batch)
	arguments := make([]any, 0, batch)
	check := func() error {
		if len(pending) == 0 {
			return nil
		}
		// NULL padding reuses one prepared statement for the final short batch.
		for len(arguments) < batch {
			arguments = append(arguments, nil)
		}
		rows, err := statement.Query(arguments...)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var entry receipt
			if err := rows.Scan(&entry.Path, &entry.Mode, &entry.Device, &entry.Inode, &entry.Size, &entry.Modified, &entry.Changed); err != nil {
				return err
			}
			actual, ok := pending[entry.Path]
			if !ok || actual != entry {
				return fmt.Errorf("changed entry: %s", entry.Path)
			}
			delete(pending, entry.Path)
		}
		if err := rows.Err(); err != nil {
			return err
		}
		if len(pending) != 0 {
			return errors.New("unexpected entry in filesystem")
		}
		clear(arguments)
		arguments = arguments[:0]
		return nil
	}
	err = walk(fixture, batch, func(entry receipt) error {
		pending[entry.Path] = entry
		arguments = append(arguments, entry.Path)
		result.Entries++
		if len(pending) == batch {
			return check()
		}
		return nil
	})
	if err != nil {
		return err
	}
	if err := check(); err != nil {
		return err
	}
	if result.Entries != expected {
		return fmt.Errorf("missing entries: filesystem=%d journal=%d", result.Entries, expected)
	}
	return nil
}

func scan(db *sql.DB, result *measurement) error {
	rows, err := db.Query("SELECT " + columns + " FROM entries ORDER BY path DESC")
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var entry receipt
		if err := rows.Scan(&entry.Path, &entry.Mode, &entry.Device, &entry.Inode, &entry.Size, &entry.Modified, &entry.Changed); err != nil {
			return err
		}
		result.Entries++
		result.Checksum += uint64(entry.Inode) + uint64(len(entry.Path))
	}
	return rows.Err()
}

func journalSize(name string) (logical, allocated int64) {
	for _, suffix := range []string{"", "-wal", "-shm"} {
		info, err := os.Stat(name + suffix)
		if err != nil {
			continue
		}
		logical += info.Size()
		allocated += info.Sys().(*syscall.Stat_t).Blocks * 512
	}
	return
}
