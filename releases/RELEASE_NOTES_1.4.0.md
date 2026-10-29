# WebFile 1.4.0

WebFile 1.4.0 adds the yellow twin-folder logo and matching browser favicon.

## Changes

- Display the logo at the top left of the shared toolbar in single-pane and split-pane views.
- Add an SVG favicon and a matching ICO fallback containing 16, 32, 48 and 256 pixel versions. Both use the dark-mode folder yellow (`#fbbf24`).
- Resolve the toolbar logo and favicon assets correctly when WebFile is hosted beneath an application URL prefix.

## Validation and deployment

- The artwork was visually checked at small sizes on light and dark backgrounds, and the toolbar was checked at desktop and mobile widths.
- Release validation covers backend tests and vet, production/PAM coverage, frontend build and dependency audit, source and binary vulnerability scans, script tests, complete Chromium and WebKit suites, mounted-path checks, and package checksum and metadata checks. Publication reuses the package validated by CI for the exact main commit.
- No configuration migration is required from 1.3.0. The package targets Linux AMD64 with systemd on Debian or Ubuntu. Clean-host installation/upgrade and physical-tablet checks remain manual validation tasks.

**Full changelog:** https://github.com/ahjephson/webfile/compare/1.3.0...1.4.0
