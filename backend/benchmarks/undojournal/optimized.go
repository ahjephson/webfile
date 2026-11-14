package main

import (
	"bufio"
	"database/sql"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

const binaryMagic = "WFUBEN01"
const bufferBytes = 1 << 20
const appendSchema = `CREATE TABLE entries (
seq INTEGER PRIMARY KEY, path TEXT NOT NULL, mode INTEGER NOT NULL,
device INTEGER NOT NULL, inode INTEGER NOT NULL, size INTEGER NOT NULL,
modified INTEGER NOT NULL, changed INTEGER NOT NULL)`

func optimized(mode, writer, input, fixture, output string, batch, commitEntries, cache int, result *measurement, syncInterval ...int) error {
	syncEntries := 0
	if len(syncInterval) > 0 {
		syncEntries = syncInterval[0]
	}
	if syncEntries < 0 {
		return errors.New("invalid sync interval")
	}
	result.Writer = writer
	result.Source = "filesystem"
	if input != "" {
		result.Source = "pre-captured metadata stream"
	}
	if mode == "validate-stream" {
		if fixture == "" || output == "" {
			return errors.New("fixture and db paths required")
		}
		return validateStream(writer, fixture, output, batch, cache, result)
	}
	if input == "" && fixture == "" {
		return errors.New("input or fixture required")
	}
	feed := func(emit func(receipt) error) error {
		observe := func(entry receipt) error {
			if err := emit(entry); err != nil {
				return err
			}
			result.Entries++
			result.Checksum ^= receiptChecksum(entry)
			return nil
		}
		if input != "" {
			return readBinary(input, observe)
		}
		return walk(fixture, batch, observe)
	}
	start := time.Now()
	var err error
	switch writer {
	case "discard":
		result.SyncPolicy = "no output; source decoding baseline"
		err = feed(func(receipt) error { return nil })
	case "binary":
		result.SyncPolicy = "buffered writes, file fsync and parent-directory fsync at completion"
		if syncEntries > 0 {
			result.SyncEntries = syncEntries
			result.SyncPolicy = "periodic buffered flush and file fsync, plus completion file and parent-directory fsync"
		}
		err = writeBinary(output, syncEntries, feed)
	case "indexed-bulk", "append-sql":
		if commitEntries < batch || commitEntries%batch != 0 {
			return errors.New("commit entries must be a positive multiple of batch size")
		}
		result.CommitEntries = commitEntries
		result.SyncPolicy = "WAL/FULL transactions and final checkpoint"
		schema := appendSchema
		if writer == "indexed-bulk" {
			schema = `CREATE TABLE entries (path TEXT PRIMARY KEY, mode INTEGER NOT NULL, device INTEGER NOT NULL, inode INTEGER NOT NULL, size INTEGER NOT NULL, modified INTEGER NOT NULL, changed INTEGER NOT NULL) WITHOUT ROWID`
		}
		var db *sql.DB
		db, err = openJournalSchema(output, true, cache, schema)
		if err != nil {
			return err
		}
		if err := db.QueryRow("SELECT sqlite_version()").Scan(&result.SQLite); err != nil {
			db.Close()
			return err
		}
		start = time.Now()
		err = bulkWrite(db, output, batch, commitEntries, feed, result)
		if closeErr := db.Close(); err == nil {
			err = closeErr
		}
		if err == nil {
			err = syncParent(output)
		}
	default:
		return errors.New("unknown capture writer")
	}
	result.Seconds = time.Since(start).Seconds()
	if writer != "discard" {
		result.DatabaseBytes, result.AllocatedBytes = journalSize(output)
	}
	return err
}

func bulkWrite(db *sql.DB, name string, batch, commitEntries int, feed func(func(receipt) error) error, result *measurement) error {
	query := func(rows int) string {
		return "INSERT INTO entries(" + columns + ") VALUES " + strings.TrimSuffix(strings.Repeat("(?,?,?,?,?,?,?),", rows), ",")
	}
	fullQuery := query(batch)
	args := make([]any, 0, batch*7)
	var tx *sql.Tx
	var statement *sql.Stmt
	transactionRows := 0
	defer func() {
		if statement != nil {
			statement.Close()
		}
		if tx != nil {
			tx.Rollback()
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
		transactionRows = 0
		size, _ := journalSize(name)
		if size > result.PeakJournalFilesBytes {
			result.PeakJournalFilesBytes = size
		}
		return nil
	}
	flush := func() error {
		if len(args) == 0 {
			return nil
		}
		if tx == nil {
			var err error
			tx, err = db.Begin()
			if err != nil {
				return err
			}
			statement, err = tx.Prepare(fullQuery)
			if err != nil {
				return err
			}
		}
		rows := len(args) / 7
		var err error
		if rows == batch {
			_, err = statement.Exec(args...)
		} else {
			_, err = tx.Exec(query(rows), args...)
		}
		if err != nil {
			return err
		}
		transactionRows += rows
		clear(args)
		args = args[:0]
		if transactionRows >= commitEntries {
			return commit()
		}
		return nil
	}
	if err := feed(func(entry receipt) error {
		args = append(args, entry.Path, entry.Mode, entry.Device, entry.Inode, entry.Size, entry.Modified, entry.Changed)
		if len(args) == batch*7 {
			return flush()
		}
		return nil
	}); err != nil {
		return err
	}
	if err := flush(); err != nil {
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

func writeBinary(name string, syncEntries int, feed func(func(receipt) error) error) error {
	if name == "" {
		return errors.New("output db path required")
	}
	file, err := os.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	defer file.Close()
	buffer := bufio.NewWriterSize(file, bufferBytes)
	if _, err := buffer.WriteString(binaryMagic); err != nil {
		return err
	}
	entries := 0
	if err := feed(func(entry receipt) error {
		length := 52 + len(entry.Path)
		if length > bufferBytes {
			return errors.New("receipt exceeds buffer limit")
		}
		if buffer.Available() < length {
			if err := buffer.Flush(); err != nil {
				return err
			}
		}
		data := buffer.AvailableBuffer()
		data = append(data, make([]byte, 52)...)
		binary.LittleEndian.PutUint32(data[:4], uint32(len(entry.Path)))
		values := [6]int64{entry.Mode, entry.Device, entry.Inode, entry.Size, entry.Modified, entry.Changed}
		for index, value := range values {
			binary.LittleEndian.PutUint64(data[4+index*8:], uint64(value))
		}
		data = append(data, entry.Path...)
		if _, err := buffer.Write(data); err != nil {
			return err
		}
		entries++
		if syncEntries > 0 && entries%syncEntries == 0 {
			if err := buffer.Flush(); err != nil {
				return err
			}
			if err := file.Sync(); err != nil {
				return err
			}
			if entries == syncEntries {
				return syncParent(name)
			}
		}
		return nil
	}); err != nil {
		return err
	}
	if err := buffer.Flush(); err != nil {
		return err
	}
	if err := file.Sync(); err != nil {
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	return syncParent(name)
}

func readBinary(name string, emit func(receipt) error) error {
	file, err := os.Open(name)
	if err != nil {
		return err
	}
	defer file.Close()
	buffer := bufio.NewReaderSize(file, bufferBytes)
	var magic [8]byte
	if _, err := io.ReadFull(buffer, magic[:]); err != nil {
		return err
	}
	if string(magic[:]) != binaryMagic {
		return errors.New("invalid benchmark journal")
	}
	var header [52]byte
	path := make([]byte, 0, 256)
	for {
		_, err := io.ReadFull(buffer, header[:])
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		length := int(binary.LittleEndian.Uint32(header[:4]))
		if length < 1 || length > 65536 {
			return errors.New("invalid receipt path length")
		}
		if cap(path) < length {
			path = make([]byte, length)
		}
		path = path[:length]
		if _, err := io.ReadFull(buffer, path); err != nil {
			return err
		}
		var values [6]int64
		for index := range values {
			values[index] = int64(binary.LittleEndian.Uint64(header[4+index*8:]))
		}
		entry := receipt{string(path), values[0], values[1], values[2], values[3], values[4], values[5]}
		if err := emit(entry); err != nil {
			return err
		}
	}
}

func receiptChecksum(entry receipt) uint64 {
	hash := uint64(14695981039346656037)
	for index := range entry.Path {
		hash = (hash ^ uint64(entry.Path[index])) * 1099511628211
	}
	for _, value := range [6]int64{entry.Mode, entry.Device, entry.Inode, entry.Size, entry.Modified, entry.Changed} {
		hash = (hash ^ uint64(value)) * 1099511628211
	}
	return hash
}

func syncParent(name string) error {
	parent, err := os.Open(filepath.Dir(name))
	if err != nil {
		return err
	}
	defer parent.Close()
	return parent.Sync()
}

// Validation assumes a receipt stream with unique paths, as produced by this
// fixture. It checks each expected identity and counts the actual tree to catch
// added/missing entries, without building a path index or an in-memory set.
// Production would still need per-entry checks at mutation time for races.
func validateStream(writer, fixture, name string, batch, cache int, result *measurement) error {
	root, err := os.OpenRoot(fixture)
	if err != nil {
		return err
	}
	defer root.Close()
	check := func(entry receipt) error {
		const prefix = "/Files/destination/photos"
		if entry.Path != prefix && !strings.HasPrefix(entry.Path, prefix+"/") {
			return errors.New("invalid receipt path")
		}
		relative := strings.TrimPrefix(strings.TrimPrefix(entry.Path, prefix), "/")
		if relative == "" {
			relative = "."
		}
		info, err := root.Lstat(relative)
		if err != nil {
			return err
		}
		stat := info.Sys().(*syscall.Stat_t)
		actual := receipt{entry.Path, int64(info.Mode()), int64(stat.Dev), int64(stat.Ino), info.Size(), info.ModTime().UnixNano(), stat.Ctim.Nano()}
		if actual != entry {
			return fmt.Errorf("changed entry: %s", entry.Path)
		}
		result.Entries++
		result.Checksum ^= receiptChecksum(entry)
		return nil
	}
	start := time.Now()
	if writer == "binary" {
		if err := readBinary(name, check); err != nil {
			return err
		}
	} else if writer == "append-sql" {
		db, err := openJournalSchema(name, false, cache, appendSchema)
		if err != nil {
			return err
		}
		defer db.Close()
		if err := db.QueryRow("SELECT sqlite_version()").Scan(&result.SQLite); err != nil {
			return err
		}
		rows, err := db.Query("SELECT " + columns + " FROM entries ORDER BY seq")
		if err != nil {
			return err
		}
		for rows.Next() {
			var entry receipt
			if err := rows.Scan(&entry.Path, &entry.Mode, &entry.Device, &entry.Inode, &entry.Size, &entry.Modified, &entry.Changed); err != nil {
				rows.Close()
				return err
			}
			if err := check(entry); err != nil {
				rows.Close()
				return err
			}
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return err
		}
		if err := rows.Close(); err != nil {
			return err
		}
	} else {
		return errors.New("stream validation supports append-sql and binary")
	}
	actualCount, err := countTree(root, ".", batch)
	if err != nil {
		return err
	}
	if actualCount != result.Entries {
		return fmt.Errorf("entry count differs: %d vs %d", actualCount, result.Entries)
	}
	result.Seconds = time.Since(start).Seconds()
	result.DatabaseBytes, result.AllocatedBytes = journalSize(name)
	return nil
}

func countTree(root *os.Root, relative string, batch int) (int64, error) {
	dir, err := root.OpenFile(relative, os.O_RDONLY|syscall.O_DIRECTORY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return 0, err
	}
	defer dir.Close()
	count := int64(1)
	for {
		children, readErr := dir.ReadDir(batch)
		for _, child := range children {
			if child.IsDir() {
				nested, err := countTree(root, filepath.Join(relative, child.Name()), batch)
				if err != nil {
					return 0, err
				}
				count += nested
			} else {
				count++
			}
		}
		if errors.Is(readErr, io.EOF) {
			return count, nil
		}
		if readErr != nil {
			return 0, readErr
		}
	}
}
