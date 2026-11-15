#!/usr/bin/env python3
"""Build the product page and searchable docs without changing repository sources."""

from __future__ import annotations

import html
from pathlib import Path
import re
import shutil
import subprocess
import sys
import tempfile
import xml.etree.ElementTree as ET

import yaml

ROOT = Path(__file__).resolve().parent.parent
SOURCE = ROOT / "site"
OUTPUT = ROOT / "dist" / "site"
PUBLIC_URL = "https://ahjephson.dev/webfile/"
REPOSITORY_URL = "https://github.com/ahjephson/webfile"
GUIDES = {
    "SECURITY.md": "security.md",
    "CONTRIBUTING.md": "contributing.md",
    "development.md": "development.md",
    "release-checklist.md": "release-checklist.md",
}


def guide_links(text: str) -> str:
    """Keep shared guides readable on GitHub and in the generated documentation."""
    def replace(match: re.Match) -> str:
        label, target = match.groups()
        if ":" in target or target.startswith("#"):
            return match.group(0)
        path, separator, anchor = target.partition("#")
        if path in GUIDES:
            destination = GUIDES[path]
        else:
            resolved = (ROOT / "docs" / path).resolve().relative_to(ROOT)
            destination = f"{REPOSITORY_URL}/blob/main/{resolved.as_posix()}"
        return f"[{label}]({destination}{separator}{anchor})"

    return re.sub(r"\[([^\]]+)\]\(([^\s)]+)\)", replace, text)


def main() -> None:
    version = (ROOT / "releases" / "RELEASE_VERSION").read_text().strip()
    if not re.fullmatch(r"[0-9]+\.[0-9]+\.[0-9]+", version):
        raise ValueError("Expected a bare release version in releases/RELEASE_VERSION")
    if OUTPUT.is_symlink() or OUTPUT.resolve() != ROOT / "dist" / "site":
        raise ValueError("Site output must stay inside the repository's dist/site directory")
    if OUTPUT.exists():
        shutil.rmtree(OUTPUT)
    OUTPUT.mkdir(parents=True, exist_ok=True)
    shutil.copytree(SOURCE / "website", OUTPUT, dirs_exist_ok=True)
    (OUTPUT / "assets").mkdir(exist_ok=True)
    shutil.copyfile(ROOT / "web" / "public" / "favicon.svg", OUTPUT / "assets" / "webfile.svg")
    for file in OUTPUT.glob("*.html"):
        text = file.read_text(encoding="utf-8")
        text = text.replace("{{VERSION}}", html.escape(version)).replace("{{PUBLIC_BASE}}", PUBLIC_URL)
        file.write_text(text, encoding="utf-8")
    (OUTPUT / ".nojekyll").touch()

    with tempfile.TemporaryDirectory(prefix="webfile-docs-") as temporary:
        staging = Path(temporary)
        content = staging / "content"
        shutil.copytree(SOURCE / "content", content)
        shutil.copyfile(ROOT / "web" / "public" / "favicon.svg", content / "assets" / "webfile.svg")
        for original, destination in GUIDES.items():
            text = (ROOT / "docs" / original).read_text(encoding="utf-8")
            (content / destination).write_text(guide_links(text), encoding="utf-8")
        release = (ROOT / "releases" / f"RELEASE_NOTES_{version}.md").read_text(encoding="utf-8")
        (content / "release-notes.md").write_text(
            release + f"\n\n[All published releases]({REPOSITORY_URL}/releases) · "
            f"[Earlier release notes]({REPOSITORY_URL}/tree/main/releases)\n", encoding="utf-8"
        )
        for file in content.rglob("*.md"):
            file.write_text(file.read_text(encoding="utf-8").replace("{{VERSION}}", version), encoding="utf-8")
        config = yaml.safe_load((SOURCE / "mkdocs.yml").read_text(encoding="utf-8"))
        config["docs_dir"] = str(content)
        config["site_dir"] = str(OUTPUT / "docs")
        config_path = staging / "mkdocs.yml"
        config_path.write_text(yaml.safe_dump(config, sort_keys=False), encoding="utf-8")
        subprocess.run([sys.executable, "-m", "mkdocs", "build", "--strict", "--config-file", str(config_path)], check=True)

    sitemap = ET.Element("urlset", xmlns="http://www.sitemaps.org/schemas/sitemap/0.9")
    for page in sorted(OUTPUT.rglob("*.html")):
        if page.name == "404.html":
            continue
        relative = page.relative_to(OUTPUT).as_posix()
        if relative.endswith("index.html"):
            relative = relative.removesuffix("index.html")
        ET.SubElement(ET.SubElement(sitemap, "url"), "loc").text = PUBLIC_URL + relative
    ET.ElementTree(sitemap).write(OUTPUT / "sitemap.xml", encoding="utf-8", xml_declaration=True)
    (OUTPUT / "robots.txt").write_text(f"User-agent: *\nAllow: /\nSitemap: {PUBLIC_URL}sitemap.xml\n", encoding="utf-8")
    subprocess.run([sys.executable, str(SOURCE / "validate.py"), str(OUTPUT)], check=True)
    print(f"Built WebFile {version} product page and documentation: {OUTPUT}")


if __name__ == "__main__":
    main()
