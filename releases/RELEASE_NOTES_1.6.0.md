# WebFile 1.6.0

WebFile 1.6.0 improves folder navigation, renaming, clipboard feedback and keyboard access to search.

## Changes

- Rename focuses the name field and selects the complete target name when the dialog opens. Typing replaces the existing name, including its extension. Renaming from a context menu uses that item's name even when a different item is selected.
- Back and Forward remember each folder's scroll position independently per pane for the current tab. An unchanged layout restores the exact offset. When window size, split view, the details pane or grid geometry changes, restoration follows the previous visible item instead of reusing an unsuitable pixel offset. If that item is no longer present, restoration falls back to its previous position in the listing.
- Completed copy and move tasks show success notifications, including transfers started with Cut/Copy and Paste. Setting the clipboard or queuing a task does not report success before the transfer completes.
- Ctrl/Cmd+Shift+F opens the search dialog for the active pane. Ctrl/Cmd+F remains the current-folder filter. The search shortcut does not interrupt an open dialog.

## Validation and deployment

- Focused Chromium and WebKit coverage checks rename replacement, real clipboard transfers and notifications, shortcut scope, backward/forward scroll restoration, separate pane positions, and grid reflow after resize, split view and details-pane changes.
- All four workflows were also checked visually using the Codex app's in-app browser with disposable files. Release validation checks the production package, full browser suites, mounted paths and package provenance before publishing the exact CI-tested archive.
- No configuration migration is required. Refresh open browser tabs after upgrading. Scroll memory is local to the current tab and is bounded to recently visited folders; it is not a persistent filesystem index. Clean-host installation/upgrade and physical-tablet checks remain manual validation tasks.

**Full changelog:** https://github.com/ahjephson/webfile/compare/1.5.1...1.6.0
