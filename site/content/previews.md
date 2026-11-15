# Previews and archives

WebFile uses the logged-in user's read permissions for previews and downloads. Format support depends on the file, installed server tools and browser.

Text and code files open in the [Monaco editor](editor.md), which also provides a live Markdown preview.

## Images, video and audio

Images can open in the gallery, with touch navigation and fullscreen/immersive controls. Video thumbnails use server-side FFmpeg; the browser does not download a source video just to animate a file tile.

Audio previews use the browser's native player, with seeking, volume and no autoplay. Common formats include MP3, WAV, FLAC, AAC, M4A, Ogg and Opus, subject to browser decoding support. Byte-range requests support seeking.

## Thumbnail settings

Settings provides per-account image/video generation switches and worker, thread, timeout and queue controls. Changes apply without restarting. Disabling video generation cancels queued and running generation. Accounts without saved preferences inherit the server defaults.

Cache paths, the Unix socket and the total cache-size limit are server configuration. A shared cached thumbnail is served only after a worker verifies access to the source as the requesting user.

## Archives

A compatible 7-Zip executable handles archive inspection, extraction and creation. Browse an archive preview before extracting, then review the destination and conflict choices. Password-protected operations prompt for the archive password; WebFile does not persist plaintext archive passwords.

Folder downloads are prepared as cancellable archive tasks. Download becomes available when creation finishes. Archive previews and extraction retain path-containment and no-overwrite protections; they do not bypass the user's Linux permissions.

If inspection or extraction fails, check the archive tool path, destination permissions and available space. See [troubleshooting](troubleshooting.md).
