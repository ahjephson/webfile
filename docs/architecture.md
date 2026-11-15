# Architecture and repository layout

WebFile combines a Go backend with a Vue and TypeScript frontend. PrimeVue and Tailwind provide interface components and styling; Monaco provides the text editor.

## Process boundaries

```text
Browser
   | HTTPS + WebSocket
Unprivileged Go broker
   | Group-protected Unix socket
Root launcher
   | Starts a worker for a validated local account
Per-user Go worker
   | File access using that account's UID, GID and supplementary groups
Linux filesystem
```

The broker handles browser connections and sessions. The launcher accepts a limited action and Linux username; it owns the executable, configuration path and worker directory. The broker cannot ask it to run arbitrary commands. Root and accounts below the configured minimum UID are refused.

Persistent per-user workers perform filesystem operations under the logged-in account's identity. Linux permissions remain the access boundary. Configuring a root does not grant additional access.

Production builds require PAM with direct TLS, or trusted proxy authentication with secure cookies. Development builds use plain HTTP without authentication. See the [security policy](SECURITY.md) and [development invariants](development.md#architecture-and-security-invariants) before changing these boundaries.

## Filesystem and application state

The filesystem is authoritative. WebFile reads directories directly and uses inotify events to invalidate visible folders so the browser can request a fresh listing. Browsing a configured root requires no catalogue or initial scan.

Application state is separate from user files. Hashed sessions and preferences persist across broker restarts; per-user SQLite databases hold task history. Upload staging and checkpoints support resumable transfers. Active tasks found after an unclean restart are marked interrupted.

Thumbnail generation runs through per-user workers. A size-bounded, broker-owned cache stores generated images and video sprites; cache hits still require source access checks under the user's identity. Its directory-reference index is populated by thumbnail requests and supports cleanup and reuse. It does not catalogue source roots or replace filesystem authorization.

7-Zip handles archive inspection, creation and extraction. FFmpeg and ffprobe provide video thumbnails. These tools run under the user's identity; archive operations use validated arguments and staging paths without invoking a shell.

## Repository layout

| Location | Purpose |
| --- | --- |
| `backend/cmd/webfile/` | Broker, launcher and worker entry points. |
| `backend/internal/` | Filesystem access, authentication, HTTP, tasks, persistence and process boundaries. |
| `web/src/` | Browser interface. |
| `web/tests/e2e/` | Playwright browser tests. |
| `deploy/` | Installer, updater, systemd services and PAM policy. |
| `scripts/` | Packaging, validation and maintenance tools. |
| `docs/` | Contributor and maintainer guidance, policies and backlog. |
| `site/` | Product page and published user documentation sources. |
| `releases/` | Release notes and version marker. |
| `.github/workflows/` | Application validation, package publication and site deployment. |

See [building](building.md) for local setup and [development](development.md) for the operating-mode policy, source workflow and validation requirements.
