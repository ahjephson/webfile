# WebFile (working name)

A Linux-first web file manager whose filesystem is the source of truth. WebFile
browses large existing filesystems immediately: there is no file catalogue to
build before a mount is usable.

The current code is an alpha implementation of the product brief: Go backend,
Vue 3/TypeScript frontend, Linux/PAM identity, direct filesystem IO, live
current-folder updates, Monaco editing, resumable uploads, file operations,
extraction, durable background tasks and persistent application state.

## Implemented

### Filesystem model and realtime

- Direct `os.ReadDir` listings; no filesystem mirror/database and no initial scan.
- Configurable roots with `{home}` and `{user}` expansion.
- Each root can set `excluded_names` to exact, case-sensitive entry names that
  should be omitted from directory listings anywhere beneath that root.
- List/grid views are virtualised for very large individual directories.
- One inotify watch for the directory each browser connection is currently
  viewing. inotify only invalidates the view; the browser then rereads the
  filesystem, which remains authoritative.
- One WebSocket per browser session carries directory invalidations and
  operation progress/cancellation.

### Linux identity and security

- PAM login using the machine's local users.
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
- Production binaries require direct TLS and PAM. Development binaries require
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
- Regular file downloads and streamed ZIP downloads for folders.
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

The task, event, artifact and checkpoint tables provide the durable foundation
for additional background work such as archive creation and resumable task
handlers without making the filesystem database-backed.

### Editor

- Monaco, lazy loaded.
- Syntax highlighting and Monaco's find/replace/regex support.
- Extensionless and arbitrary/custom filenames.
- New documents stay in browser memory until first Save.
- Existing-file ETag conflicts and first-save collisions.
- Conflict UI offers Cancel, Save As and explicit Overwrite.

### Archive extraction

Extraction is delegated to a configured CLI such as `7zz`; WebFile does not
maintain archive-format implementations.

- configurable executable/argument mapping;
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
- complete collision preflight before publishing anything;
- Linux `renameat2(RENAME_NOREPLACE)` publication, moving whole subtrees when
  possible and attempting rollback after an unexpected partial publish;
- refuse existing destination files without a check-then-rename overwrite race;
- source and destination may be in different configured roots.

Example:

```json
"archive": {
  "executable": "/usr/bin/7zz",
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
make build VERSION=v0.1.0
```

or create a release archive:

```bash
make package VERSION=v0.1.0
```

The release binary embeds the compiled Vue frontend. Node is a build-time
dependency only. The production build is compiled with the `production` and
`pam` tags and refuses to start unless PAM, secure cookies, and a TLS
certificate/private-key pair are configured.

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

Production always serves HTTPS directly and requires `auth.cookie_secure: true`.
If a reverse proxy is added, it must preserve the original `Host` header and
support WebSocket upgrades on `/api/fs/ws`, because WebFile's same-origin
mutation check deliberately compares the browser `Origin` with the request
host. Login limiting uses the direct peer address and does not trust forwarded
client-address headers.

### Updates

Release packages ship with SHA-256 checksums. The installed helper requires
both HTTPS package and checksum URLs:

```bash
sudo webfile-update \
  https://example/releases/webfile-v0.2.0-linux-amd64.tar.gz \
  https://example/releases/webfile-v0.2.0-linux-amd64.tar.gz.sha256
```

It verifies the checksum, atomically replaces the binary, updates the shipped
unit/PAM/tmpfiles files, restarts both services and restores the complete prior
installation if the new service does not start successfully.
`/etc/webfile/webfile.json` is never overwritten by an update.

`.github/workflows/release.yml` builds/tests tagged releases and publishes the
package + checksum. Frontend dependencies are reproducibly installed from the
committed `web/package-lock.json` with `npm ci`.

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

Run `go test ./...` and `go vet ./...` from `backend`, then `npm ci`,
`npm run build` and `npm run test:e2e` from `web` before packaging a release.
`make test-production` also compiles and tests the production/PAM build tags.
