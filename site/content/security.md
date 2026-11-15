# Security and permissions

WebFile accesses files using your Linux account. Signing in does not grant permissions that the account lacks on the server.

## Protect your installation

- Use HTTPS in the browser and keep secure cookies enabled.
- Use PAM login or a trusted proxy that authenticates every request.
- In proxy mode, allow only the proxy to reach WebFile's listener and list its direct addresses in `auth.trusted_proxies`.
- Keep TLS private keys restricted to the service account and administrators.
- Keep independent backups. Delete and overwrite operations can permanently change files.

Follow [authentication and TLS](authentication.md) or the [reverse proxy guide](reverse-proxy.md) to configure access. Use the latest published release for security fixes.

## Understand file access

The web service runs without root privileges. A separate launcher starts workers for permitted local accounts; each worker uses that account's real user ID and groups. A configured root sets where browsing begins, while Linux permissions determine which files the account can read or change.

WebFile checks file access before serving previews and cached thumbnails. Sessions store a hash of the browser token, and archive passwords are not stored in plaintext.

## Report a vulnerability

Use **Security → Report a vulnerability** on the [GitHub repository](https://github.com/ahjephson/webfile/security/advisories) when private reporting is available. Otherwise, open an issue asking for a private contact method without describing the vulnerability publicly.

Include your installed version, operating system, authentication mode and a reproduction using disposable files. Remove credentials, session cookies, private keys, personal filenames and unnecessary account or path details. See the [repository security policy](https://github.com/ahjephson/webfile/blob/main/docs/SECURITY.md) for the reporting policy.
