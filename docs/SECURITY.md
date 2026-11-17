# Security policy

WebFile reads and modifies files using real Linux user identities. Treat its authentication, launcher, worker isolation, path handling and archive processing as security-sensitive components.

## Reporting a vulnerability

Use **Security → Report a vulnerability** on GitHub when private reporting is available. If that option is unavailable, open an issue requesting a private contact method without including the vulnerability or exploit details. Do not post credentials, session cookies, archive passwords, private keys or private files in an issue, pull request or workflow artifact.

Include the WebFile version, operating system, authentication mode, a minimal reproduction using disposable files and the expected security impact. Redact account names, internal hostnames and filesystem paths that are not needed to understand the report. Give the maintainer an opportunity to investigate before publishing exploit details.

## Versions

Use the latest published release. Fixes are developed on `main` and included in subsequent releases; older release branches are not maintained separately.

## Deployment boundary

- Production requires PAM with direct TLS, or a trusted authenticating reverse proxy with secure cookies. Keep trusted proxy CIDRs explicit and prevent clients from reaching a proxy-authenticated listener directly. Proxy mode also requires a private shared credential on every request; a loopback source address is insufficient.
- The broker is unprivileged. The root launcher may only start validated local users' workers; it is not a general command runner or filesystem proxy.
- Ordinary workers use the logged-in user’s UID, GID and supplementary groups. A configured root does not grant access beyond that user’s Linux permissions. Root workers require explicit membership in `auth.elevation_groups` and PAM reauthentication; the empty default disables elevation. Treat membership as a grant of root authority.
- Filesystem mutations retain descriptor-based confinement. Canonical paths are traversed without following symlinks substituted after resolution, including during publication, metadata changes and cleanup.
- The HTTP, authentication-free development build is for a trusted local machine. Do not expose it publicly or reuse that configuration for production.
- Keep TLS keys and local configuration outside version control. Back up data independently: delete and overwrite operations can permanently change files.

The automated checks cover known protections and regressions. They are not an independent security certification or a substitute for reviewing a deployment's permissions, proxy configuration and network exposure.
