# Install on Linux

WebFile runs on Linux and uses local accounts and filesystem permissions. Choose a package that matches your processor and check the requirements before installing.

## Requirements

| Component | Requirement |
| --- | --- |
| Processor | The prebuilt package targets AMD64 (x86-64). |
| System libraries | Compatible glibc and Linux-PAM libraries. |
| Service manager | The supplied installer and service files use systemd. |
| Local accounts | Users must have permitted Linux accounts. |
| Archives | Install a compatible `7zz` or `7z`. |
| Video thumbnails | Install `ffmpeg` and `ffprobe` if you want this feature. |
| Browser access | HTTPS, JavaScript, cookies and WebSockets. |

WebFile is not restricted to Debian or Ubuntu. The supplied PAM policy uses their `common-auth` and `common-account` conventions; adapt it to your distribution before installation if those policies are absent. Other architectures, C libraries or service managers require a source build or deployment changes; see the [repository build guide](https://github.com/ahjephson/webfile#production-build).

Node.js and a Go toolchain are not required to run the prebuilt package.

## Download and verify

Download the Linux archive and its matching SHA-256 file from the [latest release](https://github.com/ahjephson/webfile/releases/latest) into the same empty directory. From that directory, run:

```bash
sha256sum -c webfile-*-linux-amd64.tar.gz.sha256 &&
tar -xzf webfile-*-linux-amd64.tar.gz &&
cd webfile-*-linux-amd64
```

The checksum must report `OK` before you continue.

## Prepare the installation

Check the installed tools:

```bash
command -v 7zz || command -v 7z
command -v ffmpeg
command -v ffprobe
```

Set `archive.executable` in the configuration to the installed 7-Zip path. Review `deploy/webfile.pam` and adjust its authentication and account rules to your distribution's PAM policy. The installer copies this file to `/etc/pam.d/webfile`.

The default setup uses PAM login and serves HTTPS on port 8080. Install a certificate valid for the hostname you will use, then prepare the configuration:

```bash
getent group webfile || sudo groupadd --system webfile
sudo install -d -m 0755 /etc/webfile
sudo install -m 0644 webfile.example.json /etc/webfile/webfile.json
sudo install -d -o root -g webfile -m 0750 /etc/webfile/tls
sudo install -o root -g webfile -m 0644 /path/to/webfile.crt /etc/webfile/tls/webfile.crt
sudo install -o root -g webfile -m 0640 /path/to/webfile.key /etc/webfile/tls/webfile.key
sudoedit /etc/webfile/webfile.json
```

Replace the certificate source paths with your own. Review the configured roots and tool paths, and keep `auth.cookie_secure` enabled. The configuration is readable by local users, so keep credentials and private-key contents out of it.

For proxy authentication, use the settings in the [reverse proxy guide](reverse-proxy.md). The installer currently checks for the default TLS files even in proxy mode, so provide them before running it.

## Install and sign in

```bash
sudo ./deploy/install.sh ./webfile
systemctl status webfile webfile-launcher
webfile version
```

The installer creates the service account, installs the services and starts them. Open `https://your-host:8080` and sign in with a permitted local account. The default minimum user UID is 1000.

If the service or login fails, check [troubleshooting](troubleshooting.md) before changing permissions or authentication settings.

## Update an installation

Back up your configuration and user data. On the [release page](https://github.com/ahjephson/webfile/releases/latest), copy the HTTPS links for the package and its matching checksum, then substitute them below:

```bash
sudo webfile-update '<package-url>' '<checksum-url>'
```

The updater verifies the checksum, installs the binary and shipped service files, and restarts the services. If startup fails, it restores the previous installation. It preserves `/etc/webfile/webfile.json`.

Review the shipped PAM policy before updating on a distribution that needs a custom policy: the updater replaces `/etc/pam.d/webfile` with the one from the package.
