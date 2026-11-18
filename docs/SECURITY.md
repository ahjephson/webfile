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

## Temporary dependency exception

On 18 November 2026, the maintainer approved a scoped exception for [GHSA-fc5q-39xc-vjpf](https://github.com/advisories/GHSA-fc5q-39xc-vjpf), a regular-expression denial of service in picomatch through 4.0.7. The latest published version is 4.0.7 and the advisory lists no patched release. The exception expires on 18 December 2026 UTC, or must be removed earlier when a patched dependency becomes available.

The affected operation compiles attacker-supplied glob patterns. In this checkout, picomatch is reached through Vite's build filters and import globs, tinyglobby/fdir's build-time file discovery, and vue-tsc/@vue/language-core's project matching. These patterns come from the repository's source, configuration and build tooling. Building an untrusted checkout already executes that checkout's build configuration; CI must continue to use isolated workers without production secrets. Do not accept external glob patterns in these build steps or expose the development server publicly.

The installed dependency tree places picomatch entirely in development dependencies; it is absent from `npm ls --omit=dev picomatch`. Production uses the Go server and the compiled Vue assets, not Vite or vue-tsc. Files and search patterns submitted to the running file manager do not enter the Node build process. This exposure assessment, rather than development-only classification by itself, is the reason for the exception.

The audit wrapper in `scripts/audit-frontend.py` permits only this advisory and its transitive reports for the exact development-only package versions currently locked: picomatch 4.0.7, @vue/language-core 3.3.14, vue-tsc 3.3.14, fdir 6.5.0, tinyglobby 0.2.17 and Vite 6.4.6. It continues to reject other high/critical advisories, a critical reclassification, dependency-version or production-scope changes, incomplete reports, audit errors and expiration. The exception remains visible in local and CI output. Review it when dependency updates arrive, remove it with the upstream fix, and reassess before changing how build patterns are supplied.
