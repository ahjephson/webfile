# WebFile 1.6.2

WebFile 1.6.2 makes form and confirmation dialogs consistent when using the keyboard and while submitting actions.

## Changes

- Dialogs share their focus and keyboard behaviour. Forms focus the first editable field, prioritising invalid fields; confirmation dialogs start on Cancel. Closing a dialog restores focus to the control that opened it.
- Enter in a single-line field activates the enabled primary action and respects form validation. Buttons, dropdowns, checkboxes and multiline fields keep their normal keyboard behaviour. Held-key repeats and text-composition events do not trigger submission.
- Escape closes an open dropdown first. A second press closes the dialog.
- Folder creation, rename, transfer, extraction, password entry, settings and confirmation actions show progress while submitting and prevent duplicate submissions and dismissal. Failed requests leave the dialog available for retry or cancellation.
- The editor's save-conflict dialog starts on Cancel, and Escape dismisses that dialog without also closing the editor.

## Validation and deployment

- Browser regression coverage checks invalid-field focus, native validation, keyboard submission, dropdown dismissal, multiline input, focus restoration, progress and recovery after a failed request. Development visual checks used the Codex app's in-app browser.
- Release validation checks the production package, complete Chromium and WebKit suites, mounted application paths and package provenance before publishing the exact CI-tested archive.
- No configuration migration is required. Refresh open browser tabs after upgrading. Clean-host installation, upgrade and physical-tablet checks remain manual validation tasks.

**Full changelog:** https://github.com/ahjephson/webfile/compare/1.6.1...1.6.2
