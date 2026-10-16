# Thumbnail grouping for delete and move efficiency

Status: proposed after the streaming-delete improvement, 2026-10-16. This design
is not implemented by the delete change.

## Current cost and immediate change

The current flat cache stores `<source-version-hash>.thumb`. Its version hash
includes the resolved source path, so the cache cannot enumerate a folder's
thumbnails directly. Immediate cleanup previously walked the entire source
tree, opened regular files to detect media, reconstructed their keys and queried
the cache. This made work proportional to all source files, including files
without thumbnails.

Transfers now regenerate destination thumbnails on demand. Streaming deletion
also leaves old cache entries to normal bounded-cache eviction, without a source
scan or cache calls. Every thumbnail request still opens and authorizes the real
source first, so retained cache entries cannot serve a deleted or inaccessible
file. This trades immediate space reclamation for faster file operations.

## Proposed representation

Keep image blobs in the existing bounded shared store, and add a cache-only
location index:

- A broker-issued namespace bound to the worker's UID, capability and configured
  root identifies each authorized location tree.
- Directory nodes record an opaque parent ID and a hash of the exact directory
  name. Nodes are created lazily when a thumbnail is stored. Ordinary files
  without thumbnails never acquire index entries.
- File references associate a node and exact-name hash with a source-version
  key and thumbnail variant. Separate blob keys allow sharing generated image
  bytes between authorized users without sharing mutable location ownership.
- Source-version identity should be independent of the parent path when device,
  inode and change metadata are reliable. Retain size, timestamps, output
  dimensions, media format and generator version. Fall back to a path-dependent
  identity where stable filesystem identity is unavailable.

This indexes disposable cache artifacts only. It is not a file catalogue, does
not maintain directory listings, does not scan configured roots and is never
an authorization boundary. Browsing and all file actions remain direct IO in
the user's worker.

## Operations

**Delete:** detach the affected location node and enqueue cleanup by its immutable
node ID/generation. Reclaim only cached references under that node. Blob garbage
collection and ordinary capacity eviction run independently of source deletion.
The amount of cleanup work depends on cached entries, not total source files.

**Same-filesystem folder move:** update the cached node's parent/name association
when possible. A path-independent source-version key can preserve unchanged
descendant thumbnails without reading those descendants. Resolve both source
and destination namespaces through the worker's existing authority.

**Cross-filesystem move:** inode identity changes. Default to on-demand
regeneration; do not blindly reuse stale keys. A future transfer can explicitly
carry known derived-cache references alongside verified completed file copies
if measurements justify that complexity.

**External filesystem changes:** stale references are harmless cache misses.
Continue validating the actual source before lookup. Watch events invalidate
the browser view; they do not maintain an authoritative cache-backed model of
the filesystem.

## Correctness requirements

- Never accept raw source paths or general file operations at the broker's
  cache interface. Keep operations limited to opaque cache identifiers and
  validated namespace associations.
- Authorize mutations by namespace ownership, not knowledge of a hash. One
  user's delete must not remove another user's location references. Shared
  blobs may be reclaimed only when appropriate for other references/eviction.
- Bind delayed cleanup to the old node ID/generation. Deleting and recreating
  a folder at the same path must not let old cleanup erase new thumbnails.
- Make directory-node moves atomic and revision-checked so delayed work cannot
  overwrite a newer move, delete or recreation.
- Preserve exact Linux filenames and slash boundaries; do not use unescaped
  wildcard matching for subtree cleanup.
- Cache/index failures must not prevent source deletion or move. Cache
  corruption is recoverable by discarding derived metadata and regenerating.
- Keep old flat-cache data readable during migration or let it expire under
  the existing byte budget. Do not scan source roots to populate the new index.

## Validation before implementation is enabled

Measure folders with many non-media files, many cached media files, cold caches
and externally changed files. Test same- and cross-root moves, namespace access,
shared blobs, failed cache cleanup, cancellation, crashes, rapid delete/recreate
and delayed cleanup after a subsequent move. Recheck that requests always
authorize the current source and that file operations never depend on index
availability. Compare the added index-write cost during generation against the
saved delete/move work before selecting a storage schema.
