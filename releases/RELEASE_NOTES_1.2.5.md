# WebFile 1.2.5

WebFile 1.2.5 fixes directory-watcher shutdown so an old watcher cannot interfere with live folder updates after a new watcher starts.

## Fixes

- Closing a directory watcher now interrupts its idle inotify read and waits for the reader to exit. A reader from a closed watcher can no longer consume events belonging to a later watcher that reuses its file descriptor.
- Shutdown stops pending notifications, closes subscriptions and clears watch registrations. Delayed subscription cleanup can no longer remove a new watch through a reused descriptor.
- Two regression tests cover blocked-reader shutdown and subscription cleanup after descriptor reuse.

## Release validation

The candidate runs the full production/PAM tests and vet checks, frontend build and dependency audit, source and binary vulnerability checks, updater fault tests, package provenance checks, and complete Chromium and WebKit suites with application-prefix smoke checks. Browser CI now splits each engine across four isolated parallel jobs. Publication verifies the candidate's source commit, version and package hashes and publishes the tested archive without rebuilding it.

## Deployment notes

- No configuration migration is required from 1.2.4. Restart WebFile with the new binary to use the fix.
- The packaged target remains Linux AMD64 with systemd on Debian or Ubuntu.

**Full changelog:** https://github.com/ahjephson/webfile/compare/1.2.4...1.2.5
