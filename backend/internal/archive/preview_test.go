package archive

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/unix"
	"webfile/internal/filesystem"
)

func TestRealSevenZipPreviewAndPasswordRetry(t *testing.T) {
	tool := realSevenZip(t)
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "folder"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "folder", "hello.txt"), []byte("hello"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, encrypted := range []bool{false, true} {
		name := "plain.7z"
		args := []string{"a"}
		if encrypted {
			name = "locked.7z"
			args = append(args, "-psecret", "-mhe=on")
		}
		args = append(args, filepath.Join(root, name), "folder")
		cmd := exec.Command(tool, args...)
		cmd.Dir = root
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("fixture: %v %s", err, output)
		}
		target := New(Config{Executable: tool, InspectArgs: []string{"l", "-slt", "{archive}"}}, testResolver(t, filesystem.Root{Name: "Files", Path: root}))
		if encrypted {
			if _, err := target.Preview(context.Background(), "/Files/"+name, ""); !errors.Is(err, ErrPasswordRequired) {
				t.Fatalf("password required: %v", err)
			}
			if _, err := target.Preview(context.Background(), "/Files/"+name, "wrong"); !errors.Is(err, ErrIncorrectPassword) {
				t.Fatalf("incorrect password: %v", err)
			}
		}
		password := ""
		if encrypted {
			password = "secret"
		}
		entries, err := target.Preview(context.Background(), "/Files/"+name, password)
		if err != nil {
			t.Fatal(err)
		}
		found := false
		for _, entry := range entries {
			if entry.Path == "folder/hello.txt" && entry.Size == 5 && !entry.IsDir {
				found = true
			}
		}
		if !found {
			t.Fatalf("missing metadata: %#v", entries)
		}
		if _, err := target.Preview(context.Background(), "/Files/../outside.7z", ""); err == nil {
			t.Fatal("outside root allowed")
		}
	}
}

func TestArchivePreviewToolErrorsAreNotPasswords(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "broken.rar"), []byte("not an archive"), 0600); err != nil {
		t.Fatal(err)
	}
	target := New(Config{Executable: realSevenZip(t), InspectArgs: []string{"l", "-slt", "{archive}"}}, testResolver(t, filesystem.Root{Name: "Files", Path: root}))
	if _, err := target.Preview(context.Background(), "/Files/broken.rar", ""); err == nil || errors.Is(err, ErrPasswordRequired) {
		t.Fatalf("corrupt archive: %v", err)
	}
	target.cfg.Executable = filepath.Join(root, "missing-7zz")
	if _, err := target.Preview(context.Background(), "/Files/broken.rar", ""); err == nil {
		t.Fatal("missing executable accepted")
	}
}

func TestArchivePreviewLimitsAndMetadata(t *testing.T) {
	entries, err := parsePreview("Path = folder\nFolder = +\n\nPath = folder/a.txt\nSize = 12\nEncrypted = +\nModified = 2026-11-15 01:00:00\n")
	if err != nil || len(entries) != 2 || !entries[0].IsDir || !entries[1].Encrypted || entries[1].Size != 12 {
		t.Fatalf("metadata: %#v %v", entries, err)
	}
	if _, err := parsePreview(strings.Repeat("Path = a\n\n", 50001)); !errors.Is(err, ErrPreviewLimit) {
		t.Fatalf("entry limit: %v", err)
	}
	output := &previewOutput{}
	if _, err := output.Write(make([]byte, (8<<20)+1)); !errors.Is(err, ErrPreviewLimit) {
		t.Fatalf("output limit: %v", err)
	}
}

func TestArchivePreviewRejectsSpecialFilesWithoutBlocking(t *testing.T) {
	root := t.TempDir()
	if err := unix.Mkfifo(filepath.Join(root, "pipe.zip"), 0600); err != nil {
		t.Fatal(err)
	}
	target := New(Config{Executable: "unused"}, testResolver(t, filesystem.Root{Name: "Files", Path: root}))
	done := make(chan error, 1)
	go func() { _, err := target.Preview(context.Background(), "/Files/pipe.zip", ""); done <- err }()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("special file accepted")
		}
	case <-time.After(time.Second):
		t.Fatal("opening a special file blocked")
	}
}
