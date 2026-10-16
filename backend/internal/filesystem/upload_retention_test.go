package filesystem

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestUploadRetentionExpiresAbandonedStagingAndPreservesRecentResume(t *testing.T) {
	resolver, _ := NewResolver([]Root{{Name: "Files", Path: t.TempDir()}})
	base := t.TempDir()
	manager := NewUploadManager(resolver, base)
	old, err := manager.Init(context.Background(), "/Files", "old.bin", 4, 1)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := manager.Append(context.Background(), old.ID, 0, bytes.NewBufferString("ab"), 2); err != nil {
		t.Fatal(err)
	}
	record, err := manager.readRecordLocked(old.ID)
	if err != nil {
		t.Fatal(err)
	}
	stale := time.Now().Add(-8 * 24 * time.Hour)
	record.UpdatedAt = stale
	if err := manager.writeRecordLocked(record); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(filepath.Join(base, record.TempName), stale, stale); err != nil {
		t.Fatal(err)
	}
	recent, err := manager.Init(context.Background(), "/Files", "recent.bin", 4, 2)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := manager.Append(context.Background(), recent.ID, 0, bytes.NewBufferString("c"), 1); err != nil {
		t.Fatal(err)
	}
	orphan := filepath.Join(base, "0123456789abcdef0123456789abcdef-orphan.part")
	if err := os.WriteFile(orphan, []byte("orphan"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(orphan, stale, stale); err != nil {
		t.Fatal(err)
	}
	manager = NewUploadManager(resolver, base)
	if _, err := manager.Init(context.Background(), "/Files", "next.bin", 1, 3); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{manager.recordPath(old.ID), filepath.Join(base, record.TempName), orphan} {
		if _, err := os.Stat(name); !os.IsNotExist(err) {
			t.Fatalf("abandoned staging remains: %s %v", name, err)
		}
	}
	resumed, err := manager.Init(context.Background(), "/Files", "recent.bin", 4, 2)
	if err != nil || resumed.Offset != 1 {
		t.Fatalf("recent resume lost: %#v %v", resumed, err)
	}
}
