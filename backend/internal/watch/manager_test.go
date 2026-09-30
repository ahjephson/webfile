//go:build linux

package watch

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestWatchingCurrentDirectoryInvalidatesOnChange(t *testing.T) {
	dir := t.TempDir()
	manager, err := New()
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()

	events, cancel, err := manager.Subscribe(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer cancel()

	if err := os.WriteFile(filepath.Join(dir, "new.txt"), []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}

	select {
	case event := <-events:
		if event.Kind != "invalidate" {
			t.Fatalf("got %q", event.Kind)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for inotify invalidation")
	}
}

func TestDirectoryFingerprintChangesWhenDirectoryContentsChange(t *testing.T) {
	dir := t.TempDir()
	before, err := directoryFingerprint(dir)
	if err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(dir, "new.txt"), []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	after, err := directoryFingerprint(dir)
	if err != nil {
		t.Fatal(err)
	}
	if before == after {
		t.Fatal("directory fingerprint did not change")
	}
}

func TestDirectoryFingerprintIgnoresExtractionStaging(t *testing.T) {
	dir := t.TempDir()
	before, err := directoryFingerprint(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dir, ".webfile-extract-operation"), 0o700); err != nil {
		t.Fatal(err)
	}
	after, err := directoryFingerprint(dir)
	if err != nil {
		t.Fatal(err)
	}
	if before != after {
		t.Fatal("hidden extraction staging changed the visible directory fingerprint")
	}
}

func TestDisabledPollingUsesOnlyInotify(t *testing.T) {
	dir := t.TempDir()
	manager, err := New(Options{Polling: PollingDisabled})
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()

	_, cancel, err := manager.Subscribe(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer cancel()

	manager.mu.Lock()
	poll := manager.dirs[dir].poll
	manager.mu.Unlock()
	if poll {
		t.Fatal("polling was enabled")
	}
}

func TestWatchBurstIsCoalescedAfterQuietPeriod(t *testing.T) {
	dir := t.TempDir()
	manager, err := New(Options{Polling: PollingDisabled})
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	events, cancel, err := manager.Subscribe(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer cancel()
	for index := 0; index < 5; index++ {
		name := filepath.Join(dir, string(rune('a'+index)))
		if err := os.WriteFile(name, []byte("value"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	select {
	case <-events:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for coalesced event")
	}
	select {
	case event := <-events:
		t.Fatalf("unexpected extra event: %v", event)
	case <-time.After(350 * time.Millisecond):
	}
}

func TestWatchingDeletedDirectoryStillInvalidates(t *testing.T) {
	parent := t.TempDir()
	dir := filepath.Join(parent, "watched")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	manager, err := New(Options{Polling: PollingDisabled})
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	events, cancel, err := manager.Subscribe(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer cancel()
	if err := os.Remove(dir); err != nil {
		t.Fatal(err)
	}
	select {
	case event := <-events:
		if event.Kind != "invalidate" {
			t.Fatalf("got %q", event.Kind)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for deleted-directory invalidation")
	}
}

func TestExplicitInvalidationNotifiesEvenWhenFingerprintIsUnchanged(t *testing.T) {
	dir := t.TempDir()
	manager, err := New(Options{Polling: PollingDisabled})
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	events, cancel, err := manager.Subscribe(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer cancel()
	manager.Invalidate(dir, "operation")
	select {
	case event := <-events:
		if event.Kind != "operation" {
			t.Fatalf("kind=%q", event.Kind)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for explicit invalidation")
	}
}
