# WebFile

A Linux-first web file manager whose filesystem is the source of truth. WebFile
browses large existing filesystems immediately: there is no file catalogue to
build before a mount is usable.

WebFile is released under the [MIT License](LICENSE).

WebFile combines a Go backend with a Vue 3/TypeScript frontend, Linux/PAM
identity, direct filesystem IO, live current-folder updates, Monaco editing,
resumable uploads, file operations, archive handling, durable background tasks
and persistent application state.

## Implemented

### Filesystem model and realtime

- Direct `os.ReadDir` listings; no filesystem mirror/database and no initial scan.
- Configurable roots with `{home}` and `{user}` expansion.
- Each root can set `excluded_names` to exact, case-sensitive entry names that
  should be omitted from directory listings anywhere beneath that root.
- Each root can set `excluded_paths` to exact, case-sensitive, root-relative
  paths (for example `/private/archive`) that should be omitted from listings
  and rejected when accessed directly. Descendants of an excluded directory
  are excluded too; glob patterns are not supported.
- List/grid views are virtualised for very large individual directories.
- One inotify watch for the directory each browser connection is currently
  viewing. inotify only invalidates the view; the browser then rereads the
  filesystem, which remains authoritative.
- One WebSocket per browser session carries directory invalidations and
  operation progress/cancellation.

### Linux identity and security

- PAM login using the machine's local users.
- Optional trusted reverse-proxy authentication maps an asserted username to a
  local Linux account.
- Filesystem IO happens in a persistent worker process running as the logged-in
  user's real UID/GID/supplementary groups. Linux permissions remain the access
  control boundary.
- The HTTP broker no longer needs root in production. A tiny root
  `webfile launcher` service has one job: validate a requested local user and
  start/reuse that user's worker. The broker communicates with it over a
  group-protected Unix socket.
- Persistent sessions store only a SHA-256 hash of the browser token; session
  state survives broker restarts.
- SameSite=Strict/HttpOnly cookies, configurable Secure cookies, per-session
  CSRF tokens, same-origin mutation checks, login rate limiting and baseline
  security headers.
- Production binaries require PAM with direct TLS, or trusted reverse-proxy
  authentication with a secure cookie. Development binaries require
  plain HTTP and no authentication, so an unsafe production combination cannot
  be selected only by changing configuration.

### File manager

- Browse, filter and navigate directories.
- Create folders and text/code files.
- Multi-select: Ctrl/Cmd, Shift ranges, Ctrl/Cmd+A and explicit touch selection
  mode.
- Shared right-click / touch press-and-hold context menu.
- Keyboard navigation and F2/Delete/Enter/Space/Escape shortcuts.
- Multi-item copy/move, including cross-root transfers.
- Linux copies use chunked in-kernel `copy_file_range` transfers when supported,
  with transparent buffered fallback.
- Drag/drop local files to upload.
- Conflict-safe operations: existing destinations are not silently overwritten.
- Regular file downloads. Folder downloads are prepared as cancellable 7-Zip
  archive tasks and become downloadable when creation completes.
- Delete permanently removes files and folders using native filesystem
  operations after explicit confirmation. Upgrades do not automatically purge
  files left in the legacy trash directory.
- The read-only Details pane resolves UID/GID to Linux user/group names. The
  backend has permission-change endpoints, but they are not currently exposed
  as user controls.

### Resumable uploads

Uploads use an 8 MiB chunked protocol rather than one large multipart request.

- an init request identifies the target using path, size and browser
  `lastModified` metadata;
- partial bytes and metadata live under `~/.local/share/webfile/uploads`;
- re-selecting/retrying the same file resumes from the server's recorded offset;
- each chunk is synchronised before its offset is advanced;
- completion is published into the normal operation/WebSocket UI;
- cancellation removes the staged upload;
- final publication remains no-overwrite and handles cross-filesystem staging.

### Background tasks

Copy, move, permanent delete, archive extraction and uploads report through a
shared task system. Per-user workers persist task history in
`~/.local/share/webfile/tasks.db` using SQLite/WAL.

- queued tasks run through a bounded worker pool (two concurrent tasks by
  default);
- lifecycle state includes queued, preparing, awaiting-input, running,
  cancelling, cleanup, terminal and interrupted states;
- progress, explicit filesystem phases and smoothed copy/move throughput are
  delivered over the existing WebSocket;
- task history survives worker and broker restarts;
- work found active after an unclean restart is marked interrupted rather than
  incorrectly reported as still running;
- cancellation is task-aware, and cleanup policies/errors are recorded with
  the task;
- the Tasks drawer separates active work from persistent history.

The task, event, artifact and checkpoint tables provide durable backing for
archive creation and future resumable task handlers without making the
filesystem database-backed.

### Editor

- Monaco, lazy loaded.
- Syntax highlighting and Monaco's find/replace/regex support.
- Extensionless and arbitrary/custom filenames.
- New documents stay in browser memory until first Save.
- Existing-file ETag conflicts and first-save collisions.
- Conflict UI offers Cancel, Save As and explicit Overwrite.

### Archive handling

Extraction and folder-download ZIP creation are delegated to a configured CLI
such as `7zz`; WebFile does not maintain archive-format implementations.

- configurable executable/argument mapping;
- folder downloads run as cancellable archive tasks, publish through a private
  partial file and expose a Download action only after the ZIP is complete;
- folder archive preflight enforces configured exclusions and rejects included
  symlinks or special files before invoking 7-Zip;
- encrypted archives are prepared out of the visible task list, then request a
  password through an in-memory operation channel; incorrect passwords prompt
  again until the user succeeds or cancels;
- no shell invocation;
- cancellation through the operation context;
- passwords are sent only over standard input and are never placed in command
  arguments, logs, WebSocket events or SQLite;
- extraction staging is created inside the destination filesystem and is never
  exposed by directory listings, even when hidden files are shown;
- streamed 7-Zip progress followed by a distinct publish phase;
- reject symlinks/special files and destination escapes;
- extracted files receive owner read access and extracted directories receive
  owner read/search access if those bits are missing, before validation descends
  into directories. Other permissions, including executable bits, are retained
  as produced by the archive tool and its umask. No write bits are added: a
  `0000` file becomes `0400`, preserving read-only intent even when 7-Zip
  interprets Windows attributes as unusable Unix permissions. Entries without
  stored permissions keep the tool's normal creation defaults. Existing
  destination permissions are retained, including when replacing file contents;
- complete collision preflight before publishing anything;
- Linux `renameat2(RENAME_NOREPLACE)` publication, moving whole subtrees when
  possible and attempting rollback after an unexpected partial publish;
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

`inspect_password_arg` is used only with a fixed, non-secret probe value to
detect encrypted archives. During extraction WebFile deliberately omits the
7-Zip password switch so that 7-Zip reads the real password from standard input.

### Persistent application state

WebFile deliberately does **not** persist the filesystem tree. An atomic JSON
state file under `runtime.data_dir` stores shared application state:

- hashed sessions + CSRF state;
- per-user view preferences (including hidden-file visibility).

Per-user background task state is kept separately in the SQLite database
described above. Neither store catalogues user files.

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

The launcher receives only an action + Linux username. It owns the executable,
configuration path and worker directory; the unprivileged broker cannot ask it
to execute arbitrary commands. Users below `runtime.min_user_uid` and root are
refused.

Video thumbnail sprites require `ffmpeg` and `ffprobe` on the runtime host. On
Ubuntu/Debian install both from the FFmpeg package:

```bash
sudo apt install ffmpeg
```

WebFile samples representative frames once and stores the resulting WebP sprite
in the persistent, broker-owned thumbnail cache. Cache hits still require the
requesting worker to open and stat the source as the logged-in user. Video
generation uses a separate request-driven queue (two FFmpeg workers and two
threads per process by default); the Tasks drawer shows queued and running
video work without adding it to durable file-operation history. Ordinary image
thumbnails use a separate fast path and stay out of the Tasks UI. The browser
never downloads a source video merely to animate a file tile.

## Development

Development builds use `auth.mode: none` and plain HTTP, so PAM and the root
launcher are not needed. The current process account owns the worker. A
development binary rejects PAM or TLS configuration.

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
make build VERSION=1.0.0
```

or create a release archive:

```bash
make package VERSION=1.0.0
```

The release binary embeds the compiled Vue frontend. Node is a build-time
dependency only. The production build is compiled with the `production` and
`pam` tags. PAM mode requires secure cookies and a TLS certificate/private-key
pair. Trusted proxy mode also requires secure cookies; without direct TLS its
listener must be bound to a loopback IP address.

The runtime host must also provide 7-Zip (`7zz` or a configured compatible
executable) for archive operations and `ffmpeg`/`ffprobe` for video thumbnails.

### Listener and public URL

`serve` normally uses `listen` from the configuration file. Command-line
options can override the broker listener without changing the worker-facing
configuration:

```bash
webfile serve -config /etc/webfile/webfile.json \
  --host 127.0.0.1 --port 6201 \
  --public-url https://192.168.0.254/tools/webfile
```

`--http 127.0.0.1:6201` is the equivalent single listener option and cannot be
combined with `--host` or `--port`. `--public-url` derives both the mounted base
path and the allowed browser origin. The explicit equivalent is:

```bash
webfile serve -config /etc/webfile/webfile.json \
  --http 127.0.0.1:6201 \
  --base-path /tools/webfile/ \
  --origins https://192.168.0.254
```

`--origins` may be repeated or contain a comma-separated list. The public URL
form cannot be combined with `--base-path` or `--origins`. When no public URL or
base path is supplied, WebFile retains its existing root-mounted behaviour.

### Supported environment

- Linux AMD64 with systemd; Debian and Ubuntu are the packaged deployment
  targets.
- Current Chromium, Firefox and Safari/WebKit browsers. The automated browser
  suite covers touch-enabled Chromium and WebKit; physical tablet testing is
  part of the release checklist.
- JavaScript, cookies and WebSocket support are required.

### Install

From an extracted release archive:

```bash
sudo ./deploy/install.sh ./webfile
```

The installer creates the `webfile` system account/group, installs the PAM
policy and both systemd services, and leaves configuration at:

```text
/etc/webfile/webfile.json
```

Install a certificate and private key before starting the services:

```text
/etc/webfile/tls/webfile.crt
/etc/webfile/tls/webfile.key
```

The TLS directory is owned by `root:webfile` with mode `0750`; keep the key
readable by that group only. For a local development certificate, include the
DNS name and IP addresses used by browsers in its subject alternative names,
then trust its issuing certificate on each client. The production endpoint is
`https://<host>:8080` by default.

Production runs two processes:

```text
webfile.service           User=webfile   HTTPS broker
webfile-launcher.service  User=root      worker launcher only
```

The broker stores application state in `/var/lib/webfile` and persistent
thumbnails in `/var/cache/webfile/thumbnails` by default. The cache directory
and its Unix socket are configurable under `thumbnails`; generated cache data
is size-bounded and published atomically. User file access and thumbnail
generation are performed only by UID-dropped workers.

`/etc/webfile/webfile.json` contains no credentials and is readable by local
users because UID-dropped workers load it after launch. TLS private-key material
remains separately protected under `/etc/webfile/tls` for the broker only.

Production always requires `auth.cookie_secure: true`. PAM mode serves HTTPS
directly. If a reverse proxy is added, it must preserve the original `Host`
header and support WebSocket upgrades. With `--public-url` or `--origins`,
WebFile's same-origin mutation check compares the browser `Origin` with the
configured external origins; otherwise it compares the origin with the request
host. Login limiting uses the direct peer address and does not trust forwarded
client-address headers.

### Trusted reverse-proxy authentication

Set `auth.mode` to `proxy` when nginx or another trusted reverse proxy performs
authentication. WebFile accepts the identity header only from direct peer CIDRs
in `auth.trusted_proxies`, rejects duplicate identity headers, resolves the value
as a local Linux account, and requires the same external identity on every
request even after creating its own CSRF session.

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

The proxy must overwrite, rather than append to, the identity header. Apply the
authentication check to every WebFile request, including the WebSocket
handshake. A typical nginx location contains:

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

The `/_auth/check` location should be `internal` and return
`X-Authenticated-User` only after successful authentication. Keep WebFile's
listener inaccessible except through the configured proxy. The external
username must match a permitted local Linux account because filesystem workers
still run using that account's real UID, GID, and supplementary groups.

WebFile keeps its own session only for CSRF state and temporary elevation. The
Sign out action first removes that local session and any elevated worker, then
issues a same-origin POST to `auth.logout_url`, including the `Origin` header
expected by SWAD's logout endpoint, and navigates to the resulting redirect. A
same-origin path routed by nginx is preferred; an absolute HTTPS URL is also
accepted for providers that support cross-origin form POST logout. PAM remains
available for the explicit temporary-elevation prompt in proxy mode.

### Updates

Release packages ship with SHA-256 checksums. The installed helper requires
both HTTPS package and checksum URLs:

```bash
sudo webfile-update \
  https://example/releases/webfile-1.0.0-linux-amd64.tar.gz \
  https://example/releases/webfile-1.0.0-linux-amd64.tar.gz.sha256
```

It verifies the checksum, atomically replaces the binary, updates the shipped
unit/PAM/tmpfiles files, restarts both services and restores the complete prior
installation if the new service does not start successfully.
`/etc/webfile/webfile.json` is never overwritten by an update.

`.github/workflows/release.yml` builds and tests tagged releases, audits
production frontend dependencies, scans the Go source and compiled binary,
runs the Chromium/WebKit browser suite, and publishes the package + checksum.
Frontend dependencies are reproducibly installed from the committed
`web/package-lock.json` with `npm ci`.

Release tags use bare semantic versions such as `1.0.0`, without a `v` prefix.
After validating and committing a release on `main`, create and push an
annotated tag:

```bash
make test-production
make package VERSION=1.0.0
git tag -a 1.0.0 -m "WebFile 1.0.0"
git push origin main 1.0.0
```

Pushing the tag starts the release workflow. Every future release build should
have a matching version tag and a `RELEASE_NOTES_<version>.md` file committed
before that tag is created. The workflow uses that file as the GitHub Release
description and includes all versioned release notes in the package. An
existing tag can be rebuilt from the Actions page by running **Release Linux
binary** manually and supplying its version; the workflow verifies that the
tag already exists and publishes or replaces the matching package assets.

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
deploy/
scripts/
.github/workflows/
```

## Validation

Run `go test ./...`, `go vet ./...` and `govulncheck ./...` from `backend`, then
`npm ci`, `npm audit --omit=dev --audit-level=high`, `npm run build` and
`npm run test:e2e` from `web` before packaging a release. Archive integration
tests use a real `7zz` or `7z` when available and are required in release CI.
`make test-production` also compiles and tests the production/PAM build tags.
Complete [RELEASE_CHECKLIST.md](RELEASE_CHECKLIST.md) before publishing.
The proposed stable-release summary is in
[RELEASE_NOTES_1.0.0.md](RELEASE_NOTES_1.0.0.md).
