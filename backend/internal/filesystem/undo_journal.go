package filesystem

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"hash"
	"hash/crc32"
	"io"
	"os"
	"path/filepath"
	"sync"
	"webfile/internal/taskcontrol"

	"golang.org/x/sys/unix"
)

const undoJournalMagic = "WFUNDO02"
const undoJournalEnd = "WFUEND02"
const undoJournalBuffer = 1 << 20
const undoPathLimit = 65536

type undoNode struct {
	Path  string
	Stamp undoStamp
}

// Receipts are appended in transfer completion order (children before their
// containing copied directory). No manifest map or path index is retained.
type UndoJournal struct {
	mu        sync.Mutex
	name      string
	file      *os.File
	buffer    *bufio.Writer
	digest    hash.Hash
	count     int64
	sealed    bool
	discarded bool
}

func newUndoJournal(name string) (*UndoJournal, error) {
	file, err := os.OpenFile(name, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return nil, err
	}
	journal := &UndoJournal{name: name, file: file, buffer: bufio.NewWriterSize(file, undoJournalBuffer), digest: sha256.New()}
	if _, err := journal.buffer.WriteString(undoJournalMagic); err != nil {
		journal.Abort()
		return nil, err
	}
	return journal, nil
}

func (j *UndoJournal) append(node undoNode) error {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.discarded || j.sealed {
		return errors.New("undo journal is closed")
	}
	if len(node.Path) == 0 || len(node.Path) > undoPathLimit {
		return ErrInvalidPath
	}
	length := 48 + len(node.Path)
	if j.buffer.Available() < length+8 {
		if err := j.buffer.Flush(); err != nil {
			return err
		}
	}
	data := j.buffer.AvailableBuffer()
	data = append(data, make([]byte, 48)...)
	binary.LittleEndian.PutUint32(data[:4], uint32(len(node.Path)))
	binary.LittleEndian.PutUint32(data[4:8], uint32(node.Stamp.Mode))
	values := [5]uint64{uint64(node.Stamp.Size), uint64(node.Stamp.Modified), uint64(node.Stamp.Changed), node.Stamp.Device, node.Stamp.Inode}
	for i, value := range values {
		binary.LittleEndian.PutUint64(data[8+i*8:], value)
	}
	data = append(data, node.Path...)
	checksum := crc32.ChecksumIEEE(data)
	data = binary.LittleEndian.AppendUint32(data, checksum)
	data = binary.LittleEndian.AppendUint32(data, uint32(length+8))
	_, _ = j.digest.Write(data)
	if _, err := j.buffer.Write(data); err != nil {
		return err
	}
	j.count++
	return nil
}

func (j *UndoJournal) seal() (int64, error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.discarded {
		return 0, errors.New("undo journal was discarded")
	}
	if j.sealed {
		return j.count, nil
	}
	tail := []byte(undoJournalEnd)
	tail = binary.LittleEndian.AppendUint64(tail, uint64(j.count))
	tail = append(tail, j.digest.Sum(nil)...)
	if _, err := j.buffer.Write(tail); err != nil {
		return 0, err
	}
	if err := j.buffer.Flush(); err != nil {
		return 0, err
	}
	if err := j.file.Sync(); err != nil {
		return 0, err
	}
	if err := j.file.Close(); err != nil {
		return 0, err
	}
	j.file = nil
	parent, err := os.Open(filepath.Dir(j.name))
	if err != nil {
		return 0, err
	}
	err = parent.Sync()
	parent.Close()
	if err != nil {
		return 0, err
	}
	j.sealed = true
	j.buffer, j.digest = nil, nil
	return j.count, nil
}

func (j *UndoJournal) Abort() {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.file != nil {
		_ = j.file.Close()
		j.file = nil
	}
	j.buffer, j.digest = nil, nil
	j.discarded = true
	_ = os.Remove(j.name)
}

// Open before claiming the single-use undo. The open descriptor remains valid
// when claim/expiry removes the pathname, including while the undo is queued.
type UndoReader struct {
	file  *os.File
	count int64
}

func OpenUndoJournal(plan UndoPlan) (*UndoReader, error) {
	file, err := os.OpenFile(plan.Journal, os.O_RDONLY|unix.O_NOFOLLOW, 0)
	if err != nil {
		return nil, err
	}
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		file.Close()
		return nil, ErrInvalidPath
	}
	return &UndoReader{file: file, count: plan.Entries}, nil
}

func (r *UndoReader) Close() error { return r.file.Close() }

func (r *UndoReader) each(ctx context.Context, emit func(undoNode) error) error {
	if _, err := r.file.Seek(0, io.SeekStart); err != nil {
		return err
	}
	reader := bufio.NewReaderSize(r.file, undoJournalBuffer)
	var magic [8]byte
	if _, err := io.ReadFull(reader, magic[:]); err != nil {
		return err
	}
	if string(magic[:]) != undoJournalMagic {
		return ErrUndoChanged
	}
	digest := sha256.New()
	var header [48]byte
	var suffix [8]byte
	data := make([]byte, 0, 256)
	var count int64
	for count < r.count {
		if err := taskcontrol.Wait(ctx); err != nil {
			return err
		}
		if _, err := io.ReadFull(reader, header[:]); err != nil {
			return err
		}
		length := int(binary.LittleEndian.Uint32(header[:4]))
		if length < 1 || length > undoPathLimit {
			return ErrUndoChanged
		}
		data = append(data[:0], header[:]...)
		data = append(data, make([]byte, length)...)
		if _, err := io.ReadFull(reader, data[48:]); err != nil {
			return err
		}
		if _, err := io.ReadFull(reader, suffix[:]); err != nil {
			return err
		}
		if binary.LittleEndian.Uint32(suffix[:4]) != crc32.ChecksumIEEE(data) || int(binary.LittleEndian.Uint32(suffix[4:])) != length+56 {
			return ErrUndoChanged
		}
		_, _ = digest.Write(data)
		_, _ = digest.Write(suffix[:])
		stamp := undoStamp{Mode: os.FileMode(binary.LittleEndian.Uint32(header[4:8])), Size: int64(binary.LittleEndian.Uint64(header[8:16])), Modified: int64(binary.LittleEndian.Uint64(header[16:24])), Changed: int64(binary.LittleEndian.Uint64(header[24:32])), Device: binary.LittleEndian.Uint64(header[32:40]), Inode: binary.LittleEndian.Uint64(header[40:48])}
		if err := emit(undoNode{string(data[48:]), stamp}); err != nil {
			return err
		}
		count++
	}
	var tail [48]byte
	if _, err := io.ReadFull(reader, tail[:]); err != nil {
		return err
	}
	if string(tail[:8]) != undoJournalEnd || int64(binary.LittleEndian.Uint64(tail[8:16])) != count || string(tail[16:]) != string(digest.Sum(nil)) {
		return ErrUndoChanged
	}
	if _, err := reader.ReadByte(); !errors.Is(err, io.EOF) {
		return ErrUndoChanged
	}
	return nil
}
