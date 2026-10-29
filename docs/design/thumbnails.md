# Thumbnail cache and video queue design

Status: implemented.

For the implemented cache-only directory grouping and the current delete/move
lifecycle changes, see [Thumbnail lifecycle design](thumbnail-lifecycle.md).

## Goals

- Persist generated thumbnails across process and machine restarts.
- Reuse a generated thumbnail whenever multiple authorized users access the
  same underlying source file.
- Preserve Linux filesystem permissions as the authorization boundary.
- Bound the CPU, memory, disk, and process load caused by thumbnail creation.
- Keep ordinary image thumbnails fast and unobtrusive.
- Make slower video-thumbnail work visible without mixing it into durable file
  operation history.

## Configured shared cache

The cache directory is a system setting. WebFile must not force it into a
user's home directory or place sidecar directories such as `@eaDir` beside
source files. A location outside all configured browsing roots is preferred.

The implemented configuration shape is:

```json
"thumbnails": {
  "cache_dir": "/var/cache/webfile/thumbnails",
  "cache_socket": "/run/webfile/cache/thumbnails.sock",
  "cache_max_bytes": 10737418240,
  "image_workers": 2,
  "video_workers": 2,
  "video_ffmpeg_threads": 2,
  "video_timeout_seconds": 300,
  "max_queued": 200
}
```

These are host configuration values, not user settings. The values above are
the defaults and are included in the example configuration.

The cache is shared at the server level so that a thumbnail already generated
for one authorized user can be reused by another authorized user. Cache files
must not be directly exposed as part of a configured filesystem root.

## Authorization and lookup flow

The frontend requests a thumbnail for a known virtual path. It never supplies
or receives the internal cache key.

Every request follows this order, including cache hits:

1. Resolve the virtual path in the requesting user's worker.
2. Open and stat the actual source using that user's real filesystem identity.
3. Confirm that the source is an accessible, supported image or video.
4. Derive an internal cache key from the resolved source identity and metadata.
5. Return the cached thumbnail, or atomically get or create a generation job.

A cache hit must never bypass the source-file permission check. The source must
also be reopened and validated when queued generation starts. If its metadata
changes during generation, discard the output rather than publishing it under
the old key.

The key should include enough source identity to avoid collisions between
different files exposed under the same virtual path. Relevant inputs include:

- resolved root/source identity;
- device and inode where reliable;
- size, modification time, and change time;
- requested dimensions and output format; and
- a thumbnail-generator format/version identifier.

The key is hashed for the on-disk filename. Cache publication uses a temporary
file in the cache directory followed by an atomic rename. A bounded cache is
pruned by size and optionally age; superseded source versions naturally become
unreachable because their metadata produces a different key.

The broker owns the cache directory and exposes a narrow local Unix-socket
cache interface. Each worker connection is authorized using both the
kernel-reported peer UID and a per-worker capability issued by the broker.
User workers perform source authorization and generation; the cache accepts
only validated hashed keys and supported derived image formats. It never reads
source paths or source bytes.

## Atomic generation and deduplication

Generation is deduplicated by final cache key:

```text
cache key
  cached       -> return the result
  generating   -> attach to the existing job
  absent       -> create exactly one job
```

Concurrent requests for the same key must never launch duplicate FFmpeg
processes. Request cancellation detaches that requester but does not normally
cancel generation that has already started, because the completed result will
remain useful in the persistent cache.

Completed cache data is durable. Queue state is intentionally in memory: after
a restart, missing results are safely requested and generated again.

## Ordinary image thumbnails

Ordinary image thumbnails remain on a fast, bounded path. They do not create
visible queue entries, WebSocket events, task badges, or history records.

Image and video generation have separate concurrency limits so that slow video
decoding cannot prevent inexpensive image thumbnails from completing. Normal
images can continue to be generated synchronously unless their cost later
justifies changing that behavior.

## Video-thumbnail queue

Video thumbnails use a separate in-memory scheduler rather than the durable
copy, move, upload, delete, and extraction task queue. Video-thumbnail jobs are
derived cache maintenance: they are high-volume, deduplicated, reprioritizable,
and safe to discard or regenerate. They must not delay user-initiated file
operations or pollute durable task history.

Jobs are request-driven. Opening a folder does not enumerate and enqueue every
video. Work is admitted for:

1. a video explicitly selected or opened by the user;
2. video tiles visible in the current viewport; and
3. optionally, a small look-ahead set.

These groups are also the priority order. FIFO ordering is used within a
priority. An existing queued job can be promoted when a user opens that video.
Queued jobs with no remaining interested clients may be demoted or removed
before they start. Once running, a job normally finishes and populates the
cache.

The queue has a finite pending limit and a configurable worker count. FFmpeg's
thread count and a per-job wall-clock timeout are also bounded; limiting the
number of processes alone does not prevent two multithreaded FFmpeg processes
from saturating the server. Failures receive a short retry backoff so a corrupt
or unsupported video cannot cause a tight retry loop.

The existing FFmpeg sampling strategy may decode through most of a long video.
Timestamp-based seeking should be evaluated separately so the queue does not
merely hide unnecessarily expensive generation.

Thumbnail scheduling may reuse generic queue/state primitives, but it remains
a separate queue. A lightweight shared resource governor can prevent it from
competing excessively with user operations: foreground file tasks take
priority, and the thumbnail scheduler can pause admission of new video jobs
while a heavy foreground task is active.

## Frontend protocol

Cached requests return the thumbnail normally. A video cache miss or existing
job returns HTTP 202 with the current job state instead of holding an image
request open. The frontend keeps its placeholder and retries the endpoint;
queue state is also sent over the existing WebSocket for the Tasks UI. Once
ready, the endpoint returns the cached image.

The protocol distinguishes queued, probing, generating, publishing, ready,
failed, and cancelled states without exposing internal cache paths or keys.

## Queue visibility

The Tasks drawer gains a transient **Video thumbnails** section. It is hidden
when idle and remains distinct from durable file-operation tasks.

The section shows an aggregate such as `2 generating, 8 queued` and can list:

- filename or authorized virtual path;
- `Probing`, `Queued`, `Generating`, `Publishing`, or `Failed` state;
- queue position for waiting jobs;
- elapsed time and, where reliable, FFmpeg progress; and
- retry or cancellation controls.

Successful entries disappear shortly after completion and are not added to
task history. Failures can remain visible while the relevant folder is open.
Users may cancel an individual job or clear queued work without deleting
completed cache entries. The Tasks button can show aggregate video activity,
but its presentation must remain visually distinct from active file tasks.

Queue snapshots are fetched when the drawer opens and updated through the
existing WebSocket connection. In a multi-user deployment, a session sees only
jobs associated with source paths it requested and is authorized to access,
even when the underlying cache entry or generation job is shared.
