# WebFile 1.5.1

WebFile 1.5.1 fixes the brief appearance of an item's active border through its context menu during press-and-hold.

## Fixes

- Context menus appear fully opaque from their first frame instead of fading in over the active item. The fix applies to list and grid views, including touch press-and-hold and mouse right-click. Menu commands and outside-click dismissal continue to work normally.

## Validation and deployment

- Regression coverage checks the first rendered frame over an outlined item in both views. Focused Chromium and WebKit checks also cover menu commands, dismissal and press-and-hold behavior.
- The release pipeline validates the production package and complete browser suites before publication, and publishes the exact package tested by CI.
- No configuration migration is required. Refresh open browser tabs after upgrading to load the updated frontend. Clean-host installation/upgrade and physical-tablet checks remain manual validation tasks.

**Full changelog:** https://github.com/ahjephson/webfile/compare/1.5.0...1.5.1
