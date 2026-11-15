# WebFile 1.9.0

WebFile 1.9.0 adds archive contents previews, thumbnail generation settings and confirmation before removing elevated access.

## Changes

- Browse archive contents and folders without extracting files. When encrypted archive headers require a password, enter it directly in the preview dialog and retry incorrect passwords. Passwords stay in memory and are sent to the archive tool through standard input, never command-line arguments or saved preferences.
- Show archive-opening errors with a Retry action, including a clear message when the configured 7-Zip executable is missing. Archive preview is bounded to 30 seconds, 50,000 entries and 8 MiB of listing output. Extract archives to open individual members or access listings beyond those limits.
- Configure image and video thumbnail generation in Settings: enable or disable either kind, choose concurrent workers, video processing threads, generation timeout and queue capacity. Disabling video thumbnails cancels queued and running generation; resource changes apply to new work without restarting the service. Generation settings are saved per account. Shared cache locations and capacity remain server configuration options.
- Confirm before removing elevated access, with Cancel keeping elevation active.
- Anchor inaccessible-file lock badges to the file icon, matching folder badges.

## Deployment

No configuration migration is required. Existing generation limits remain the defaults for accounts without thumbnail preferences. Server configuration accepts zero image or video workers to disable that type by default. Worker and thread counts are limited to 32, video timeout to 3600 seconds and queued videos to 10,000. Existing configurations above those limits must be adjusted before upgrading.

Archive preview and extraction require the configured 7-Zip executable (`7zz` or a compatible `7z`); RAR decoding depends on the installed tool. Video thumbnails require `ffmpeg` and `ffprobe`. Refresh open browser tabs after upgrading.

Release validation covers the production package, complete Chromium and WebKit suites, mounted application paths and package provenance. Clean-host installation, upgrade and physical-tablet checks remain manual validation tasks.

**Full changelog:** https://github.com/ahjephson/webfile/compare/1.8.0...1.9.0
