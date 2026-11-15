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
| Publish WebFile behind a proxy | [Reverse proxy](reverse-proxy.md) |
| Work with files in the browser | [Browse and manage files](files.md) |
| Edit text, code or Markdown | [Text and code editor](editor.md) |
| Understand background work and Undo | [Transfers and tasks](tasks.md) |
| Diagnose a deployment problem | [Troubleshooting](troubleshooting.md) |

## Supported environment

WebFile runs on Linux. The prebuilt package requires an **AMD64 (x86-64)** system with compatible glibc and Linux-PAM libraries. The supplied installer uses systemd; its PAM policy may need adapting to your distribution. See [installation requirements](installation.md#requirements).

Install 7-Zip for archive operations and FFmpeg/ffprobe for video thumbnails. Running a release package does not require Node.js or a Go toolchain.

Use a current browser with JavaScript, cookies and WebSockets enabled. The interface supports desktop and touch controls.

## The security boundary

The broker runs as an unprivileged service account. A small root launcher starts per-user workers, and those workers access files using the user's real UID, GID and supplementary groups. Configuring a root does not grant access beyond the user's Linux permissions.

Production builds require PAM with direct TLS, or a trusted authenticating reverse proxy with secure cookies. The unauthenticated HTTP development build is for a trusted local machine. Read the [security policy](security.md) before exposing an installation.

For changes between releases, see the [release history](https://github.com/ahjephson/webfile/releases).
