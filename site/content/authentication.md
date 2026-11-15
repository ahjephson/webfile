# Authentication and TLS

Production builds support two authentication modes. Both require secure cookies. The HTTP, authentication-free development build is deliberately incompatible with production settings.

## PAM with direct TLS

Use `auth.mode: "pam"`, `auth.cookie_secure: true`, and a TLS certificate/private-key pair. The package installs a PAM policy at `/etc/pam.d/webfile`. Users authenticate against local Linux accounts permitted by that policy and the launcher's minimum UID setting.

```json
"auth": {
  "mode": "pam",
  "pam_service": "webfile",
  "cookie_secure": true,
  "session_ttl": "12h",
  "elevation_ttl": "5m",
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

Use `auth.mode: "proxy"` when a trusted reverse proxy authenticates every request and supplies a local username. Declare trusted direct-peer CIDRs and prevent direct access to the backend. Without direct TLS, the WebFile listener must bind to a loopback IP address.

Follow the [reverse proxy guide](reverse-proxy.md) for the identity header, mounted paths, WebSockets and logout configuration.

## Sessions and elevation

WebFile stores a SHA-256 hash of each browser session token. Persistent sessions survive broker restarts. Cookies are HttpOnly and SameSite=Strict; production requires the Secure flag. Mutation requests use a per-session CSRF token and same-origin checks.

Temporary elevation is explicit and time-limited. In proxy mode, the proxy identity must still accompany requests; PAM remains available for the elevation prompt. An elevated worker does not make the ordinary broker privileged.

## Keep development local

Development builds accept plain HTTP with `auth.mode: "none"`. They reject PAM or TLS settings. Use the checked-in loopback development configuration on a trusted local machine, with disposable test files. Never expose this mode publicly.
