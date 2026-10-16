//go:build linux

package watch

import (
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/unix"
)

type PollingMode string

const (
	PollingAuto     PollingMode = "auto"
	PollingAlways   PollingMode = "always"
	PollingDisabled PollingMode = "disabled"
)

type Options struct {
	Polling      PollingMode
	PollInterval time.Duration
}

const watchMask = syscall.IN_CREATE | syscall.IN_DELETE | syscall.IN_MOVED_FROM |
	syscall.IN_MOVED_TO | syscall.IN_CLOSE_WRITE | syscall.IN_ATTRIB |
	syscall.IN_DELETE_SELF | syscall.IN_MOVE_SELF

type Event struct {
	Kind string    `json:"kind"`
	At   time.Time `json:"at"`
}

type dirState struct {
	wd          int
	refs        int
	subs        map[chan Event]struct{}
	fingerprint string
	poll        bool
	dirtyKind   string
	quietTimer  *time.Timer
}

type Manager struct {
	fd         int
	file       *os.File
	readerDone chan struct{}
	mu         sync.Mutex
	dirs       map[string]*dirState
	byWD       map[int]string
	closed     chan struct{}
	once       sync.Once
	polling    PollingMode
	interval   time.Duration
}

func New(options ...Options) (*Manager, error) {
	opts := Options{Polling: PollingAuto, PollInterval: 5 * time.Second}
	if len(options) > 0 {
		opts = options[0]
	}
	if opts.Polling == "" {
		opts.Polling = PollingAuto
	}
	if opts.PollInterval <= 0 {
		opts.PollInterval = 5 * time.Second
	}
	fd, err := syscall.InotifyInit1(syscall.IN_CLOEXEC | syscall.IN_NONBLOCK)
	if err != nil {
		return nil, fmt.Errorf("inotify init: %w", err)
	}
	m := &Manager{
		fd: fd,
		// Go's poller lets Close interrupt an idle inotify read safely.
		file:       os.NewFile(uintptr(fd), "webfile-inotify"),
		readerDone: make(chan struct{}),
		dirs:       map[string]*dirState{},
		byWD:       map[int]string{},
		closed:     make(chan struct{}),
		polling:    opts.Polling,
		interval:   opts.PollInterval,
	}
	go m.loop()
	// Even without content polling, retry watches whose directory was removed
	// or renamed. Inotify follows the old inode rather than its original path.
	go m.pollLoop()
	return m, nil
}

func (m *Manager) Close() error {
	var err error
	m.once.Do(func() {
		close(m.closed)
		m.mu.Lock()
		for _, state := range m.dirs {
			if state.quietTimer != nil {
				state.quietTimer.Stop()
			}
			for subscriber := range state.subs {
				close(subscriber)
			}
		}
		// Stale unsubscribe callbacks must not operate on a reused descriptor.
		clear(m.dirs)
		clear(m.byWD)
		err = m.file.Close()
		m.mu.Unlock()
		<-m.readerDone
	})
	return err
}

func (m *Manager) Subscribe(dir string) (<-chan Event, func(), error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	select {
	case <-m.closed:
		return nil, nil, ErrClosed
	default:
	}

	state := m.dirs[dir]
	if state == nil {
		wd, watchErr := syscall.InotifyAddWatch(m.fd, dir, watchMask)
		poll := m.polling == PollingAlways || m.polling == PollingAuto && (watchErr != nil || pollingRecommended(dir))
		if watchErr != nil && !poll {
			return nil, nil, fmt.Errorf("watch %s: %w", dir, watchErr)
		}
		fingerprint, fingerprintErr := directoryFingerprint(dir)
		if fingerprintErr != nil {
			return nil, nil, fmt.Errorf("snapshot %s: %w", dir, fingerprintErr)
		}
		state = &dirState{wd: wd, subs: map[chan Event]struct{}{}, fingerprint: fingerprint, poll: poll}
		m.dirs[dir] = state
		if watchErr == nil {
			m.byWD[wd] = dir
		} else {
			state.wd = -1
		}
	}

	ch := make(chan Event, 1)
	state.refs++
	state.subs[ch] = struct{}{}

	var once sync.Once
	cancel := func() {
		once.Do(func() {
			m.mu.Lock()
			defer m.mu.Unlock()
			state := m.dirs[dir]
			if state == nil {
				return
			}
			delete(state.subs, ch)
			state.refs--
			close(ch)
			if state.refs <= 0 {
				if state.wd >= 0 {
					_, _ = syscall.InotifyRmWatch(m.fd, uint32(state.wd))
					delete(m.byWD, state.wd)
				}
				if state.quietTimer != nil {
					state.quietTimer.Stop()
				}
				delete(m.dirs, dir)
			}
		})
	}

	return ch, cancel, nil
}

func (m *Manager) pollLoop() {
	ticker := time.NewTicker(m.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			m.pollDirectories()
		case <-m.closed:
			return
		}
	}
}

func (m *Manager) pollDirectories() {
	m.mu.Lock()
	directories := make(map[string]string, len(m.dirs))
	unwatched := make(map[string]*dirState)
	for dir, state := range m.dirs {
		if state.wd < 0 {
			unwatched[dir] = state
		}
		if state.poll {
			directories[dir] = state.fingerprint
		}
	}
	m.mu.Unlock()
	for dir, state := range unwatched {
		if m.restoreWatch(dir, state) {
			m.Invalidate(dir, "invalidate")
		}
	}

	for dir, previous := range directories {
		fingerprint, err := directoryFingerprint(dir)
		if err != nil || fingerprint == previous {
			continue
		}
		m.mu.Lock()
		state := m.dirs[dir]
		if state != nil && state.fingerprint == previous {
			state.fingerprint = fingerprint
			broadcast(state, Event{Kind: "poll", At: time.Now()})
		}
		m.mu.Unlock()
	}
}

func pollingRecommended(dir string) bool {
	var stat unix.Statfs_t
	if err := unix.Statfs(dir, &stat); err != nil {
		return false
	}
	switch uint64(stat.Type) {
	case 0x01021997, 0x53464846, 0x65735546, 0xff534d42:
		return true
	default:
		return false
	}
}

func directoryFingerprint(dir string) (string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", err
	}
	parts := make([]string, 0, len(entries))
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".webfile-extract-") {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		parts = append(parts, entry.Name()+"\x00"+strconv.FormatInt(info.Size(), 10)+"\x00"+strconv.FormatInt(info.ModTime().UnixNano(), 10)+"\x00"+info.Mode().String())
	}
	sort.Strings(parts)
	return strings.Join(parts, "\x01"), nil
}

func (m *Manager) loop() {
	defer close(m.readerDone)
	buf := make([]byte, 64*1024)
	for {
		select {
		case <-m.closed:
			return
		default:
		}
		n, err := m.file.Read(buf)
		if err != nil {
			if errors.Is(err, syscall.EINTR) {
				continue
			}
			select {
			case <-m.closed:
				return
			default:
				m.invalidateAll("watch-error")
				return
			}
		}
		if n <= 0 {
			continue
		}

		for offset := 0; offset+syscall.SizeofInotifyEvent <= n; {
			raw := (*syscall.InotifyEvent)(unsafe.Pointer(&buf[offset]))
			mask := uint32(raw.Mask)
			wd := int(raw.Wd)
			offset += syscall.SizeofInotifyEvent + int(raw.Len)

			if mask&syscall.IN_Q_OVERFLOW != 0 {
				m.invalidateAll("watch-overflow")
				continue
			}
			m.invalidateWD(wd, mask)
		}
	}
}

func (m *Manager) invalidateWD(wd int, mask uint32) {
	m.mu.Lock()
	dir := m.byWD[wd]
	state := m.dirs[dir]
	lost := mask&(syscall.IN_MOVE_SELF|syscall.IN_DELETE_SELF|syscall.IN_IGNORED) != 0
	if lost && state != nil && state.wd == wd {
		delete(m.byWD, wd)
		state.wd = -1
		if mask&syscall.IN_IGNORED == 0 {
			_, _ = syscall.InotifyRmWatch(m.fd, uint32(wd))
		}
	}
	m.mu.Unlock()
	if dir == "" {
		return
	}
	m.markDirty(dir, "invalidate")
	if lost && m.restoreWatch(dir, state) {
		m.Invalidate(dir, "invalidate")
	}
}

func (m *Manager) restoreWatch(dir string, expected *dirState) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	select {
	case <-m.closed:
		return false
	default:
	}
	state := m.dirs[dir]
	if state == nil || state != expected || state.wd >= 0 {
		return false
	}
	wd, err := syscall.InotifyAddWatch(m.fd, dir, watchMask)
	if err != nil {
		return false
	}
	state.wd = wd
	m.byWD[wd] = dir
	return true
}

func (m *Manager) markDirty(dir, kind string) {
	m.mu.Lock()
	state := m.dirs[dir]
	if state == nil {
		m.mu.Unlock()
		return
	}
	state.dirtyKind = kind
	if state.quietTimer != nil {
		state.quietTimer.Stop()
	}
	state.quietTimer = time.AfterFunc(200*time.Millisecond, func() { m.flushDirty(dir, false) })
	m.mu.Unlock()
}

func (m *Manager) flushDirty(dir string, force bool) {
	fingerprint, err := directoryFingerprint(dir)
	m.mu.Lock()
	defer m.mu.Unlock()
	state := m.dirs[dir]
	if state == nil {
		return
	}
	kind := state.dirtyKind
	state.dirtyKind = ""
	state.quietTimer = nil
	if err != nil {
		broadcast(state, Event{Kind: kind, At: time.Now()})
		return
	}
	if force || fingerprint != state.fingerprint {
		state.fingerprint = fingerprint
		broadcast(state, Event{Kind: kind, At: time.Now()})
	}
}

func (m *Manager) Invalidate(dir, kind string) {
	m.mu.Lock()
	state := m.dirs[dir]
	if state == nil {
		m.mu.Unlock()
		return
	}
	if state.quietTimer != nil {
		state.quietTimer.Stop()
	}
	state.dirtyKind = kind
	m.mu.Unlock()
	m.flushDirty(dir, true)
}

func (m *Manager) invalidateAll(kind string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, state := range m.dirs {
		broadcast(state, Event{Kind: kind, At: time.Now()})
	}
}

func broadcast(state *dirState, ev Event) {
	if state == nil {
		return
	}
	for ch := range state.subs {
		select {
		case ch <- ev:
		default:
			// Coalesce bursts: the consumer re-reads the whole directory.
		}
	}
}

var ErrClosed = errors.New("watch manager closed")

// Keep encoding/binary imported as a guard against architecture assumptions in
// future event parsing changes; InotifyEvent itself is read using the kernel's
// native layout above.
var _ = binary.LittleEndian
