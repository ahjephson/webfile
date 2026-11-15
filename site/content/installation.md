# Install on Linux

Use the packaged Linux AMD64 release on a Debian or Ubuntu host with systemd. You need administrator access to install the services and a permitted local account to sign in.

## 1. Install runtime dependencies

Install a compatible `7zz` or `7z`, plus `ffmpeg` and `ffprobe` if you want video thumbnails. Debian and Ubuntu package names vary by distribution release. Confirm the installed tools:

```bash
command -v 7zz || command -v 7z
command -v ffmpeg
command -v ffprobe
```

The example configuration uses `/usr/bin/7zz`. Set `archive.executable` to your installed executable if its path differs. Node.js and a Go SDK are not required on the runtime host.

## 2. Download and verify

Download the archive and its matching checksum from the [release page](https://github.com/ahjephson/webfile/releases). For the release recorded in this repository:

```bash
version={{VERSION}}
curl --fail --location --remote-name \
  "https://github.com/ahjephson/webfile/releases/download/$version/webfile-$version-linux-amd64.tar.gz"
curl --fail --location --remote-name \
  "https://github.com/ahjephson/webfile/releases/download/$version/webfile-$version-linux-amd64.tar.gz.sha256"
sha256sum -c "webfile-$version-linux-amd64.tar.gz.sha256"
tar -xzf "webfile-$version-linux-amd64.tar.gz"
cd "webfile-$version-linux-amd64"
```

Continue only if verification succeeds.

## 3. Prepare configuration and TLS

The default installation uses PAM and serves HTTPS on port 8080. Prepare the configuration and a certificate/key pair valid for the address your browsers will use:

```bash
sudo install -d -m 0755 /etc/webfile
sudo install -m 0644 webfile.example.json /etc/webfile/webfile.json
sudo install -d -m 0700 /etc/webfile/tls
sudo install -m 0644 /path/to/webfile.crt /etc/webfile/tls/webfile.crt
sudo install -m 0600 /path/to/webfile.key /etc/webfile/tls/webfile.key
sudoedit /etc/webfile/webfile.json
```

Review `roots`, the archive executable and thumbnail paths. Keep `auth.cookie_secure` enabled. The configuration must contain no credentials: workers read it after switching to the user's identity.

The installer creates the `webfile` service account and group. It sets the TLS directory to `root:webfile` with mode `0750`. Before running it, allow the broker to read the private key once that group exists:

```bash
getent group webfile || sudo groupadd --system webfile
sudo chown root:webfile /etc/webfile/tls/webfile.key
sudo chmod 0640 /etc/webfile/tls/webfile.key
sudo ./deploy/install.sh ./webfile
```

For a trusted proxy deployment, configure the [proxy authentication mode](reverse-proxy.md) before starting services. The installer still checks for the default certificate files; install them as above even when the proxy listener will not use direct TLS.

## 4. Check the services

```bash
systemctl status webfile webfile-launcher
webfile version
```

Visit `https://your-host:8080` and sign in using a permitted local Linux account. The default minimum user UID is 1000. Filesystem permissions still determine which files that account can read or change.

If startup fails, use the [troubleshooting guide](troubleshooting.md). Do not switch a production installation to the unauthenticated development mode.

## Update an existing installation

Back up configuration and user data independently. Use HTTPS URLs for both the desired package and its matching checksum:

```bash
sudo webfile-update \
  'https://github.com/ahjephson/webfile/releases/download/{{VERSION}}/webfile-{{VERSION}}-linux-amd64.tar.gz' \
  'https://github.com/ahjephson/webfile/releases/download/{{VERSION}}/webfile-{{VERSION}}-linux-amd64.tar.gz.sha256'
```

The updater verifies the checksum, replaces the binary and shipped service files, restarts both services, and restores the prior installation if startup fails. It preserves `/etc/webfile/webfile.json`. Confirm the version and service status afterward.
