//go:build linux

package watch

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestWatchingReplacementDirectoryContinuesInvalidating(t *testing.T) {
	for _, polling := range []PollingMode{PollingAuto, PollingDisabled} {
		for _, replacement := range []string{"rename", "delete"} {
			t.Run(string(polling)+"/"+replacement, func(t *testing.T) {
				parent := t.TempDir()
				dir := filepath.Join(parent, "current")
				if err := os.Mkdir(dir, 0o700); err != nil {
					t.Fatal(err)
				}
				manager, err := New(Options{Polling: polling, PollInterval: 25 * time.Millisecond})
				if err != nil {
					t.Fatal(err)
				}
				defer manager.Close()
				events, cancel, err := manager.Subscribe(dir)
				if err != nil {
					t.Fatal(err)
				}
				defer cancel()
				wait := func() {
					t.Helper()
					select {
					case <-events:
					case <-time.After(2 * time.Second):
						t.Fatal("replacement directory stopped invalidating")
					}
				}
				for round := 0; round < 3; round++ {
					if replacement == "rename" {
						err = os.Rename(dir, filepath.Join(parent, fmt.Sprintf("parked-%d", round)))
					} else {
						err = os.RemoveAll(dir)
					}
					if err != nil {
						t.Fatal(err)
					}
					wait() // Consume the event while the path is absent.
					if err := os.Mkdir(dir, 0o700); err != nil {
						t.Fatal(err)
					}
					if err := os.WriteFile(filepath.Join(dir, "initial"), []byte("new"), 0o600); err != nil {
						t.Fatal(err)
					}
					wait() // Reappearance must also invalidate the old listing.
					if err := os.WriteFile(filepath.Join(dir, "later"), []byte("later"), 0o600); err != nil {
						t.Fatal(err)
					}
					wait()
				}
			})
		}
	}
}

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
