# Copy and move progress design

Status: implemented 2026-10-01.

## Small-file transfer update (2026-10-16)

Copy and move tasks keep their main detail message above the progress bar. The
byte-progress line shows counts and a rate when available, without repeating
the per-file copying/flushing/publishing phase. Preparation reads **Scanning**.

Per-file phases remain available internally, but they follow the ordinary
100 ms broadcast and one-second persistence cadence. They do not create task
history events on every file. Lifecycle changes, conflict review, cancellation
and completion remain immediate. Copy files still synchronize before publication
and source deletion; this update does not relax durability to match plain `cp`.

Transfers no longer walk and inspect source files to migrate thumbnail cache
entries. Unchanged same-filesystem folder descendants reuse cached blobs, with
lazy directory-reference updates described in [Thumbnail lifecycle design](thumbnail-lifecycle.md).
Other destination thumbnails are generated on demand; obsolete keys expire
through the bounded cache's normal eviction. The explicit Rename action keeps
its existing cache-migration behavior. Top-level directory moves reuse the
initial size measurement rather than scanning the same tree a second time.

Cross-root moves attempt an anchored, no-overwrite rename and fall back to
copy-and-delete on `EXDEV`. Cross-root directory rename is restricted to roots
without exclusion rules, so the optimization cannot bypass descendant filtering.

The benchmark `BenchmarkSmallFileMove` compares 1,000 small files through the
persistent task manager with `cp -r` plus deletion, both with and without a
final filesystem sync. It uses tmpfs and the native temporary directory to
force different filesystems; actual two-disk measurements are still required
to tune the small-file pipeline or consider a broader flush-batching design.

Small-file copies now use at most four concurrent workers for independent
regular files up to 64 KiB with absent destinations. This overlaps writes and
the existing per-file syncs. Same-filesystem rename moves, directories, large
files and conflict decisions remain sequential. A group drains fully before
reviewing a conflict or returning; successfully completed items are retained,
failed/conflicting items remain queued, and no worker deletes its source before
its own destination is synchronized and safely published.

## Goals

- Make copy and move progress understandable in the same way as a desktop file
  manager, including a current transfer rate as well as bytes and percentage.
- Distinguish active byte transfer from preparation, filesystem flushes,
  publication, and cleanup so an operation does not appear to have silently
  stalled.
- Preserve cancellation, no-overwrite behaviour, atomic publication where the
  destination filesystem supports it, and durable task history.

## Previous behaviour

WebFile measures the complete source tree before copying so it can report total
bytes and items. It then copies each regular file through Go using a 1 MiB
buffer into a temporary file in the destination directory. Byte progress is
reported after each successful write and task updates are broadcast over the
WebSocket at most every 100 milliseconds.

After writing a file, WebFile synchronizes the temporary file and publishes it
with a hard link to retain no-overwrite semantics without exposing partial
output. If the filesystem does not support hard links, it falls back to copying
the completed temporary file into an exclusively created destination file.

Several parts of this process currently advance neither the byte counter nor a
visible phase:

- measuring a large source tree;
- synchronizing file data to storage;
- the fallback copy from the completed temporary file;
- creating directories and applying permissions and timestamps;
- deleting the source after a cross-filesystem move.

The fallback publication path is particularly important: it can physically
write a large file a second time after logical byte progress has already
reached the file size.

## Progress payload

The backend should calculate transfer speed and include it in live operation
updates. The operation payload should add an optional field expressed in raw
bytes per second:

```json
{
  "bytes_done": 1524629504,
  "bytes_total": 8589934592,
  "bytes_per_second": 90492134
}
```

`bytes_per_second` should be absent or `null` until enough samples exist and
whenever the operation is not actively transferring logical file data. It is
live telemetry and should not be persisted in task history.

The backend is the authority for the rate because it owns the byte counter and
timing, is unaffected by browser timer throttling or suspended tabs, and can
provide consistent values to every connected client.

## Rate calculation

- Record samples from monotonic elapsed time and cumulative logical bytes.
- Calculate a rolling average over approximately two to three seconds rather
  than displaying the most recent chunk's instantaneous rate.
- Do not count directory metadata or repeated physical writes in logical
  `bytes_done` or `bytes_total`.
- Reset the estimator when an operation changes phase or byte progress moves
  backwards as part of error recovery.
- While a transfer is active but no bytes arrive, emit a low-frequency update,
  approximately once per second, so a stale rate can expire.
- Prefer a phase label to `0 B/s` when the operation is deliberately scanning,
  flushing, publishing, or removing source data.

## Operation phases

Copy and move tasks should expose enough phase detail to explain pauses:

- `scanning`: measure the source tree and validate destinations;
- `copying`: read source data and write destination temporary files;
- `flushing`: synchronize a completed temporary file to storage;
- `publishing`: expose the completed file at its final destination;
- `removing_source`: remove source entries for a cross-filesystem move;
- `completed`: the operation and any required cleanup have finished.

A same-filesystem move that succeeds through an atomic rename may move directly
from scanning to completed because no byte transfer occurs.

## User interface

For an active byte transfer, show logical progress and the smoothed rate on one
line, for example:

```text
1.42 GiB / 8.00 GiB · 86.3 MiB/s
```

Use the existing IEC byte formatting convention: `B/s`, `KiB/s`, `MiB/s`,
`GiB/s`, and `TiB/s`. Continue showing the progress bar and percentage derived
from logical bytes. When there is no meaningful speed, show the current phase
instead of a misleading zero rate.

## Copy engine

Linux copies first use `copy_file_range` calls of up to 4 MiB so compatible
local filesystems can keep data movement inside the kernel. Each completed
chunk updates cancellation and progress normally. If the kernel or either
filesystem does not support the operation, copying resumes from the current
file offsets through the portable buffered implementation without restarting
or double-counting the transfer. A future advanced per-user setting may select
1 MiB, 4 MiB, 16 MiB, or 64 MiB; 4 MiB remains the default.

Temporary files are published through atomic no-replace rename where supported.
The fallback retains exclusive no-overwrite behaviour, and any slower
publication work is exposed through the publishing phase rather than appearing
to be a completed or frozen task.

## Validation

- Unit-test rolling-rate calculation, sample expiry, phase resets, and clock
  edge cases with a controllable clock.
- Cover ordinary copy, same-filesystem move, cross-filesystem move, many small
  files, slow writes, cancellation, and publication fallback behaviour.
- Verify WebSocket payload compatibility for clients that do not know the new
  optional field.
- Add browser coverage for rate formatting, phase transitions, and removal of a
  stale speed when byte progress stops.
- Visually inspect active tasks at narrow and desktop widths with both light and
  dark themes.
