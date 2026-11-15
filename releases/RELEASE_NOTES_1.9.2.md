# WebFile 1.9.2

WebFile 1.9.2 fixes dependency vulnerabilities and hardens upload paths and filesystem worker ownership.

## Security fixes

- Replace the vulnerable image-processing dependency with an API-compatible fork containing the fix for CVE-2023-36308. Malformed palette indices no longer crash thumbnail resizing.
- Update Vite and Vue's type-checking tools, removing the vulnerable minimatch dependency from the development toolchain.
- Restrict upload IDs to their generated hexadecimal format and keep metadata and cancellation paths within the staging directory.
- Check Linux user and group IDs before converting them for filesystem ownership changes. Reject IDs outside the native integer range and the reserved unchanged-ownership value.

## Documentation

- Add the product page and searchable user documentation, including the Monaco editor guide and a screenshot captured from a running demo.
- Simplify the README and move source-build and architecture details into linked repository guides.

## Deployment

No configuration migration is required. Refresh open browser tabs after upgrading.

Release validation covers the production package, complete Chromium and WebKit suites, mounted application paths and package provenance. Clean-host installation, upgrade and physical-tablet checks remain manual validation tasks.

**Full changelog:** https://github.com/ahjephson/webfile/compare/1.9.1...1.9.2
