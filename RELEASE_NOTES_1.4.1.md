# WebFile 1.4.1

WebFile 1.4.1 updates the Go build toolchain to 1.27.3 and fixes frontend dependency vulnerabilities.

## Changes

- Build with Go 1.27.3, including its `crypto/x509` security fix for certificate-chain verification.
- Retain the existing Go 1.26 language compatibility requirement.
- Update DOMPurify to 3.4.17 and markdown-it to 15.0.3 to address the vulnerabilities reported by the production dependency audit.

## Validation and deployment

- Release validation covers backend tests and vet, production/PAM coverage, frontend build and dependency audit, source and binary vulnerability scans, script tests, complete Chromium and WebKit suites, mounted-path checks, and package checksum and metadata checks. Publication reuses the package validated by CI for the exact main commit.
- No configuration migration is required from 1.4.0. The package targets Linux AMD64 with systemd on Debian or Ubuntu. Clean-host installation/upgrade and physical-tablet checks remain manual validation tasks.

**Go release notes:** https://go.dev/doc/devel/release#go1.27.3

**Full changelog:** https://github.com/ahjephson/webfile/compare/1.4.0...1.4.1
