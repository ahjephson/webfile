# WebFile 1.9.1

WebFile 1.9.1 keeps image icons visible while generated thumbnails load.

## Changes

- Show the image file icon until its thumbnail has loaded, matching video tiles. If generation or loading fails, keep the icon visible.
- Reset the loading state when an image changes, while retaining thumbnails for unchanged files. Image thumbnails that are switched off continue to show file icons.
- Cover pending thumbnails, successful loading, failures, refreshes and disabled thumbnail settings in browser checks.

## Deployment

No configuration migration is required. Refresh open browser tabs after upgrading.

Release validation covers the production package, complete Chromium and WebKit suites, mounted application paths and package provenance. Clean-host installation, upgrade and physical-tablet checks remain manual validation tasks.

**Full changelog:** https://github.com/ahjephson/webfile/compare/1.9.0...1.9.1
