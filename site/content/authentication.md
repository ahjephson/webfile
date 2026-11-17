# Authentication and TLS

Choose where users sign in. WebFile can check local account credentials through PAM, or accept an authenticated username from a trusted proxy. Both modes require HTTPS in the browser and secure cookies.

## PAM with direct TLS

Use `auth.mode: "pam"`, `auth.cookie_secure: true`, and a TLS certificate/private-key pair. The package installs a PAM policy at `/etc/pam.d/webfile`. Users authenticate against local Linux accounts permitted by that policy and the launcher's minimum UID setting.

```json
"auth": {
  "mode": "pam",
  "pam_service": "webfile",
  "cookie_secure": true,
  "session_ttl": "12h",
  "elevation_ttl": "5m",
  "elevation_groups": [],
  "login_max_attempts": 5,
  "login_window": "1m",
  "login_block": "5m"
},
"tls": {
  "certificate_file": "/etc/webfile/tls/webfile.crt",
  "private_key_file": "/etc/webfile/tls/webfile.key"
}
```

These fields are a section of the full configuration, not a complete file. The certificate must cover the DNS names or IP addresses used by clients. Browsers must trust its issuer. Keep the private key owned by `root:webfile` with mode `0640` inside a `0750` TLS directory.

## Trusted proxy authentication

Use `auth.mode: "proxy"` when a trusted reverse proxy authenticates every request and supplies a local username. Declare trusted direct-peer CIDRs, configure a private `auth.proxy_secret_file`, and prevent direct access to the backend. The proxy must overwrite both the identity and credential headers on every request; loopback addresses alone do not identify the proxy. Without direct TLS, the WebFile listener must bind to a loopback IP address.

Follow the [reverse proxy guide](reverse-proxy.md) for the identity header, mounted paths, WebSockets and logout configuration.

## Sessions and elevation

WebFile stores a SHA-256 hash of each browser session token. Persistent sessions survive broker restarts. Cookies are HttpOnly and SameSite=Strict; production requires the Secure flag. Mutation requests use a per-session CSRF token and same-origin checks.

Elevation grants root filesystem authority within the configured roots. It is disabled by default: `auth.elevation_groups` is empty. To enable it, an administrator must list a dedicated group, such as `["webfile-admins"]`, in the root-owned configuration and add only accounts trusted with root access to that group. The launcher checks current group membership and the user’s PAM password before starting a root worker. Unknown groups and failed lookups deny elevation. Keep the configuration and its parent directory unwritable by ordinary users.

Elevation expires after `auth.elevation_ttl`. Removing a user from the group prevents new grants; terminate existing elevation by restarting the launcher if immediate revocation is required. In proxy mode, every request must also carry the proxy identity and credential. The broker remains unprivileged.

## Use a server release for deployment

Install a release package for a server deployment. Authentication-free development builds are intended for local testing and must not be exposed publicly.
