# Product page and documentation site

The website source lives in `site/`. The product page is static HTML/CSS/JavaScript in `site/website/`, and the searchable documentation uses MkDocs Material with pages in `site/content/`.

The canonical public address will be `https://ahjephson.dev/webfile/`, with documentation under `/webfile/docs/`. The GitHub Pages origin will be `https://ahjephson.github.io/webfile/`. Both use the same project path. Publication remains deferred until the repository becomes public.

## Build and preview

Use a native Linux workspace. Windows-hosted agents must follow the dry-run and apply sync procedure in [development.md](development.md) before running these commands in the WSL mirror:

```bash
python3 -m venv dist/docs-venv
dist/docs-venv/bin/python -m pip install -r site/requirements.txt
dist/docs-venv/bin/python -m unittest discover -s site -p 'test_*.py'
dist/docs-venv/bin/python site/build.py
```

The build writes only to `dist/site` and temporary staging. It reads the current version and release notes from `releases/`, copies shared security/contribution/development/release guides from `docs/`, and validates local links, assets and fragment targets using the `/webfile/` project prefix. Generated output is not committed.

To preview the same project prefix locally:

```bash
mkdir -p dist/pages-preview
ln -sfn ../site dist/pages-preview/webfile
python3 -m http.server 8123 --bind 127.0.0.1 --directory dist/pages-preview
```

Open `http://127.0.0.1:8123/webfile/` in the Codex app's in-app browser. Check the product page at desktop and phone widths, the navigation menu, copy button, documentation search, theme switch and installation links.

## Maintain the site

Edit product copy and styles in `site/website/`; edit user guides in `site/content/`. Shared maintainer policies remain authoritative in `docs/` and are copied into the site during the build. Keep installation instructions consistent with the actual installer, configuration schema and service files. The illustrated file workspace on the product page is labelled as an illustration and uses fictional files.

The documentation describes the current source tree and shows the release marker. It does not maintain separate documentation snapshots for older releases; versioned release notes remain available on GitHub.

## Publish at the public launch

After the repository becomes public, configure **Settings → Pages → Source → GitHub Actions**. Restrict the `github-pages` deployment environment to `main`.

The personal domain is owned by the separate infrastructure deployment for `ahjephson.dev`. At launch, arrange for `/webfile/` and its descendants to proxy to the GitHub Pages origin while preserving the complete path and query; redirect `/webfile` to `/webfile/`. Keep the personal site's other routes intact. Do not add an apex `CNAME` to this repository or change the domain's DNS to point at this project: that would take over the personal site's domain instead of mounting a project path. Domain routing must be reviewed and released through the infrastructure repository's existing process. The generated canonical URLs and sitemap already use `ahjephson.dev`.

`.github/workflows/pages.yml` builds and validates the site for relevant pull requests and main-branch changes, and deploys only `main`. It uses GitHub-hosted runners and skips all jobs while the repository is private, preserving the private-runner cost policy. A manual run from `main` can publish the initial site after Pages is enabled. The site workflow is separate from application release validation and does not modify release assets or tags.

Pushes remain subject to the repository's existing authorization rules. Enabling Pages, publishing and changing repository visibility are separate launch actions.
