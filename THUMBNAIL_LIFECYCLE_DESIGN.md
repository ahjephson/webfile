# Thumbnail grouping for delete and move efficiency

Status: proposed after the streaming-delete improvement, 2026-10-16. This design is not implemented by the delete change.

## Current cost and immediate change

The current flat cache stores `<source-version-hash>.thumb`. Its version hash includes the resolved source path, so the cache cannot enumerate a folder's thumbnails directly. Immediate cleanup previously walked the entire source tree, opened regular files to detect media, reconstructed their keys and queried the cache. This made work proportional to all source files, including files without thumbnails.

Transfers now regenerate destination thumbnails on demand. Streaming deletion also leaves old cache entries to normal bounded-cache eviction, without a source scan or cache calls. Every thumbnail request still opens and authorizes the real source first, so retained cache entries cannot serve a deleted or inaccessible file. This trades immediate space reclamation for faster file operations.

## Proposed representation

Keep image blobs in the existing bounded shared store, and add a cache-only location index:

- A stable broker-owned namespace identifies each authorized location tree and its owner/authority context. The current worker capability authorizes access to that namespace; it must not become the persistent namespace identity. Normal and elevated workers need distinct authority associations, including the originating account for elevated workers, and configured-root changes must invalidate obsolete associations.
- Directory nodes record an opaque parent ID and a hash of the exact directory name. Nodes are created lazily when a thumbnail is stored. Ordinary files without thumbnails never acquire index entries.
- File references associate a node and exact-name hash with a source-version key and thumbnail variant. Separate blob keys allow sharing generated image bytes between authorized users without sharing mutable location ownership.
- Source-version identity should be independent of the parent path when device, inode and change metadata are reliable. Retain size, timestamps, output dimensions, media format and generator version. Fall back to a path-dependent identity where stable filesystem identity is unavailable.

This indexes disposable cache artifacts only. It is not a file catalogue, does not maintain directory listings, does not scan configured roots and is never an authorization boundary. Browsing and all file actions remain direct IO in the user's worker.

## Operations

**Delete:** after a whole subtree has actually been removed, detach the affected location node and enqueue cleanup by its immutable node ID/generation. A cancelled or failed partial deletion must preserve references for surviving entries; leaving obsolete references for bounded eviction is an acceptable fallback. Reclaim only cached references under the detached node. Blob garbage collection and ordinary capacity eviction run independently of source deletion. The amount of cleanup work depends on cached entries, not total source files.

**Same-filesystem folder move:** update the cached node's parent/name association when possible. A path-independent source-version key can preserve unchanged descendant thumbnails without reading those descendants. Resolve both source and destination namespaces through the worker's existing authority.

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

## Validation before implementation is enabled

Measure folders with many non-media files, many cached media files, cold caches and externally changed files. Test same- and cross-root moves, namespace access, shared blobs, failed cache cleanup, cancellation, crashes, rapid delete/recreate and delayed cleanup after a subsequent move. Recheck that requests always authorize the current source and that file operations never depend on index availability. Compare the added index-write cost during generation against the saved delete/move work before selecting a storage schema.

## Code-validation findings, 2026-10-16

The grouping approach fits the existing worker/broker split, but its implementation and performance remain unvalidated. The requirements above incorporate these findings from the current code:

- `Service.Rename` still calls `collectCachedThumbnails`, which walks folder descendants. Background copy/move and streaming delete no longer do this. Rename must be included in the implementation scope.
- Cache capabilities are issued per worker and revoked on replacement or elevation shutdown. Persistent directory ownership cannot be keyed only by those temporary tokens. The current cache authorizer checks peer UID and token; it does not yet enforce per-root directory namespaces.
- A native-WSL filesystem probe preserved a descendant file's device, inode, size, modification time and change time when its parent directory was renamed. Renaming the file itself changed its change time. This supports folder-descendant reuse on the tested filesystem, with conservative fallback for other cases.
- Delete can stop after removing only some children, and move supports conflicts, merging and cross-filesystem copy-delete. Whole-node cleanup/reparenting must be conditional on the actual operation outcome.
- Generation can overlap file operations. Node revisions must guard new reference publication as well as delayed cleanup, and index metadata needs an independent size/lifetime bound.
- The existing blob store enumerates and sorts the flat cache on every write in `pruneLocked`. Directory grouping does not itself remove that generation cost. Measure it separately before claiming an improvement in thumbnail generation speed.

Existing regression tests can verify today's source authorization, invalidation, cache capability checks and delete/move behavior. They cannot prove the proposed index's ownership, recovery, concurrency or performance until that index exists. No directory-index implementation is enabled by this document.

Validation outcome: the focused cache, source-version invalidation, Rename, background Move, Delete and thumbnail HTTP checks passed with Go's race detector. The broader thumbnail selection failed race detection in the existing `TestVideoThumbnailQueueCoalescesChangedSourceAndDismissesFailure`: the test mutates `queue.settling` without locking while the settler goroutine reads it under `queue.mu`. This test synchronization issue remains unresolved; the broader run is not a clean pass. This validation changed documentation only and did not run the full application suite or implement the proposed index.
