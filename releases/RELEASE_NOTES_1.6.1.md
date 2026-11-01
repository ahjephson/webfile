# WebFile 1.6.1

WebFile 1.6.1 improves copy/move progress and disk writeback, and changes selection-border rendering to address the context-menu flash reported in Firefox on iPad.

## Changes

- Copy/move speed estimates retain timing history across files and disk-flush waits. Stalled transfers update their estimate instead of retaining a stale speed or restarting with an exaggerated burst. Resumed tasks exclude previously copied bytes from their new speed baseline.
- Task bars retain fractional progress instead of jumping in whole-percent steps. Tasks show “Waiting for disk…” when their transfer estimate reaches zero and “Finishing…” while the last bytes are being flushed or published. Success notifications remain tied to task completion.
- Linux file copies overlap bounded range writeback with subsequent copying, reducing the work left for the final flush on supported filesystems. The full-file safety flush before publication remains in place. Unsupported filesystems retain ordinary copying and flushing; writeback errors still fail the transfer rather than being ignored.
- Selected and focused file items use inset selection rings instead of CSS outlines in list and grid views. This avoids the separate outline-painting path suspected in the brief overlap above context menus. Menus continue to open without a fade.

## Validation and deployment

- Regression coverage checks rate history, blocked-flush updates, resumed tasks, bounded writeback, unsupported-filesystem fallback, IO errors, music-sized copies/moves and cancellation cleanup. Browser coverage checks fractional progress, final task states, selection and context-menu stacking across the first six rendered frames.
- Development visual checks used the Codex app's in-app browser. A real 32 MiB cross-filesystem move passed its destination checksum and source-removal checks. Local virtual-disk measurements showed shorter final-flush waits, but do not establish a sustained speedup on physical HDDs.
- The selection-border change has Chromium and WebKit coverage and local visual checks; the reported flash still needs confirmation in Firefox on the affected physical iPad. Clean-host installation, upgrade and physical-tablet checks remain manual validation tasks.
- No configuration migration is required. Refresh open browser tabs after upgrading to load the updated interface.

**Full changelog:** https://github.com/ahjephson/webfile/compare/1.6.0...1.6.1
