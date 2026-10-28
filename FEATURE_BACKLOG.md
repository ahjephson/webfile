# Feature backlog

This list tracks proposed features and reported bugs. Items are not scheduled unless explicitly agreed.

Remove an item when it is released; do not retain it here with a completed status. Release notes record delivered work.

WebFile primarily serves file browsing and moving. Keep additions focused on those workflows; previously discarded suggestions should not be treated as backlog items without renewed agreement.

## 1. Drag and drop into folders in the same pane

- Status: implemented locally; awaiting release.
- Enhancement reported on 2026-10-28: allow a file or folder to be dragged onto another folder in the same browsing pane, without enabling split-pane mode.
- Expected behaviour: the destination folder accepts the drop and starts the transfer using the existing move/copy modifier behaviour and task handling.
- Finding: confirmed restriction in `FileExplorer.vue`. Both list/grid `can-drag` bindings require split view, and `startEntryDrag` independently rejects single-pane drags. Folder drop handlers, modifier handling and self/descendant destination checks already exist.
- Proposed change: enable internal dragging independently of split view, preserving the configured-root restriction and existing destination validation.
- Validation needed: list/grid folder drops in single-pane mode, multi-selection, move/copy modifiers, self/descendant rejection and split-pane regression coverage.

## 2. Show every item in a multiple-item upload

- Status: investigated; not scheduled or implemented.
- Bug reported on 2026-10-28: uploading multiple items shows only one item in the task list.
- Expected behaviour: every upload item appears in Tasks, with items waiting to upload shown as queued.
- Finding: confirmed scheduling gap in `FileExplorer.vue` and `api/client.ts`. `uploadFileList` awaits each complete upload before starting the next. A task is created only by that file's upload-init request, so the remaining selected files have no task records yet. The backend init handler currently creates a running task, not a queued task.
- Proposed change: represent the entire selected batch as queued tasks before sending file contents, then transition each task to running when its upload starts. Simply starting every upload concurrently or initializing every task as running would not provide the requested queue behaviour.
- Validation needed: all selected items visible immediately, queued-item cancellation, per-file failure handling, resumable retries, multiple batches and navigation/reload behaviour. Browser-held file contents must not be assumed recoverable after reload.

## 3. Paste from the context menu in an empty folder

- Status: implemented locally; awaiting release.
- Bug reported on 2026-10-28: after copying or cutting a file, right-clicking inside an empty destination folder does not open a context menu, preventing Paste through that menu.
- Expected behaviour: right-clicking the empty folder background opens a context menu with Paste available when the clipboard contains copied or cut items.
- Finding: confirmed missing event handler. List/grid entries emit context-menu events, but the file-view background has no context-menu handler. The menu model already supports Paste with no selected entries, and the keyboard and Actions-menu paste paths already exist.
- Proposed change: add a background context menu that targets the current folder, excludes file rows and interactive controls, and does not inherit stale entry selection/context.
- Validation needed: copy and cut into empty folders in list/grid views, blank space in populated folders, correct active pane/destination and disabled paste where mutation is unavailable.

## 4. Show the archive-named output path in the Extract dialog

- Status: investigated; not scheduled or implemented.
- Enhancement reported on 2026-10-28: the normal Extract… dialog should show the archive name in the destination path so it is clear that extraction creates an archive-named folder.
- Expected behaviour: extracting `archive.7z` beneath `/Files` visibly shows the resulting `/Files/archive` path before extraction starts.
- Finding: confirmed presentation gap. `ExtractDialog.vue` displays only the parent destination. `prepareExtraction` requests `separate_directories`, and the backend appends the archive basename after stripping supported extensions, including compound extensions such as `.tar.gz`.
- Proposed change: show the complete resulting output path before submission, updating it when the parent changes. Match the backend's naming rules and show each output folder for multiple archives. If the editable field itself becomes the full output path, adjust request semantics too, otherwise the backend would append the archive name twice.
- Validation needed: `.7z`, compound and uppercase extensions, root/trailing-slash paths, destination edits and multiple selected archives. Keep Extract here behaviour unchanged.

## 5. Cancel queued video thumbnail conversions

- Status: investigated; exact reported sequence not yet reproduced; not scheduled or implemented.
- Bug reported on 2026-10-28: attempting to cancel a queued video thumbnail conversion shows `video thumbnail job is not active`.
- Expected behaviour: a queued video thumbnail job can be cancelled before conversion starts, and Tasks reflects the cancellation without an error.
- Finding: the backend explicitly supports cancelling queued jobs in `thumbnail_queue.go`. The reported HTTP error is returned when cancellation reports false, which includes missing jobs and jobs that are already non-cancellable; it does not establish that queued cancellation is unsupported.
- Likely cause: stale task state. Thumbnail events use a 16-entry subscriber buffer and silently drop updates when full. Publishing queue-position updates for every queued job can fill that buffer. Unlike file-operation tasks, thumbnail tasks have no periodic reconciliation. The frontend also replaces job lists from HTTP responses and accepts updates without checking timestamps, so delayed responses can overwrite newer state. A job may therefore still look queued after it completes, is cancelled or is removed from the backend's transient history.
- Proposed change: reconcile active thumbnail jobs, reject stale state updates, and refresh authoritative state when cancellation races with completion/removal. Distinguish an already finished job from a real cancellation failure. Verify the backend transition races before choosing the final fix.
- Validation needed: cancel an actually queued job behind occupied workers, cancel during the queued-to-running transition, completion before click, lost terminal events under a large queue, delayed list responses and repeated cancellation.

## Investigation checks

- Investigated on 2026-10-28 through source and existing test review; no implementation changes made.
- Existing focused Go tests passed in native WSL: filesystem `TestVideoThumbnail(Batch|Queue|Requests|Retries)` tests and HTTP-server `TestUpload|Test.*Extract` tests.
- These checks do not reproduce the exact reported thumbnail cancellation sequence or provide new browser regression coverage for the proposed changes.
