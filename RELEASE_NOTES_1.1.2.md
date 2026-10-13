# WebFile 1.1.2

WebFile 1.1.2 fixes unreadable archive extraction results and makes the image gallery responsive to consecutive swipes.

## Fixes

- Repaired owner access for newly extracted archive entries before publishing them. Files receive missing owner read access and directories receive missing owner read/search access. A file extracted with mode `0000` becomes `0400`; no write permissions are added. Valid permissions, executable bits and read-only intent are retained, and existing destination permissions remain unchanged, including when replacing file contents.
- Made gallery slide animations interruptible. Touch gestures are handled across a stationary surface covering the gallery, so a new swipe is accepted while an image is still sliding. The gesture continues from the current visual position and can advance again or reverse direction. Pressing and releasing without swiping settles the incoming image into the centre. Stale animation timers and delayed image-type responses cannot override newer navigation.

## Deployment notes

- No configuration migration is required from 1.1.1.
- The packaged target remains Linux AMD64 with systemd on Debian or Ubuntu.
- The archive permission repair applies to new extraction operations. Files extracted by an earlier version are not modified automatically.

## Validation

Regression coverage includes a representative 7z archive with `Attributes = RA ----------`, Windows-only read-only attributes, missing attributes, executable files, directory traversal, WebFile reads/downloads and preservation of destination modes. Gallery coverage includes consecutive touch swipes, reversals, edge bounce-back and delayed image-type responses. The frontend build, production/PAM backend tests and vet checks, and 18 applicable gallery browser checks passed locally; two existing Chromium-only gallery checks are skipped in WebKit. The release workflow also runs the complete browser suite, proxy-path smoke test and dependency vulnerability checks before publishing the package.

The gallery was visually checked in the in-app browser. Physical-device WebKit validation has not been performed for this release.

**Full changelog:** https://github.com/ahjephson/webfile/compare/1.1.1...1.1.2
