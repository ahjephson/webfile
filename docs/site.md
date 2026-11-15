# Product page and documentation site

The website source lives in `site/`. The product page is static HTML/CSS/JavaScript in `site/website/`, and the searchable documentation uses MkDocs Material with pages in `site/content/`.

The canonical public address is `https://ahjephson.dev/webfile/`, with documentation under `/webfile/docs/`. GitHub Pages hosts the site at `https://ahjephson.github.io/webfile/`. Both use the same project path.

## Build and preview

Use a native Linux workspace. Windows-hosted agents must follow the dry-run and apply sync procedure in [development.md](development.md) before running these commands in the WSL mirror:

```bash
python3 -m venv dist/docs-venv
dist/docs-venv/bin/python -m pip install -r site/requirements.txt
dist/docs-venv/bin/python -m unittest discover -s site -p 'test_*.py'
dist/docs-venv/bin/python site/build.py
```

The build writes only to `dist/site` and temporary staging. It publishes the user guides in `site/content/` and validates local links, assets and fragment targets using the `/webfile/` project prefix. Repository development and release guidance stays in `docs/`; it is not copied into the website. Generated output is not committed.

To preview the same project prefix locally:

```bash
mkdir -p dist/pages-preview
ln -sfn ../site dist/pages-preview/webfile
python3 -m http.server 8123 --bind 127.0.0.1 --directory dist/pages-preview
```

Open `http://127.0.0.1:8123/webfile/` in the Codex app's in-app browser. Check the product page at desktop and phone widths, the navigation menu, copy button, documentation search, theme switch and installation links.

## Maintain the site

Edit product copy and styles in `site/website/`; edit user guides in `site/content/`. Keep maintainer instructions and contribution policies in the repository. Review user guides for unnecessary implementation details and assumptions about a particular distribution or authentication provider. Keep installation instructions consistent with the actual installer, configuration schema and service files. The product page uses an unretouched screenshot of the running development app with fictional files. Capture provenance and reproduction instructions are in `site/website/assets/README.md`; `site/demo.py` prepares an isolated fixture workspace without exposing personal folders or account details. Asset README files are not published.

The user guides contain no pinned application release numbers. Product download and release links lead to the latest GitHub release; versioned release notes remain on GitHub.

## Publication

Pages uses **Settings → Pages → Source → GitHub Actions**. The `github-pages` deployment environment is restricted to `main`.

The personal domain is owned by the separate infrastructure deployment for `ahjephson.dev`. Its Worker proxies `/webfile/` and its descendants to the GitHub Pages origin, preserving the complete path and query, and redirects `/webfile` to `/webfile/`. Other personal-site routes retain their existing handling. Do not add an apex `CNAME` to this repository or point the domain's DNS at this project: that would take over the personal site's domain. Release domain routing changes through the infrastructure repository's existing process. Canonical URLs and the sitemap use `ahjephson.dev`.

`.github/workflows/pages.yml` builds and validates the site for relevant pull requests and main-branch changes, and deploys only `main`. It uses GitHub-hosted runners and skips all jobs while the repository is private, preserving the private-runner cost policy. A manual run from `main` can publish the initial site after Pages is enabled. The site workflow is separate from application release validation and does not modify release assets or tags.

Pushes remain subject to the repository's existing authorization rules. Enabling Pages, publishing and changing repository visibility are separate launch actions.
