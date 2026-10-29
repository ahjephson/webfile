# WebFile 1.5.0

WebFile 1.5.0 improves file transfers, upload task visibility and archive extraction, and fixes password prompts and thumbnail cancellation races.

## Changes

- Drag selected files and folders onto a folder in the same pane without enabling split view. Plain drag moves, Ctrl-drag copies and Shift-drag moves; existing destination and conflict protections remain in place.
- Right-click the folder background to Paste copied or cut items, including in an empty folder. The menu targets the current folder and clears the previous item selection.
- The normal Extract… dialog previews the full archive-named output path before extraction starts. Selecting several archives shows each output folder, and editing the parent destination updates the preview.
- Archive password prompts follow the newest accepted task state, so a delayed queued response cannot dismiss a newer password request. Repeated incorrect-password replies prompt again, and another waiting archive does not replace the current prompt.
- Waiting extraction tasks provide an outlined Enter password button with a key icon. Later closes the prompt while leaving the task waiting, so the password can be entered from Tasks when convenient.
- Every selected upload is registered in Tasks before file contents are sent. Waiting files appear as queued and can be cancelled before starting. Batches share a serial upload queue in the current workspace, retain their destination during navigation and continue after an individual file fails.
- Reloading or leaving the page reports abandoned upload attempts as stopped. Resumable data is retained after interruption; reselect the original files to resume. Cancelling a queued upload removes its staging data, and cancelled or superseded attempts cannot restart through a delayed chunk request.
- Thumbnail tasks periodically reconcile with the worker and reject older updates. Cancelling a job that has already finished refreshes its state instead of reporting a misleading cancellation error.
- The breadcrumb trail stays aligned when the available content width changes.
- Release packages group release notes and the version marker under `releases/`, and supporting documentation under `docs/`. Packages include security and contribution guidance and third-party dependency notices.

## Validation and deployment

- Release validation covers development and production/PAM backend tests and vet, real archive integration, frontend build and dependency audit, source and binary vulnerability scans, release-script tests, complete Chromium and WebKit suites, mounted-path checks, and package checksum and metadata checks. Publication reuses the package validated by CI for the exact main commit.
- No configuration migration is required from 1.4.2. Refresh open browser tabs after upgrading to load the updated task and upload client. The package targets Linux AMD64 with systemd on Debian or Ubuntu.
- File contents selected in a browser are not retained across a page reload. Reselect interrupted uploads to resume them. Clean-host installation/upgrade and physical-tablet checks remain manual validation tasks.

**Full changelog:** https://github.com/ahjephson/webfile/compare/1.4.2...1.5.0
