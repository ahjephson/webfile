# WebFile 1.1.1

WebFile 1.1.1 is a cumulative release from 0.12.0, the previous version that was published with downloadable GitHub assets. It includes the intervening 0.13.0, 1.0.0, 1.0.1 and 1.1.0 tagged changes.

## Highlights

- Added first-class reverse-proxy path mounting. `--public-url` derives the external origin and base path, while `--http`, `--base-path` and repeatable `--origins` provide explicit equivalents. HTTP API, WebSocket, assets and browser navigation remain inside the configured prefix.
- Made thumbnail generation safer for videos that are still being copied by another process. Files wait for stable metadata before joining the visible generation queue, settled work is appended in predictable order, and a later successful attempt clears the corresponding failed task.
- Preserved proxy-authenticated sessions during transient network failures instead of incorrectly returning to WebFile's own login screen.
- Refined light mode across selected files, task status pills, folders, controls and overlays so semantic colours retain contrast without inheriting dark-theme surfaces.
- Made gallery touch behaviour consistent: double-tap zoom no longer competes with immersive mode, swipe completion is smoother, and the thumbnail strip reserves space for the browser's actual scrollbar size.
- Restored the GitHub release pipeline. Tagged builds now run production/PAM tests, frontend and Go vulnerability checks, Chromium and WebKit workflows, reverse-proxy path validation, and publish a Linux AMD64 archive with its SHA-256 checksum.

## Deployment notes

- The packaged target remains Linux AMD64 with systemd on Debian or Ubuntu.
- No configuration migration is required from 0.12.0. Existing root-mounted deployments continue to work without the new listener/public-URL options.
- Path-mounted reverse proxies must forward the complete request path unchanged and preserve WebSocket upgrades. See the reverse-proxy example in `README.md`.
- Archive features require `7zz` or a configured compatible `7z` executable. Video thumbnails require `ffmpeg` and `ffprobe`.

## Validation

The release was built from tag `1.1.1`. The hosted release pipeline completed the production/PAM backend suite and vet checks, production frontend audit, source and compiled-binary vulnerability scans, real 7-Zip integration tests, the full touch-enabled Chromium and WebKit suite, and the non-root proxy-path smoke test.

**Full changelog:** https://github.com/ahjephson/webfile/compare/0.12.0...1.1.1
