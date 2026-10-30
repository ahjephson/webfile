# WebFile agent guidance

## Project overview

WebFile is a Linux-first web file manager. The Go backend performs direct filesystem IO and the Vue 3/TypeScript frontend provides the browser UI. The filesystem is the source of truth: do not introduce a file catalogue or require an initial scan before a configured root can be used.

Read the [project README](../README.md) for the current feature and architecture summary. This file is the self-contained authority for the Windows/WSL development process. Run the commands below from the repository root unless a different working directory is specified.

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

- Preserve the broker/launcher/worker privilege split. In production the broker is unprivileged, the narrowly scoped launcher runs as root, and filesystem IO occurs in a persistent process using the logged-in user's real UID, GID, and supplementary groups.
- Treat Linux filesystem permissions as the authorization boundary. Do not make the broker a privileged filesystem proxy.
- Keep the launcher request surface limited to validated local users; it must not become a general command runner.
- The filesystem remains authoritative. inotify/WebSocket events invalidate a view and cause a reread; they do not maintain a parallel filesystem model.
- Do not persist plaintext session tokens or archive passwords. Preserve the existing cookie, CSRF, same-origin, path-containment, no-overwrite, and archive-extraction protections.
- Development and production modes are intentionally compile-time separated. Follow the operating-mode policy below; do not weaken the build-time or startup compatibility checks.
- Keep API and frontend type/behaviour changes synchronized. Add focused Go tests beside backend changes and Playwright coverage for important user workflows when practical.

## Authentication and TLS operating modes

WebFile has two deliberate operating modes:

1. **Development/test mode:** use a development build, plain HTTP, and `auth.mode: none` (normally `webfile.dev.json`). This is the default for routine feature work, debugging, automated tests, Playwright, and any other work that is not specifically exercising authentication or TLS. The tests depend on this unauthenticated mode.
2. **Production/auth mode:** use a production build compiled with the `production pam` tags and configure PAM authentication, direct TLS, and `auth.cookie_secure: true` (see `webfile.wsl.json` and `webfile.example.json`). Use this mode when specifically developing or validating PAM, login/session security, TLS, launcher integration, or the deployable production package.

Agents have explicit permission to switch the local application between these modes as needed for development and validation. Switching modes is an expected workflow action and is not itself a security weakening. Keep the build and its configuration paired correctly: a development build must remain non-auth HTTP, and a production build must only ever run with PAM authentication and TLS. Never make a production binary accept `auth.mode: none`, insecure cookies, or plain HTTP, and never bypass the compatibility checks to make a test pass.

## Windows-hosted Codex and WSL development

The Windows checkout and native-WSL workspace have different roles:

| Role | Location |
| --- | --- |
| Authoritative Git checkout and GitHub remote | `C:\Code\webfile` |
| Linux validation mirror, dependencies, tests, and builds | `$HOME/code/webfile` inside WSL |

`/mnt/c/Code/webfile` is the Windows checkout viewed from Linux. It is not a native Linux workspace. Apart from initial setup or an explicit documentation maintenance task, do not routinely edit it and never run Linux builds, tests, package installs, virtual environments, or release packaging there.

When Codex is hosted in the Windows sandbox, run Linux commands through WSL, for example:

```powershell
wsl.exe -- bash -lc 'cd "$HOME/code/webfile" && <command>'
```

Do not hard-code a Linux username or `/home/<user>`; expand `$HOME` inside the WSL shell. Keep the single quotes around the WSL script when invoking it from PowerShell so Windows does not expand Linux variables.

### Initial WSL workspace setup

The Windows directory is the only Git checkout. The native-Linux WSL directory is a plain validation mirror and must not contain `.git` metadata:

```powershell
git -C C:\Code\webfile status --short --branch
wsl.exe -- bash -lc 'mkdir -p "$HOME/code/webfile" && test ! -e "$HOME/code/webfile/.git"'
```

Do not clone into WSL or `/mnt/c`, reuse dependencies installed in Windows, or copy `node_modules`, build caches, and other platform-specific working state between the two locations. The workflow below is self-contained; no external machine-specific guide is required.

### Beginning work

Before changing code, inspect the authoritative repository and confirm that the WSL destination is a plain directory:

```powershell
git -C C:\Code\webfile status --short --branch
wsl.exe -- bash -lc 'test -d "$HOME/code/webfile" && test ! -e "$HOME/code/webfile/.git"'
```

If Windows contains unexpected or unrelated changes, preserve them and avoid overlapping edits. If WSL contains source changes that are not represented in Windows, preserve and reconcile them before synchronizing.

Make all hand edits in `C:\Code\webfile` with `apply_patch`. Preview the itemized Windows-to-WSL transfer, review it carefully (especially deletions), then apply the same sync:

```powershell
pwsh -NoProfile -File .\scripts\sync-to-wsl.ps1
pwsh -NoProfile -File .\scripts\sync-to-wsl.ps1 -Apply
```

Never fix ordinary source only in WSL because the next sync will replace it. Generators and formatters may create tracked source or lockfiles in WSL; copy only each intentional generated file explicitly back to Windows, review it, resync, and validate again. Never broadly mirror WSL back into Windows.

### Validation in WSL

Use the narrowest relevant checks while iterating, then run the applicable full checks before delivery.

For development tasks with browser-visible effects, run the application in the applicable operating mode and inspect the affected workflow using only the Codex app's in-app browser. Never request access to the user's personal or external browser. Exercise the changed UI, check the rendered result at a representative viewport, and look for visible errors or regressions; automated unit, build, and headless end-to-end results do not replace this development visual check. For development work with no browser-visible effect, inspect the relevant output or final diff instead.

Release tasks do not require interactive UI or browser visual inspection. Follow [the release checklist](release-checklist.md), including its required automated production, Chromium and WebKit validation, and inspect the package contents, checksums, embedded version/toolchain metadata, provenance and final diff as applicable. A toolchain or security patch release does not by itself justify opening the UI. Documentation-only corrections after a full local pass require document review, not another full application test/build cycle; the final release commit must still pass CI before tagging. Report the checks performed and any limitations in the task handoff.

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

`npm run build` runs `vue-tsc --noEmit` and writes the Vite output used by the Go embed to `backend/internal/ui/dist`. Those generated assets are ignored apart from the placeholder index and should not be hand-edited.

For production-tag coverage (requires a C toolchain and PAM development headers):

```bash
cd "$HOME/code/webfile"
make test-production
```

For browser tests, start the development backend, ensure Playwright's Chromium is available, and run:

```bash
cd "$HOME/code/webfile/web"
npm run test:e2e
```

The suite defaults to `http://127.0.0.1:8080`; override it with `WEBFILE_E2E_BASE_URL` when necessary. Use only the Codex app's in-app browser for interactive smoke tests, navigation, form interaction, and visual verification.

Useful aggregate commands from the repository root are:

```bash
make test
make build-dev
make build VERSION="$(cat releases/RELEASE_VERSION)"
make package VERSION="$(cat releases/RELEASE_VERSION)"
```

Production builds and packages require Linux, CGO, `build-essential`, and `libpam0g-dev`. Keep all generated release artifacts only in the WSL workspace under `$HOME/code/webfile/dist`.

### Committing and pushing

For releases and changes to release tooling, read [the release checklist](release-checklist.md) and perform the full shared production, Chromium and WebKit validation scripts in native WSL before any push or CI trigger. Focused tests alone are insufficient. Inspect the package checksum/metadata, workflow lint and artifact-provenance guards as applicable. Report the local results. Push the candidate commit first only when authorized, wait for successful CI validation of that exact `main` commit, then create/push its tag separately. Never use a tag-triggered build as the first complete validation pass.

After WSL validates the synchronized candidate tree, create an unsigned conventional commit in the authoritative Windows checkout:

```powershell
git -C C:\Code\webfile diff --check
git -C C:\Code\webfile status --short
git -C C:\Code\webfile add <files>
git -C C:\Code\webfile -c commit.gpgsign=false commit --no-gpg-sign -m "type: concise description"
```

Use a conventional type such as `feat`, `fix`, `docs`, `test`, `refactor`, `build`, or `chore`. Do not include unrelated user changes in the commit.

Push commits and tags to GitHub only from `C:\Code\webfile`, and only when the user asks for a push.

Never use `git reset --hard`, `git clean`, `--delete-excluded`, reverse broad mirroring, or recreate the removed legacy reverse-sync scripts. Never run Git operations in the WSL mirror, build from `/mnt/c`, or copy `dist/` into Windows. The sync wrapper's dry run is mandatory before every applied sync.
