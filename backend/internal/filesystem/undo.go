package filesystem

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"syscall"

	"webfile/internal/clienterror"
	"webfile/internal/taskcontrol"
	"webfile/internal/thumbnailcache"

	"golang.org/x/sys/unix"
)

// Undo records only entries published by this transfer, never a later scan's
// contents. The bounded journal is task metadata, not a filesystem catalogue.
const maxUndoNodes = 10000

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
	Kind  string               `json:"kind"`
	Roots []UndoEntry          `json:"roots"`
	Nodes map[string]undoStamp `json:"nodes"`
}

func stampUndo(info os.FileInfo) undoStamp {
	stat := info.Sys().(*syscall.Stat_t)
	stamp := undoStamp{Mode: info.Mode(), Device: uint64(stat.Dev), Inode: stat.Ino}
	// Child publication/removal changes directory timestamps. Identity and the
	// exact recorded child set protect directories without rejecting that work.
	if !info.IsDir() {
		stamp.Size = info.Size()
		stamp.Modified = info.ModTime().UnixNano()
		stamp.Changed = stat.Ctim.Nano()
	}
	return stamp
}

func (t *Transfer) disableUndo(reason string) {
	t.undoReason = reason
	t.undoNodes = nil
	t.undoRoots = nil
}

func (t *Transfer) recordUndoNode(target string, info os.FileInfo, err error) {
	if t.undoNodes == nil {
		return
	}
	if err != nil || info == nil {
		t.disableUndo("Undo is unavailable because the transferred items could not be verified.")
		return
	}
	if _, exists := t.undoNodes[CleanVirtual(target)]; !exists && len(t.undoNodes) >= maxUndoNodes {
		t.disableUndo("Undo is unavailable for transfers with more than 10,000 entries.")
		return
	}
	t.undoNodes[CleanVirtual(target)] = stampUndo(info)
}

func (t *Transfer) recordUndoRoot(item transferItem) {
	if item.top && t.undoNodes != nil {
		t.undoRoots = append(t.undoRoots, UndoEntry{Source: item.source, Target: item.target, SourceParent: t.undoParents[item.source]})
	}
}

func (t *Transfer) finishUndoDirectory(target string, mode os.FileMode) {
	if stamp, ok := t.undoNodes[target]; ok {
		stamp.Mode = os.ModeDir | mode
		t.undoNodes[target] = stamp
	}
}

func (t *Transfer) captureUndoTree(ctx context.Context, source, target string) map[string]undoStamp {
	if t.undoNodes == nil {
		return nil
	}
	nodes, err := t.service.scanUndoTree(ctx, source, maxUndoNodes-len(t.undoNodes))
	if err != nil {
		t.disableUndo("Undo is unavailable because the transferred tree could not be recorded within the 10,000-entry limit.")
		return nil
	}
	result := make(map[string]undoStamp, len(nodes))
	for name, stamp := range nodes {
		result[target+strings.TrimPrefix(name, source)] = stamp
	}
	return result
}

func (t *Transfer) recordUndoMove(item transferItem, root Root, absolute string, expected os.FileInfo, nodes map[string]undoStamp) {
	if t.undoNodes == nil {
		return
	}
	opened, err := os.OpenRoot(root.Path)
	if err != nil {
		t.recordUndoNode(item.target, nil, err)
		return
	}
	defer opened.Close()
	relative, err := filepath.Rel(root.Path, absolute)
	if err != nil {
		t.recordUndoNode(item.target, nil, err)
		return
	}
	info, err := opened.Lstat(relative)
	if err == nil && (!os.SameFile(info, expected) || info.Size() != expected.Size() || !info.ModTime().Equal(expected.ModTime())) {
		err = ErrUndoChanged
	}
	for name, stamp := range nodes {
		t.undoNodes[name] = stamp
	}
	t.recordUndoNode(item.target, info, err)
	t.recordUndoRoot(item)
}

func (t *Transfer) UndoPlan() (*UndoPlan, string) {
	if t.kind == "move" && t.skipped > 0 {
		return nil, "Undo is unavailable for moves with skipped items."
	}
	if t.undoReason != "" {
		return nil, t.undoReason
	}
	if len(t.undoRoots) == 0 {
		return nil, "No transferred items to undo."
	}
	for i, root := range t.undoRoots {
		for _, other := range t.undoRoots[:i] {
			if virtualContains(root.Target, other.Target) || virtualContains(other.Target, root.Target) ||
				virtualContains(root.Source, other.Source) || virtualContains(other.Source, root.Source) {
				return nil, "Undo is unavailable for overlapping source items."
			}
		}
	}
	return &UndoPlan{Kind: t.kind, Roots: t.undoRoots, Nodes: t.undoNodes}, ""
}

func virtualContains(parent, child string) bool {
	return parent == child || strings.HasPrefix(child, parent+"/")
}

func (s *Service) scanUndoTree(ctx context.Context, source string, limit int) (map[string]undoStamp, error) {
	configured, absolute, err := s.resolver.ResolveEntry(source)
	if err != nil {
		return nil, err
	}
	root, err := os.OpenRoot(configured.Path)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	relative, err := filepath.Rel(configured.Path, absolute)
	if err != nil {
		return nil, err
	}
	nodes := map[string]undoStamp{}
	var walk func(string, string) error
	walk = func(rel, virtual string) error {
		if err := taskcontrol.Wait(ctx); err != nil {
			return err
		}
		if len(nodes) >= limit {
			return fmt.Errorf("undo entry limit exceeded")
		}
		if err := configured.ValidatePath(filepath.Join(configured.Path, rel)); err != nil {
			return err
		}
		info, err := root.Lstat(rel)
		if err != nil {
			return err
		}
		nodes[virtual] = stampUndo(info)
		if !info.IsDir() {
			return nil
		}
		file, err := root.Open(rel)
		if err != nil {
			return err
		}
		opened, err := file.Stat()
		if err != nil || !os.SameFile(info, opened) {
			file.Close()
			return ErrUndoChanged
		}
		defer file.Close()
		for {
			children, readErr := file.ReadDir(128)
			for _, child := range children {
				if err := walk(filepath.Join(rel, child.Name()), path.Join(virtual, child.Name())); err != nil {
					return err
				}
			}
			if errors.Is(readErr, io.EOF) {
				return nil
			}
			if readErr != nil {
				return readErr
			}
		}
	}
	err = walk(relative, CleanVirtual(source))
	return nodes, err
}

func (s *Service) checkUndoTree(ctx context.Context, target string, nodes map[string]undoStamp) error {
	actual, err := s.scanUndoTree(ctx, target, maxUndoNodes)
	if err != nil {
		return errors.Join(ErrUndoChanged, err)
	}
	expected := map[string]undoStamp{}
	for name, stamp := range nodes {
		if virtualContains(target, name) {
			expected[name] = stamp
		}
	}
	if !reflect.DeepEqual(expected, actual) {
		return ErrUndoChanged
	}
	return nil
}

// UndoTransfer validates the entire recorded output before changing anything.
// A claim is consumed durably by the task manager before this method is called;
// interrupted or partially completed undo is never replayed automatically.
func (s *Service) UndoTransfer(ctx context.Context, plan UndoPlan, report func(TransferProgress)) error {
	if (plan.Kind != "copy" && plan.Kind != "move") || len(plan.Roots) == 0 || len(plan.Nodes) == 0 || len(plan.Nodes) > maxUndoNodes {
		return ErrInvalidPath
	}
	var paths []string
	for _, entry := range plan.Roots {
		if IsConfiguredRootPath(entry.Source) || IsConfiguredRootPath(entry.Target) {
			return ErrInvalidPath
		}
		paths = append(paths, entry.Source, entry.Target)
		if err := s.checkUndoTree(ctx, entry.Target, plan.Nodes); err != nil {
			return err
		}
		if plan.Kind == "move" {
			parent, err := s.resolver.OpenExisting(path.Dir(entry.Source))
			if err != nil {
				return errors.Join(ErrUndoChanged, err)
			}
			info, err := parent.Stat()
			parent.Close()
			if err != nil || stampUndo(info) != entry.SourceParent {
				return ErrUndoChanged
			}
			_, absolute, err := s.resolver.ResolveForCreate(entry.Source)
			if err != nil {
				return err
			}
			if _, err := os.Lstat(absolute); !errors.Is(err, fs.ErrNotExist) {
				return ErrUndoChanged
			}
		}
	}
	defer s.InvalidateFolderSizes(paths...)
	var removed int64
	for index := len(plan.Roots) - 1; index >= 0; index-- {
		entry := plan.Roots[index]
		if err := taskcontrol.Wait(ctx); err != nil {
			return err
		}
		if plan.Kind == "move" {
			// The ordinary transfer retains anchored, no-overwrite publication,
			// cross-filesystem fallback, exclusions and source-change checks.
			transfer := s.NewTransfer("move", []string{entry.Target}, path.Dir(entry.Source))
			transfer.disableUndo("")
			transfer.undoExpected = plan.Nodes
			if err := transfer.initialize(ctx); err != nil {
				return err
			}
			transfer.queue[0].target = entry.Source
			if err := transfer.Run(ctx, report); err != nil {
				return err
			}
		} else {
			s.videoQueue.cancelPath(ctx, entry.Target)
			tickets := s.captureThumbnailChanges(ctx, []string{entry.Target})
			var names []string
			for name := range plan.Nodes {
				if virtualContains(entry.Target, name) {
					names = append(names, name)
				}
			}
			sort.Slice(names, func(i, j int) bool { return len(names[i]) > len(names[j]) })
			for _, name := range names {
				if err := taskcontrol.Wait(ctx); err != nil {
					return err
				}
				configured, absolute, err := s.resolver.ResolveEntry(name)
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
				err = removeUndoEntry(parent, filepath.Base(relative), plan.Nodes[name])
				parent.Close()
				if err != nil {
					return errors.Join(err, ErrUndoChanged)
				}
				removed++
				if report != nil {
					report(TransferProgress{Phase: "undoing", ItemsDone: removed, ItemsTotal: int64(len(plan.Nodes)), Message: "Removing copied items"})
				}
			}
			if len(tickets) == 1 {
				s.applyThumbnailChanges([]thumbnailcache.LocationChange{{Ticket: tickets[0], Purge: true}})
			}
		}
		if report != nil && plan.Kind == "move" {
			report(TransferProgress{Phase: "undoing", ItemsDone: int64(len(plan.Roots) - index), ItemsTotal: int64(len(plan.Roots)), Message: "Undoing " + plan.Kind})
		}
	}
	if report != nil {
		total := int64(len(plan.Nodes))
		if plan.Kind == "move" {
			total = int64(len(plan.Roots))
		}
		report(TransferProgress{Phase: transferPhaseCompleted, ItemsDone: total, ItemsTotal: total, Message: strings.ToUpper(plan.Kind[:1]) + plan.Kind[1:] + " undone"})
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
