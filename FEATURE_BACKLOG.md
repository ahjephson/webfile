# Feature backlog

This list tracks proposed features and reported bugs. Items are not scheduled unless explicitly agreed.

Remove an item when it is released; do not retain it here with a completed status. Release notes record delivered work.

WebFile primarily serves file browsing and moving. Keep additions focused on those workflows; previously discarded suggestions should not be treated as backlog items without renewed agreement.

## 1. Drag and drop into folders in the same pane

- Status: recorded; not scheduled or investigated.
- Enhancement reported on 2026-10-28: allow a file or folder to be dragged onto another folder in the same browsing pane, without enabling split-pane mode.
- Expected behaviour: the destination folder accepts the drop and starts the transfer using the existing move/copy modifier behaviour and task handling.

## 2. Show every item in a multiple-item upload

- Status: recorded; not scheduled or investigated.
- Bug reported on 2026-10-28: uploading multiple items shows only one item in the task list.
- Expected behaviour: every upload item appears in Tasks, with items waiting to upload shown as queued.

## 3. Paste from the context menu in an empty folder

- Status: recorded; not scheduled or investigated.
- Bug reported on 2026-10-28: after copying or cutting a file, right-clicking inside an empty destination folder does not open a context menu, preventing Paste through that menu.
- Expected behaviour: right-clicking the empty folder background opens a context menu with Paste available when the clipboard contains copied or cut items.

## 4. Show the archive-named output path in the Extract dialog

- Status: recorded; not scheduled or investigated.
- Enhancement reported on 2026-10-28: the normal Extract… dialog should show the archive name in the destination path so it is clear that extraction creates an archive-named folder.
- Expected behaviour: extracting `archive.7z` beneath `/Files` visibly shows the resulting `/Files/archive` path before extraction starts.

## 5. Cancel queued video thumbnail conversions

- Status: recorded; not scheduled or investigated.
- Bug reported on 2026-10-28: attempting to cancel a queued video thumbnail conversion shows `video thumbnail job is not active`.
- Expected behaviour: a queued video thumbnail job can be cancelled before conversion starts, and Tasks reflects the cancellation without an error.
