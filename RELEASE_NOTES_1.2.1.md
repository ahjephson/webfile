# WebFile 1.2.1

WebFile 1.2.1 adds persistent file sorting, recursive filename search and interactive copy/move conflict resolution.

## Highlights

- Sort the loaded directory listing by name, modified time, size or type in either direction. Folders remain grouped first, selection and focus are retained, and the choice is saved per user. Sorting does not trigger recursive folder-size calculations.
- Search filenames beneath the current folder, with optional wildcards, case sensitivity and file/folder filtering. The main list/grid view opens immediately and fills as matches are found, with location information, progress and cancellation. Results support normal selection, sorting and file actions, including Show in folder.
- Resolve copy/move conflicts with Skip, Keep both, explicit Replace for regular files, or Merge folders. Decisions can apply to remaining conflicts of the same type. Changed destinations require fresh review, waiting tasks release their worker slot, and pending conflicts can be reviewed after reconnect. Worker restarts interrupt waiting tasks without replaying decisions.

## Fixes

- Renaming a recursive search result keeps it in its containing folder and refreshes the results instead of moving it into the search's starting folder.
- Moves to an absent destination use a safe whole-directory rename when supported within the same root and filesystem. This preserves read-only descendants and directory identity. Existing destination folders still require an explicit merge decision, and the rename cannot overwrite an item created concurrently.

## Deployment notes

- No configuration migration is required from 1.1.2. The packaged target remains Linux AMD64 with systemd on Debian or Ubuntu.
- Search scans the live filesystem without creating a catalogue. Each worker permits one search, bounded to 1,000 matches, 100,000 entries, 64 levels and 30 seconds. Limit hits are reported as partial results. Hidden files are omitted by default, configured exclusions are honoured, and directory links are not followed.
- File replacement requires atomic rename-exchange support. Unsupported filesystems report an error rather than falling back to an unsafe overwrite.

## Release workflow

- Browser tests wait for initial navigation setup before opening real filesystem fixtures and use stable right-click actions when saved view preferences change the rendered entries.
- Release jobs show individual test names, preserve failure screenshots, traces and server logs, and bound browser-suite runtime and server shutdown.
- Version 1.2.0 was tagged but not published because its CI browser checks failed. Version 1.2.1 includes the same application features and fixes with these test and workflow corrections.

## Validation

Local backend tests and vet checks, the frontend build, and search/conflict/sorting browser coverage passed. Regression tests cover nested search-result renames and whole-directory moves containing read-only descendants, including Keep both and transfer progress. The renamed result's location was also verified visually in the in-app browser. The release workflow runs production/PAM tests, dependency and compiled-binary vulnerability scans, the complete Chromium/WebKit suite and the proxy-path smoke test before publishing the Linux archive and checksum.

**Full changelog:** https://github.com/ahjephson/webfile/compare/1.1.2...1.2.1
