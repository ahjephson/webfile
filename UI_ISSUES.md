# UI issue log

Per-user settings are tracked separately in `SETTINGS_DESIGN.md`.

## Open — verified 2026-10-01

- [ ] Prevent a rapid touch/pen double-tap in grid view from navigating through
  more than one folder level. The equivalent list-view workflow passes in the
  full suite, but the grid regression currently reaches the nested folder.

## Audit 2026-10-01

- The latest full Playwright suite passed 63 of 64 tests after the thumbnail
  endpoint mocks were updated for the new queue API. The only failure was the
  grid rapid-double-tap regression recorded above.
- `go test ./...` and `go vet ./...` passed for the backend, including the folder
  watcher coverage.
- In-app visual checks confirmed compact toggle styling and Copy/Move destination
  navigation by focus, double-click, and parent entry.
- The same visual review found the light-theme selection-control issue resolved
  below. The remaining historical resolved sections are still consistent with
  the automated coverage, rendered UI, and implementation review.

## Resolved 2026-10-02 — light-theme selection controls

- [x] Unchecked file-selection boxes are visibly unfilled in the light theme in
  both list and grid views, while selected boxes retain their blue fill.

## Resolved 2026-10-02 — direct deletion

- [x] Remove the Recycle Bin UI, HTTP endpoints, and storage implementation.
- [x] Submit Delete as a durable background task that directly removes each item.
- [x] State clearly in the confirmation that deletion is permanent and cannot be
  undone.

## Resolved 2026-09-28 — consistency and toggle behaviour

- [x] Context menus, dialogs, and tooltips use the same compact typography, spacing, and control density as the main interface.
- [x] Right-clicking a file or folder focuses that row and opens its context menu without changing checkbox multi-selection.
- [x] Details is a true toggle with a visible active state.
- [x] The same toggle convention applies to toolbar actions that open closable persistent views or panels:
  - activating the action opens the view;
  - its toolbar button visibly indicates the active state;
  - activating the same button again closes the view;
  - closing the view by another supported method clears the button's active state.

## Resolved 2026-09-28 — toggle styling, dialog density, editor actions, and toolbar grouping

Visual references: `codex-clipboard-42f25200-ad50-4af3-b4b6-0d1291c397e3.png` and `codex-clipboard-825f7158-c949-466f-8b29-2c3207da5dea.png`.

- [x] Toggle buttons use an unmistakable selected state based on a visible border, consistently applied to every toggled toolbar button.
- [x] Toggles retain their normal icon in both states.
- [x] Dialog title, field, supporting text, action text, header/content/footer padding, and overall control density match the primary interface.
- [x] The text editor toolbar has separate Find and Replace buttons using the established icon-only button and tooltip convention.
- [x] The app bar is grouped into creation, contextual file actions, application controls, appearance, and identity, with separators between groups.
- [x] Operations and Settings live in the app bar.
- [x] Breadcrumb-row controls are grouped on the right: selection status, Refresh, Filter, and the List/Grid/Show-hidden view controls.

## Resolved 2026-09-28 — destination folder navigation

- [x] Copy/Move destination lists show a `..` parent entry whenever the current destination has a parent.
- [x] The separate Up arrow action has been removed.
- [x] Destination rows follow the main pane interaction: single click/tap focuses a folder, double-click/double-tap opens it, and Enter provides keyboard activation.
- [x] Touch/pen interaction in the main file list supports ordinary taps; platform long-press behaviour is provided by the browser's standard `contextmenu` event.

## Resolved 2026-09-28 — touch navigation

- [x] Mouse right-click and platform-provided touch/pen context-menu events use the same context-menu action.
- [x] Opening a context menu does not alter checkbox selection.
- [x] PrimeVue dismisses an open context menu on its standard outside-click interaction.
- [x] A touch/pen tap opens an item or parent folder directly; desktop single-click continues to focus and double-click continues to open.

## Resolved 2026-09-28 — per-tab folder restoration

- [x] The last successfully loaded folder is stored per user in `sessionStorage` and restored after a browser refresh.
- [x] The saved folder is scoped to the current tab/session and does not persist after that browser session ends.
- [x] If the stored folder is unavailable, the saved value is cleared and the explorer falls back to `/`.

## Resolved 2026-09-28 — native context-menu interaction and browser coverage

- [x] File rows use the browser's standard `contextmenu` event for desktop and native platform context-menu behaviour.
- [x] A reusable pointer-event `v-longpress` directive supplements browsers that do not reliably emit `contextmenu` for touch/pen, with a 500 ms threshold and 10 px movement cancellation.
- [x] The directive invokes the existing context-menu handler, suppresses a duplicate native event, and consumes only the one compatibility click generated at the opening coordinates.
- [x] PrimeVue exclusively owns context-menu display, commands, outside-click dismissal, focus, and lifecycle.
- [x] Internal drag-and-drop copy/move is disabled; explicit Copy and Move actions are the only way to transfer existing entries.
- [x] External operating-system file drops remain available for uploads to the current folder or a visible subfolder.
- [x] Playwright covers touch navigation, checkbox-only selection, browser context-menu events, stationary touch and mouse-emulated long-press, movement cancellation, actionable mouse context menus, PrimeVue outside-click dismissal, and disabled internal drag-and-drop in both list and grid views.

## Resolved 2026-09-29 — selection controls

- [x] Replace the current selected-count/clear treatment with one compact group containing the selection count and two icon-only buttons: Select all and Clear selection.
- [x] Give both icon buttons descriptive tooltips and accessible labels; remove the current text-based Clear button.

## Resolved 2026-09-29 — URL folder navigation and browser history

- [x] Encode the current folder in the URL fragment (`#`) whenever navigation succeeds, using a reversible and safely encoded path representation.
- [x] Push folder changes into browser history so Back and Forward navigate through previously visited folders rather than leaving the current explorer state unchanged.
- [x] Keep URL navigation, `sessionStorage` restoration, direct fragment loading, and unavailable-path fallback behaviour consistent without creating duplicate history entries.

## Resolved 2026-09-29 — content-aware file previews

- [x] Replace the current open-everything-in-the-text-editor behaviour with a preview dispatcher based on the file's detected content type.
- [x] Continue opening supported text content in the text editor.
- [x] Show supported images in an image preview/gallery with previous and next navigation through nearby previewable images.
- [x] Attempt to play supported video content in an appropriate video player with native playback controls.
- [x] Present clear unsupported/unknown-type feedback when no preview handler is mapped, without attempting to decode the file as text; keep Download in the main file view.
- [x] Handle content-type detection and preview failures explicitly, including incorrect extensions, unavailable content, and files the browser cannot decode.

## Resolved 2026-09-29 — folder-watch reliability

- [x] Investigate why externally created folders are not appearing through the live folder watch until manual refresh.
- [x] Use native inotify alone on ordinary Linux filesystems and automatically add bounded polling for WSL/DrvFs, 9p, FUSE and SMB mounts.
- [x] Ensure create, rename, move, and delete changes invalidate the visible directory reliably; provide configurable `auto`, `always`, and `disabled` polling modes with a five-second default fallback interval.
- [x] Add watcher coverage and document the filesystem-specific fallback configuration.
