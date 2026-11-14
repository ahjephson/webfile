package filesystem

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

func undoFixture(t *testing.T, kind string, names ...string) (*Service, string, *UndoPlan) {
	t.Helper()
	s, root := testService(t)
	if err := os.MkdirAll(filepath.Join(root, "from", "folder", "nested"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, "to"), 0700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"one.txt", "two.txt", "folder/nested/child.txt"} {
		if err := os.WriteFile(filepath.Join(root, "from", name), []byte(name), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink("nested/child.txt", filepath.Join(root, "from", "folder", "link")); err != nil {
		t.Fatal(err)
	}
	var sources []string
	for _, name := range names {
		sources = append(sources, "/Files/from/"+name)
	}
	transfer := s.NewTransfer(kind, sources, "/Files/to")
	enableUndoTest(t, transfer)
	if err := transfer.Run(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	plan, reason := transfer.UndoPlan()
	if plan == nil {
		t.Fatal(reason)
	}
	// Exercise the exact representation retained in durable task history.
	data, err := json.Marshal(plan)
	if err != nil {
		t.Fatal(err)
	}
	var restored UndoPlan
	if err := json.Unmarshal(data, &restored); err != nil {
		t.Fatal(err)
	}
	return s, root, &restored
}

func enableUndoTest(t *testing.T, transfer *Transfer) {
	t.Helper()
	journal, err := transfer.BeginUndoJournal(filepath.Join(t.TempDir(), "receipt.undo"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(journal.Abort)
}

func TestUndoCopyAndMove(t *testing.T) {
	for _, kind := range []string{"copy", "move"} {
		for _, names := range [][]string{{"one.txt", "two.txt"}, {"folder"}} {
			t.Run(kind+names[0], func(t *testing.T) {
				s, root, plan := undoFixture(t, kind, names...)
				if err := s.UndoTransfer(context.Background(), *plan, nil); err != nil {
					t.Fatal(err)
				}
				for _, name := range names {
					if _, err := os.Lstat(filepath.Join(root, "to", name)); !errors.Is(err, os.ErrNotExist) {
						t.Fatalf("output remains: %v", err)
					}
					if _, err := os.Lstat(filepath.Join(root, "from", name)); err != nil {
						t.Fatal(err)
					}
				}
			})
		}
	}
}

func TestUndoRejectsChangedOutputsBeforeAnyMutation(t *testing.T) {
	for _, kind := range []string{"copy", "move"} {
		for _, change := range []string{"edit", "replace", "add", "remove", "symlink", "source_collision", "source_parent"} {
			if kind == "move" && change != "source_collision" && change != "source_parent" {
				continue
			}
			t.Run(kind+change, func(t *testing.T) {
				s, root, plan := undoFixture(t, kind, "one.txt", "folder")
				child := filepath.Join(root, "to", "folder", "nested", "child.txt")
				switch change {
				case "edit":
					os.WriteFile(child, []byte("edited"), 0600)
				case "replace":
					os.Rename(child, child+".old")
					os.WriteFile(child, []byte("folder/nested/child.txt"), 0600)
				case "add":
					os.WriteFile(child+".new", []byte("new"), 0600)
				case "remove":
					os.Remove(child)
				case "symlink":
					os.Rename(filepath.Dir(child), filepath.Dir(child)+".old")
					os.Symlink(filepath.Join(root, "from"), filepath.Dir(child))
				case "source_collision":
					if kind == "copy" {
						t.Skip("copy undo retains source")
					}
					os.WriteFile(filepath.Join(root, "from", "one.txt"), []byte("new source"), 0600)
				case "source_parent":
					if kind == "copy" {
						t.Skip("copy undo retains source")
					}
					os.Rename(filepath.Join(root, "from"), filepath.Join(root, "from-old"))
					os.Mkdir(filepath.Join(root, "from"), 0700)
				}
				if err := s.UndoTransfer(context.Background(), *plan, nil); !errors.Is(err, ErrUndoChanged) {
					t.Fatalf("got %v", err)
				}
				if _, err := os.Stat(filepath.Join(root, "to", "one.txt")); err != nil {
					t.Fatal("preflight changed another output", err)
				}
			})
		}
	}
}

func TestUndoRecordsOnlyPublishedChildren(t *testing.T) {
	s, root := testService(t)
	os.MkdirAll(filepath.Join(root, "from", "folder"), 0700)
	os.Mkdir(filepath.Join(root, "to"), 0700)
	os.WriteFile(filepath.Join(root, "from", "folder", "child"), []byte("content"), 0600)
	transfer := s.NewTransfer("copy", []string{"/Files/from/folder"}, "/Files/to")
	enableUndoTest(t, transfer)
	added := false
	if err := transfer.Run(context.Background(), func(p TransferProgress) {
		if !added && p.Phase == transferPhaseCopying {
			if _, err := os.Stat(filepath.Join(root, "to", "folder")); err == nil {
				os.WriteFile(filepath.Join(root, "to", "folder", "external"), []byte("keep"), 0600)
				added = true
			}
		}
	}); err != nil {
		t.Fatal(err)
	}
	plan, _ := transfer.UndoPlan()
	if !added || plan == nil {
		t.Fatal("fixture did not produce an undo record")
	}
	if err := s.UndoTransfer(context.Background(), *plan, nil); !errors.Is(err, ErrUndoChanged) {
		t.Fatal(err)
	}
}

func TestUndoKeepBothUsesPublishedNameAndReplaceIsUnavailable(t *testing.T) {
	for _, kind := range []string{"copy", "move"} {
		for _, choice := range []string{"keep_both", "replace", "skip", "merge"} {
			t.Run(kind+choice, func(t *testing.T) {
				s, root := testService(t)
				os.MkdirAll(filepath.Join(root, "from"), 0700)
				os.Mkdir(filepath.Join(root, "to"), 0700)
				if choice == "merge" {
					os.Mkdir(filepath.Join(root, "from", "item"), 0700)
					os.Mkdir(filepath.Join(root, "to", "item"), 0700)
				} else {
					os.WriteFile(filepath.Join(root, "from", "item"), []byte("source"), 0600)
					os.WriteFile(filepath.Join(root, "to", "item"), []byte("existing"), 0600)
				}
				transfer := s.NewTransfer(kind, []string{"/Files/from/item"}, "/Files/to")
				enableUndoTest(t, transfer)
				var conflict *TransferConflict
				if err := transfer.Run(context.Background(), nil); !errors.As(err, &conflict) {
					t.Fatal(err)
				}
				if err := transfer.Decide(ConflictDecision{Revision: conflict.Revision, Choice: choice}); err != nil {
					t.Fatal(err)
				}
				if err := transfer.Run(context.Background(), nil); err != nil {
					t.Fatal(err)
				}
				plan, reason := transfer.UndoPlan()
				if choice != "keep_both" {
					if plan != nil || reason == "" {
						t.Fatal("unsafe undo offered", choice)
					}
					return
				}
				if plan == nil {
					t.Fatal(reason)
				}
				if err := s.UndoTransfer(context.Background(), *plan, nil); err != nil {
					t.Fatal(err)
				}
				data, err := os.ReadFile(filepath.Join(root, "to", "item"))
				if err != nil || string(data) != "existing" {
					t.Fatal("existing destination changed", err)
				}
			})
		}
	}
}

func TestUndoCrossFilesystemMove(t *testing.T) {
	from := t.TempDir()
	to, err := os.MkdirTemp("/dev/shm", "webfile-undo-")
	if err != nil {
		t.Skip("second filesystem unavailable", err)
	}
	t.Cleanup(func() { os.RemoveAll(to) })
	fromInfo, _ := os.Stat(from)
	toInfo, _ := os.Stat(to)
	if fromInfo.Sys().(*syscall.Stat_t).Dev == toInfo.Sys().(*syscall.Stat_t).Dev {
		t.Skip("requires different filesystems")
	}
	resolver, err := NewResolver([]Root{{Name: "From", Path: from}, {Name: "To", Path: to}})
	if err != nil {
		t.Fatal(err)
	}
	s := NewService(resolver, 1024*1024)
	t.Cleanup(s.Close)
	os.MkdirAll(filepath.Join(from, "folder", "nested"), 0700)
	for _, name := range []string{"one", "two"} {
		os.WriteFile(filepath.Join(from, "folder", "nested", name), []byte(name), 0600)
	}
	transfer := s.NewTransfer("move", []string{"/From/folder"}, "/To")
	enableUndoTest(t, transfer)
	if err := transfer.Run(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	plan, reason := transfer.UndoPlan()
	if plan == nil {
		t.Fatal(reason)
	}
	if err := s.UndoTransfer(context.Background(), *plan, nil); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"one", "two"} {
		data, err := os.ReadFile(filepath.Join(from, "folder", "nested", name))
		if err != nil || string(data) != name {
			t.Fatal(string(data), err)
		}
	}
	if _, err := os.Stat(filepath.Join(to, "folder")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal(err)
	}
}

func TestUndoCancellationKeepsRemainingItems(t *testing.T) {
	for _, kind := range []string{"copy", "move"} {
		t.Run(kind, func(t *testing.T) {
			s, root, plan := undoFixture(t, kind, "one.txt", "two.txt")
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			if err := s.UndoTransfer(ctx, *plan, nil); !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
			for _, name := range []string{"one.txt", "two.txt"} {
				if _, err := os.Stat(filepath.Join(root, "to", name)); err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}

func TestUndoRemovalRestoresDirectoryWithUnexpectedChildren(t *testing.T) {
	root := t.TempDir()
	os.Mkdir(filepath.Join(root, "folder"), 0700)
	os.WriteFile(filepath.Join(root, "folder", "external"), []byte("keep"), 0600)
	info, err := os.Stat(filepath.Join(root, "folder"))
	if err != nil {
		t.Fatal(err)
	}
	parent, err := os.OpenRoot(root)
	if err != nil {
		t.Fatal(err)
	}
	defer parent.Close()
	if err := removeUndoEntry(parent, "folder", stampUndo(info)); err == nil {
		t.Fatal("nonempty directory removed")
	}
	data, err := os.ReadFile(filepath.Join(root, "folder", "external"))
	if err != nil || string(data) != "keep" {
		t.Fatal(string(data), err)
	}
	entries, err := os.ReadDir(root)
	if err != nil || len(entries) != 1 {
		t.Fatal("staging directory left behind", err)
	}
}
