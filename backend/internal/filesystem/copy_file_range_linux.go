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

// copyFileRangePlatform copies from the files' current offsets and advances
// both offsets. A false complete result asks the caller to resume with its
// portable buffered copy path from those same offsets.
func copyFileRangePlatform(ctx context.Context, source, destination *os.File, progress func(int64)) (complete bool, err error) {
	writeback, err := newCopyWriteback(destination)
	if err != nil {
		return false, err
	}
	for {
		if err := taskcontrol.Wait(ctx); err != nil {
			return false, err
		}
		n, copyErr := unix.CopyFileRange(int(source.Fd()), nil, int(destination.Fd()), nil, copyChunkSize(ctx), 0)
		if n > 0 && progress != nil {
			progress(int64(n))
		}
		if n > 0 {
			if err := writeback(ctx, int64(n)); err != nil {
				return false, err
			}
		}
		if copyErr != nil {
			if errors.Is(copyErr, unix.EINTR) {
				continue
			}
			if copyFileRangeUnsupported(copyErr) {
				return false, nil
			}
			return false, copyErr
		}
		if n == 0 {
			// A zero return should mean EOF. Verify that before declaring the
			// fast path complete because older kernels/filesystems have returned
			// a false success for unsupported copies.
			offset, seekErr := source.Seek(0, io.SeekCurrent)
			if seekErr != nil {
				return false, seekErr
			}
			info, statErr := source.Stat()
			if statErr != nil {
				return false, statErr
			}
			if offset < info.Size() {
				return false, nil
			}
			return true, nil
		}
	}
}

func copyFileRangeUnsupported(err error) bool {
	return errors.Is(err, unix.ENOSYS) ||
		errors.Is(err, unix.EXDEV) ||
		errors.Is(err, unix.EOPNOTSUPP) ||
		errors.Is(err, unix.EINVAL) ||
		errors.Is(err, unix.EPERM)
}
