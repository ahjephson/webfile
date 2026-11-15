package filesystem

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestUploadRejectsInvalidIDsBeforeFilesystemAccess(t *testing.T) {
	base := t.TempDir()
	manager := NewUploadManager(nil, filepath.Join(base, "uploads"))
	victim := filepath.Join(base, "victim.json")
	if err := os.WriteFile(victim, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"", ".", "..", "../victim", "nested/id", `nested\id`, strings.Repeat("a", 31), strings.Repeat("g", 32), strings.Repeat("A", 32), strings.Repeat("a", 32) + "\x00"} {
		if err := manager.Cancel(id); !errors.Is(err, ErrInvalidPath) {
			t.Errorf("Cancel(%q): %v", id, err)
		}
		if _, _, err := manager.Append(context.Background(), id, 0, bytes.NewReader(nil), 0); !errors.Is(err, ErrInvalidPath) {
			t.Errorf("Append(%q): %v", id, err)
		}
		if err := manager.Discard(id); !errors.Is(err, ErrInvalidPath) {
			t.Errorf("Discard(%q): %v", id, err)
		}
	}
	if data, err := os.ReadFile(victim); err != nil || string(data) != "keep" {
		t.Fatalf("outside file changed: %q, %v", data, err)
	}
	if got := manager.recordPath("../victim"); filepath.Dir(got) != manager.baseDir {
		t.Fatalf("record path escaped staging directory: %s", got)
	}
}

func TestUploadRejectsEscapingStagingMetadata(t *testing.T) {
	base := t.TempDir()
	manager := NewUploadManager(nil, filepath.Join(base, "uploads"))
	if err := manager.ensureDir(); err != nil {
		t.Fatal(err)
	}
	id := strings.Repeat("a", 32)
	victim := filepath.Join(base, "victim.part")
	if err := os.WriteFile(victim, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"../victim.part", victim, id + "-../victim.part"} {
		rec := uploadRecord{UploadSession: UploadSession{ID: id}, TempName: name}
		data, err := json.Marshal(rec)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(manager.recordPath(id), data, 0600); err != nil {
			t.Fatal(err)
		}
		if err := manager.Cancel(id); !errors.Is(err, ErrInvalidPath) {
			t.Errorf("escaping metadata %q accepted: %v", name, err)
		}
	}
	if data, err := os.ReadFile(victim); err != nil || string(data) != "keep" {
		t.Fatalf("outside file changed: %q, %v", data, err)
	}
}
