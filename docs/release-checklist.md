# Release checklist

Use this checklist for every stable WebFile release. A tagged release should be built from a clean `main` commit and use the same bare semantic version in the tag, package name and embedded binary metadata.

## Local validation comes first

During development, use the narrow checks relevant to the changed area:

- CI/checklist changes: workflow lint, Bash syntax checks and the package provenance tests. Run a focused browser check when changing its runner.
- Backend changes: tests for the affected Go package, plus related integration coverage. Archive changes need the real 7-Zip tests.
- Search, sorting or conflicts: select the corresponding `web/tests/e2e/*.spec.ts` file in each relevant browser.
- Gallery, navigation, editor or settings: use a focused `--grep` selection until those areas are split into separate feature files or tagged groups.

For example, after dependencies are installed:

```bash
bash scripts/validate-browser.sh chromium-touch tests/e2e/search.spec.ts
bash scripts/validate-browser.sh webkit-ipad --grep 'gallery'
```

These runs are labelled focused and also exercise the mounted-path smoke check. They are iteration checks, not a complete release pass. Without selectors or a shard setting, the default invocation runs the whole project. CI runs all four shards for each browser, and every shard is required. Keep the full local pass until source edits are finished and the candidate is ready to push. Documentation-only edits after a pass need documentation review, not another complete application test/build cycle.

Before pushing a candidate to CI, committing a release version, or creating a tag, run the complete checks locally. Focused tests are useful during development but do not replace this release pass. Record the exact source tree, commands and results; rerun affected checks after any source change. Agents must perform these operations, not use GitHub Actions as their first validation environment. Run workflow lint and Bash checks as well when editing the runner/workflows.

For Windows-hosted development, inspect the authoritative checkout, confirm the native WSL mirror has no `.git`, then preview and apply `scripts/sync-to-wsl.ps1` as described in [the development guide](development.md). Preserve unrelated changes. Run all Linux commands below from `$HOME/code/webfile` inside WSL, never `/mnt/c`.

```bash
python3 -m unittest discover -s scripts -p 'test_*.py'
COMMIT=local-validation bash scripts/validate-production.sh
cd web
npx playwright install --with-deps chromium webkit
cd ..
bash scripts/validate-browser.sh chromium-touch
bash scripts/validate-browser.sh webkit-ipad
```

Supply `COMMIT` explicitly because the WSL mirror has no Git metadata. A local label such as `local-validation` is appropriate for an uncommitted working tree; only CI records a full source SHA in a publishable package manifest. Run the two browser scripts sequentially in the shared WSL mirror. They each build a development HTTP/non-auth server, use isolated fixture and persistence data, run their entire project plus the mounted-path smoke check, and stop the server. Their default ports are 8094 and 8095; override `WEBFILE_E2E_PORT` if necessary.

CI divides each browser suite into four parallel jobs on separate runners, with one Playwright worker and an independent application instance per shard. Tests are distributed individually, so the large interaction file is divided too. Run `WEBFILE_E2E_SHARD=2/4 bash scripts/validate-browser.sh webkit-ipad` to reproduce one shard locally; omit the variable for the complete local suite. Run shards sequentially in the shared local mirror because their frontend builds share generated assets. Shard logs and diagnostics are isolated under `dist/validation/<project>/shard-<index>-of-<total>/`, including separate output directories for the main tests and mounted-path smoke check. Each CI shard has its own diagnostic artifact, and every shard must pass before publication.

Inspect the final output and any affected UI before pushing. Browser diagnostics are retained under `dist/validation/<project>/` in WSL, including JSON reports with individual test durations. Browser scripts expect frontend dependencies already installed by production validation or `npm --prefix web ci`.

Local production validation includes development tests/vet, production/PAM tests/vet, frontend build/audit, source/binary vulnerability scans, package checksum and embedded version/commit checks. Install the Linux dependencies listed in [the development guide](development.md) first. Python 3.11 or later is required by package provenance verification.

## Automated release gates

- Install Go and frontend dependencies from the committed module and lock files.
- Run all Go tests and vet checks with the production and PAM build tags.
- Run `govulncheck` against the Go source and dependency graph.
- Audit production frontend dependencies; high and critical findings block the release. Review development-only findings separately because they are not shipped in the browser bundle.
- Build the frontend and exact production binary intended for release.
- Scan the compiled production binary with `govulncheck -mode=binary`.
- Run real 7-Zip integration coverage, including encrypted extraction, an incorrect-password retry, progress, extracted content and archive creation.
- Run the complete Playwright suite in touch-enabled Chromium and WebKit.
- Run the browser path-mount smoke test with a non-root `--public-url`.
- Publish a SHA-256 checksum beside the release archive.

`Validate release candidate` runs on branch pushes and pull requests, with independent Production package, Chromium and WebKit jobs. The browser jobs each have their own server and state. Production and all eight browser shards must pass. Retry only a failed job after examining its named test output, screenshots, traces and server logs; do not blindly restart the entire pipeline or add retries that hide failures.

The production job uploads the archive, checksum and `release-candidate.json` provenance manifest under `release-candidate-<full-commit-SHA>`, retained for 30 days. A package is publishable only after the entire validation run succeeds for a push or manual validation of `main` at that exact commit. A PR or feature-branch validation does not replace validation of the final merged `main` commit.

The release workflow only downloads and verifies that tested package. It checks the tag/version, source SHA and archive/checksum hashes and publishes the same bytes without rebuilding. Missing, failed, expired or mismatched validation blocks publication. If an artifact has expired, rerun validation for the same `main` commit before publishing; do not move the tag or increment a version just to retry infrastructure. Published assets are not overwritten by this workflow.

## Manual release checks

- Install the package on a clean supported Debian or Ubuntu AMD64 host.
- Confirm PAM login, logout, temporary elevation and direct TLS.
- Upgrade an existing installation from the previous supported release and confirm that configuration, sessions, preferences, task history and thumbnail cache remain usable.
- Force an update startup failure and confirm that `webfile-update` restores the previous installation.
- Confirm configured `7zz`/`7z`, `ffmpeg`, `ffprobe`, data and cache paths are usable by their expected service accounts.
- Exercise browse, upload, download, copy, move, delete, edit, preview, archive, task cancellation and WebSocket refresh workflows.
- On a physical tablet, exercise touch selection, double-tap opening, press-and-hold actions, gallery swipes, fullscreen/immersive transitions and the copy/move destination browser.
- Verify the Settings About section and `webfile version` show the expected release version, commit and build date.
- Review the release notes for configuration changes, external dependencies, supported platforms and known limitations.

## Release hygiene

- Confirm the working trees are clean and the release commit is on `main`.
- Set `releases/RELEASE_VERSION` to the intended bare version and create the matching release notes before final validation. Do not create a tag yet.
- Run the complete local release checks above and inspect `dist/webfile-<version>-linux-amd64.tar.gz`, its checksum and binary metadata.
- Create and review `releases/RELEASE_NOTES_<version>.md`; commit it before tagging so the workflow can use it as the GitHub Release description.
- Push the candidate commit to `main` only when the user authorizes a push. Wait for production and every browser shard to succeed at that exact commit. Inspect the candidate artifact and provenance before tagging. If checks fail, diagnose and fix them, validate locally, then validate the revised commit in CI.
- Create an annotated bare semantic-version tag, without a `v` prefix, pointing to the validated commit. Push the tag separately from `main` from the authoritative Windows checkout. Never push the candidate and tag together.
- For an already validated tag whose publication was interrupted, manually run **Release Linux binary** with that exact tag. This process applies to commits containing the new validation workflow and `releases/RELEASE_VERSION`; older releases do not have candidate artifacts in this format.
- Confirm publication succeeded, both assets are present, and their hashes match the tested candidate manifest. The publication job must not rebuild.
