# Thumbnail grouping for delete and move efficiency

Status: implemented. The historical design and measurements below describe the cache lifecycle introduced in the 1.2 series.

## Current cost and immediate change

Image blobs remain flat `<source-version-hash>.thumb` files. A disposable SQLite location index now groups references by directory. Previously, the version key included the resolved path and immediate cleanup walked the source tree, opened regular files to detect media, reconstructed their keys and queried the cache. This made work proportional to all source files, including files without thumbnails.

Same-filesystem folder moves and renames can reuse unchanged descendant thumbnails without decoding them again. Successful deletion detaches known cached references and schedules background reclamation; unindexed or uncertain entries retain ordinary bounded-cache eviction. Every thumbnail request still opens and authorizes the actual source before looking up the blob. File operations do not scan source contents for thumbnails or depend on index availability.

## Implemented representation

Image blobs remain in the existing bounded shared store, with a cache-only location index:

- A stable broker-owned namespace identifies each authorized location tree and its owner/authority context. The current worker capability authorizes access to that namespace; it must not become the persistent namespace identity. Normal and elevated workers need distinct authority associations, including the originating account for elevated workers, and configured-root changes must invalidate obsolete associations.
- Directory nodes record an opaque parent ID and a hash of the exact directory name. Nodes are created lazily when a thumbnail is stored. Ordinary files without thumbnails never acquire index entries.
- File references associate a node and exact-name hash with a source-version key and thumbnail variant. Separate blob keys allow sharing generated image bytes between authorized users without sharing mutable location ownership.
- Source-version identity is independent of the parent path on ext4, Btrfs, XFS and tmpfs, retaining device, inode, size, modification/change times, mode, output dimensions, output format and generator version. Other filesystem types retain a path-dependent identity. Direct file renames can change change time and therefore regenerate. Version `thumbnail-v5` lets old flat-cache entries expire within the existing blob budget without scanning source roots.

This indexes disposable cache artifacts only. It is not a file catalogue, does not maintain directory listings, does not scan configured roots and is never an authorization boundary. Browsing and all file actions remain direct IO in the user's worker.

## Operations

**Delete:** after a whole subtree has actually been removed, detach the affected location node and enqueue cleanup by its immutable node ID/generation. A cancelled or failed partial deletion must preserve references for surviving entries; leaving obsolete references for bounded eviction is an acceptable fallback. Reclaim only cached references under the detached node. Blob garbage collection and ordinary capacity eviction run independently of source deletion. The amount of cleanup work depends on cached entries, not total source files.

**Same-filesystem folder move:** atomically update the cached node's parent/name association after a confirmed whole-folder rename within one namespace. Path-independent source-version keys preserve unchanged descendant thumbnails without reading those descendants. A move between configured-root namespaces retires the source references and can reuse unchanged blobs when the destination is next requested; it does not rewrite every descendant's namespace.

**Rename and individual-file move:** cover the separate Rename endpoint as well as background Move. A direct file rename can change the file's change time even when its content is unchanged, so removing the path from the key alone does not guarantee reuse. Keep change-time validation; regenerate by default when the version changes. Do not drop change time just to make a rename appear unchanged.

**Merge, replace and partial move:** reparent a whole node only after a successful whole-subtree rename into an absent destination. A merge, skipped conflict, replacement or cancelled copy-delete can leave entries at both locations. Update only verified completed references, or use on-demand regeneration and bounded eviction. Explicitly invalidate replaced destination generations rather than attaching old destination references to new files.

**Cross-filesystem move:** inode identity changes. Default to on-demand regeneration; do not blindly reuse stale keys. A future transfer can explicitly carry known derived-cache references alongside verified completed file copies if measurements justify that complexity.

**External filesystem changes:** stale references are harmless cache misses. Continue validating the actual source before lookup. Watch events invalidate the browser view; they do not maintain an authoritative cache-backed model of the filesystem.

## Correctness requirements

- Never accept raw source paths or general file operations at the broker's cache interface. Keep operations limited to opaque cache identifiers and validated namespace associations.
- Authorize mutations by namespace ownership, not knowledge of a hash. One user's delete must not remove another user's location references. Shared blobs may be reclaimed only when appropriate for other references/eviction.
- Bind delayed cleanup to the old node ID/generation. Deleting and recreating a folder at the same path must not let old cleanup erase new thumbnails.
- Make directory-node moves atomic and revision-checked so delayed work cannot overwrite a newer move, delete or recreation.
- Bind thumbnail publication to the namespace/node generation captured for the request. Reject a delayed reference insertion after that generation is detached or moved, even if the image blob was successfully written. A later authorized request can attach the blob to a freshly validated location.
- Bound index entries, retained versions, empty ancestor nodes, tombstones and cleanup queues as well as image bytes. Evicting a blob must leave at most bounded disposable references, and cleanup must not hold a global cache lock throughout a large subtree walk.
- Treat device/inode identities as filesystem-scoped and potentially reusable. Root remounts, inode reuse, hard links and unreliable network-filesystem metadata must cause conservative misses where identity cannot be established reliably.
- Preserve exact Linux filenames and slash boundaries; do not use unescaped wildcard matching for subtree cleanup.
- Cache/index failures must not prevent source deletion or move. Cache corruption is recoverable by discarding derived metadata and regenerating.
- Keep old flat-cache data readable during migration or let it expire under the existing byte budget. Do not scan source roots to populate the new index.

## Validation coverage and limits

Validation covers source authorization and invalidation, normal/elevated ownership, capability rotation and broker restart, shared blobs, cache failure/timeouts, cancelled deletion and resume, repeated folder moves, merge/skip/replace decisions, same- and cross-root moves, native-WSL/tmpfs cross-filesystem moves, delete/recreate during delayed cleanup, generation during source changes, metadata bounds, corrupted metadata and interrupted cleanup across restart. Physical two-disk workloads and network filesystems remain unmeasured. Process restart and corruption tests exercise recoverable metadata; this is not a power-loss certification.

## Storage and operation limits

The broker lazily opens `locations.sqlite` inside its cache directory. It receives only configured-root fingerprints, hashed exact filename components, cache keys, node IDs and revisions. Stable namespace ownership is derived from the originating UID/account, normal/elevated authority and configured-root fingerprint; temporary capabilities authorize those associations without determining their persistent identity. The broker performs no source filesystem IO.

Namespace epochs fence late generation, while a separate publication serial makes overlapping operations conservative: a newly attached reference causes an older cleanup/reparenting snapshot to be skipped. That may retain stale references until eviction, but does not erase new ones. Cache requests made by filesystem operations have a 100 ms context deadline and failures are ignored. Capture/apply calls are batched for selected items, never issued for every descendant file.

The index is limited to between 1,024 and 100,000 nodes according to the configured blob budget, four thumbnail variants per file node, 256 namespaces and 128 hashed path components. Exceeding metadata quotas discards disposable index rows; image blobs remain under the independent byte budget. The SQLite main database is capped at 64 MiB, with WAL checkpointing and a 4 MiB retained-journal target. Request bodies are limited to 1 MiB and 2,000 locations/changes. These bounds include stale nodes and cleanup work. Cleanup processes at most 32 detached nodes per batch, propagating detachment to their cached children; it never holds the index lock for an entire large subtree walk.

Blob garbage collection preserves blobs referenced by any namespace. Move retirement keeps blobs available for on-demand reuse; deletion can reclaim blobs after the last indexed reference disappears. Legacy, superseded or uncertain blobs retain ordinary capacity eviction. The existing flat-store prune scan remains unchanged and is a separate potential optimization.

## Measurements

Native WSL on the same machine, three runs per scenario, median values below. The baseline is commit `4c23259` in an isolated native directory without Git metadata. Both baseline and candidate use the same benchmark fixture and the broker's actual Unix-socket cache protocol. Fixture creation and initial thumbnail generation are excluded. These are filesystem-service timings, without durable task persistence; delete timing excludes asynchronous thumbnail reclamation.

| Scenario | Baseline | Directory index | Result |
| --- | ---: | ---: | --- |
| Rename a folder with 5,000 text files and one cached image | 112.87 ms | 0.91 ms | About 124× faster; no thumbnail discovery walk |
| Rename a folder with 5,000 text files and a cold cache | 110.95 ms | 8.75 ms | About 12.7× faster, including lazy index creation |
| Move a folder and reopen 32 cached image thumbnails | 191.72 ms | 17.85 ms | About 10.7× faster; no image regeneration |
| Move a folder and reopen 256 cached image thumbnails | 1,715.39 ms | 137.12 ms | About 12.5× faster; no image regeneration |
| Delete a folder with 5,000 text files and one cached image | 60.31 ms | 61.47 ms | Similar source-delete time, with additional cache bookkeeping |

An in-process cache microbenchmark measured a warm blob lookup at 10 µs without location tracking and 100 µs with tracking. Atomic blob publication plus a new file reference measured 1.33 ms versus 1.71 ms, adding about 0.39 ms. These component measurements exclude source inspection, image decoding and IPC. Existing references skip redundant reference publication on warm hits. The added bookkeeping is a tradeoff for avoiding source discovery walks and destination regeneration; it does not make every individual thumbnail request cheaper.

## Initial code-validation findings, 2026-10-16

The initial review found the following issues, which informed the implementation above:

- `Service.Rename` previously called `collectCachedThumbnails`, walking folder descendants. This discovery walk is now removed, including the separate Rename endpoint.
- Cache capabilities are issued per worker and revoked on replacement or elevation shutdown. Persistent directory ownership is now separate from those temporary tokens, and location requests enforce broker-issued root/owner associations as well as peer UID and capability checks.
- A native-WSL filesystem probe preserved a descendant file's device, inode, size, modification time and change time when its parent directory was renamed. Renaming the file itself changed its change time. This supports folder-descendant reuse on the tested filesystem, with conservative fallback for other cases.
- Delete can stop after removing only some children, and move supports conflicts, merging and cross-filesystem copy-delete. Whole-node cleanup/reparenting must be conditional on the actual operation outcome.
- Generation can overlap file operations. Node revisions must guard new reference publication as well as delayed cleanup, and index metadata needs an independent size/lifetime bound.
- The existing blob store enumerates and sorts the flat cache on every write in `pruneLocked`. Directory grouping does not itself remove that generation cost. Measure it separately before claiming an improvement in thumbnail generation speed.

New index and filesystem integration tests cover ownership, recovery, overlapping mutations and the implemented lifecycle behavior. The unchanged-descendant reuse regression fails on the baseline because the destination creates a second thumbnail entry, and passes on the implementation.

The initial broader thumbnail run exposed a synchronization race in `TestVideoThumbnailQueueCoalescesChangedSourceAndDismissesFailure`. The test's queue mutations now use the queue mutex, and its repeated race-enabled validation passes. This changes test synchronization only; it does not suppress race detection.

## Final validation

- Full backend tests passed with `go test -race -count=1 ./...`; development vet and production/PAM tests and vet passed.
- Index, filesystem integration and the repaired queue test passed ten race-enabled repetitions. A separate regression first reproduced Rename continuing after cancellation during cache capture; the added cancellation check passes the regression.
- Full Chromium validation passed all 168 tests. Full WebKit validation passed 157 tests with 11 expected skips. Both mounted-path smoke checks and frontend builds passed. The final cancellation guard and cache batch-loop refinement were additionally covered by the final backend rerun and the live browser build.
- In the Codex browser at 1310×1244, three generated PNG thumbnails remained visible after UI Rename and Move actions. Their blob filenames were unchanged. Deleting that generated gallery through the local API completed with four deleted entries; background cleanup left zero references and zero blobs. The live destination view became empty without a manual refresh. Only the two generated task-history entries were removed, and the temporary server was stopped.
- The previously committed work at `4c23259` passed local production/package and both complete browser gates before pushing. Its nine CI jobs are green after inspecting and retrying one WebKit elevation-click failure once. The thumbnail-index implementation remains local and has not been pushed or released.

Raw benchmark and validation logs are retained in the native WSL mirror under `$HOME/code/webfile/dist/thumbnail-index/`. Fixture preparation is excluded from measurements. The benchmark and test sources are checked into the authoritative Windows repository with the implementation.
