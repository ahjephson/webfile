# WebFile 1.2.4

WebFile 1.2.4 fixes repeated-action and failure-path bugs in editing, uploads, task history, media updates, archive handling and upgrades.

## Fixes

- Archive extraction restores overwritten originals if publication later fails or is cancelled. If a concurrent edit prevents safe restoration, recovery files are retained and their location is reported.
- Updates install the updater included in the release and roll back failures during binary, updater, service-file installation, service reload, restart or health checks. Rollback failures are reported instead of silently leaving a mixed installation.
- Folder ZIP downloads apply configured exclusions to files arriving after preflight. Exclusion rules preserve exact Linux names and paths, including whitespace and literal wildcard characters.
- Logout reports session-storage failures and allows retry instead of falsely reporting success.
- Worker and launcher liveness checks are synchronized with process exit.
- Worker restarts recover every unfinished task before applying the finished-history limit. Active tasks no longer consume the finished-history allowance in the backend or browser.
- Prepared folder ZIPs are cleaned up when their task history expires. Failed cleanup remains retryable, and obsolete orphan archives are removed on worker startup.
- Repeated uploads with reused task IDs display fresh progress, cancellation controls and completion notifications. Delayed task-history responses cannot restore entries already cleared or removed.
- Rapid preference changes are saved in action order, so reloading retains the latest choice.
- Image thumbnails refresh when their source changes, and open galleries discover images added to or removed from the current folder.
- Permission changes and Details reporting correctly handle setuid, setgid and sticky bits.
- Directory watches resume after the watched directory is renamed, deleted or replaced, including when content polling is disabled.
- Stalled uploads can be cancelled without blocking other uploads, and cancellation waits for incomplete-upload cleanup to finish.
- Editor saves coordinate conditional writes across workers. Expired elevation preserves unsaved text, and delayed file-open responses cannot replace a newer editor or preview.
- Recursive deletion remains anchored to the configured root. Moving a directory preserves excluded descendants instead of removing skipped content.
- Creation and rename dialogs preserve exact filenames. Save As keeps its folder and browser history consistent, and destination pickers ignore superseded requests.
- Interrupted HTTP streams retain their abort behaviour through panic recovery.

## Release validation

The release candidate runs the full production/PAM test and vet checks, frontend build and dependency audit, source and binary vulnerability checks, updater fault tests, package provenance checks, and complete Chromium and WebKit suites with application-prefix smoke checks. Publication verifies the candidate's source commit, version and package hashes and publishes the tested archive without rebuilding it.

## Deployment notes

- No configuration migration is required from 1.2.3.
- The packaged target remains Linux AMD64 with systemd on Debian or Ubuntu.
- Archive overwrite requires atomic rename-exchange support from the destination filesystem. Unsupported filesystems report an error without destructively replacing the original.
- An updater already installed by an older release cannot update itself during that first upgrade. After upgrading, install the packaged `deploy/webfile-update` at `/usr/local/sbin/webfile-update` with root ownership and mode `0755` once; subsequent upgrades update it automatically.

**Full changelog:** https://github.com/ahjephson/webfile/compare/1.2.3...1.2.4
