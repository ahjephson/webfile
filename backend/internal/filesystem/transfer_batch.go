package filesystem

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"webfile/internal/taskcontrol"
)

const smallCopyLimit = 64 << 10
const smallCopyWorkers = 4

type smallCopy struct {
	item                   transferItem
	sourceRoot, targetRoot Root
	source, target         string
	info                   os.FileInfo
}

// Only independent, absent-destination small files are pipelined. Each copy
// retains its ordinary sync, anchored publication and source checks; conflicts,
// directories and large files stay on the sequential review path.
func (t *Transfer) copySmallBatch(ctx context.Context, emit func(string, string)) (bool, error) {
	if t.pending != nil || t.decision != nil || t.forceReview {
		return false, nil
	}
	var batch []smallCopy
	targets := make(map[string]bool)
	for _, item := range t.queue {
		if len(batch) == smallCopyWorkers || item.finish {
			break
		}
		sourceRoot, source, err := t.service.resolver.ResolveEntry(item.source)
		if err != nil {
			break
		}
		targetRoot, target, err := t.service.resolver.ResolveForCreate(item.target)
		if err != nil {
			break
		}
		if targets[target] {
			break
		}
		info, err := os.Lstat(source)
		if err != nil || !info.Mode().IsRegular() || info.Size() > smallCopyLimit {
			break
		}
		if t.undoExpected != nil {
			if expected, ok := t.undoExpected[item.source]; ok && !sameUndoIdentity(stampUndo(info), expected) {
				return false, ErrUndoChanged
			}
		}
		if _, err := os.Lstat(target); !errors.Is(err, fs.ErrNotExist) {
			break
		}
		if t.kind == "move" {
			parent, err := os.Stat(filepath.Dir(target))
			if err != nil {
				break
			}
			// Preserve the cheap rename path when no byte copy is needed.
			if info.Sys().(*syscall.Stat_t).Dev == parent.Sys().(*syscall.Stat_t).Dev {
				break
			}
			t.service.videoQueue.cancelPath(ctx, item.source)
		}
		if err := taskcontrol.Wait(ctx); err != nil {
			return false, err
		}
		batch = append(batch, smallCopy{item: item, sourceRoot: sourceRoot, targetRoot: targetRoot, source: source, target: target, info: info})
		targets[target] = true
	}
	if len(batch) < 2 {
		return false, nil
	}

	var progressMu sync.Mutex
	var workers sync.WaitGroup
	results := make([]error, len(batch))
	published := make([]os.FileInfo, len(batch))
	for index, copy := range batch {
		workers.Add(1)
		go func() {
			defer workers.Done()
			worker := &Transfer{service: t.service, kind: t.kind}
			var reported int64
			report := func(phase, message string) {
				progressMu.Lock()
				defer progressMu.Unlock()
				t.bytes += worker.bytes - reported
				reported = worker.bytes
				emit(phase, message)
			}
			results[index] = worker.copyItem(ctx, copy.sourceRoot, copy.targetRoot, copy.source, copy.target, copy.info, nil, false, report)
			published[index] = worker.published
			if results[index] != nil {
				progressMu.Lock()
				t.bytes -= reported
				progressMu.Unlock()
			}
		}()
	}
	workers.Wait()
	var remaining []transferItem
	var failure error
	for index, err := range results {
		if err == nil {
			t.recordUndoPublication(batch[index].item, published[index], nil)
			t.items++
			continue
		}
		remaining = append(remaining, batch[index].item)
		if errors.Is(err, ErrConflict) {
			t.forceReview = true
		} else if failure == nil {
			failure = err
		}
	}
	t.queue = append(remaining, t.queue[len(batch):]...)
	return true, failure
}
