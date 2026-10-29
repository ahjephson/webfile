# Historical development notes

These notes preserve earlier investigations and measurements. They are not a
list of outstanding bugs. Use the [feature backlog](../feature-backlog.md)
for pending work and the [published releases](https://github.com/ahjephson/webfile/releases)
for delivered changes.

## Remaining-action performance audit fixes (2026-10-16)

| Item | Priority | Fix |
| --- | --- | --- |
| 1 | P1 | Release the global upload lock during publication, including cross-disk copying; keep one writer per upload and propagate cancellation through fallback copies. |
| 2 | P2 | Finish stopped uploads as failed while preserving resumable staging. Late failures cannot overwrite cancellation or a newer attempt. |
| 3 | P2 | Coalesce batch-upload folder reads and terminal notifications into a final refresh. |
| 4 | P2 | Expire inactive upload staging after seven days, clean orphan payloads, preserve active writers, and discard staged data when its terminal task history is removed. Cleanup errors retain history for retry. |
| 5 | P2 | Bound folder-size work to 32 jobs and two IO slots; cancel abandoned viewer leases and shut down queued scans. Store scan times and directory metadata in the backend, invalidate ancestors after writes/transfers/deletes/uploads/extraction and watcher events, and rescan previously measured stale folders when listed. Unwatched external nested edits use the five-minute fallback. No scan timestamp is displayed. |
| 6 | P2 | Rebuild thumbnail blob size/LRU bookkeeping once at startup, then maintain it incrementally; ordinary writes no longer enumerate and sort the cache directory. |
| 7 | P2 | Abort gallery content-type detection when the gallery closes or its inputs change. |
| 8 | P2 | Retain at most the current original and five neighbours on either side; release obsolete images and settle cancelled load promises. |
| 9 | P2 | Limit failed video-thumbnail history to 50 jobs and expire it after five minutes, swept every 30 seconds while idle. Failed-job retries respect queue capacity. |
| 10 | P2 | Stream archive-inspection flags with bounded records and diagnostics instead of retaining and copying the entire listing. |
| 11 | P2 | Check cancellation during extraction validation, exclusion checks, destination preflight and publication counting; rollback and private-stage cleanup still complete. |
| 12 | P2 | Virtualise destination folders and scroll newly created folders into view. |

Regressions reproduced the upload-lock blockage, stopped tasks remaining active, per-file upload refreshes, abandoned staging, queued folder scans, gallery requests continuing after close, retained originals, unbounded failed thumbnail history, whole-list archive allocations, cancelled publication checks and 2,000 destination buttons. The existing Details race tests now accommodate aborted HTTP requests while retaining their stale-response assertions.

Native-WSL measurements: with 10,000 cached blobs, median warm thumbnail write time fell from 30.45 ms to 1.85 ms (three runs of 20 writes). Allocations fell from about 60,057 to 18 per write; with an empty cache, the new path measured 1.64 ms. The 200,000-entry archive-inspection fixture fell from 74.4 MiB of total Go allocations to 0.05 MiB. These are local cache/inspection measurements, not physical two-disk transfer measurements.

Visual checks in the Codex browser: an externally added file changed a previously measured folder from 7 B to 17 B after a listing refresh with Details closed; the destination picker rendered a small window of 2,001 folders and selected folder 1,999; the gallery navigated from the first image to the sixteenth, back again and closed cleanly. All fixtures were isolated development data.

Validation: full backend race tests, vet and production/PAM tests and vet passed. Browser coverage passed all 175 Chromium cases across the full run and focused follow-ups, and the full WebKit run passed 164 cases with 11 expected skips. The final folder-size/cancellation group passed all 14 cases in both browsers, with frontend builds and both mounted-path smoke checks. Changes are committed locally; no push or release was requested for this work.

## Large-folder delete work

- [x] Remove the thumbnail discovery/content-reading pass before recursive deletion; let old derived cache entries expire normally until grouped cleanup is available.
- [x] Stream directory names in bounded batches, unlink relative to open directory descriptors, never follow child symlinks and check cancellation between entries.
- [x] Report actual deleted child entries; retain completed deletions on cancellation.
- [x] Show an indeterminate bar while counting directory entries, then switch to a percentage bar with deleted/total items. Counting is cancellable and skips file contents and thumbnails; unknown-total tasks retain the animated fallback, including cancellation and reduced motion.
- [x] Validate cancellation/resume, symlinks, parent replacement, many-child folders and end-to-end task counts; benchmark against the original delete path and OS deletion.
- [x] Investigate and implement cache-only directory grouping. Lazy broker-owned nodes, stable ownership, revision-checked moves and bounded background delete cleanup are documented with measurements in [Thumbnail lifecycle design](../design/thumbnail-lifecycle.md). Source filesystem scanning is not used to populate the index.

Implementation and regression coverage were completed before tests ran. The
original-code regressions failed as expected for thumbnail-cache access and
top-level-only task counts. The final delete regressions passed 10 race-enabled
runs; full backend race tests, vet, production/PAM tests and vet passed. Both
browsers passed seven task checks and the mounted-path smoke check; frontend
builds passed.

Local deletion benchmark: 5,000 small text files in native WSL, fixture creation
excluded, three runs per method. The original WebFile median was 168 ms; the new
streaming delete median was 59 ms (about 2.9x faster). OS `rm` measured about
59–60 ms. This compares the filesystem service paths without task persistence;
physical/network filesystem results may differ.

The final visual check used a generated 250,000-file folder in the Codex
browser. The running removed-entry count was visible without a fabricated total;
completion reported 250,001 entries including the folder, with matching task
counters and the generated folder absent. Only the generated task's history
entry was removed afterward. Subsequent cache directory grouping adds ownership,
shared-blob and delete/recreate protections without restoring the source scan;
see [Thumbnail lifecycle design](../design/thumbnail-lifecycle.md) for its separate measurements and validation.

Delete progress-bar follow-up: the regression first failed because no progress bar was rendered for an unknown total. The fix passed 11 task checks in each browser, both mounted-path smoke checks and frontend builds, including animation, live counts, reduced motion, cancellation and completion. In the Codex browser, an active real deletion showed the animated bar with 16,307 removed items and no fabricated percentage. Generated deletions of 250,000 and 100,000 files completed with 250,001 and 100,001 items respectively, including the folders. Only those two task-history entries were removed, and the isolated test server was stopped.

Counted-progress follow-up: tasks now show **Scanning** with an indeterminate bar while counting directory entries, then switch to a determinate bar and deleted/total items. The count uses bounded directory batches, never follows child symlinks, does not read file contents or thumbnails and can be cancelled before anything is removed. Totals cover all selected sources and are reconciled after each source; if actual removals exceed the snapshot, the task falls back to the unknown-total bar until completion. The API regression first failed for the missing scan/count transition, then passed with multi-source totals. Full backend race tests and vet passed; Chromium and WebKit each passed seven task checks and one mounted-path check, with frontend builds. The final Codex-browser check showed 121,067 / 150,001 items with a filled percentage bar, then completed at 150,001 / 150,001. In three native-WSL benchmark runs for 5,000 small files, median streaming deletion was 61.20 ms, count-plus-delete 63.55 ms and OS `rm` 60.58 ms; setup and task persistence are excluded, and physical/network filesystems remain unmeasured.

## Completed transfer work

- [x] Transfer progress wording: change **Scanning transfer sources** to **Scanning**. Implemented and validated.
- [x] Remove the separate transfer phase/status text. The main task details above the progress bar (for example **Copying**) are sufficient. Moving a folder containing many files previously made the redundant status repeatedly flip between **Copying**, **Flushing** and **Publishing**. Implemented and validated.
- [x] Investigate moves containing many small files being much slower than OS moves, including between different disks where copy-and-delete is required. Identified excessive synchronous task-history writes, thumbnail inspection and avoidable same-filesystem copies. Implemented the changes below and measured a local improvement; performance on the user's physical disks remains unverified.

## Proposed transfer fixes

User comparison: manual `cp -r` followed by deletion completed in under one minute; WebFile took almost five minutes to process less than 5% of the files. This was a manual observation, not a controlled benchmark.

- [x] Throttle ordinary browser progress updates to 100–250 ms and ordinary task persistence to about once per second. Keep completion, errors, conflicts and cancellation immediate; avoid a database transaction/event for every file phase. Implemented and validated.
- [x] Remove thumbnail scanning/cache maintenance from the transfer's critical path; regenerate thumbnails on demand or perform cache upkeep in the background. Implemented on-demand regeneration and validated.
- [x] Review per-file disk flushing and investigate safe batching or overlap. Never delete a move source before its destination data has been safely written. The bounded small-file pipeline overlaps writes/flushes while retaining every per-file sync; broader batching requires additional staging/recovery work and disk measurements.
- [x] Consider bounded file-copy concurrency after checking disk behavior; avoid excessive concurrency and preserve conflict review, cancellation and no-overwrite behavior. Initial local measurements showed about a 3x improvement from bookkeeping/cache fixes but a substantial remaining gap. Implemented and validated a four-worker pipeline only for independent absent-destination regular files up to 64 KiB; per-file sync remains mandatory.
- [x] Attempt safe rename moves across configured roots on the same filesystem, with copy-and-delete fallback for different filesystems. Implemented and validated with a guard for directory exclusion rules. Top-level moves also reuse their initial size measurement.
- [x] Measure local many-small-file transfers before and after implementation, including OS copy comparisons; results below.
- [ ] Measure the user's physical two-disk workload and investigate the remaining OS-copy gap before tuning concurrency or implementing broader flush batching.

## Validation and measurements (2026-10-16)

No tests ran during the initial implementation. Its completed regression/race checks and measurements motivated the bounded-pipeline refinement, which was fully implemented before its validation pass.

- Regression tests against the original code failed as expected: 100 files created 299 transient task events, and cross-root moves on one filesystem copied rather than renamed.
- Full backend race tests, vet, production/PAM tests and production/PAM vet passed on the final code. Cancellation/conflict and related regressions passed 10 race-enabled repetitions.
- All five task-update browser tests passed in Chromium and WebKit, plus both mounted-path smoke checks; frontend builds passed.
- Visually checked an active 12,000-file copy in the Codex browser: the main details and byte progress remained visible without repeated phase text. The task completed with matching counters and all files present; only its own task-history entry was removed afterward.

Local benchmark: 1,000 files of 4 KiB each, tmpfs source and native WSL destination to force different filesystems, persistent task database enabled, three runs per method. Median elapsed times exclude fixture creation.

| Method | Median elapsed |
| --- | ---: |
| Original WebFile | 5.25 s |
| Bookkeeping/cache/rename changes | 1.72 s |
| Final code with bounded small-file pipeline | 0.96 s |
| OS `cp -r` plus deletion (final comparison) | 0.067 s |
| OS `cp -r`, final filesystem sync, then deletion (final comparison) | 0.099 s |

The final local result is about 5.5x faster than the original WebFile path. OS copying remains faster in this fixture. A final filesystem-wide sync differs from per-file synchronization and publication. These results do not establish performance on two physical disks; detailed attribution of the remaining gap and a broader flush-batching/recovery redesign remain follow-ups.
