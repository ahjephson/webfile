//go:build linux

package watch

import (
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"
)

func watchReaders() int {
	stack := make([]byte, 1<<20)
	stack = stack[:runtime.Stack(stack, true)]
	count := 0
	for _, goroutine := range strings.Split(string(stack), "\n\n") {
		if strings.Contains(goroutine, "webfile/internal/watch.(*Manager).loop(") &&
			(strings.Contains(goroutine, "syscall.Read(") || strings.Contains(goroutine, "internal/poll.(*FD).Read(")) {
			count++
		}
	}
	return count
}

func TestCloseStopsBlockedWatchReaders(t *testing.T) {
	before := watchReaders()
	var managers []*Manager
	for range 3 {
		manager, err := New()
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = manager.Close() })
		managers = append(managers, manager)
	}
	deadline := time.Now().Add(2 * time.Second)
	for watchReaders() < before+len(managers) {
		if time.Now().After(deadline) {
			t.Fatal("readers did not start")
		}
		time.Sleep(time.Millisecond)
	}
	for _, manager := range managers {
		if err := manager.Close(); err != nil {
			t.Fatal(err)
		}
	}
	deadline = time.Now().Add(2 * time.Second)
	for watchReaders() > before {
		if time.Now().After(deadline) {
			t.Fatal("Close left blocked inotify readers alive")
		}
		time.Sleep(time.Millisecond)
	}
}

func TestCancelAfterCloseDoesNotRemoveReplacementManagerWatch(t *testing.T) {
	old, err := New()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = old.Close() })
	_, cancel, err := old.Subscribe(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer cancel()
	if err := old.Close(); err != nil {
		t.Fatal(err)
	}
	next, err := New()
	if err != nil {
		t.Fatal(err)
	}
	defer next.Close()
	if next.fd != old.fd {
		t.Skip("kernel did not reuse the closed descriptor")
	}
	directory := t.TempDir()
	_, unsubscribe, err := next.Subscribe(directory)
	if err != nil {
		t.Fatal(err)
	}
	defer unsubscribe()
	// Keep the new reader from repairing a watch incorrectly removed by cancel.
	next.mu.Lock()
	wd := next.dirs[directory].wd
	cancel()
	_, err = syscall.InotifyRmWatch(next.fd, uint32(wd))
	next.mu.Unlock()
	if err != nil {
		t.Fatalf("old cancellation removed the new manager's watch: %v", err)
	}
}
