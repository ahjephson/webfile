# Release checklist

Use this checklist for every stable WebFile release. A tagged release should be
built from a clean `main` commit and use the same bare semantic version in the
tag, package name and embedded binary metadata.

## Automated release gates

- Install Go and frontend dependencies from the committed module and lock files.
- Run all Go tests and vet checks with the production and PAM build tags.
- Run `govulncheck` against the Go source and dependency graph.
- Audit production frontend dependencies; high and critical findings block the
  release. Review development-only findings separately because they are not
  shipped in the browser bundle.
- Build the frontend and exact production binary intended for release.
- Scan the compiled production binary with `govulncheck -mode=binary`.
- Run real 7-Zip integration coverage, including encrypted extraction, an
  incorrect-password retry, progress, extracted content and archive creation.
- Run the complete Playwright suite in touch-enabled Chromium and WebKit.
- Publish a SHA-256 checksum beside the release archive.

The release workflow performs these automated gates. Any failure prevents the
GitHub release from being published.

## Manual release checks

- Install the package on a clean supported Debian or Ubuntu AMD64 host.
- Confirm PAM login, logout, temporary elevation and direct TLS.
- Upgrade an existing 0.11.0 installation and confirm that configuration,
  sessions, preferences, task history and thumbnail cache remain usable.
- Force an update startup failure and confirm that `webfile-update` restores the
  previous installation.
- Confirm configured `7zz`/`7z`, `ffmpeg`, `ffprobe`, data and cache paths are
  usable by their expected service accounts.
- Exercise browse, upload, download, copy, move, delete, edit, preview, archive,
  task cancellation and WebSocket refresh workflows.
- On a physical tablet, exercise touch selection, double-tap opening,
  press-and-hold actions, gallery swipes, fullscreen/immersive transitions and
  the copy/move destination browser.
- Verify the Settings About section and `webfile version` show the expected
  release version, commit and build date.
- Review the release notes for configuration changes, external dependencies,
  supported platforms and known limitations.

## Release hygiene

- Confirm the working trees are clean and the release commit is on `main`.
- Build and inspect `dist/webfile-<version>-linux-amd64.tar.gz` and its checksum.
- Create an annotated bare semantic-version tag, without a `v` prefix.
- Push `main` and the tag from the authoritative Windows checkout.
- Confirm the GitHub release workflow completed and its artifacts match the
  locally inspected package layout.
