# Feature backlog

This list tracks proposed features and reported bugs. Items are not scheduled unless explicitly agreed.

WebFile primarily serves file browsing and moving. Keep additions focused on those workflows; previously discarded suggestions should not be treated as backlog items without renewed agreement.

## 1. Audio preview handler

- Status: implemented and validated.
- Play supported audio files directly in WebFile using native browser controls, without autoplay.
- Supports common audio extensions and detected audio content types; codec support depends on the browser.

## 2. Split-pane browsing

- Status: implemented and validated on `codex/split-pane-browsing`.
- Browse two locations side by side, with independent navigation and selection in each pane.
- Support copying and moving selected items between panes, including drag and drop.
- Use the same keyboard modifier behaviour as transfers between windows, with the action shown before dropping.

## 3. Drag-and-drop transfers between windows

- Status: proposed.
- Drag selected files and folders from one WebFile window into a folder in another WebFile window to move or copy them.
- Make the chosen operation and destination clear before starting the transfer.
- Use the existing file-operation tasks and conflict handling for transfers.
- Use keyboard modifiers to choose the operation directly: plain drag moves, Ctrl-drag copies, and Shift-drag explicitly moves.
- Update the drag cursor and destination feedback as modifiers change, and use the modifier state at drop time. A separate move/copy chooser is unnecessary for mouse dragging.

## 4. Drag and drop into folders in the same pane

- Status: recorded; not scheduled or investigated.
- Enhancement reported on 2026-10-28: allow a file or folder to be dragged onto another folder in the same browsing pane, without enabling split-pane mode.
- Expected behaviour: the destination folder accepts the drop and starts the transfer using the existing move/copy modifier behaviour and task handling.

## 5. Show every item in a multiple-item upload

- Status: recorded; not scheduled or investigated.
- Bug reported on 2026-10-28: uploading multiple items shows only one item in the task list.
- Expected behaviour: every upload item appears in Tasks, with items waiting to upload shown as queued.

## 6. Paste from the context menu in an empty folder

- Status: recorded; not scheduled or investigated.
- Bug reported on 2026-10-28: after copying or cutting a file, right-clicking inside an empty destination folder does not open a context menu, preventing Paste through that menu.
- Expected behaviour: right-clicking the empty folder background opens a context menu with Paste available when the clipboard contains copied or cut items.
