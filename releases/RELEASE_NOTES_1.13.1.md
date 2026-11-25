# WebFile 1.13.1

## Changes

- Fixed split-view folder navigation closing or reloading the editor in the other pane. Unsaved edits now remain intact when navigating folders or using browser Back and Forward in the other pane.
- File previews also remain open without reloading during unrelated folder history navigation.

## Deployment

Upgrading from 1.13.0 requires no configuration changes. Refresh open browser tabs after upgrading. The release package targets Linux AMD64 and still uses the configured 7-Zip executable for archive operations.

The frontend audit retains the existing [scoped picomatch exception](https://github.com/ahjephson/webfile/blob/1.13.1/docs/SECURITY.md#temporary-dependency-exception) for the locked build tools. It expires on 18 December 2026 UTC or when a patched dependency becomes available.

[Full changelog](https://github.com/ahjephson/webfile/compare/1.13.0...1.13.1)
