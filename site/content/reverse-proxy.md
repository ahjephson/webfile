# Reverse proxy

WebFile can trust an authenticating reverse proxy that maps an asserted username to a permitted local Linux account. Authentication must cover every request, including WebSocket handshakes.

## Configure WebFile

Merge these settings into your complete configuration:

```json
"listen": "127.0.0.1:6201",
"auth": {
  "mode": "proxy",
  "pam_service": "webfile",
  "user_header": "X-Authenticated-User",
  "trusted_proxies": ["127.0.0.1/32", "::1/128"],
  "logout_url": "/_auth/logout",
  "cookie_secure": true
},
"tls": {
  "certificate_file": "",
  "private_key_file": ""
}
```

Only list actual direct proxy peers in `trusted_proxies`. WebFile rejects duplicate identity headers and resolves the asserted name as a local account. The account's real permissions govern file access.

Serve with the external URL so base paths and origin checks match the browser:

```bash
webfile serve -config /etc/webfile/webfile.json \
  --public-url https://files.example.com/tools/webfile
```

For a packaged service, supply command-line overrides through a systemd service override while preserving the service's existing user, sandbox and launcher configuration. Inspect the shipped `ExecStart` first with `systemctl cat webfile`.

## Example nginx routing

In nginx's `http` context, define the WebSocket connection mapping:

```nginx
map $http_upgrade $connection_upgrade {
    default upgrade;
    '' close;
}
```

In the TLS virtual host:

```nginx
location = /tools/webfile {
    return 308 /tools/webfile/;
}

location /tools/webfile/ {
    auth_request /_auth/check;
    auth_request_set $authenticated_user $upstream_http_x_authenticated_user;

    proxy_set_header X-Authenticated-User $authenticated_user;
    proxy_set_header Host $http_host;
    proxy_set_header Upgrade $http_upgrade;
    proxy_set_header Connection $connection_upgrade;

    proxy_pass http://127.0.0.1:6201;
}
```

The authentication provider and its `/_auth/check` and `/_auth/logout` locations are deployment-specific and are not defined by this example. Make the check location `internal`; return the username header only after successful authentication. Overwrite the identity header rather than appending to it. The `proxy_pass` has no trailing slash so nginx preserves the complete mounted path.

## Logout and request identity

WebFile keeps a local session for CSRF state and temporary elevation, while requiring the proxy identity on each request. Sign out removes the local session and elevated worker, then POSTs to `auth.logout_url` and follows the resulting redirect. Prefer a same-origin logout path routed by the proxy.

Keep the backend listener inaccessible except through the trusted proxy. WebFile's login limiter uses the direct peer address and does not trust forwarded client-address headers.
