# WebFile 1.12.0

WebFile 1.12.0 adds preview and extraction support for multipart 7z and ZIP archives, including files such as `sample.7z.001` and `sample.7z.002`.

## Changes

- Recognize numbered archive volumes in the file menu, archive preview and extraction API. Select any part to open or extract the archive. Keep all parts in the same folder.
- Treat selected parts of the same archive as one extraction task. Use the base archive name for the destination folder, such as `sample` for `sample.7z.001`.
- Give 7-Zip access to validated, open volume files through a private directory, preserving filesystem containment without copying large archives.
- Support encrypted multipart archives through the existing password workflow. Reject missing, corrupt or excluded volumes and symlink substitutions before publishing extracted files.
- Delete all source volumes after successful extraction when source deletion is enabled. Keep the volumes when files are skipped or a source volume changes.

## Build security

The frontend audit retains the temporary, maintainer-approved exception for [GHSA-fc5q-39xc-vjpf](https://github.com/advisories/GHSA-fc5q-39xc-vjpf) in picomatch 4.0.7 and its exact locked development-tool dependency chains. The exception expires on 18 December 2026 UTC and must be removed earlier when a patched dependency becomes available. Other high/critical advisories, production dependencies, version changes and audit errors remain blocking. See the [exposure assessment and removal conditions](https://github.com/ahjephson/webfile/blob/1.12.0/docs/SECURITY.md#temporary-dependency-exception).

## Deployment

No configuration migration is required when upgrading from 1.11.0. Archive operations continue to require the configured 7-Zip executable. Refresh open browser tabs after upgrading.

Release validation covers the production package, complete Chromium and WebKit suites, mounted application paths and package provenance. Clean-host installation, upgrade and physical-tablet checks remain manual validation tasks.

**Full changelog:** https://github.com/ahjephson/webfile/compare/1.11.0...1.12.0
