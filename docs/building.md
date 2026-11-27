# Build and run from source

Use a native Linux workspace. Windows-hosted contributors must follow the checkout, sync and WSL validation process in the [development guide](development.md).

## Prerequisites

- The Go toolchain declared in [backend/go.mod](../backend/go.mod).
- Node.js 22.12 or later, npm and Make.
- A C compiler and Linux-PAM development headers for production builds.
- 7-Zip for archive operations and integration tests.
- FFmpeg and ffprobe for video thumbnails and their tests.

Install these through your distribution's package manager. Package names vary; Debian and Ubuntu provide the production build dependencies as `build-essential` and `libpam0g-dev`.

## Development

From the repository root, build and start the development server:

```bash
make build-dev
./dist/webfile-dev serve -config ./webfile.dev.json
```

Open `http://127.0.0.1:8080`. The binary embeds the compiled frontend.

For frontend development with live reload, keep that server running and start Vite in another terminal:

```bash
cd web
npm ci
npm run dev
```

Open the local URL printed by Vite. It proxies API requests and WebSocket connections to `127.0.0.1:8080`.

The development configuration binds to loopback, disables authentication and exposes the current account's home directory. Use fictional files in a trusted local environment. Development binaries require plain HTTP and no authentication; production binaries enforce their separate authentication and transport requirements.

## Production build

From the repository root:

```bash
make build VERSION="$(cat releases/RELEASE_VERSION)"
```

This installs the locked frontend dependencies, builds the browser assets, runs production-tag tests and embeds the assets in `dist/webfile`. The production binary uses the `production` and `pam` build tags.

To create a release archive instead:

```bash
make package VERSION="$(cat releases/RELEASE_VERSION)"
```

Generated binaries and archives stay in `dist/`. Windows-hosted contributors keep them in the native WSL mirror. Node.js and Go are build dependencies; the runtime host needs compatible system libraries and the tools used by enabled features.

Follow the [installation guide](https://ahjephson.dev/webfile/docs/installation.html) for deployment, [configuration guide](https://ahjephson.dev/webfile/docs/configuration.html) for settings and [reverse proxy guide](https://ahjephson.dev/webfile/docs/reverse-proxy.html) for mounted paths and proxy authentication.

## Validation and releases

The [contribution guide](CONTRIBUTING.md#validation) describes development checks. The [release checklist](release-checklist.md) defines the complete local validation, CI, package provenance and tagging process. Building a package alone does not complete those release checks.
