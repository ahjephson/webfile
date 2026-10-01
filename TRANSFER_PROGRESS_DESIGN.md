# Copy and move progress design

Status: planned; not yet implemented.

## Goals

- Make copy and move progress understandable in the same way as a desktop file
  manager, including a current transfer rate as well as bytes and percentage.
- Distinguish active byte transfer from preparation, filesystem flushes,
  publication, and cleanup so an operation does not appear to have silently
  stalled.
- Preserve cancellation, no-overwrite behaviour, atomic publication where the
  destination filesystem supports it, and durable task history.

## Current behaviour

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

## Copy-engine follow-up

The hard-link publication fallback should be reviewed as part of this work. It
must retain exclusive no-overwrite behaviour, but a second unreported full-file
copy should not look like a completed or frozen task. At minimum, expose it as
the publishing phase. Where supported, investigate chunked Linux
`copy_file_range` calls while retaining the current buffered implementation as
a fallback. Any optimized path must preserve cancellation, progress reporting,
cross-filesystem compatibility, and correctness on mounted or network
filesystems.

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
