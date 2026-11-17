# WebFile 1.10.0

WebFile 1.10.0 adds group-based authorization for root elevation and shared credentials for trusted reverse proxies. It prevents filesystem mutation races and fixes Monaco language workers and dialog focus restoration.

## Security fixes

- Require membership in an explicitly configured `auth.elevation_groups` group before PAM reauthentication can grant root elevation. The root launcher enforces the policy independently of the broker. An empty list disables elevation.
- Pin filesystem operations to open directory handles and reject substituted symlinks during uploads, transfers, editor saves, metadata changes and archive publication. Preserve configured-root containment and Linux filesystem permissions.
- Require a shared proxy credential as well as a trusted peer address for proxy authentication. Reject missing, incorrect or duplicate credentials and remove the credential before forwarding requests to filesystem workers.

## Hardening

- Apply a Content Security Policy to the application while preserving the stricter preview sandbox policy.
- Audit frontend development dependencies as part of release validation and add weekly Go and npm dependency updates.
- Document elevation authorization, proxy credentials, symlink containment and the associated deployment requirements.

## Fixes

- Load dedicated Monaco workers for JavaScript, TypeScript, JSON, CSS and HTML so language services and document formatting work without the worker startup error.
- Restore dialog focus after the close transition finishes, preserving focus deliberately moved elsewhere. Add regression coverage for the WebKit focus race.

## Deployment

Configuration changes are required for deployments using root elevation or proxy authentication:

- To allow root elevation, set `auth.elevation_groups` to the Linux groups authorized to become root. Review membership before enabling it. Restart WebFile to revoke existing elevation grants immediately.
- For proxy authentication, configure `auth.proxy_secret_file` with a private file containing a 64-character hexadecimal secret. Configure the trusted proxy to replace `X-WebFile-Proxy-Token` with that secret on every request, and retain the trusted proxy CIDR configuration. See the [reverse-proxy guide](https://github.com/ahjephson/webfile/blob/1.10.0/site/content/reverse-proxy.md).

Refresh open browser tabs after upgrading.

Release validation covers the production package, complete Chromium and WebKit suites, mounted application paths and package provenance. Clean-host installation, upgrade and physical-tablet checks remain manual validation tasks.

**Full changelog:** https://github.com/ahjephson/webfile/compare/1.9.2...1.10.0
