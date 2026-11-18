# WebFile 1.11.0

WebFile 1.11.0 keeps folder-size calculations running when some children cannot be accessed and marks the result as incomplete. It also improves browser-test reliability while preserving gesture and upload coverage.

## Changes

- Continue calculating accessible children after a child cannot be read, rather than stopping at the first error. If the selected folder itself cannot be read, its size remains unavailable.
- Show an incomplete total in yellow with a warning icon in Details, list and grid views. The tooltip explains that inaccessible items were excluded. Cached results retain the warning, which clears after a successful recalculation includes all children.
- Use descriptor-confined filesystem access for folder scans, preserving the symlink protections introduced in 1.10.0.

## Validation improvements

- Split the combined gallery gesture test into seven independent scenarios covering preloading, swiping, double tap, pinch, fullscreen controls, mouse panning and thumbnail visibility. Extend supported scenarios to WebKit.
- Wait for gallery discovery, image layout and the destination folder listing before interacting. Control gesture timing and event timestamps without adding sleeps, browser-test retries or longer assertion timeouts.
- Exercise external-upload highlighting with a deliberately delayed listing in both list and grid views.

## Build security

The frontend audit retains a temporary, maintainer-approved exception for [GHSA-fc5q-39xc-vjpf](https://github.com/advisories/GHSA-fc5q-39xc-vjpf) in picomatch 4.0.7 and its exact locked development-tool dependency chains. The vulnerable operation receives repository-controlled build patterns; uploaded files and searches in the running application do not enter this Node build process. The exception expires on 18 December 2026 UTC and must be removed earlier when a patched dependency becomes available. Other high/critical advisories, production dependencies, version changes and audit errors remain blocking. See the [exposure assessment and removal conditions](https://github.com/ahjephson/webfile/blob/1.11.0/docs/SECURITY.md#temporary-dependency-exception).

## Deployment

No configuration migration is required when upgrading from 1.10.0. Refresh open browser tabs after upgrading.

Release validation covers the production package, complete Chromium and WebKit suites, mounted application paths and package provenance. Clean-host installation, upgrade and physical-tablet checks remain manual validation tasks.

**Full changelog:** https://github.com/ahjephson/webfile/compare/1.10.0...1.11.0
