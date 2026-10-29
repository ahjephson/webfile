# Contributing to WebFile

WebFile is a Linux-first file manager built with Go and Vue. Contributions should
keep filesystem browsing immediate, respect Linux permissions and preserve the
broker, launcher and per-user worker boundary.

## Before making a change

Check existing issues and the [feature backlog](FEATURE_BACKLOG.md). Discuss
substantial features before implementation so their scope fits the project.
For a security issue, follow [SECURITY.md](SECURITY.md) rather than posting
exploitation details publicly.

Read [AGENTS.md](AGENTS.md) for architecture invariants and the Windows/WSL
workflow. All contributors should use a native Linux workspace for Linux builds
and tests. The Windows-specific directory examples apply to that host setup;
ordinary Linux contributors can work directly in their clone.

## Local setup

Use the Go toolchain declared in `backend/go.mod` and Node.js 22 or later. The
Linux production build also needs a C compiler and PAM development headers.
Install 7-Zip for archive tests and FFmpeg for video-thumbnail tests.

The [development instructions](README.md#development) start an HTTP development
build with authentication disabled on loopback. Development and production
configurations are deliberately incompatible; do not weaken those checks.
Never use real credentials, customer files or private configuration as fixtures.

## Validation

Run the checks that cover your change, then the relevant complete checks:

```bash
go -C backend test ./...
go -C backend vet ./...
npm --prefix web ci --no-audit --no-fund
npm --prefix web run build
```

With Linux production dependencies installed, `make test-production` checks the
production/PAM build. Browser-visible changes need Playwright coverage and a
visual check. The shared browser scripts start isolated development servers:

```bash
bash scripts/validate-browser.sh chromium-touch
bash scripts/validate-browser.sh webkit-ipad
```

Run those scripts sequentially when they share a workspace. The
[release checklist](RELEASE_CHECKLIST.md) defines the additional audit,
packaging and provenance gates used before publication.

## Pull requests

Explain the problem, the resulting behaviour and how it was validated. Include
screenshots for visible UI changes and note any checks you could not run.
Keep changes focused and add regression coverage for meaningful bugs. Do not
commit generated frontend bundles, local configuration, credentials, runtime
databases or dependency directories.

Use conventional commit subjects such as `fix:`, `feat:` or `docs:`. Contributions
are provided under this repository's [MIT license](LICENSE); preserve dependency
licenses and attribution when adding or updating libraries.

After a dependency or Go toolchain update, install the locked dependencies and
run `python3 scripts/generate-notices.py` from the repository root. Review and
commit the generated `THIRD_PARTY_NOTICES.txt`; packaging checks that it matches
the runtime dependencies and bundled stylesheet.
