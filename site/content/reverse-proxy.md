# Reverse proxy

Use a reverse proxy to publish WebFile at a hostname or path. In the nginx example below, users still sign in through WebFile's PAM login. nginx forwards requests and WebSocket connections; it needs no separate authentication provider.

## Configure WebFile

Keep `auth.mode: "pam"`, secure cookies and the TLS certificate settings from [installation](installation.md). Bind WebFile to a local address:

```json
"listen": "127.0.0.1:8080"
```

WebFile's PAM mode requires HTTPS between nginx and WebFile as well as between the browser and nginx.

For the installed systemd service, run `sudo systemctl edit webfile` and add:

```ini
[Service]
ExecStart=
ExecStart=/usr/local/bin/webfile serve -config /etc/webfile/webfile.json --public-url https://files.example.com
```

Replace `files.example.com` with the address people will use, then restart:

```bash
sudo systemctl restart webfile-launcher webfile
```

## Configure nginx

Install nginx and a TLS certificate for your public hostname. nginx must also trust the issuer of WebFile's certificate. In this example, `webfile-ca.pem` contains the trusted CA certificate chain, and WebFile's certificate is valid for `files.example.com`.

Add this configuration inside nginx's `http` context. Replace the hostname and certificate paths with your own:

```nginx
map $http_upgrade $connection_upgrade {
    default upgrade;
    '' close;
}

server {
    listen 443 ssl;
    server_name files.example.com;

    ssl_certificate /etc/nginx/tls/files.crt;
    ssl_certificate_key /etc/nginx/tls/files.key;

    location / {
        client_max_body_size 16m; # Allow WebFile's chunked upload requests.
        proxy_http_version 1.1;
        proxy_set_header Host $http_host;
        proxy_set_header Upgrade $http_upgrade;
        proxy_set_header Connection $connection_upgrade;

        proxy_ssl_server_name on;
        proxy_ssl_name files.example.com;
        proxy_ssl_verify on;
        proxy_ssl_trusted_certificate /etc/nginx/tls/webfile-ca.pem;

        proxy_pass https://127.0.0.1:8080;
    }
}
```

Check the configuration before reloading nginx:

```bash
sudo nginx -t && sudo systemctl reload nginx
```

Open your public address and sign in to WebFile. Check browsing, an upload and live folder updates. If nginx cannot verify WebFile's certificate, correct the trusted CA or certificate name rather than disabling verification.

## Serve beneath a path

To use `https://files.example.com/files/`, set that full address in `--public-url` and change the nginx location to `/files/`. Keep `proxy_pass https://127.0.0.1:8080;` without a path suffix so nginx forwards the complete request path. Redirect `/files` to `/files/` if you want both forms to work.

## Let a proxy handle authentication

Use this option when you already have an authenticating proxy or SSO gateway and want it to sign users in. Change these WebFile authentication settings:

```json
"auth": {
  "mode": "proxy",
  "pam_service": "webfile",
  "user_header": "X-Authenticated-User",
  "trusted_proxies": ["127.0.0.1/32"],
  "proxy_secret_file": "/etc/webfile/proxy-token",
  "cookie_secure": true
}
```

Configure the proxy according to its authentication provider's instructions. It must:

- Authenticate every request, including WebSocket handshakes.
- Replace `X-Authenticated-User` with the authenticated user's local Linux username.
- Replace `X-WebFile-Proxy-Token` with the private proxy credential. Never forward a client-supplied value.
- Prevent clients from reaching WebFile directly.

A loopback IP allowlist cannot distinguish nginx from other local processes. WebFile therefore also requires a proxy credential. Generate it without displaying it:

```bash
sudo sh -c 'umask 027; openssl rand -hex 32 > /etc/webfile/proxy-token'
sudo chown root:webfile /etc/webfile/proxy-token
sudo chmod 0640 /etc/webfile/proxy-token
```

Keep the file and its parent directory protected from untrusted users. WebFile rejects public, writable-by-group, symlink and malformed credential files. The file must contain 64 hexadecimal characters, optionally followed by a newline.

For nginx, create a root-only include containing the header directive:

```bash
sudo sh -c 'umask 077; printf "proxy_set_header X-WebFile-Proxy-Token %s;\n" "$(cat /etc/webfile/proxy-token)" > /etc/nginx/webfile-proxy-token.conf'
```

Add `include /etc/nginx/webfile-proxy-token.conf;` inside the authenticating `location` alongside its identity-header directive. Use the same protection for WebSocket requests. Keep the credential out of browser responses, URLs and logs. After rotation, regenerate the include and restart WebFile and reload nginx. Existing proxy installations must configure this credential before upgrading; startup fails when it is absent.

Set `trusted_proxies` to the proxy's actual direct-peer addresses. WebFile resolves the supplied username as a local account and uses that account's Linux permissions.

You can keep the HTTPS backend shown above. Proxy mode also allows HTTP on a loopback listener if you remove WebFile's TLS settings and update the proxy's upstream URL to match. Browser access must still use HTTPS.

Configure `auth.logout_url` only if the provider supplies a logout endpoint that accepts a browser POST. WebFile clears its local session and then POSTs to that endpoint; signing out of the provider depends on its logout support.

For directive details, see nginx's [proxy module](https://nginx.org/en/docs/http/ngx_http_proxy_module.html) and [WebSocket proxying](https://nginx.org/en/docs/http/websocket.html) documentation.
