# WebFile 1.0.0

WebFile 1.0.0 is the first stable release of the Linux-first web file manager.
It operates directly on configured filesystem roots without building a file
catalogue.

WebFile is distributed under the MIT License.

## Highlights

- List and grid browsing with touch and mouse interaction, accessibility-aware
  selection, filtering, breadcrumbs and browser-history navigation.
- Copy, move, cut/paste, permanent delete, resumable upload, file download and
  cancellable folder-download archive tasks.
- Durable per-user task history, queue ordering, progress and throughput.
- Multi-archive extraction with password prompting, password reuse within a
  batch, skip/overwrite policies and guarded staged publication.
- Monaco text editing, Markdown preview, image galleries, native video preview
  and persistent image/video thumbnails.
- PAM authentication, trusted reverse-proxy authentication and temporary
  password-backed elevation using UID-dropped workers.
- Persistent user settings and build/version information in Settings.

## Runtime requirements

- Supported packaged target: Linux AMD64 with systemd, tested on Debian/Ubuntu.
- A configured `7zz` or compatible `7z` executable for archive operations.
- `ffmpeg` and `ffprobe` for video thumbnails.
- A current browser with JavaScript, cookies and WebSocket support.

## Upgrading from 0.11.0

The 1.0.0 package can be installed with `webfile-update` using the release
archive and its matching SHA-256 checksum. The updater preserves
`/etc/webfile/webfile.json` and rolls back the installed files if the new
service does not start.

No intentional configuration-schema migration is required from 0.11.0. Review
the example configuration before deployment, confirm the configured archive
executable exists, and complete the clean-install and upgrade checks in
`RELEASE_CHECKLIST.md` before promoting the release.

## Security validation

The release pipeline audits production frontend dependencies, scans both Go
source reachability and the compiled production binary, runs production/PAM Go
tests, exercises a real 7-Zip executable, and runs the browser suite under
Chromium and WebKit.
