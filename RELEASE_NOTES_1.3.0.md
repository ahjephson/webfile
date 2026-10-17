# WebFile 1.3.0

WebFile 1.3.0 adds split-pane browsing and native audio previews, with improvements to interaction performance in large folders.

## Features

- Split-pane browsing provides independent navigation, selection, filters and list/grid controls, with a resizable divider and shared task list. Closing split view keeps the active pane; reopening it keeps that pane on the left and reuses an available current listing.
- Copy and move selected items to the other pane through Actions, the context menu or the shared clipboard. Dragging moves by default, Ctrl-drag copies and Shift-drag moves. Dropping onto a highlighted folder targets that folder; dropping onto the list background targets the pane's current folder.
- Touch users can select the current folder as a destination by tapping the list background or current-folder breadcrumb. Choosing an item clears that destination selection. Responsive toolbars and vertically stacked panes support narrower screens.
- Audio previews use the browser's native player for playback, seeking and volume, without autoplay. Common formats include MP3, WAV, FLAC, AAC, M4A, Ogg and Opus; decoding depends on browser support. Streams support byte-range requests.

## Performance and fixes

- Large listings avoid repeated full-directory work during selection and reduce sorting overhead. Listings with at least 10,000 entries coalesce background refreshes for five seconds; explicit Refresh remains immediate.
- Visible panes maintain independent folder watches. Opening a second pane at the same location can reuse a clean, connected listing rather than fetching it again.
- Pane order, keyboard navigation, destination highlighting and selection controls remain consistent across single-pane and split-pane transitions.

## Validation and deployment

- Release validation covers development and production/PAM tests and vet, real archive integration, frontend build and dependency audit, source and binary vulnerability scans, package checksums and metadata, updater/provenance tests, and complete Chromium and WebKit suites with mounted-path checks. CI validates the exact main commit before the tag is published; publication reuses the tested package.
- No configuration migration is required from 1.2.6. The package targets Linux AMD64 with systemd on Debian or Ubuntu.
- Directory responses still contain the complete listing, so extremely large folders can remain slow to load initially. Cross-window dragging remains planned. Clean-host installation/upgrade and physical-tablet checks remain manual validation tasks.

**Full changelog:** https://github.com/ahjephson/webfile/compare/1.2.6...1.3.0
