//go:build linux

package filesystem

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"golang.org/x/sys/unix"
)

type writebackCall struct {
	offset, length int64
	wait           bool
}

func TestCopyWritebackPipelinesBoundedRanges(t *testing.T) {
	var calls []writebackCall
	writer := &copyWriteback{syncRange: func(_ context.Context, offset, length int64, wait bool) error {
		calls = append(calls, writebackCall{offset, length, wait})
		return nil
	}}
	for index := 0; index < 6; index++ {
		if err := writer.advance(context.Background(), copyWritebackWindow/2); err != nil {
			t.Fatal(err)
		}
	}
	want := []writebackCall{{0, copyWritebackWindow, false}, {0, copyWritebackWindow, true}, {copyWritebackWindow, copyWritebackWindow, false}, {copyWritebackWindow, copyWritebackWindow, true}, {2 * copyWritebackWindow, copyWritebackWindow, false}}
	if !reflect.DeepEqual(calls, want) {
		t.Fatalf("writeback schedule=%v, want %v", calls, want)
	}
	if writer.copied-writer.waited != copyWritebackWindow {
		t.Fatal("more than one queued range remains")
	}
}

func TestCopyWritebackFallbackAndErrors(t *testing.T) {
	for _, waitFailure := range []bool{false, true} {
		for _, failure := range []error{errCopyWritebackUnsupported, unix.EIO, unix.ENOSPC} {
			t.Run(failure.Error()+map[bool]string{false: "/submit", true: "/wait"}[waitFailure], func(t *testing.T) {
				calls := 0
				writer := &copyWriteback{syncRange: func(_ context.Context, _, _ int64, wait bool) error {
					calls++
					if wait == waitFailure {
						return failure
					}
					return nil
				}}
				err := writer.advance(context.Background(), copyWritebackWindow)
				if waitFailure {
					if err != nil {
						t.Fatal(err)
					}
					err = writer.advance(context.Background(), copyWritebackWindow)
				}
				if errors.Is(failure, errCopyWritebackUnsupported) {
					if err != nil || !writer.disabled {
						t.Fatalf("unsupported filesystem did not fall back: %v", err)
					}
					previousCalls := calls
					if err = writer.advance(context.Background(), copyWritebackWindow); err != nil || calls != previousCalls {
						t.Fatal("disabled writeback retried")
					}
				} else if !errors.Is(err, failure) {
					t.Fatalf("IO failure swallowed: %v", err)
				}
			})
		}
	}
}

func TestCopyWritebackStopsBeforeSubmittingNextRangeOnCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	calls := 0
	writer := &copyWriteback{syncRange: func(ctx context.Context, _, _ int64, wait bool) error {
		calls++
		if wait {
			cancel()
		}
		return ctx.Err()
	}}
	if err := writer.advance(ctx, copyWritebackWindow); err != nil {
		t.Fatal(err)
	}
	if err := writer.advance(ctx, copyWritebackWindow); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation=%v", err)
	}
	if calls != 2 {
		t.Fatalf("submitted after cancellation: %d calls", calls)
	}
}

func TestCopyWritebackPreservesDestinationOffset(t *testing.T) {
	file, err := os.Create(filepath.Join(t.TempDir(), "partial"))
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	if _, err = file.Write([]byte("existing prefix")); err != nil {
		t.Fatal(err)
	}
	advance, err := newCopyWriteback(file)
	if err != nil {
		t.Fatal(err)
	}
	if err = advance(context.Background(), copyWritebackWindow); err != nil {
		t.Fatal(err)
	}
	position, err := file.Seek(0, 1)
	if err != nil || position != int64(len("existing prefix")) {
		t.Fatalf("writeback changed file offset: %d %v", position, err)
	}
}

func TestMusicSizedCrossFilesystemMoveAndCancellation(t *testing.T) {
	for _, cancelDuringCopy := range []bool{false, true} {
		t.Run(map[bool]string{false: "complete", true: "cancel"}[cancelDuringCopy], func(t *testing.T) {
			service, from, to, sources := crossFilesystemSmallFiles(t)
			// Reuse just one disposable source, now large enough to exercise
			// multiple writeback ranges on the buffered cross-filesystem path.
			name := filepath.Base(sources[0])
			content := bytes.Repeat([]byte("music-sized transfer fixture\n"), (32<<20)/len("music-sized transfer fixture\n"))
			if err := os.WriteFile(filepath.Join(from, name), content, 0600); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			var maximumBytes int64
			err := service.NewTransfer("move", sources[:1], "/To").Run(ctx, func(p TransferProgress) {
				if p.BytesDone > maximumBytes {
					maximumBytes = p.BytesDone
				}
				if cancelDuringCopy && p.BytesDone >= 2*copyWritebackWindow {
					cancel()
				}
			})
			if cancelDuringCopy {
				if !errors.Is(err, context.Canceled) {
					t.Fatalf("cancelled move=%v", err)
				}
				got, e := os.ReadFile(filepath.Join(from, name))
				if e != nil || !bytes.Equal(got, content) {
					t.Fatalf("cancel damaged source: %v", e)
				}
				entries, e := os.ReadDir(to)
				if e != nil || len(entries) != 0 {
					t.Fatalf("cancel retained partial output: %v %v", entries, e)
				}
			} else {
				if err != nil {
					t.Fatal(err)
				}
				got, e := os.ReadFile(filepath.Join(to, name))
				if e != nil || !bytes.Equal(got, content) {
					t.Fatalf("copied content mismatch: %v", e)
				}
				if _, e = os.Stat(filepath.Join(from, name)); !os.IsNotExist(e) {
					t.Fatalf("source not removed: %v", e)
				}
				if maximumBytes != int64(len(content)) {
					t.Fatalf("progress=%d want %d", maximumBytes, len(content))
				}
			}
		})
	}
}

func TestMusicSizedAtomicCopyAndCancellation(t *testing.T) {
	content := bytes.Repeat([]byte("audio fixture\n"), (32<<20)/len("audio fixture\n"))
	for _, cancelDuringCopy := range []bool{false, true} {
		t.Run(map[bool]string{false: "complete", true: "cancel"}[cancelDuringCopy], func(t *testing.T) {
			base := t.TempDir()
			source := filepath.Join(base, "source")
			destination := filepath.Join(base, "destination")
			if err := os.WriteFile(source, content, 0600); err != nil {
				t.Fatal(err)
			}
			info, err := os.Stat(source)
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			var copied int64
			err = copyFileAtomic(testRootedIO(t), ctx, source, destination, 0600, info.ModTime(), func(_ string, delta int64) {
				copied += delta
				if cancelDuringCopy && copied >= 2*copyWritebackWindow {
					cancel()
				}
			})
			if cancelDuringCopy {
				if !errors.Is(err, context.Canceled) {
					t.Fatalf("cancelled copy=%v", err)
				}
				entries, e := os.ReadDir(base)
				if e != nil || len(entries) != 1 || entries[0].Name() != "source" {
					t.Fatalf("partial output retained: %v %v", entries, e)
				}
			} else {
				if err != nil {
					t.Fatal(err)
				}
				got, e := os.ReadFile(destination)
				if e != nil || !bytes.Equal(got, content) {
					t.Fatalf("destination mismatch: %v", e)
				}
			}
			got, e := os.ReadFile(source)
			if e != nil || !bytes.Equal(got, content) {
				t.Fatalf("source changed: %v", e)
			}
		})
	}
}
