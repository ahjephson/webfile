package filesystem

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestTransferDecisionsPreserveOrReplaceOnlyReviewedFiles(t *testing.T) {
	for _, choice := range []string{"skip", "keep_both", "replace"} {
		t.Run(choice, func(t *testing.T) {
			service, root := testService(t)
			os.Mkdir(filepath.Join(root, "from"), 0700)
			os.Mkdir(filepath.Join(root, "to"), 0700)
			os.WriteFile(filepath.Join(root, "from", "item.txt"), []byte("new"), 0600)
			os.WriteFile(filepath.Join(root, "to", "item.txt"), []byte("old"), 0640)
			transfer := service.NewTransfer("copy", []string{"/Files/from/item.txt"}, "/Files/to")
			var conflict *TransferConflict
			if err := transfer.Run(context.Background(), nil); !errors.As(err, &conflict) {
				t.Fatal(err)
			}
			if err := transfer.Decide(ConflictDecision{Revision: "stale", Choice: choice}); err == nil {
				t.Fatal("stale revision accepted")
			}
			if err := transfer.Decide(ConflictDecision{Revision: conflict.Revision, Choice: choice}); err != nil {
				t.Fatal(err)
			}
			if err := transfer.Run(context.Background(), nil); err != nil {
				t.Fatal(err)
			}
			data, _ := os.ReadFile(filepath.Join(root, "to", "item.txt"))
			want := "old"
			if choice == "replace" {
				want = "new"
			}
			if string(data) != want {
				t.Fatal(string(data), want)
			}
			if choice == "keep_both" {
				data, err := os.ReadFile(filepath.Join(root, "to", "item (copy).txt"))
				if err != nil || string(data) != "new" {
					t.Fatal(string(data), err)
				}
			}
			if choice == "replace" {
				info, _ := os.Stat(filepath.Join(root, "to", "item.txt"))
				if info.Mode().Perm() != 0640 {
					t.Fatal("destination mode changed", info.Mode())
				}
			}
			data, _ = os.ReadFile(filepath.Join(root, "from", "item.txt"))
			if string(data) != "new" {
				t.Fatal("copy changed source")
			}
		})
	}
}

func TestTransferChangedDestinationRequiresFreshReview(t *testing.T) {
	service, root := testService(t)
	os.Mkdir(filepath.Join(root, "to"), 0700)
	os.WriteFile(filepath.Join(root, "item.txt"), []byte("new"), 0600)
	target := filepath.Join(root, "to", "item.txt")
	os.WriteFile(target, []byte("old"), 0600)
	transfer := service.NewTransfer("move", []string{"/Files/item.txt"}, "/Files/to")
	var original, current *TransferConflict
	if err := transfer.Run(context.Background(), nil); !errors.As(err, &original) {
		t.Fatal(err)
	}
	if err := transfer.Decide(ConflictDecision{Revision: original.Revision, Choice: "replace", ApplyRemaining: true}); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(target, []byte("changed destination"), 0600)
	if err := transfer.Run(context.Background(), nil); !errors.As(err, &current) || !current.Changed || current.Revision == original.Revision {
		t.Fatal(err, current)
	}
	data, _ := os.ReadFile(target)
	if string(data) != "changed destination" {
		t.Fatal("changed destination overwritten")
	}
	if err := transfer.Decide(ConflictDecision{Revision: current.Revision, Choice: "keep_both"}); err != nil {
		t.Fatal(err)
	}
	if err := transfer.Run(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "item.txt")); !os.IsNotExist(err) {
		t.Fatal("move retained completed source", err)
	}
}

func TestMergeMovesOnlyCompletedChildrenAndRetainsSkippedSource(t *testing.T) {
	service, root := testService(t)
	os.MkdirAll(filepath.Join(root, "from", "folder"), 0700)
	os.MkdirAll(filepath.Join(root, "to", "folder"), 0700)
	for _, name := range []string{"conflict.txt", "other.txt"} {
		os.WriteFile(filepath.Join(root, "from", "folder", name), []byte("new"), 0600)
	}
	os.WriteFile(filepath.Join(root, "to", "folder", "conflict.txt"), []byte("old"), 0600)
	transfer := service.NewTransfer("move", []string{"/Files/from/folder"}, "/Files/to")
	var conflict *TransferConflict
	if err := transfer.Run(context.Background(), nil); !errors.As(err, &conflict) || conflict.Category != "folders" {
		t.Fatal(err)
	}
	transfer.Decide(ConflictDecision{Revision: conflict.Revision, Choice: "merge"})
	if err := transfer.Run(context.Background(), nil); !errors.As(err, &conflict) || conflict.Category != "files" {
		t.Fatal(err)
	}
	transfer.Decide(ConflictDecision{Revision: conflict.Revision, Choice: "skip", ApplyRemaining: true})
	if err := transfer.Run(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "from", "folder", "conflict.txt")); err != nil {
		t.Fatal("skipped source lost", err)
	}
	if _, err := os.Stat(filepath.Join(root, "from", "folder", "other.txt")); !os.IsNotExist(err) {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(filepath.Join(root, "to", "folder", "other.txt")); err != nil || string(data) != "new" {
		t.Fatal(err, string(data))
	}
	if err := service.NewTransfer("copy", []string{"/Files/to/folder"}, "/Files/to").Run(context.Background(), nil); err == nil {
		t.Fatal("same item accepted")
	}
}

func TestRemovedDestinationInvalidatesPreviouslyApprovedReplacement(t *testing.T) {
	service, root := testService(t)
	if err := os.Mkdir(filepath.Join(root, "to"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "item"), []byte("source"), 0600); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(root, "to", "item")
	if err := os.WriteFile(target, []byte("destination"), 0600); err != nil {
		t.Fatal(err)
	}
	transfer := service.NewTransfer("copy", []string{"/Files/item"}, "/Files/to")
	var conflict *TransferConflict
	if err := transfer.Run(context.Background(), nil); !errors.As(err, &conflict) {
		t.Fatal(err)
	}
	if err := transfer.Decide(ConflictDecision{Revision: conflict.Revision, Choice: "replace"}); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(target); err != nil {
		t.Fatal(err)
	}
	if err := transfer.Run(context.Background(), nil); !errors.As(err, &conflict) || !conflict.Changed {
		t.Fatal(err, conflict)
	}
	if err := transfer.Decide(ConflictDecision{Revision: conflict.Revision, Choice: "replace"}); err == nil {
		t.Fatal("replacement allowed for missing destination")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := transfer.Run(ctx, nil); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Fatal("cancelled transfer published a file", err)
	}
}

func TestCrossRootMoveDoesNotApplyFileReplacementToFolderMismatch(t *testing.T) {
	from, to := t.TempDir(), t.TempDir()
	resolver, err := NewResolver([]Root{{Name: "From", Path: from}, {Name: "To", Path: to}})
	if err != nil {
		t.Fatal(err)
	}
	service := NewService(resolver, 1024)
	t.Cleanup(service.Close)
	for _, name := range []string{"a.txt", "b.txt", "folder"} {
		if err := os.WriteFile(filepath.Join(from, name), []byte("source"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"a.txt", "b.txt"} {
		if err := os.WriteFile(filepath.Join(to, name), []byte("destination"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Mkdir(filepath.Join(to, "folder"), 0700); err != nil {
		t.Fatal(err)
	}
	transfer := service.NewTransfer("move", []string{"/From/a.txt", "/From/b.txt", "/From/folder"}, "/To")
	var conflict *TransferConflict
	if err := transfer.Run(context.Background(), nil); !errors.As(err, &conflict) {
		t.Fatal(err)
	}
	if err := transfer.Decide(ConflictDecision{Revision: conflict.Revision, Choice: "replace", ApplyRemaining: true}); err != nil {
		t.Fatal(err)
	}
	if err := transfer.Run(context.Background(), nil); !errors.As(err, &conflict) || conflict.Category != "type_mismatch" {
		t.Fatal(err, conflict)
	}
	for _, name := range []string{"a.txt", "b.txt"} {
		if data, err := os.ReadFile(filepath.Join(to, name)); err != nil || string(data) != "source" {
			t.Fatal(string(data), err)
		}
		if _, err := os.Stat(filepath.Join(from, name)); !os.IsNotExist(err) {
			t.Fatal("completed move retained source", err)
		}
	}
	if err := transfer.Decide(ConflictDecision{Revision: conflict.Revision, Choice: "replace"}); err == nil {
		t.Fatal("type mismatch accepted replacement")
	}
	if err := transfer.Decide(ConflictDecision{Revision: conflict.Revision, Choice: "keep_both"}); err != nil {
		t.Fatal(err)
	}
	if err := transfer.Run(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(filepath.Join(to, "folder (copy)")); err != nil || string(data) != "source" {
		t.Fatal(string(data), err)
	}
	if info, err := os.Stat(filepath.Join(to, "folder")); err != nil || !info.IsDir() {
		t.Fatal("existing folder changed", err)
	}
}
