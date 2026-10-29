# WebFile 1.2.6

WebFile 1.2.6 improves large-folder transfers and deletion, uploads, folder-size freshness, thumbnail caching, gallery resource use and archive handling.

## Fixes

- Large transfers reduce task-history and browser-update overhead, reuse safe same-filesystem moves across configured roots, and overlap independent small-file copies with bounded concurrency. Per-file synchronisation, conflict review and no-overwrite protections remain in place. Task details use the main copying/moving description without rapidly changing redundant phase text.
- Large-folder deletion streams entries without reading their contents or discovering thumbnails first. Tasks show an indeterminate bar while counting, followed by counted percentage progress, and remain cancellable during both stages.
- Thumbnail responses prevent browser caching. A disposable directory grouping index supports cache cleanup and moves without building a source filesystem catalogue or scanning source contents.
- Cross-disk upload publication releases the global upload lock, keeping other uploads and cancellation responsive. Fallback copies also observe cancellation and remove incomplete output.
- Failed uploads finish their task instead of remaining Running, while retaining resumable data. Late errors cannot overwrite a newer attempt or cancellation. Batch uploads coalesce folder refreshes.
- Abandoned upload staging expires after seven days without activity. Removing failed or interrupted upload history discards its partial bytes and checkpoint; active writers are preserved during expiry cleanup.
- Folder-size scans use a bounded queue and expiring viewer leases. The backend records scan times and directory metadata, invalidates cached ancestors after known changes, and refreshes previously measured folders when their results expire. Previously unscanned folders are not recursively scanned during browsing. Cached sizes in open listings and Details use a five-minute freshness limit, including a fallback for external nested changes outside active watches.
- Thumbnail cache writes maintain size and LRU bookkeeping incrementally instead of enumerating and sorting every cached blob on each write. Failed video-thumbnail history is bounded and expires, and retries respect queue capacity.
- Closing the gallery aborts obsolete content-type requests. Original-image preloading retains only the current image and up to five neighbours on either side, releasing older resources as navigation proceeds.
- Archive password inspection streams bounded output instead of retaining the complete listing. Extraction validation, destination preflight and file-count walks observe cancellation while preserving rollback and staging cleanup.
- The copy/move destination picker virtualises large folder lists and scrolls newly created folders into view.

## Performance measurements

In native WSL, median thumbnail-cache write time with 10,000 existing blobs fell from 30.45 ms to 1.85 ms. A 200,000-entry archive-inspection fixture fell from 74.4 MiB to 0.05 MiB of total Go allocations. These local measurements do not establish performance on physical or network disks.

## Release validation

The release candidate runs backend tests and vet, production/PAM coverage, real 7-Zip integration tests, frontend build and dependency audit, source and binary vulnerability scans, updater and package-provenance tests, and complete Chromium and WebKit suites with application-prefix smoke checks. Publication verifies the exact candidate commit, version and package hashes and uses the tested archive without rebuilding it.

## Deployment notes

- No configuration migration is required from 1.2.5. Restart WebFile with the new binary.
- Removing interrupted or failed upload history now also removes its resumable staging. Inactive staging expires after seven days.
- The packaged target remains Linux AMD64 with systemd on Debian or Ubuntu.
- Clean-host installation/upgrade, physical-tablet interaction and the user's physical two-disk workload remain manual validation tasks.

**Full changelog:** https://github.com/ahjephson/webfile/compare/1.2.5...1.2.6
