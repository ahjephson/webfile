# WebFile

A self-hosted Linux file manager for your browser. Browse your existing files, edit code and manage transfers using your Linux account's permissions. WebFile reads the filesystem directly, so you can start browsing without building a catalogue.

[Website](https://ahjephson.dev/webfile/) · [Documentation](https://ahjephson.dev/webfile/docs/) · [Latest release](https://github.com/ahjephson/webfile/releases/latest)

![WebFile running with two file panes](https://ahjephson.dev/webfile/assets/webfile-demo.jpg)

## What you can do

- Browse in list, grid or split-pane views, with live folder updates, filename search and keyboard or touch controls.
- Upload files with resume support, copy and move between folders, and follow background tasks with pause and cancellation controls.
- Edit text and code with Monaco, including syntax highlighting, find and replace, and Markdown preview.
- Preview images, audio and video, browse archives and extract files with 7-Zip.
- Sign in through PAM or a trusted authenticating proxy. File operations run as your real Linux user, with the same filesystem permissions.

## Get started

Download the Linux AMD64 package and matching checksum from the [latest release](https://github.com/ahjephson/webfile/releases/latest), then follow the [installation guide](https://ahjephson.dev/webfile/docs/installation.html).

The prebuilt package needs compatible glibc and Linux-PAM libraries; the supplied installer uses systemd. Adapt the PAM policy to your distribution where needed. Install 7-Zip for archives and FFmpeg for video thumbnails. Node.js and Go are only needed when building from source.

Production requires HTTPS and secure cookies, with PAM or a trusted authenticating proxy. Read the [authentication](https://ahjephson.dev/webfile/docs/authentication.html) and [reverse proxy](https://ahjephson.dev/webfile/docs/reverse-proxy.html) guides when planning your deployment.

## Work on WebFile

WebFile uses Go for the backend and Vue, TypeScript and Monaco for the browser interface.

- [Build and run from source](docs/building.md)
- [Architecture and repository layout](docs/architecture.md)
- [Contribute and validate changes](docs/CONTRIBUTING.md)
- [Development workflow and invariants](docs/development.md)
- [Maintainer guides](docs/README.md), including releases, website maintenance and the feature backlog

## Help and license

[Report a bug or request a feature](https://github.com/ahjephson/webfile/issues). For vulnerabilities, follow the [security policy](docs/SECURITY.md).

WebFile is licensed under [MIT](LICENSE). See [third-party notices](docs/THIRD_PARTY_NOTICES.txt) for bundled dependencies.
