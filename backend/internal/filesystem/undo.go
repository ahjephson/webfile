package filesystem

import (
	"context"
	"errors"
	"fmt"
	"golang.org/x/sys/unix"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
	"syscall"
	"webfile/internal/clienterror"
	"webfile/internal/taskcontrol"
	"webfile/internal/thumbnailcache"
)

var ErrUndoChanged = clienterror.New("undo paths changed", clienterror.UndoChanged)

type undoStamp struct {
	Mode     os.FileMode `json:"mode"`
	Size     int64       `json:"size,omitempty"`
	Modified int64       `json:"modified,omitempty"`
	Changed  int64       `json:"changed,omitempty"`
	Device   uint64      `json:"device"`
	Inode    uint64      `json:"inode"`
}
type UndoEntry struct {
	Source       string    `json:"source"`
	Target       string    `json:"target"`
	SourceParent undoStamp `json:"source_parent"`
}
type UndoPlan struct {
	Kind    string      `json:"kind"`
	Roots   []UndoEntry `json:"roots"`
	Journal string      `json:"journal"`
	Entries int64       `json:"entries"`
}

func stampUndo(info os.FileInfo) undoStamp {
	stat := info.Sys().(*syscall.Stat_t)
	stamp := undoStamp{Mode: info.Mode(), Device: uint64(stat.Dev), Inode: stat.Ino}
	if !info.IsDir() {
		stamp.Size, stamp.Modified, stamp.Changed = info.Size(), info.ModTime().UnixNano(), stat.Ctim.Nano()
	}
	return stamp
}
func (t *Transfer) BeginUndoJournal(name string) (*UndoJournal, error) {
	journal, err := newUndoJournal(name)
	if err != nil {
		t.disableUndo("Undo is unavailable because its journal could not be created.")
		return nil, err
	}
	t.undoJournal = journal
	return journal, nil
}
func (t *Transfer) disableUndo(reason string) {
	if t.undoJournal != nil {
		t.undoJournal.Abort()
		t.undoJournal = nil
	}
	t.undoReason, t.undoRoots = reason, nil
}
func (t *Transfer) DiscardUndo() { t.disableUndo("Undo is unavailable for an incomplete transfer.") }

func (t *Transfer) recordUndoPublication(item transferItem, info os.FileInfo, err error) {
	if t.undoJournal == nil || t.kind == "move" && !item.top {
		return
	}
	if err != nil || info == nil {
		t.disableUndo("Undo is unavailable because the transferred items could not be verified.")
		return
	}
	stamp := stampUndo(info)
	if item.top {
		t.undoRoots = append(t.undoRoots, UndoEntry{Source: item.source, Target: item.target, SourceParent: t.undoParents[item.source]})
	}
	if info.IsDir() {
		return
	} // Directory receipts are appended after their children.
	if err := t.undoJournal.append(undoNode{item.target, stamp}); err != nil {
		t.disableUndo("Undo is unavailable because its journal was discarded or could not be written.")
	}
}
func (t *Transfer) finishUndoDirectory(item transferItem) {
	if t.undoJournal == nil || item.createdInfo == nil || t.kind == "move" && !item.top {
		return
	}
	stamp := stampUndo(item.createdInfo)
	stamp.Mode = os.ModeDir | item.info.Mode().Perm()
	if err := t.undoJournal.append(undoNode{item.target, stamp}); err != nil {
		t.disableUndo("Undo is unavailable because its journal was discarded or could not be written.")
	}
}
func (t *Transfer) recordUndoMove(item transferItem, configured Root, absolute string, expected os.FileInfo) {
	if t.undoJournal == nil || !item.top {
		return
	}
	root, err := os.OpenRoot(configured.Path)
	if err != nil {
		t.recordUndoPublication(item, nil, err)
		return
	}
	defer root.Close()
	relative, err := filepath.Rel(configured.Path, absolute)
	if err != nil {
		t.recordUndoPublication(item, nil, err)
		return
	}
	info, err := root.Lstat(relative)
	if err == nil && (!os.SameFile(info, expected) || info.Size() != expected.Size() || !info.ModTime().Equal(expected.ModTime())) {
		err = ErrUndoChanged
	}
	t.recordUndoPublication(item, info, err)
	if err == nil && info.IsDir() && t.undoJournal != nil {
		if err := t.undoJournal.append(undoNode{item.target, stampUndo(info)}); err != nil {
			t.disableUndo("Undo is unavailable because its journal could not be written.")
		}
	}
}
func (t *Transfer) UndoPlan() (*UndoPlan, string) {
	if t.kind == "move" && t.skipped > 0 {
		t.disableUndo("Undo is unavailable for moves with skipped items.")
	}
	if t.undoReason != "" {
		return nil, t.undoReason
	}
	if t.undoJournal == nil || len(t.undoRoots) == 0 {
		t.disableUndo("No transferred items to undo.")
		return nil, t.undoReason
	}
	for i, root := range t.undoRoots {
		for _, other := range t.undoRoots[:i] {
			if virtualContains(root.Target, other.Target) || virtualContains(other.Target, root.Target) || virtualContains(root.Source, other.Source) || virtualContains(other.Source, root.Source) {
				t.disableUndo("Undo is unavailable for overlapping source items.")
				return nil, t.undoReason
			}
		}
	}
	count, err := t.undoJournal.seal()
	if err != nil {
		t.disableUndo("Undo is unavailable because its journal could not be sealed.")
		return nil, t.undoReason
	}
	return &UndoPlan{Kind: t.kind, Roots: t.undoRoots, Journal: t.undoJournal.name, Entries: count}, ""
}
func virtualContains(parent, child string) bool {
	return parent == child || strings.HasPrefix(child, parent+"/")
}
func sameUndoIdentity(actual, expected undoStamp) bool {
	return actual.Device == expected.Device && actual.Inode == expected.Inode && actual.Mode.Type() == expected.Mode.Type()
}

func (s *Service) checkUndoNode(node undoNode, identityOnly bool) error {
	if IsConfiguredRootPath(node.Path) {
		return ErrInvalidPath
	}
	configured, absolute, err := s.resolver.ResolveEntry(node.Path)
	if err != nil {
		return errors.Join(ErrUndoChanged, err)
	}
	root, err := os.OpenRoot(configured.Path)
	if err != nil {
		return err
	}
	defer root.Close()
	relative, err := filepath.Rel(configured.Path, absolute)
	if err != nil {
		return err
	}
	info, err := root.Lstat(relative)
	if err != nil {
		return errors.Join(ErrUndoChanged, err)
	}
	stamp := stampUndo(info)
	if identityOnly {
		if !sameUndoIdentity(stamp, node.Stamp) {
			return ErrUndoChanged
		}
	} else if stamp != node.Stamp {
		return ErrUndoChanged
	}
	return nil
}
func (s *Service) checkUndoSource(entry UndoEntry) error {
	if IsConfiguredRootPath(entry.Source) {
		return ErrInvalidPath
	}
	parent, err := s.resolver.OpenExisting(path.Dir(entry.Source))
	if err != nil {
		return errors.Join(ErrUndoChanged, err)
	}
	info, err := parent.Stat()
	parent.Close()
	if err != nil || !sameUndoIdentity(stampUndo(info), entry.SourceParent) {
		return ErrUndoChanged
	}
	_, absolute, err := s.resolver.ResolveForCreate(entry.Source)
	if err != nil {
		return err
	}
	if _, err := os.Lstat(absolute); !errors.Is(err, fs.ErrNotExist) {
		return ErrUndoChanged
	}
	return nil
}
func (s *Service) UndoTransfer(ctx context.Context, plan UndoPlan, report func(TransferProgress)) error {
	reader, err := OpenUndoJournal(plan)
	if err != nil {
		return err
	}
	defer reader.Close()
	return s.UndoTransferJournal(ctx, plan, reader, report)
}

// Two streaming passes validate the complete sealed receipt set before any
// mutation, then replay children-before-parent receipts with fresh checks.
func (s *Service) UndoTransferJournal(ctx context.Context, plan UndoPlan, reader *UndoReader, report func(TransferProgress)) error {
	if (plan.Kind != "copy" && plan.Kind != "move") || len(plan.Roots) == 0 || len(plan.Roots) > 1000 || plan.Entries <= 0 {
		return ErrInvalidPath
	}
	var paths []string
	for _, root := range plan.Roots {
		if CleanVirtual(root.Target) != root.Target || IsConfiguredRootPath(root.Target) {
			return ErrInvalidPath
		}
		paths = append(paths, root.Source, root.Target)
		if plan.Kind == "move" {
			if err := s.checkUndoSource(root); err != nil {
				return err
			}
		}
	}
	var seen int64
	if err := reader.each(ctx, func(node undoNode) error {
		allowed := false
		for _, root := range plan.Roots {
			if plan.Kind == "copy" && virtualContains(root.Target, node.Path) || plan.Kind == "move" && root.Target == node.Path {
				allowed = true
				break
			}
		}
		if !allowed || CleanVirtual(node.Path) != node.Path {
			return ErrInvalidPath
		}
		seen++
		return s.checkUndoNode(node, plan.Kind == "move")
	}); err != nil {
		return err
	}
	if seen != plan.Entries {
		return ErrUndoChanged
	}
	if plan.Kind == "copy" {
		var actual int64
		for _, root := range plan.Roots {
			count, err := s.CountDeleteEntries(ctx, root.Target)
			if err != nil {
				return err
			}
			actual += count
		}
		if actual != plan.Entries {
			return ErrUndoChanged
		}
	} else if plan.Entries != int64(len(plan.Roots)) {
		return ErrUndoChanged
	}
	defer s.InvalidateFolderSizes(paths...)
	if plan.Kind == "copy" {
		targets := make([]string, 0, len(plan.Roots))
		for _, root := range plan.Roots {
			s.videoQueue.cancelPath(ctx, root.Target)
			targets = append(targets, root.Target)
		}
		tickets := s.captureThumbnailChanges(ctx, targets)
		defer func() {
			changes := make([]thumbnailcache.LocationChange, 0, len(tickets))
			for _, ticket := range tickets {
				changes = append(changes, thumbnailcache.LocationChange{Ticket: ticket, Purge: true})
			}
			s.applyThumbnailChanges(changes)
		}()
	}
	var done int64
	err := reader.each(ctx, func(node undoNode) error {
		if err := taskcontrol.Wait(ctx); err != nil {
			return err
		}
		if plan.Kind == "move" {
			var entry *UndoEntry
			for i := range plan.Roots {
				if plan.Roots[i].Target == node.Path {
					entry = &plan.Roots[i]
					break
				}
			}
			if entry == nil {
				return ErrInvalidPath
			}
			if err := s.checkUndoSource(*entry); err != nil {
				return err
			}
			if err := s.checkUndoNode(node, true); err != nil {
				return err
			}
			transfer := s.NewTransfer("move", []string{entry.Target}, path.Dir(entry.Source))
			transfer.disableUndo("")
			transfer.undoExpected = map[string]undoStamp{entry.Target: node.Stamp}
			sourceRoot, source, err := s.resolver.ResolveEntry(entry.Target)
			if err != nil {
				return err
			}
			targetRoot, target, err := s.resolver.ResolveForCreate(entry.Source)
			if err != nil {
				return err
			}
			if isSameOrDescendant(source, target) {
				return ErrUndoChanged
			}
			info, err := os.Lstat(source)
			if err != nil {
				return err
			}
			parent, err := os.Stat(filepath.Dir(target))
			if err != nil {
				return err
			}
			// A same-filesystem folder rename needs one receipt and no subtree
			// scan. Cross-filesystem fallback still uses the ordinary transfer.
			if canRenameTransferTree(sourceRoot, targetRoot) && info.Sys().(*syscall.Stat_t).Dev == parent.Sys().(*syscall.Stat_t).Dev {
				transfer.initialized = true
				transfer.totalItems = 1
				transfer.queue = []transferItem{{top: true, source: entry.Target, target: entry.Source, measured: true, items: 1}}
			} else {
				if err := transfer.initialize(ctx); err != nil {
					return err
				}
				transfer.queue[0].target = entry.Source
			}
			if err := transfer.Run(ctx, report); err != nil {
				return err
			}
		} else {
			configured, absolute, err := s.resolver.ResolveEntry(node.Path)
			if err != nil {
				return err
			}
			root, err := os.OpenRoot(configured.Path)
			if err != nil {
				return err
			}
			relative, err := filepath.Rel(configured.Path, absolute)
			if err != nil {
				root.Close()
				return err
			}
			parent, err := root.OpenRoot(filepath.Dir(relative))
			root.Close()
			if err != nil {
				return err
			}
			err = removeUndoEntry(parent, filepath.Base(relative), node.Stamp)
			parent.Close()
			if err != nil {
				return errors.Join(err, ErrUndoChanged)
			}
		}
		done++
		if report != nil {
			report(TransferProgress{Phase: "undoing", ItemsDone: done, ItemsTotal: plan.Entries, Message: "Undoing " + plan.Kind})
		}
		return nil
	})
	if err != nil {
		return err
	}
	if report != nil {
		report(TransferProgress{Phase: transferPhaseCompleted, ItemsDone: done, ItemsTotal: plan.Entries, Message: strings.ToUpper(plan.Kind[:1]) + plan.Kind[1:] + " undone"})
	}
	return nil
}

// Claim an entry into a private directory before removing it, so a replacement
// between the initial stat and rename is verified and restored, never unlinked.
// No recursive removal is used: unexpected children keep a directory intact.
func removeUndoEntry(parent *os.Root, name string, expected undoStamp) error {
	info, err := parent.Lstat(name)
	if err != nil {
		return err
	}
	if stampUndo(info) != expected {
		return ErrUndoChanged
	}
	directory, err := parent.Open(".")
	if err != nil {
		return err
	}
	defer directory.Close()
	stage, err := os.MkdirTemp(fmt.Sprintf("/proc/self/fd/%d", directory.Fd()), ".webfile-undo-")
	if err != nil {
		return err
	}
	defer os.Remove(stage) // Only an empty staging directory may be removed.
	private, err := os.OpenRoot(stage)
	if err != nil {
		return err
	}
	defer private.Close()
	staging, err := private.Open(".")
	if err != nil {
		return err
	}
	defer staging.Close()
	if err := unix.Renameat2(int(directory.Fd()), name, int(staging.Fd()), "entry", unix.RENAME_NOREPLACE); err != nil {
		return err
	}
	claimed, err := private.Lstat("entry")
	if err == nil {
		stamp := stampUndo(claimed)
		stamp.Changed = expected.Changed // Rename changes the entry's ctime.
		if stamp != expected {
			err = ErrUndoChanged
		}
	}
	if err == nil {
		err = private.Remove("entry")
	}
	if err == nil {
		return nil
	}
	if rollback := unix.Renameat2(int(staging.Fd()), "entry", int(directory.Fd()), name, unix.RENAME_NOREPLACE); rollback != nil {
		return fmt.Errorf("undo recovery retained in %s: %v: %w", filepath.Base(stage), rollback, clienterror.New("undo rollback blocked", clienterror.Recovery))
	}
	return err
}
