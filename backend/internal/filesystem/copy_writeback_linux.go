//go:build linux

package filesystem

import (
	"context"
	"errors"
	"io"
	"os"
	"webfile/internal/taskcontrol"

	"golang.org/x/sys/unix"
)

const copyWritebackWindow = 8 << 20

var errCopyWritebackUnsupported = errors.New("range writeback unsupported")

// copyWriteback overlaps writing one range with copying the next. It bounds
// queued ranges rather than letting a whole large file accumulate in cache.
// This is only writeback scheduling: copyFileAtomic must still Sync before
// publishing output. Range writeback does not flush metadata/device caches.
type copyWriteback struct {
	copied, submitted, waited int64
	disabled                  bool
	syncRange                 func(context.Context, int64, int64, bool) error
}

func newCopyWriteback(file *os.File) (func(context.Context, int64) error, error) {
	offset, err := file.Seek(0, io.SeekCurrent)
	if err != nil {
		return nil, err
	}
	writer := &copyWriteback{copied: offset, submitted: offset, waited: offset}
	writer.syncRange = func(ctx context.Context, offset, length int64, wait bool) error {
		flags := unix.SYNC_FILE_RANGE_WRITE
		if wait {
			flags = unix.SYNC_FILE_RANGE_WAIT_BEFORE | unix.SYNC_FILE_RANGE_WAIT_AFTER
		}
		for {
			if err := taskcontrol.Wait(ctx); err != nil {
				return err
			}
			err := unix.SyncFileRange(int(file.Fd()), offset, length, flags)
			if errors.Is(err, unix.EINTR) {
				continue
			}
			if errors.Is(err, unix.ENOSYS) || errors.Is(err, unix.EOPNOTSUPP) || errors.Is(err, unix.EINVAL) || errors.Is(err, unix.EPERM) {
				return errCopyWritebackUnsupported
			}
			return err
		}
	}
	return writer.advance, nil
}

func (writer *copyWriteback) advance(ctx context.Context, bytes int64) error {
	if err := taskcontrol.Wait(ctx); err != nil {
		return err
	}
	writer.copied += bytes
	if writer.disabled || writer.copied-writer.submitted < copyWritebackWindow {
		return nil
	}
	// Keep one submitted range and one accumulating range ahead of completed
	// writeback. A range may grow to the explicitly configured copy chunk size.
	if writer.submitted > writer.waited {
		if err := writer.syncRange(ctx, writer.waited, writer.submitted-writer.waited, true); err != nil {
			return writer.handleError(err)
		}
		writer.waited = writer.submitted
	}
	if err := writer.syncRange(ctx, writer.submitted, writer.copied-writer.submitted, false); err != nil {
		return writer.handleError(err)
	}
	writer.submitted = writer.copied
	return nil
}

func (writer *copyWriteback) handleError(err error) error {
	if errors.Is(err, errCopyWritebackUnsupported) {
		// Some network/FUSE filesystems and sandbox policies do not implement
		// range writeback. Retain the ordinary copy + final Sync path there.
		writer.disabled = true
		return nil
	}
	return err
}
