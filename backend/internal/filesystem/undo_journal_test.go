package filesystem

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestUndoJournalStreamsBeyondOldLimitAndRejectsCorruption(t *testing.T) {
	name := filepath.Join(t.TempDir(), "receipt.undo")
	journal, err := newUndoJournal(name)
	if err != nil {
		t.Fatal(err)
	}
	defer journal.Abort()
	for i := range 20000 {
		if err := journal.append(undoNode{fmt.Sprintf("/Files/to/file-%06d", i), undoStamp{Mode: 0600, Device: 1, Inode: uint64(i + 1)}}); err != nil {
			t.Fatal(err)
		}
	}
	count, err := journal.seal()
	if err != nil || count != 20000 {
		t.Fatal(count, err)
	}
	if journal.buffer != nil || journal.file != nil {
		t.Fatal("completed writer retained buffers or descriptor")
	}
	plan := UndoPlan{Journal: name, Entries: count}
	reader, err := OpenUndoJournal(plan)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	var actual int64
	if err := reader.each(context.Background(), func(node undoNode) error {
		actual++
		if node.Stamp.Inode != uint64(actual) {
			t.Fatal("receipt changed")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if actual != count {
		t.Fatal(actual)
	}
	file, err := os.OpenFile(name, os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.WriteAt([]byte{255}, 64); err != nil {
		t.Fatal(err)
	}
	file.Close()
	if err := reader.each(context.Background(), func(undoNode) error { return nil }); err == nil {
		t.Fatal("corrupt receipt accepted")
	}
}

func TestUndoCorruptOrIncompleteJournalDoesNotRemoveFiles(t *testing.T) {
	for _, change := range []string{"corrupt", "truncate", "unsealed"} {
		t.Run(change, func(t *testing.T) {
			s, root, plan := undoFixture(t, "copy", "one.txt", "two.txt")
			file, err := os.OpenFile(plan.Journal, os.O_RDWR, 0)
			if err != nil {
				t.Fatal(err)
			}
			info, _ := file.Stat()
			if change == "corrupt" {
				_, err = file.WriteAt([]byte{255}, 64)
			} else if change == "truncate" {
				err = file.Truncate(info.Size() - 1)
			} else {
				err = file.Truncate(info.Size() - 48)
			}
			file.Close()
			if err != nil {
				t.Fatal(err)
			}
			if err := s.UndoTransfer(context.Background(), *plan, nil); err == nil {
				t.Fatal("invalid journal accepted")
			}
			for _, name := range []string{"one.txt", "two.txt"} {
				if _, err := os.Stat(filepath.Join(root, "to", name)); err != nil {
					t.Fatal("file removed before validation", err)
				}
			}
		})
	}
}

func TestUndoMoveFolderKeepsEditsWithOneReceipt(t *testing.T) {
	s, root, plan := undoFixture(t, "move", "folder")
	if plan.Entries != 1 {
		t.Fatal("moved folder recorded descendants", plan.Entries)
	}
	child := filepath.Join(root, "to", "folder", "nested", "child.txt")
	if err := os.WriteFile(child, []byte("edited"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(child+".new", []byte("added"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := s.UndoTransfer(context.Background(), *plan, nil); err != nil {
		t.Fatal(err)
	}
	for _, pair := range [][2]string{{"child.txt", "edited"}, {"child.txt.new", "added"}} {
		data, err := os.ReadFile(filepath.Join(root, "from", "folder", "nested", pair[0]))
		if err != nil || string(data) != pair[1] {
			t.Fatal(string(data), err)
		}
	}
}

func TestUndoMoveRejectsReplacedTopLevelItem(t *testing.T) {
	s, root, plan := undoFixture(t, "move", "folder")
	if err := os.Rename(filepath.Join(root, "to", "folder"), filepath.Join(root, "to", "old-folder")); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, "to", "folder"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := s.UndoTransfer(context.Background(), *plan, nil); !errors.Is(err, ErrUndoChanged) {
		t.Fatal(err)
	}
}

func TestUndoMoveDoesNotScanFolderDescendants(t *testing.T) {
	s, root, plan := undoFixture(t, "move", "folder")
	if err := os.Chmod(filepath.Join(root, "to", "folder", "nested"), 0000); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(filepath.Join(root, "from", "folder", "nested"), 0700)
	if err := s.UndoTransfer(context.Background(), *plan, nil); err != nil {
		t.Fatal(err)
	}
}

func TestUndoMoveRejectsOriginalParentRedirectedInsideMovedFolder(t *testing.T) {
	s, root, plan := undoFixture(t, "move", "folder")
	nested := filepath.Join(root, "to", "folder", "from")
	if err := os.Rename(filepath.Join(root, "from"), nested); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(nested, filepath.Join(root, "from")); err != nil {
		t.Fatal(err)
	}
	if err := s.UndoTransfer(context.Background(), *plan, nil); !errors.Is(err, ErrUndoChanged) {
		t.Fatal(err)
	}
}
