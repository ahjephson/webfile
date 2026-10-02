# Per-user settings design

Status: initial delivery implemented.

The current settings page exposes appearance (theme, default view, grid tile
size, and dotfile visibility), editor defaults, and the transfer copy chunk
size. The remaining appearance, preview, and file-interaction preferences below
are planned follow-up work.

Settings are durable preferences controlled by the signed-in user and should
follow that user across browsers and sessions. They do not include per-tab
`sessionStorage` state or administrator-controlled system configuration.

## Appearance

- Theme: System, Light, or Dark.
- Default view: List or Grid.
- Grid tile size: Small, Medium, or Large.
- Interface density: Compact or Comfortable.
- Show dotfiles by default.

## Editor

- Word wrap.
- Font size.
- Show line numbers.
- Show whitespace.
- Show minimap.
- Compact or rich toolbar by default.
- Markdown preview enabled by default.

## Previews

- Show the image thumbnail strip by default.
- Default image sizing: Fit or Natural size.
- Video autoplay, defaulting to off.

## File interaction

- Default sort field and direction.
- Show folders first.
- Confirm before permanently deleting items.
- Automatically open the Tasks panel when a background operation starts.
- Optional persistent start location, distinct from restoring a tab's last folder.

## Advanced

### Transfers

- Copy chunk size: 1 MiB, 4 MiB, 16 MiB, or 64 MiB, defaulting to 4 MiB.
  This controls the maximum size of each Linux `copy_file_range` request,
  trading syscall frequency against progress and cancellation granularity.

## Delivery order and boundaries

The initial delivery exposes the four preferences already supported by the
backend (theme, view mode, grid size, and dotfile visibility), the editor
defaults, and the transfer copy chunk size. Interface density, preview defaults,
and file-interaction preferences remain planned.

Current folder, filter text, selection, open panels, and scroll position remain
tab/session state. Roots, authentication, TLS, archive commands, watcher
behaviour, caches, file-size limits, worker paths, and task concurrency remain
administrator configuration. Task history remains user data managed by its
existing interface rather than settings.
