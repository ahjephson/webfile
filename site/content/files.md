# Browse and manage files

Choose a configured root and open a folder. WebFile reads the live directory immediately. It does not build a catalogue or scan an entire mount before you can browse.

## Navigate and select

Use breadcrumbs and Back/Forward to move between folders. Each pane remembers its scroll position for the current browser tab. Choose list or grid view and sort by name, modified time, size or type; folders stay first.

| Action | Desktop shortcut |
| --- | --- |
| Select multiple items | Ctrl/Cmd-click |
| Select a range | Shift-click |
| Select all | Ctrl/Cmd+A |
| Filter this folder | Ctrl/Cmd+F |
| Search beneath this folder | Ctrl/Cmd+Shift+F |
| Rename | F2 |
| Delete | Delete |
| Open the selected item | Enter |

Right-click opens the context menu. On touch devices, use explicit selection mode or press and hold for actions. The interface also supports a split workspace with independent navigation and selection in each pane.

## Copy and move

Use Cut/Copy and Paste, the destination browser, or drag and drop. Dragging moves by default; Ctrl-drag copies and Shift-drag moves, with the choice applied at drop time. Copy and move work across configured roots when Linux permissions allow it.

If a destination exists, review the conflict options. Rename and Keep both preserve existing files; if the filesystem cannot safely create the new name, WebFile reports an error.

See [transfers and tasks](tasks.md) for progress, cancellation and Undo.

## Search filenames

The search dialog scans beneath the current folder, optionally using `*` and `?` wildcards, case sensitivity and file/folder filtering. Results stream into the normal file view. “Show in folder” opens a result's containing folder and selects the item.

Each worker permits one search, limited to 1,000 matches, 100,000 entries, 64 levels and 30 seconds. Reaching a limit reports partial results. Hidden/excluded paths and symlink traversal are omitted by default. Cancel stops the scan.

## Edit and delete

Create a folder or text/code file from the file actions. See [Text and code editor](editor.md) for Monaco's editing controls, Markdown preview, saving and conflict handling.

!!! warning "Delete is permanent"
    Delete removes files and folders after confirmation. There is no trash workflow. Keep independent backups, and check the selected items before confirming.

Details shows file metadata, Linux ownership and permissions. These values are read-only in the interface.
