# Configuration

Installed services load `/etc/webfile/webfile.json`. Start with the [complete example configuration](https://github.com/ahjephson/webfile/blob/main/webfile.example.json); unknown JSON fields are rejected. Restart the services after editing server configuration.

## Choose roots

Roots are entry points into the filesystem. `{home}` expands to the logged-in user's home directory and `{user}` to their username.

```json
"roots": [
  { "name": "Home", "path": "{home}", "excluded_names": ["lost+found"] },
  { "name": "Media", "path": "/srv/media", "excluded_paths": ["/private/archive"] }
]
```

`excluded_names` matches exact, case-sensitive entry names anywhere below that root. `excluded_paths` matches exact, case-sensitive paths relative to the root and rejects direct access to those paths and their descendants. Neither option supports glob patterns. Linux permissions remain the access boundary.

## Server settings

| Setting | Purpose |
| --- | --- |
| `listen` | Broker address and port. The production example listens on `0.0.0.0:8080`. |
| `editor_max_bytes` | Maximum size accepted by the text editor; the example uses 5 MiB. |
| `auth` | Authentication mode, cookie policy, session lifetime and login limits. See [authentication](authentication.md). |
| `tls` | Certificate and private-key paths for direct HTTPS. |
| `runtime.worker_dir` | Per-user worker sockets and runtime state. |
| `runtime.data_dir` | Persistent application state; the package uses `/var/lib/webfile`. |
| `runtime.launcher_socket` | Local socket used by the broker to request workers. |
| `runtime.launcher_group` | Group allowed to communicate with the root launcher. |
| `runtime.min_user_uid` | Minimum permitted local user UID; defaults to 1000. |
| `archive.executable` | Compatible 7-Zip executable. |
| `watch.polling` | Polling fallback policy; defaults to `auto`. |
| `watch.poll_interval` | Poll interval; defaults to `5s`. |

Keep the broker unprivileged. Do not solve file-access errors by running it as root.

## Thumbnail defaults

`thumbnails` configures the shared cache and default generation settings. The example uses a 10 GiB cache at `/var/cache/webfile/thumbnails`, two image workers, two video workers, two FFmpeg threads per process, a 300-second video timeout and a queue limit of 200 videos.

Set `image_workers` or `video_workers` to zero to disable that kind by default. Users can override generation settings for their own account in Settings. Cache paths, the cache socket and total cache size remain server settings.

Video thumbnails require `ffmpeg` and `ffprobe`. The cache never grants access to a source file: workers must first open it using the requesting user's permissions.

## Listener and public URL overrides

Use `--public-url` to tell WebFile the address people use in their browsers. For a proxy listening on the same server:

```bash
webfile serve -config /etc/webfile/webfile.json \
  --http 127.0.0.1:6201 \
  --public-url https://files.example.com
```

`--http` cannot be combined with `--host` or `--port`. `--public-url` derives the base path and allowed external origin; it cannot be combined with `--base-path` or `--origins`. See the [reverse proxy guide](reverse-proxy.md) before using an HTTP backend listener in production.

To serve beneath a path, include it in the URL, for example `https://files.example.com/files/`, and configure the proxy to preserve that path.
