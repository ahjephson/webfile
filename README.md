# WebFile

A Linux-first web file manager whose filesystem is the source of truth. WebFile browses large existing filesystems immediately: there is no file catalogue to build before a mount is usable.

WebFile is released under the [MIT License](LICENSE).

WebFile combines a Go backend with a Vue 3/TypeScript frontend, Linux/PAM identity, direct filesystem IO, live current-folder updates, Monaco editing, resumable uploads, file operations, archive handling, durable background tasks and persistent application state.

[Releases](https://github.com/ahjephson/webfile/releases) · [Installation](#install) · [Development](#development) · [Security](docs/SECURITY.md) · [Contributing](docs/CONTRIBUTING.md)

## Getting started

For a server installation, download the Linux AMD64 archive and its matching SHA-256 file from the release page. Verify the checksum with `sha256sum -c`, extract the archive, and follow [Install](#install). Production requires Linux, systemd, a permitted local user and either PAM with direct TLS or a trusted authenticating reverse proxy. Node.js is not required on the runtime host.

For local development, follow [Development](#development). The checked-in development configuration binds to loopback, disables authentication and exposes the developer's home directory. Use it only on a trusted local machine.

The [documentation index](docs/README.md) links to development guidance, security policies and the release process. Feature proposals belong in the [backlog](docs/feature-backlog.md); published changes are recorded in [release notes](releases/).

## Features

### Filesystem model and realtime

- Direct `os.ReadDir` listings; no filesystem mirror/database and no initial scan.
- Configurable roots with `{home}` and `{user}` expansion.
- Each root can set `excluded_names` to exact, case-sensitive entry names that should be omitted from directory listings anywhere beneath that root.
- Each root can set `excluded_paths` to exact, case-sensitive, root-relative paths (for example `/private/archive`) that should be omitted from listings and rejected when accessed directly. Descendants of an excluded directory are excluded too; glob patterns are not supported.
- List/grid views are virtualised for very large individual directories.
- Listings with at least 10,000 entries coalesce background invalidations until five seconds after the previous listing finishes. Explicit Refresh remains immediate. Listings still fetch the complete directory; virtualisation limits rendered rows, not response size.
- One inotify watch for the directory each visible browsing pane is currently viewing. inotify only invalidates the view; the browser then rereads the filesystem, which remains authoritative.
- One WebSocket per visible pane carries directory invalidations and operation progress/cancellation.

### Linux identity and security

- PAM login using the machine's local users.
- Optional trusted reverse-proxy authentication maps an asserted username to a local Linux account.
- Filesystem IO happens in a persistent worker process running as the logged-in user's real UID/GID/supplementary groups. Linux permissions remain the access control boundary.
- The HTTP broker no longer needs root in production. A tiny root `webfile launcher` service has one job: validate a requested local user and start/reuse that user's worker. The broker communicates with it over a group-protected Unix socket.
- Persistent sessions store only a SHA-256 hash of the browser token; session state survives broker restarts.
- SameSite=Strict/HttpOnly cookies, configurable Secure cookies, per-session CSRF tokens, same-origin mutation checks, login rate limiting and baseline security headers.
- Production binaries require PAM with direct TLS, or trusted reverse-proxy authentication with a secure cookie. Development binaries require plain HTTP and no authentication, so an unsafe production combination cannot be selected only by changing configuration.

### File manager

- Browse, filter and navigate directories.
- Folder scroll positions are remembered separately per pane for the current tab. Back and Forward restore the exact offset when the layout is unchanged, or keep the previous visible item in view after changes in window size, split view, grid size or the details pane.
- Optional split-pane browsing provides independent navigation, selection, filtering and view controls, a resizable divider, and a shared toolbar and task list. Copy/move actions and a shared clipboard transfer items between panes. Dragging moves by default; Ctrl-drag copies and Shift-drag moves, with the operation determined at drop time. Narrow screens stack the panes vertically. Pane locations, selection, layout and divider position are retained for the browser tab; closing split view keeps the active pane.
- Preview audio using the browser's native player, with playback, seeking and volume controls and no autoplay. Common audio formats include MP3, WAV, FLAC, AAC, M4A, Ogg and Opus; decoding support depends on the browser. Audio streams through the existing inline download endpoint with byte-range support.
- Sort by name, modified time, size or type in either direction, with folders first and the choice saved per user. Sorting uses the loaded listing; it does not trigger recursive folder-size calculations.
- Search filenames beneath the current folder, with optional `*`/`?` wildcards, case sensitivity and file/folder filtering. Results stream from a live scan into the normal list/grid view, with location information and a search banner. The search dialog configures the scan; results use the usual selection, sorting, previews and file actions. “Show in folder” in a result's context menu opens its containing folder and selects the item. Cancel stops the scan. Each worker allows one search, bounded to 1,000 matches, 100,000 entries, 64 levels and 30 seconds; hitting a limit clearly reports partial results. Hidden/excluded paths and symlink traversal are omitted by default, and no search catalogue is created.
- Create folders and text/code files.
- Rename opens with the complete name selected, so typing replaces it immediately.
- Multi-select: Ctrl/Cmd, Shift ranges, Ctrl/Cmd+A and explicit touch selection mode.
- Shared right-click / touch press-and-hold context menu.
- Keyboard navigation and F2/Delete/Enter/Space/Escape shortcuts.
- Ctrl/Cmd+F filters the current folder; Ctrl/Cmd+Shift+F opens the search dialog for the active pane.
- Multi-item copy/move, including cross-root transfers.
- Completed copy and move operations show a success notification, including transfers started with Cut/Copy and Paste.
- Linux copies use chunked in-kernel `copy_file_range` transfers when supported, with transparent buffered fallback.
- Drag/drop local files to upload.
- Conflict-safe operations: existing destinations are not silently overwritten.
- Drag files and folders onto another folder in the same pane, or transfer them between split panes. Plain drag moves, Ctrl-drag copies and Shift-drag moves. Right-click the folder background to Paste copied or cut items, including in an empty folder.
- Copy/move conflicts offer Skip, Keep both, explicit Replace for regular files, or Merge folders. File/folder mismatches cannot be replaced. Decisions can be applied to remaining conflicts of the same type within that task. Changed destinations require fresh review, and skipped move sources remain in place. Waiting tasks release their worker slot and can be reviewed after reconnect; worker restarts interrupt them rather than replaying previous decisions. Replacement requires filesystem support for atomic rename exchange; an unsupported filesystem reports an error without falling back to overwrite.
- Regular file downloads. Folder downloads are prepared as cancellable 7-Zip archive tasks and become downloadable when creation completes.
- Delete permanently removes files and folders using native filesystem operations after explicit confirmation. Tasks show an indeterminate bar while counting directory entries, then a percentage bar and deleted/total item count. Counting and deletion are cancellable and do not read file contents or inspect thumbnails. Deletion streams entries in bounded batches. Upgrades do not automatically purge files left in the legacy trash directory.
- The read-only Details pane resolves UID/GID to Linux user/group names. The backend has permission-change endpoints, but they are not currently exposed as user controls.
- Folder sizes are calculated on demand. The backend keeps scan times and directory metadata, invalidates sizes after known changes, and rescans stale results when viewed. External nested changes outside active directory watches are covered by a five-minute freshness limit. Unscanned folders are not walked during browsing. Scans use two IO slots, a bounded queue and expiring viewer leases, so abandoned views do not leave work queued indefinitely.

### Resumable uploads

Uploads use an 8 MiB chunked protocol rather than one large multipart request.

- every selected file is registered in Tasks before content transfer begins; waiting files appear as queued and can be cancelled before starting;
- batches share a serial upload queue in the current workspace and retain their destination during navigation;
- an init request identifies the target using path, size and browser `lastModified` metadata;
- partial bytes and metadata live under `~/.local/share/webfile/uploads`;
- re-selecting/retrying the same file resumes from the server's recorded offset;
- each chunk is synchronised before its offset is advanced;
- completion is published into the normal operation/WebSocket UI;
- cancellation removes the staged upload;
- abandoned staging expires after seven days without activity; removing failed or interrupted upload history also discards its checkpoint and partial bytes;
- final publication remains no-overwrite and handles cross-filesystem staging.

### Background tasks

Copy, move, permanent delete, archive extraction and uploads report through a shared task system. Per-user workers persist task history in `~/.local/share/webfile/tasks.db` using SQLite/WAL.

- queued tasks run through a bounded worker pool (two concurrent tasks by default);
- lifecycle state includes queued, preparing, awaiting-input, running, cancelling, cleanup, terminal and interrupted states;
- progress, explicit filesystem phases and smoothed copy/move throughput, including disk-write waits, are delivered over the existing WebSocket; the task bar retains fractional progress and distinguishes final flushing from completion;
- Linux file copies overlap bounded range writeback with subsequent copying and retain the final full-file flush before publication; filesystems without range-writeback support retain the ordinary copy-and-flush path;
- task history survives worker and broker restarts;
- work found active after an unclean restart is marked interrupted rather than incorrectly reported as still running;
- cancellation is task-aware, and cleanup policies/errors are recorded with the task;
- the Tasks drawer separates active work from persistent history.

The task, event, artifact and checkpoint tables provide durable backing for archive creation and future resumable task handlers without making the filesystem database-backed.

### Editor

- Monaco, lazy loaded.
- Syntax highlighting and Monaco's find/replace/regex support.
- Extensionless and arbitrary/custom filenames.
- New documents stay in browser memory until first Save.
- Existing-file ETag conflicts and first-save collisions.
- Conflict UI offers Cancel, Save As and explicit Overwrite.

### Archive handling

Extraction and folder-download ZIP creation are delegated to a configured CLI such as `7zz`; WebFile does not maintain archive-format implementations.

- configurable executable/argument mapping;
- folder downloads run as cancellable archive tasks, publish through a private partial file and expose a Download action only after the ZIP is complete;
- folder archive preflight enforces configured exclusions and rejects included symlinks or special files before invoking 7-Zip;
- the normal Extract… dialog previews each archive-named output folder before extraction starts;
- encrypted extraction tasks request a password through an in-memory operation channel; incorrect passwords prompt again until the user succeeds or cancels. Later leaves a task waiting, and Enter password on its task card reopens the prompt;
- no shell invocation;
- cancellation through the operation context;
- passwords are sent only over standard input and are never placed in command arguments, logs, WebSocket events or SQLite;
- extraction staging is created inside the destination filesystem and is never exposed by directory listings, even when hidden files are shown;
- streamed 7-Zip progress followed by a distinct publish phase;
- reject symlinks/special files and destination escapes;
- extracted files receive owner read access and extracted directories receive owner read/search access if those bits are missing, before validation descends into directories. Other permissions, including executable bits, are retained as produced by the archive tool and its umask. No write bits are added: a `0000` file becomes `0400`, preserving read-only intent even when 7-Zip interprets Windows attributes as unusable Unix permissions. Entries without stored permissions keep the tool's normal creation defaults. Existing destination permissions are retained, including when replacing file contents;
- complete collision preflight before publishing anything;
- Linux `renameat2(RENAME_NOREPLACE)` publication, moving whole subtrees when possible and attempting rollback after an unexpected partial publish;
- refuse existing destination files without a check-then-rename overwrite race;
- source and destination may be in different configured roots.

Example:

```json
"archive": {
  "executable": "/usr/bin/7zz",
  "create_args": ["a", "-tzip", "-y", "-bsp1", "-bso0", "-bse2", "-snl", "{archive}"],
  "extract_args": ["x", "-y", "-bsp1", "-bso0", "-bse2", "{archive}", "-o{destination}"],
  "inspect_args": ["l", "-slt", "{archive}"],
  "inspect_password_arg": "-p"
}
```

`inspect_password_arg` is used only with a fixed, non-secret probe value to detect encrypted archives. During extraction WebFile deliberately omits the 7-Zip password switch so that 7-Zip reads the real password from standard input.

### Persistent application state

WebFile deliberately does **not** persist the filesystem tree. An atomic JSON state file under `runtime.data_dir` stores shared application state:

- hashed sessions + CSRF state;
- per-user view preferences (including hidden-file visibility).

Per-user background task state is kept separately in the SQLite database described above. Neither store catalogues user files.

## Architecture

```text
Browser
  Vue 3 / TypeScript / PrimeVue / Tailwind / Monaco
                |
          HTTPS + WebSocket
                |
       unprivileged Go broker
          |             |
 JSON + task SQLite     PAM
          |             |
          +------ local identity
                        |
                root launcher service
                        |
             per-user Go worker
                        |
     ReadDir / inotify / IO / archive CLI
                        |
             Linux filesystem (truth)
```

The launcher receives only an action + Linux username. It owns the executable, configuration path and worker directory; the unprivileged broker cannot ask it to execute arbitrary commands. Users below `runtime.min_user_uid` and root are refused.

Video thumbnail sprites require `ffmpeg` and `ffprobe` on the runtime host. On Ubuntu/Debian install both from the FFmpeg package:

```bash
sudo apt install ffmpeg
```

WebFile samples representative frames once and stores the resulting WebP sprite in the persistent, broker-owned thumbnail cache. Cache hits still require the requesting worker to open and stat the source as the logged-in user. Video generation uses a separate request-driven queue (two FFmpeg workers and two threads per process by default); the Tasks drawer shows queued and running video work without adding it to durable file-operation history. Ordinary image thumbnails use a separate fast path and stay out of the Tasks UI. The browser never downloads a source video merely to animate a file tile.

## Development

Development builds use `auth.mode: none` and plain HTTP, so PAM and the root launcher are not needed. The current process account owns the worker. A development binary rejects PAM or TLS configuration.

Terminal 1:

```bash
make build-dev
./dist/webfile-dev serve -config ./webfile.dev.json
```

Terminal 2:

```bash
cd web
npm ci
npm run dev
```

Vite proxies `/api`, including WebSocket upgrades, to `127.0.0.1:8080`.

## Production build

Ubuntu/Debian build dependencies:

```bash
sudo apt install build-essential libpam0g-dev
```

Then:

```bash
make build VERSION="$(cat releases/RELEASE_VERSION)"
```

or create a release archive:

```bash
make package VERSION="$(cat releases/RELEASE_VERSION)"
```

The release binary embeds the compiled Vue frontend. Node is a build-time dependency only. The production build is compiled with the `production` and `pam` tags. PAM mode requires secure cookies and a TLS certificate/private-key pair. Trusted proxy mode also requires secure cookies; without direct TLS its listener must be bound to a loopback IP address.

The runtime host must also provide 7-Zip (`7zz` or a configured compatible executable) for archive operations and `ffmpeg`/`ffprobe` for video thumbnails.

### Listener and public URL

`serve` normally uses `listen` from the configuration file. Command-line options can override the broker listener without changing the worker-facing configuration:

```bash
webfile serve -config /etc/webfile/webfile.json \
  --host 127.0.0.1 --port 6201 \
  --public-url https://files.example.com/tools/webfile
```

`--http 127.0.0.1:6201` is the equivalent single listener option and cannot be combined with `--host` or `--port`. `--public-url` derives both the mounted base path and the allowed browser origin. The explicit equivalent is:

```bash
webfile serve -config /etc/webfile/webfile.json \
  --http 127.0.0.1:6201 \
  --base-path /tools/webfile/ \
  --origins https://files.example.com
```

`--origins` may be repeated or contain a comma-separated list. The public URL form cannot be combined with `--base-path` or `--origins`. When no public URL or base path is supplied, WebFile retains its existing root-mounted behaviour.

### Supported environment

- Linux AMD64 with systemd; Debian and Ubuntu are the packaged deployment targets.
- Current Chromium, Firefox and Safari/WebKit browsers. The automated browser suite covers touch-enabled Chromium and WebKit; physical tablet testing is part of the release checklist.
- JavaScript, cookies and WebSocket support are required.

### Install

From an extracted release archive:

```bash
sudo ./deploy/install.sh ./webfile
```

The installer creates the `webfile` system account/group, installs the PAM policy and both systemd services, and leaves configuration at:

```text
/etc/webfile/webfile.json
```

Install a certificate and private key before starting the services:

```text
/etc/webfile/tls/webfile.crt
/etc/webfile/tls/webfile.key
```

The TLS directory is owned by `root:webfile` with mode `0750`; keep the key readable by that group only. For a local development certificate, include the DNS name and IP addresses used by browsers in its subject alternative names, then trust its issuing certificate on each client. The production endpoint is `https://<host>:8080` by default.

Production runs two processes:

```text
webfile.service           User=webfile   HTTPS broker
webfile-launcher.service  User=root      worker launcher only
```

The broker stores application state in `/var/lib/webfile` and persistent thumbnails in `/var/cache/webfile/thumbnails` by default. The cache directory and its Unix socket are configurable under `thumbnails`; generated cache data is size-bounded and published atomically. User file access and thumbnail generation are performed only by UID-dropped workers.

A bounded, disposable directory-reference index groups cached thumbnails for background deletion cleanup and same-filesystem folder-move reuse. It is populated only by thumbnail requests, never by scanning source roots, and does not replace filesystem authorization or listings.

`/etc/webfile/webfile.json` contains no credentials and is readable by local users because UID-dropped workers load it after launch. TLS private-key material remains separately protected under `/etc/webfile/tls` for the broker only.

Production always requires `auth.cookie_secure: true`. PAM mode serves HTTPS directly. If a reverse proxy is added, it must preserve the original `Host` header and support WebSocket upgrades. With `--public-url` or `--origins`, WebFile's same-origin mutation check compares the browser `Origin` with the configured external origins; otherwise it compares the origin with the request host. Login limiting uses the direct peer address and does not trust forwarded client-address headers.

### Trusted reverse-proxy authentication

Set `auth.mode` to `proxy` when nginx or another trusted reverse proxy performs authentication. WebFile accepts the identity header only from direct peer CIDRs in `auth.trusted_proxies`, rejects duplicate identity headers, resolves the value as a local Linux account, and requires the same external identity on every request even after creating its own CSRF session.

```json
{
  "listen": "127.0.0.1:8080",
  "auth": {
    "mode": "proxy",
    "pam_service": "webfile",
    "user_header": "X-Authenticated-User",
    "trusted_proxies": ["127.0.0.1/32", "::1/128"],
    "logout_url": "/_auth/logout",
    "session_ttl": "12h",
    "elevation_ttl": "5m",
    "cookie_secure": true,
    "login_max_attempts": 5,
    "login_window": "1m",
    "login_block": "5m"
  },
  "tls": {
    "certificate_file": "",
    "private_key_file": ""
  }
}
```

The proxy must overwrite, rather than append to, the identity header. Apply the authentication check to every WebFile request, including the WebSocket handshake. A typical nginx location contains:

```nginx
location = /tools/webfile {
    return 308 /tools/webfile/;
}

location /tools/webfile/ {
    auth_request /_auth/check;
    auth_request_set $authenticated_user $upstream_http_x_authenticated_user;

    proxy_set_header X-Authenticated-User $authenticated_user;
    proxy_set_header Host $http_host;
    proxy_set_header Upgrade $http_upgrade;
    proxy_set_header Connection $connection_upgrade;

    # No trailing slash: nginx forwards the complete /tools/webfile/... path.
    proxy_pass http://127.0.0.1:6201;
}
```

The `/_auth/check` location should be `internal` and return `X-Authenticated-User` only after successful authentication. Keep WebFile's listener inaccessible except through the configured proxy. The external username must match a permitted local Linux account because filesystem workers still run using that account's real UID, GID, and supplementary groups.

WebFile keeps its own session only for CSRF state and temporary elevation. The Sign out action first removes that local session and any elevated worker, then issues a same-origin POST to `auth.logout_url`, including the `Origin` header expected by SWAD's logout endpoint, and navigates to the resulting redirect. A same-origin path routed by nginx is preferred; an absolute HTTPS URL is also accepted for providers that support cross-origin form POST logout. PAM remains available for the explicit temporary-elevation prompt in proxy mode.

### Updates

Release packages ship with SHA-256 checksums. The installed helper requires both HTTPS package and checksum URLs. Replace `<version>` with the desired release, or copy its download URLs from the [release page](https://github.com/ahjephson/webfile/releases):

```bash
sudo webfile-update \
  'https://github.com/ahjephson/webfile/releases/download/<version>/webfile-<version>-linux-amd64.tar.gz' \
  'https://github.com/ahjephson/webfile/releases/download/<version>/webfile-<version>-linux-amd64.tar.gz.sha256'
```

It verifies the checksum, atomically replaces the binary, updates the shipped unit/PAM/tmpfiles files, restarts both services and restores the complete prior installation if the new service does not start successfully. `/etc/webfile/webfile.json` is never overwritten by an update.

`.github/workflows/validate.yml` validates branch and PR candidates in separate production, Chromium and WebKit jobs. It audits dependencies, scans the Go source and compiled binary, and retains the tested package with a source/hash manifest. `.github/workflows/release.yml` publishes that package after verifying successful validation of the exact tagged `main` commit; it does not rebuild it. Frontend dependencies are reproducibly installed from the committed `web/package-lock.json` with `npm ci`.

Release tags use bare semantic versions without a `v` prefix. Set `releases/RELEASE_VERSION` and prepare matching `releases/RELEASE_NOTES_<version>.md` before final validation. Run the shared scripts locally first (in native WSL for Windows-hosted development):

```bash
COMMIT=local-validation bash scripts/validate-production.sh
bash scripts/validate-browser.sh chromium-touch
bash scripts/validate-browser.sh webkit-ipad
```

After local checks pass, commit the candidate and push it to `main` from the authoritative checkout when authorized. Wait for all three CI jobs to pass at that exact commit, then create and push its annotated tag separately:

```bash
git push origin main
# Wait for successful validation of the exact main commit before tagging.
release_version="$(cat releases/RELEASE_VERSION)"
git tag -a "$release_version" -m "WebFile $release_version"
git push origin "$release_version"
```

Pushing the tag starts the release workflow. Every future release build should have a matching version tag and a `releases/RELEASE_NOTES_<version>.md` file committed before that tag is created. The workflow uses that file as the GitHub Release description and includes all versioned release notes in the package. An validated tag can be published from the Actions page by running **Release Linux binary** manually and supplying its version. Missing or mismatched validation blocks publication, and existing published assets are not replaced. Candidate artifacts expire after 30 days; rerun validation if necessary. See [release checklist](docs/release-checklist.md) for the complete local-first process.

## Repository layout

```text
backend/
  cmd/webfile/
  internal/archive/
  internal/auth/
  internal/filesystem/
  internal/httpserver/
  internal/identity/
  internal/launcher/
  internal/operations/
  internal/persistence/
  internal/session/
  internal/watch/
  internal/worker/
web/
  src/
docs/                     # development and release guides, policies, backlog and notices
releases/                 # release notes and the release version marker
deploy/
scripts/
.github/workflows/
```

## Validation

Run `go test ./...`, `go vet ./...` and `govulncheck ./...` from `backend`, then `npm ci`, `npm audit --omit=dev --audit-level=high`, `npm run build` and `npm run test:e2e` from `web` before packaging a release. Archive integration tests use a real `7zz` or `7z` when available and are required in release CI. `make test-production` also compiles and tests the production/PAM build tags. Complete the [release checklist](docs/release-checklist.md) before publishing. See the [published releases](https://github.com/ahjephson/webfile/releases) for stable packages and release notes. [Third-party notices](docs/THIRD_PARTY_NOTICES.txt) identify the dependencies distributed with the application.
