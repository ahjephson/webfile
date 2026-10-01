# WebFile agent guidance

## Project overview

WebFile is a Linux-first web file manager. The Go backend performs direct
filesystem IO and the Vue 3/TypeScript frontend provides the browser UI. The
filesystem is the source of truth: do not introduce a file catalogue or require
an initial scan before a configured root can be used.

Read `README.md` for the current feature and architecture summary. This file is
the self-contained authority for the Windows/WSL development process.

Important locations:

- `backend/cmd/webfile`: broker, launcher, and worker entry points.
- `backend/internal/filesystem`: path resolution and file operations.
- `backend/internal/httpserver`: HTTP API, WebSocket, and security middleware.
- `backend/internal/operations`: durable background task management.
- `backend/internal/ui/dist`: generated frontend embedded by the Go binary.
- `web/src`: Vue application.
- `web/tests/e2e`: Playwright tests.
- `deploy`: systemd, PAM, installation, and update assets.
- `scripts/package.sh`: Linux release packaging.

## Architecture and security invariants

- Preserve the broker/launcher/worker privilege split. In production the broker
  is unprivileged, the narrowly scoped launcher runs as root, and filesystem IO
  occurs in a persistent process using the logged-in user's real UID, GID, and
  supplementary groups.
- Treat Linux filesystem permissions as the authorization boundary. Do not make
  the broker a privileged filesystem proxy.
- Keep the launcher request surface limited to validated local users; it must
  not become a general command runner.
- The filesystem remains authoritative. inotify/WebSocket events invalidate a
  view and cause a reread; they do not maintain a parallel filesystem model.
- Do not persist plaintext session tokens or archive passwords. Preserve the
  existing cookie, CSRF, same-origin, path-containment, no-overwrite, and
  archive-extraction protections.
- Development and production modes are intentionally compile-time separated.
  Follow the operating-mode policy below; do not weaken the build-time or
  startup compatibility checks.
- Keep API and frontend type/behaviour changes synchronized. Add focused Go
  tests beside backend changes and Playwright coverage for important user
  workflows when practical.

## Authentication and TLS operating modes

WebFile has two deliberate operating modes:

1. **Development/test mode:** use a development build, plain HTTP, and
   `auth.mode: none` (normally `webfile.dev.json`). This is the default for
   routine feature work, debugging, automated tests, Playwright, and any other
   work that is not specifically exercising authentication or TLS. The tests
   depend on this unauthenticated mode.
2. **Production/auth mode:** use a production build compiled with the
   `production pam` tags and configure PAM authentication, direct TLS, and
   `auth.cookie_secure: true` (see `webfile.wsl.json` and
   `webfile.example.json`). Use this mode when specifically developing or
   validating PAM, login/session security, TLS, launcher integration, or the
   deployable production package.

Agents have explicit permission to switch the local application between these
modes as needed for development and validation. Switching modes is an expected
workflow action and is not itself a security weakening. Keep the build and its
configuration paired correctly: a development build must remain non-auth HTTP,
and a production build must only ever run with PAM authentication and TLS.
Never make a production binary accept `auth.mode: none`, insecure cookies, or
plain HTTP, and never bypass the compatibility checks to make a test pass.

## Windows-hosted Codex and WSL development

The Windows checkout and native-WSL workspace have different roles:

| Role | Location |
| --- | --- |
| Authoritative Git checkout and GitHub remote | `C:\Code\webfile` |
| Linux editing, testing, and builds | `$HOME/code/webfile` inside WSL |

`/mnt/c/Code/webfile` is the Windows checkout viewed from Linux. It is not a
native Linux workspace. Apart from initial setup or an explicit documentation
maintenance task, do not routinely edit it and never run Linux builds, tests,
package installs, virtual environments, or release packaging there.

When Codex is hosted in the Windows sandbox, run Linux commands through WSL,
for example:

```powershell
wsl.exe -- bash -lc 'cd "$HOME/code/webfile" && <command>'
```

Do not hard-code a Linux username or `/home/<user>`; expand `$HOME` inside the
WSL shell. Keep the single quotes around the WSL script when invoking it from
PowerShell so Windows does not expand Linux variables.

### Initial WSL workspace setup

The Windows repository's `origin` points to GitHub. The WSL repository is a
native-Linux clone whose `origin` deliberately points to the local Windows
checkout:

```text
Windows origin -> GitHub
WSL origin     -> /mnt/c/Code/webfile
```

Before creating the WSL clone, confirm that the Windows checkout has no work
that would be lost or obscured:

```powershell
git -C C:\Code\webfile status --short --branch
```

Then create and verify the native-Linux clone:

```powershell
wsl.exe -- bash -lc 'mkdir -p "$HOME/code"'
wsl.exe -- bash -lc 'git clone --no-hardlinks /mnt/c/Code/webfile "$HOME/code/webfile"'
git -C C:\Code\webfile rev-parse HEAD
wsl.exe -- bash -lc 'git -C "$HOME/code/webfile" rev-parse HEAD'
```

The two commit IDs must match before development starts. Do not clone into
`/mnt/c`, reuse dependencies installed in Windows, or copy `node_modules`,
build caches, and other platform-specific working state between the checkouts.

### Beginning work

Before changing code, inspect both repositories:

```powershell
git -C C:\Code\webfile status --short --branch
wsl.exe -- bash -lc 'git -C "$HOME/code/webfile" status --short --branch'
```

If either checkout contains unexpected or unrelated changes, preserve them and
stop before synchronizing overlapping work. If both are clean, update the WSL
workspace from the Windows checkout:

```powershell
wsl.exe -- bash -lc 'git -C "$HOME/code/webfile" fetch origin main'
wsl.exe -- bash -lc 'git -C "$HOME/code/webfile" merge --ff-only origin/main'
```

Make all source edits in `$HOME/code/webfile`. Tool paths, patches, searches,
and test commands for implementation work must target that native-WSL copy.

The commands assume the authoritative branch is `main`. If the repository's
authoritative branch changes, determine its actual name and use it consistently
instead of silently creating or merging the wrong branch.

### Validation in WSL

Use the narrowest relevant checks while iterating, then run the applicable full
checks before delivery. Before completing any task, perform an appropriate
visual final check of the result. For browser-facing work, run the application
in the applicable operating mode and inspect the affected workflow in the
in-app browser using the computer-use skill (or the Playwright MCP server when
that is the available browser-control integration). Exercise the changed UI,
check the rendered result at a representative viewport, and look for visible
errors or regressions; automated unit, build, and headless end-to-end results do
not replace this final visual check. For work with no browser-visible effect,
visually inspect the relevant output instead (for example a rendered artefact,
generated package contents, command output, or final diff). Report what was
checked and any limitation in the task handoff.

Backend:

```bash
cd "$HOME/code/webfile/backend"
gofmt -w <changed-go-files>
go test ./...
go vet ./...
```

Frontend:

```bash
cd "$HOME/code/webfile/web"
npm ci --no-audit --no-fund
npm run build
```

`npm run build` runs `vue-tsc --noEmit` and writes the Vite output used by the
Go embed to `backend/internal/ui/dist`. Those generated assets are ignored
apart from the placeholder index and should not be hand-edited.

For production-tag coverage (requires a C toolchain and PAM development
headers):

```bash
cd "$HOME/code/webfile"
make test-production
```

For browser tests, start the development backend, ensure Playwright's Chromium
is available, and run:

```bash
cd "$HOME/code/webfile/web"
npm run test:e2e
```

The suite defaults to `http://127.0.0.1:8080`; override it with
`WEBFILE_E2E_BASE_URL` when necessary. Use the Playwright MCP server for browser
smoke tests, navigation, form interaction, and DOM/visual verification when it
is available.

Useful aggregate commands from the repository root are:

```bash
make test
make build-dev
make build VERSION=vX.Y.Z
make package VERSION=vX.Y.Z
```

Production builds and packages require Linux, CGO, `build-essential`, and
`libpam0g-dev`. Keep all generated release artifacts only in the WSL workspace
under `$HOME/code/webfile/dist`.

### Returning validated work to Windows

Use Git commits as the transfer mechanism; do not copy or mirror the working
tree. Create an unsigned conventional commit in WSL when the task calls for a
commit or for transferring the completed change to the authoritative checkout:

```powershell
wsl.exe -- bash -lc 'cd "$HOME/code/webfile" && git diff --check'
wsl.exe -- bash -lc 'cd "$HOME/code/webfile" && git status --short'
wsl.exe -- bash -lc 'cd "$HOME/code/webfile" && git add <files> && git -c commit.gpgsign=false commit --no-gpg-sign -m "type: concise description"'
```

Use a conventional type such as `feat`, `fix`, `docs`, `test`, `refactor`,
`build`, or `chore`. Do not include unrelated user changes in the commit.

Confirm the Windows checkout is clean, then fetch the WSL commit into Windows
and fast-forward:

```powershell
git -C C:\Code\webfile status --short --branch
wsl.exe -- bash -lc 'git -C /mnt/c/Code/webfile fetch "$HOME/code/webfile" main'
wsl.exe -- bash -lc 'git -C /mnt/c/Code/webfile merge --ff-only FETCH_HEAD'
wsl.exe -- bash -lc 'git -C "$HOME/code/webfile" fetch origin main'
```

After transfer, verify that both repositories are on the expected commit and
have no unexpected changes:

```powershell
git -C C:\Code\webfile status --short --branch
wsl.exe -- bash -lc 'git -C "$HOME/code/webfile" status --short --branch'
```

Push commits and tags to GitHub only from `C:\Code\webfile`, and only when the
user asks for a push.

Never use `git reset --hard`, `git clean`, `rsync --delete`, broad directory
mirroring, or the legacy sync scripts to reconcile the two repositories. Never
overwrite a dirty Windows checkout, push from the WSL clone into its local
Windows `origin`, build from `/mnt/c`, or copy `dist/` into Windows.
