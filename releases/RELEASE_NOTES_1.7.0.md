# WebFile 1.7.0

WebFile 1.7.0 adds pause/resume controls, task speed and remaining-time estimates, and more convenient archive extraction.

## Changes

- Pause and resume copy, move, delete, upload, archive creation and extraction tasks. Video thumbnail generation is excluded. Copies and uploads pause between chunks within a file; deletion pauses between entries. Paused tasks remain visible after reconnecting, but server restarts mark interrupted work rather than automatically resuming it.
- Copy, move and upload tasks show bytes/s and an estimated time remaining. Deletion shows items/s, counting files and folders, with an estimate when the total count is known. Estimates exclude paused time and are hidden when there is insufficient progress. The 7-Zip command line does not expose live speed or ETA, so archive tasks retain native progress without custom estimates.
- Extraction destinations can be typed or selected with a Browse button. Missing destination folders and their parents are created automatically while retaining configured-root and excluded-path protections.
- A new Archives setting deletes the source archive after successful extraction and defaults to enabled. Archives are retained after failures, cancellation, skipped files, or a changed or unsafe source.
- File drag gestures use mouse input. Touch and pen holds open the context menu without starting a file drag, and list rows no longer briefly appear above the menu because of transformed positioning.

## Validation and deployment

- Backend regression coverage includes pause/resume, task metrics, upload failure reporting, successful archive deletion, missing extraction destinations and rejected excluded or escaping paths. Development visual checks used the Codex app's in-app browser.
- Release validation checks the production package, complete Chromium and WebKit suites, mounted application paths and package provenance before publishing the exact CI-tested archive.
- No configuration migration is required. The default-enabled source-archive deletion setting can be changed under Settings → Archives. Refresh open browser tabs after upgrading. Clean-host installation, upgrade and physical-tablet checks remain manual validation tasks.

**Full changelog:** https://github.com/ahjephson/webfile/compare/1.6.3...1.7.0
