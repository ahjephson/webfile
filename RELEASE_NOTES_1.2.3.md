# WebFile 1.2.3

WebFile 1.2.3 fixes folder-refresh flickering, protects unsaved editor changes, and strengthens filesystem access and archive extraction.

## Fixes

- Folder invalidations coalesce during external copies, and directory refreshes preserve existing thumbnails instead of repeatedly rebuilding the view.
- Downloads, editor reads, and content detection remain confined to the configured root when a directory is concurrently exchanged with a symbolic link.
- File operations and uploads preserve trailing whitespace in filenames, so a file such as `report ` cannot accidentally target its sibling `report`.
- Archive extraction checks every staged destination against configured excluded paths before publishing any extracted content.
- Text typed while a save is pending remains marked as unsaved. Initial saves and retries that recreate a removed file preserve the same submitted snapshot.
- Browser Back asks before discarding unsaved editor changes. Cancelling retains the editor and its text; accepting retains normal Forward navigation.
- Independent elevated workers retain their own thumbnail-cache capabilities. Ending one worker's access does not invalidate another's.
- Delayed Details responses cannot replace the current selection, display stale errors, or restart polling after the panel closes.
- Show in folder waits for the current directory listing before selecting a search result, including when an older listing request succeeds or fails after a newer read starts.

## Release validation

Release candidates run shared local and CI production, Chromium, and WebKit checks. Publication verifies the candidate's source commit, version, and package hashes and publishes the tested archive without rebuilding it.

## Deployment notes

- No configuration migration is required from 1.2.2.
- The packaged target remains Linux AMD64 with systemd on Debian or Ubuntu.

**Full changelog:** https://github.com/ahjephson/webfile/compare/1.2.2...1.2.3
