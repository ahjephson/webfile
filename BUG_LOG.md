# Bug log

## Large-folder delete work

- [x] Remove the thumbnail discovery/content-reading pass before recursive deletion; let old derived cache entries expire normally until grouped cleanup is available.
- [x] Stream directory names in bounded batches, unlink relative to open directory descriptors, never follow child symlinks and check cancellation between entries.
- [x] Report actual deleted child entries without a preliminary count or invented percentage; retain completed deletions on cancellation.
- [x] Validate cancellation/resume, symlinks, parent replacement, many-child folders and end-to-end task counts; benchmark against the original delete path and OS deletion.
- [x] Investigate and implement cache-only directory grouping. Lazy broker-owned nodes, stable ownership, revision-checked moves and bounded background delete cleanup are documented with measurements in `THUMBNAIL_LIFECYCLE_DESIGN.md`. Source filesystem scanning is not used to populate the index.

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
see `THUMBNAIL_LIFECYCLE_DESIGN.md` for its separate measurements and validation.

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
