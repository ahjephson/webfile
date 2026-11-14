# WebFile 1.8.0

WebFile 1.8.0 adds single-use undo for the latest copy or move.

## Changes

- Undo a completed copy or move from Tasks history, including transfers that used Keep both. Undo runs as a cancellable task and never overwrites existing files.
- Keep one undo set for one hour after completion. It survives restarts without extending the deadline. Any later accepted operation that changes user files clears it, including delete, rename, upload, editor save, folder creation, permission changes and extraction. Failure or cancellation does not restore the older set. Browsing, searches, previews and downloads preserve it.
- Record undo receipts in a private buffered disk journal, with bounded journal memory and no 10,000-entry cap. Folder moves record only the selected items and return their current contents, including later external edits. Copy undo validates every created entry before removing anything and protects changed or unexpected contents.
- Clean up discarded journals on expiry, replacement, history removal and restart recovery. Replacement, folder merge, moves with skipped items and overlapping source trees do not offer Undo. Permanent deletion remains irreversible.

## Deployment

No configuration migration is required. Refresh open browser tabs after upgrading.

Release validation covers the production package, complete Chromium and WebKit suites, mounted application paths and package provenance. Clean-host installation, upgrade and physical-tablet checks remain manual validation tasks.

**Full changelog:** https://github.com/ahjephson/webfile/compare/1.7.1...1.8.0
