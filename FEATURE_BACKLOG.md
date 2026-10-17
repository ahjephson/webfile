# Feature backlog

This list tracks proposed general features. Items are not scheduled unless explicitly agreed.

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
