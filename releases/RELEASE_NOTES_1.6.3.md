# WebFile 1.6.3

WebFile 1.6.3 corrects the default keyboard action in confirmation dialogs.

## Changes

- Confirmation dialogs focus their enabled primary action when they open. In the Delete dialog, Enter accepts deletion and Escape cancels, instead of Enter activating Cancel by default.
- Moving focus to another button still makes Enter activate that button. Form validation, dropdown and multiline keyboard behaviour, and protection against duplicate submissions remain in place.
- Confirmation actions continue to show progress while submitting, block dismissal during the request, and allow retry or cancellation after a failure.

## Validation and deployment

- Chromium and WebKit regression coverage checks the initial action focus, Enter acceptance, Escape cancellation, explicitly choosing Cancel, and recovery after a failed request. Development visual checks used the Codex app's in-app browser.
- Release validation checks the production package, complete browser suites, mounted application paths and package provenance before publishing the exact CI-tested archive.
- No configuration migration is required. Refresh open browser tabs after upgrading. Clean-host installation, upgrade and physical-tablet checks remain manual validation tasks.

**Full changelog:** https://github.com/ahjephson/webfile/compare/1.6.2...1.6.3
