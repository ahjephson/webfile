# Troubleshooting

Start with the installed version, both service states and their recent logs:

```bash
webfile version
systemctl status webfile webfile-launcher
journalctl -u webfile -u webfile-launcher -n 100 --no-pager
```

Redact usernames, paths, credentials and private filenames before sharing logs. Reproduce problems using disposable files where possible.

## The service does not start

Check the configuration for unknown fields, missing roots and incompatible authentication/TLS settings. Production requires secure cookies and PAM with TLS, or the trusted proxy mode. Check that the `webfile` group can read the TLS key and that runtime directories and launcher sockets use the shipped ownership and permissions.

Do not run the broker as root or disable authentication to work around startup errors.

## Login fails

Confirm the username is a real local account permitted by the PAM policy and `runtime.min_user_uid`. After repeated attempts, allow the configured login block to expire. For proxy mode, check that the direct peer is trusted and the proxy supplies exactly one identity header for every request.

## Files are missing or access is denied

Check the configured root, exclusions and permissions as the affected user. That user needs traversal permission on parent directories as well as access to the file itself. A service account's access is not evidence that the logged-in user's worker can open the same path.

## Archives or thumbnails fail

Confirm `archive.executable` points to an installed compatible `7zz` or `7z`. Video thumbnails need `ffmpeg` and `ffprobe`. Check Settings for disabled generation, worker limits and timeouts. Verify the intended service accounts can use the cache path and socket. Check free space for extracted data and staging.

## A mounted path or live refresh fails

Check the external `--public-url`, proxy path preservation, original `Host` header and WebSocket upgrade headers. A proxy must authenticate WebSocket handshakes too. Browser origins must match the allowed external origin. Follow the [reverse proxy example](reverse-proxy.md).

WebFile watches visible directories and rereads their contents after invalidation. External nested changes outside active watches can take time to affect cached folder sizes; folder-size data has a five-minute freshness limit.

## Report a problem

For an ordinary bug, [open an issue](https://github.com/ahjephson/webfile/issues) with the version, operating system, browser, authentication mode, reproduction steps and expected result. Follow the [security policy](security.md) for vulnerabilities, without publishing exploit details or private data.
