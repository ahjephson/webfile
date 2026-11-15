# WebFile documentation

WebFile is a Linux file manager you use in a browser. It reads your existing filesystem directly and runs file operations using the logged-in user's Linux identity. There is no catalogue to build before browsing a configured root.

[Install WebFile](installation.md){ .md-button .md-button--primary }
[Explore the source](https://github.com/ahjephson/webfile){ .md-button }

## Start here

| You want to… | Read |
| --- | --- |
| Install the Linux AMD64 package | [Installation](installation.md) |
| Choose folders and configure services | [Configuration](configuration.md) |
| Use local accounts and secure connections | [Authentication and TLS](authentication.md) |
| Put WebFile behind an authenticating proxy | [Reverse proxy](reverse-proxy.md) |
| Work with files in the browser | [Browse and manage files](files.md) |
| Understand background work and Undo | [Transfers and tasks](tasks.md) |
| Diagnose a deployment problem | [Troubleshooting](troubleshooting.md) |

## Supported environment

The packaged target is **Linux AMD64 with systemd**, using Debian or Ubuntu. The server needs 7-Zip for archive operations and FFmpeg/ffprobe for video thumbnails. Node.js is needed to build the frontend, not to run a release package.

Use a current browser with JavaScript, cookies and WebSockets enabled. Automated browser coverage uses touch-enabled Chromium and WebKit; physical tablet checks are part of the release process.

## The security boundary

The broker runs as an unprivileged service account. A small root launcher starts per-user workers, and those workers access files using the user's real UID, GID and supplementary groups. Configuring a root does not grant access beyond the user's Linux permissions.

Production builds require PAM with direct TLS, or a trusted authenticating reverse proxy with secure cookies. The unauthenticated HTTP development build is for a trusted local machine. Read the [security policy](security.md) before exposing an installation.

These guides describe the current source tree. The release marker is **{{VERSION}}**; consult the [release notes](release-notes.md) for published changes.
