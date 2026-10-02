# Potential per-user settings

Status: future options; not currently scheduled.

This document tracks only possible additions to the implemented settings page.
It is not a commitment to deliver them.

Settings are durable preferences controlled by the signed-in user and should
follow that user across browsers and sessions. They do not include per-tab
`sessionStorage` state or administrator-controlled system configuration.

## Appearance

- Interface density: Compact or Comfortable.

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

## Boundaries

Current folder, filter text, selection, open panels, and scroll position remain
tab/session state. Roots, authentication, TLS, archive commands, watcher
behaviour, caches, file-size limits, worker paths, and task concurrency remain
administrator configuration. Task history remains user data managed by its
existing interface rather than settings.
