package filesystem

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
	"syscall"
	"webfile/internal/rootedio"
	"webfile/internal/taskcontrol"

	"golang.org/x/sys/unix"
)

type TransferConflict struct {
	Revision    string   `json:"revision"`
	Source      Entry    `json:"source"`
	Destination Entry    `json:"destination"`
	Category    string   `json:"category"`
	Choices     []string `json:"choices"`
	Changed     bool     `json:"changed"`
}

func (c *TransferConflict) Error() string { return "destination conflict requires a decision" }

type ConflictDecision struct {
	Revision       string `json:"revision"`
	Choice         string `json:"choice"`
	ApplyRemaining bool   `json:"apply_remaining"`
}
type transferItem struct {
	createdInfo    os.FileInfo
	top            bool
	source, target string
	bytes, items   int64
	measured       bool
	finish         bool
	restore        bool
	info           os.FileInfo
}
type Transfer struct {
	undoJournal                          *UndoJournal
	undoRoots                            []UndoEntry
	undoReason                           string
	undoExpected                         map[string]undoStamp
	undoParents                          map[string]undoStamp
	published                            os.FileInfo
	service                              *Service
	kind                                 string
	sources                              []string
	destination                          string
	initialized                          bool
	queue                                []transferItem
	pending                              *TransferConflict
	decision                             *ConflictDecision
	defaults                             map[string]string
	bytes, items, totalBytes, totalItems int64
	skipped                              int
	forceReview                          bool
}

func (s *Service) NewTransfer(kind string, sources []string, destination string) *Transfer {
	return &Transfer{service: s, kind: kind, sources: append([]string(nil), sources...), destination: CleanVirtual(destination), defaults: map[string]string{}, undoParents: map[string]undoStamp{}}
}
func (t *Transfer) Decide(decision ConflictDecision) error {
	if t.pending == nil || decision.Revision != t.pending.Revision {
		return fmt.Errorf("this conflict has changed; review it again")
	}
	allowed := false
	for _, choice := range t.pending.Choices {
		if choice == decision.Choice {
			allowed = true
		}
	}
	if !allowed {
		return fmt.Errorf("this action is not available for this conflict")
	}
	t.decision = &decision
	return nil
}

func fingerprint(info os.FileInfo) string {
	stat, _ := info.Sys().(*syscall.Stat_t)
	return fmt.Sprintf("%v:%d:%d:%d:%d:%v", info.Mode(), info.Size(), info.ModTime().UnixNano(), stat.Dev, stat.Ino, stat.Ctim)
}
func (t *Transfer) initialize(ctx context.Context) error {
	if t.kind != "copy" && t.kind != "move" {
		return fmt.Errorf("invalid transfer kind")
	}
	if len(t.sources) == 0 || len(t.sources) > 1000 {
		return fmt.Errorf("transfer requires 1–1000 source items")
	}
	_, destination, err := t.service.resolver.ResolveExisting(t.destination)
	if err != nil {
		return err
	}
	destinationInfo, err := t.service.resolver.disk.Stat(destination)
	if err != nil {
		return err
	}
	if !destinationInfo.IsDir() {
		return ErrNotDirectory
	}
	seen := map[string]bool{}
	for _, source := range t.sources {
		if err := taskcontrol.Wait(ctx); err != nil {
			return err
		}
		if IsConfiguredRootPath(source) {
			return fmt.Errorf("configured roots cannot be copied or moved")
		}
		root, absolute, err := t.service.resolver.ResolveEntry(source)
		if err != nil {
			return err
		}
		if t.kind == "move" {
			if err := root.validateMoveSource(ctx, absolute); err != nil {
				return err
			}
		}
		if seen[absolute] {
			return fmt.Errorf("duplicate source item")
		}
		seen[absolute] = true
		parentInfo, err := t.service.resolver.disk.Stat(filepath.Dir(absolute))
		if err != nil {
			return err
		}
		t.undoParents[CleanVirtual(source)] = stampUndo(parentInfo)
		target := filepath.Join(destination, filepath.Base(absolute))
		if isSameOrDescendant(absolute, target) {
			return fmt.Errorf("destination cannot be the source or beneath it")
		}
		sourceInfo, err := t.service.resolver.disk.Lstat(absolute)
		if err != nil {
			return err
		}
		if targetInfo, e := t.service.resolver.disk.Lstat(target); e == nil && os.SameFile(sourceInfo, targetInfo) {
			return fmt.Errorf("source and destination are the same item")
		}
		bytes, items, err := measureTree(ctx, absolute)
		if err != nil {
			return err
		}
		t.totalBytes += bytes
		t.totalItems += items
		t.queue = append(t.queue, transferItem{top: true, source: CleanVirtual(source), target: path.Join(t.destination, path.Base(CleanVirtual(source))), bytes: bytes, items: items, measured: true})
	}
	t.initialized = true
	return nil
}

func (t *Transfer) Run(ctx context.Context, report func(TransferProgress)) error {
	defer t.service.InvalidateFolderSizes(append(append([]string(nil), t.sources...), t.destination)...)
	capturedThumbnails := t.service.captureThumbnailTransfer(ctx, t.kind, t.sources, t.destination)
	defer t.service.finishThumbnailTransfer(capturedThumbnails)
	emit := func(phase, message string) {
		if report != nil {
			report(TransferProgress{Phase: phase, Message: message, BytesDone: t.bytes, BytesTotal: t.totalBytes, ItemsDone: t.items, ItemsTotal: t.totalItems})
		}
	}
	if !t.initialized {
		emit(transferPhaseScanning, "Scanning")
		if err := t.initialize(ctx); err != nil {
			return err
		}
	}
	for len(t.queue) > 0 {
		if err := taskcontrol.Wait(ctx); err != nil {
			return err
		}
		if copied, err := t.copySmallBatch(ctx, emit); copied || err != nil {
			if err != nil {
				return err
			}
			continue
		}
		item := t.queue[0]
		if item.finish {
			if item.restore {
				_, target, e := t.service.resolver.ResolveEntry(item.target)
				if e != nil {
					return e
				}
				_ = t.service.resolver.disk.Chmod(target, item.info.Mode().Perm())
				_ = t.service.resolver.disk.Chtimes(target, item.info.ModTime(), item.info.ModTime())
				t.finishUndoDirectory(item)
			}
			if t.kind == "move" {
				_, source, e := t.service.resolver.ResolveEntry(item.source)
				if e == nil {
					if e = t.service.resolver.disk.Remove(source); e != nil && !errors.Is(e, unix.ENOTEMPTY) && !errors.Is(e, fs.ErrNotExist) {
						return e
					}
				} else if !errors.Is(e, fs.ErrNotExist) {
					return e
				}
			}
			t.queue = t.queue[1:]
			continue
		}
		sourceRoot, source, err := t.service.resolver.ResolveEntry(item.source)
		if err != nil {
			return err
		}
		targetRoot, target, err := t.service.resolver.ResolveForCreate(item.target)
		if err != nil {
			return err
		}
		sourceInfo, err := t.service.resolver.disk.Lstat(source)
		if err != nil {
			return err
		}
		if t.undoExpected != nil {
			if expected, ok := t.undoExpected[item.source]; ok && !sameUndoIdentity(stampUndo(sourceInfo), expected) {
				return ErrUndoChanged
			}
		}
		targetInfo, targetErr := t.service.resolver.disk.Lstat(target)
		if targetErr != nil && !errors.Is(targetErr, fs.ErrNotExist) {
			return targetErr
		}
		choice := ""
		expected := targetInfo
		if targetErr == nil || t.pending != nil || t.forceReview {
			if targetInfo != nil && os.SameFile(sourceInfo, targetInfo) {
				return fmt.Errorf("source and destination are the same item")
			}
			conflict := &TransferConflict{Source: toEntry(path.Dir(item.source), path.Base(item.source), sourceInfo), Destination: Entry{Path: item.target, Name: path.Base(item.target), Mode: "unavailable"}, Choices: []string{"skip", "keep_both"}, Category: "type_mismatch"}
			destinationSignature := "missing"
			if targetInfo != nil {
				conflict.Destination = toEntry(path.Dir(item.target), path.Base(item.target), targetInfo)
				destinationSignature = fingerprint(targetInfo)
				if sourceInfo.IsDir() && targetInfo.IsDir() {
					conflict.Category = "folders"
					conflict.Choices = append(conflict.Choices, "merge")
				}
				if sourceInfo.Mode().IsRegular() && targetInfo.Mode().IsRegular() {
					conflict.Category = "files"
					conflict.Choices = append(conflict.Choices, "replace")
				}
			}
			digest := sha256.Sum256([]byte(item.source + item.target + fingerprint(sourceInfo) + destinationSignature))
			conflict.Revision = hex.EncodeToString(digest[:])
			if t.forceReview {
				t.forceReview = false
				conflict.Changed = true
				t.decision = nil
				t.pending = conflict
				return conflict
			}
			if t.decision != nil {
				if t.decision.Revision != conflict.Revision {
					conflict.Changed = true
					t.decision = nil
					t.pending = conflict
					return conflict
				}
				choice = t.decision.Choice
				if t.decision.ApplyRemaining {
					t.defaults[conflict.Category] = choice
				}
				t.decision = nil
			} else if t.pending != nil && t.pending.Revision != conflict.Revision {
				conflict.Changed = true
				t.pending = conflict
				return conflict
			} else {
				choice = t.defaults[conflict.Category]
			}
			if choice == "" {
				t.pending = conflict
				return conflict
			}
			t.pending = nil
			if choice == "skip" {
				t.skipped++
				t.queue = t.queue[1:]
				emit(transferPhaseCopying, fmt.Sprintf("Skipped %s", path.Base(item.source)))
				continue
			}
			if choice == "keep_both" {
				base := path.Base(item.target)
				ext := ""
				if !sourceInfo.IsDir() {
					ext = path.Ext(base)
				}
				stem := strings.TrimSuffix(base, ext)
				for number := 1; number <= 10000; number++ {
					suffix := " (copy)"
					if number > 1 {
						suffix = fmt.Sprintf(" (copy %d)", number)
					}
					item.target = path.Join(path.Dir(item.target), stem+suffix+ext)
					_, target, err = t.service.resolver.ResolveForCreate(item.target)
					if err != nil {
						return err
					}
					if _, e := t.service.resolver.disk.Lstat(target); errors.Is(e, fs.ErrNotExist) {
						break
					} else if e != nil {
						return e
					}
					if number == 10000 {
						return fmt.Errorf("could not choose an unused filename")
					}
				}
				choice = ""
			}
		} else {
			t.pending = nil
			t.decision = nil
		}
		if choice == "replace" || choice == "merge" {
			t.disableUndo("Undo is unavailable for transfers that replace files or merge existing folders.")
		}
		if sourceInfo.IsDir() {
			if t.kind == "move" {
				if err := sourceRoot.validateMoveSource(ctx, source); err != nil {
					return err
				}
			}
			// An absent destination can receive the whole directory without
			// requiring write access to its descendants. Existing folders still
			// follow the explicit merge path and review child conflicts.
			if t.kind == "move" && choice != "merge" && canRenameTransferTree(sourceRoot, targetRoot) {
				bytes, items := item.bytes, item.items
				if !item.measured {
					var err error
					bytes, items, err = measureTree(ctx, source)
					if err != nil {
						return err
					}
				}
				t.service.videoQueue.cancelPath(ctx, item.source)
				if err := taskcontrol.Wait(ctx); err != nil {
					return err
				}
				err = moveReviewedItem(t.service.resolver.disk, sourceRoot, targetRoot, source, target, sourceInfo)
				if err == nil {
					t.recordUndoMove(item, targetRoot, target, sourceInfo)
					t.bytes += bytes
					t.items += items
					t.queue = t.queue[1:]
					emit(transferPhaseCopying, "Moved "+path.Base(item.source))
					continue
				}
				if errors.Is(err, ErrConflict) {
					t.forceReview = true
					continue
				}
				if !errors.Is(err, unix.EXDEV) && !errors.Is(err, errRenameNoReplaceUnsupported) {
					return err
				}
			}
			if choice != "merge" {
				err := t.service.resolver.disk.Mkdir(target, sourceInfo.Mode().Perm()|0700)
				if err == nil {
					info, statErr := t.service.resolver.disk.Lstat(target)
					item.createdInfo = info
					t.recordUndoPublication(item, info, statErr)
				}
				if err != nil {
					if errors.Is(err, fs.ErrExist) {
						continue
					}
					return err
				}
			}
			children, err := t.service.List(ctx, item.source)
			if err != nil {
				return err
			}
			jobs := make([]transferItem, 0, len(children)+1)
			for _, child := range children {
				jobs = append(jobs, transferItem{source: child.Path, target: path.Join(item.target, child.Name)})
			}
			jobs = append(jobs, transferItem{top: item.top, source: item.source, target: item.target, finish: true, restore: choice != "merge", info: sourceInfo, createdInfo: item.createdInfo})
			t.queue = append(jobs, t.queue[1:]...)
			t.items++
			continue
		}
		if !sourceInfo.Mode().IsRegular() && sourceInfo.Mode()&os.ModeSymlink == 0 {
			return fmt.Errorf("unsupported special file")
		}
		if t.kind == "move" {
			t.service.videoQueue.cancelPath(ctx, item.source)
		}
		if err := taskcontrol.Wait(ctx); err != nil {
			return err
		}
		if t.kind == "move" && choice != "replace" {
			if err := moveReviewedItem(t.service.resolver.disk, sourceRoot, targetRoot, source, target, sourceInfo); err == nil {
				t.recordUndoMove(item, targetRoot, target, sourceInfo)
				t.bytes += sourceInfo.Size()
				t.items++
				t.queue = t.queue[1:]
				emit(transferPhaseCopying, "Moved "+path.Base(item.source))
				continue
			} else if !errors.Is(err, unix.EXDEV) && !errors.Is(err, ErrConflict) && !errors.Is(err, errRenameNoReplaceUnsupported) {
				return err
			}
		}
		err = t.copyItem(ctx, sourceRoot, targetRoot, source, target, sourceInfo, expected, choice == "replace", emit)
		if errors.Is(err, ErrConflict) {
			t.forceReview = true
			continue
		}
		if err != nil {
			return err
		}
		t.recordUndoPublication(item, t.published, nil)
		t.items++
		t.queue = t.queue[1:]
	}
	emit(transferPhaseCompleted, fmt.Sprintf("Complete · %d items processed · %d conflicts skipped", t.items, t.skipped))
	return nil
}

func (t *Transfer) copyItem(ctx context.Context, sourceRoot, targetRoot Root, source, target string, sourceInfo, expected os.FileInfo, replace bool, emit func(string, string)) error {
	disk := t.service.resolver.disk
	stage, err := disk.CreateTemp(filepath.Dir(target), ".webfile-extract-transfer-")
	if err != nil {
		return err
	}
	staged := stage.Name()
	if err := stage.Close(); err != nil {
		disk.Remove(staged)
		return err
	}
	if err := disk.Remove(staged); err != nil {
		return err
	}
	defer func() {
		if staged != "" {
			disk.Remove(staged)
		}
	}()
	startBytes := t.bytes
	if sourceInfo.Mode().IsRegular() {
		file, e := disk.OpenFile(source, os.O_RDONLY|unix.O_NOFOLLOW, 0)
		if e != nil {
			return e
		}
		defer file.Close()
		opened, e := file.Stat()
		if e != nil {
			return e
		}
		if fingerprint(opened) != fingerprint(sourceInfo) {
			return fmt.Errorf("source changed; review it again")
		}
		err = copyOpenFileAtomic(disk, ctx, file, staged, sourceInfo.Mode().Perm(), sourceInfo.ModTime(), func(phase string, bytes int64) { t.bytes += bytes; emit(phase, "Copying "+filepath.Base(source)) })
	} else {
		link, e := disk.Readlink(source)
		if e != nil {
			return e
		}
		err = disk.Symlink(link, staged)
	}
	if err != nil {
		t.bytes = startBytes
		return err
	}
	current, err := disk.Lstat(source)
	if err != nil {
		return err
	}
	if fingerprint(current) != fingerprint(sourceInfo) {
		return fmt.Errorf("source changed during transfer; review it again")
	}
	if err := taskcontrol.Wait(ctx); err != nil {
		return err
	}
	stagedInfo, err := disk.Lstat(staged)
	if err != nil {
		return err
	}
	if replace {
		current, err = disk.Lstat(target)
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				t.bytes = startBytes
				return ErrConflict
			}
			return err
		}
		if fingerprint(current) != fingerprint(expected) {
			t.bytes = startBytes
			return ErrConflict
		}
		if err := disk.Chmod(staged, expected.Mode().Perm()); err != nil {
			return err
		}
		published, e := disk.Lstat(staged)
		if e != nil {
			return e
		}
		if err = disk.RenameFlags(staged, target, unix.RENAME_EXCHANGE); err != nil {
			return fmt.Errorf("filesystem cannot safely replace this item: %w", err)
		}
		displaced, e := disk.Lstat(staged)
		if e != nil || !os.SameFile(displaced, expected) || displaced.Size() != expected.Size() || !displaced.ModTime().Equal(expected.ModTime()) {
			live, e := disk.Lstat(target)
			if e != nil || !os.SameFile(live, published) || live.Size() != published.Size() || !live.ModTime().Equal(published.ModTime()) {
				recovery := filepath.Base(staged)
				staged = ""
				return fmt.Errorf("destination changed concurrently; displaced data retained in destination as %s", recovery)
			}
			if rollback := disk.RenameFlags(staged, target, unix.RENAME_EXCHANGE); rollback != nil {
				staged = ""
				return fmt.Errorf("destination changed; retained displaced data for recovery: %w", rollback)
			}
			restored, e := disk.Lstat(staged)
			if e != nil || !os.SameFile(restored, published) || restored.Size() != published.Size() || !restored.ModTime().Equal(published.ModTime()) {
				recovery := filepath.Base(staged)
				staged = ""
				return fmt.Errorf("concurrent destination change; recovery file retained as %s", recovery)
			}
			t.bytes = startBytes
			return ErrConflict
		}
	} else if err = renameNoReplace(disk, staged, target); err != nil {
		t.bytes = startBytes
		return err
	}
	t.published, err = disk.Lstat(target)
	if err != nil {
		return err
	}
	if !os.SameFile(t.published, stagedInfo) || t.published.Size() != stagedInfo.Size() || !t.published.ModTime().Equal(stagedInfo.ModTime()) {
		t.published = nil
	}
	if t.kind == "move" {
		current, err = disk.Lstat(source)
		if err != nil {
			return err
		}
		if fingerprint(current) != fingerprint(sourceInfo) {
			return fmt.Errorf("copy completed but source changed; source was retained")
		}
		if err = disk.Remove(source); err != nil {
			return fmt.Errorf("copy completed but source could not be removed: %w", err)
		}
	}
	return nil
}

func moveReviewedItem(disk *rootedio.FS, sourceRoot, targetRoot Root, source, target string, expected os.FileInfo) error {
	current, err := disk.Lstat(source)
	if err != nil {
		return err
	}
	if fingerprint(current) != fingerprint(expected) {
		return fmt.Errorf("source changed; review it again")
	}
	err = disk.RenameFlags(source, target, unix.RENAME_NOREPLACE)
	if errors.Is(err, fs.ErrExist) || errors.Is(err, unix.ENOTEMPTY) {
		return ErrConflict
	}
	if errors.Is(err, unix.ENOSYS) || errors.Is(err, unix.EINVAL) || errors.Is(err, unix.EOPNOTSUPP) {
		return errRenameNoReplaceUnsupported
	}
	return err
}

func canRenameTransferTree(sourceRoot, targetRoot Root) bool {
	// A cross-root rename must not move descendants that the ordinary transfer
	// would filter out, or bypass destination exclusion checks on those children.
	return sourceRoot.Name == targetRoot.Name ||
		len(sourceRoot.ExcludedNames) == 0 && len(sourceRoot.ExcludedPaths) == 0 &&
			len(targetRoot.ExcludedNames) == 0 && len(targetRoot.ExcludedPaths) == 0
}
