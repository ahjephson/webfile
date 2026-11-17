//go:build linux

package filesystem

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/unix"
)

func TestWriteTextPreservesHardLinkIdentity(t *testing.T) {
	target, root := testService(t)
	original := filepath.Join(root, "original.txt")
	linked := filepath.Join(root, "linked.txt")
	if err := os.WriteFile(original, []byte("before"), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(original, linked); err != nil {
		t.Fatal(err)
	}

	if _, err := target.WriteText(context.Background(), "/Files/original.txt", []byte("after"), "", false); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(linked)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "after" {
		t.Fatalf("linked content=%q", data)
	}
}

func TestWriteTextPreservesUserExtendedAttributes(t *testing.T) {
	target, root := testService(t)
	path := filepath.Join(root, "notes.txt")
	if err := os.WriteFile(path, []byte("before"), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := unix.Setxattr(path, "user.webfile-test", []byte("value"), 0); err != nil {
		t.Skipf("extended attributes unavailable: %v", err)
	}

	if _, err := target.WriteText(context.Background(), "/Files/notes.txt", []byte("after"), "", false); err != nil {
		t.Fatal(err)
	}
	value := make([]byte, 32)
	size, err := unix.Getxattr(path, "user.webfile-test", value)
	if err != nil {
		t.Fatal(err)
	}
	if string(value[:size]) != "value" {
		t.Fatalf("attribute=%q", value[:size])
	}
}

func TestGIVEN_WriteOnlyFile_WHEN_SavingText_THEN_PreservesWriteAccessWithoutRequiringRead(t *testing.T) {
	target, root := testService(t)
	path := filepath.Join(root, "notes.txt")
	if err := os.WriteFile(path, []byte("before"), 0200); err != nil {
		t.Fatal(err)
	}
	if _, err := target.WriteText(context.Background(), "/Files/notes.txt", []byte("after"), "", false); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0200 {
		t.Fatalf("permissions changed: %v %v", info, err)
	}
	if err := os.Chmod(path, 0600); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "after" {
		t.Fatalf("save failed: %q %v", data, err)
	}
}
