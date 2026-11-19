# WebFile 1.13.0

## Changes

- The file table now shows a **Type** column. Click its header to sort by file extension, then click again to reverse the order. Folders stay first, and WebFile remembers the sort order.
- Fixed a crash when opening file previews in browsers without the fullscreen API, including WebKit on iPad.
- Updated the image processing, system interface and SQLite dependencies used by the Go backend.

## Deployment

Upgrading from 1.12.0 requires no configuration changes. Refresh open browser tabs after upgrading. The release package targets Linux AMD64 and still uses the configured 7-Zip executable for archive operations.

The frontend audit retains the existing [scoped picomatch exception](https://github.com/ahjephson/webfile/blob/1.13.0/docs/SECURITY.md#temporary-dependency-exception) for the locked build tools. It expires on 18 December 2026 UTC or when a patched dependency becomes available.

[Full changelog](https://github.com/ahjephson/webfile/compare/1.12.0...1.13.0)
