#!/usr/bin/env python3
"""Build the product page and searchable docs without changing repository sources."""

from __future__ import annotations

from pathlib import Path
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


def main() -> None:
    if OUTPUT.is_symlink() or OUTPUT.resolve() != ROOT / "dist" / "site":
        raise ValueError("Site output must stay inside the repository's dist/site directory")
    if OUTPUT.exists():
        shutil.rmtree(OUTPUT)
    OUTPUT.mkdir(parents=True, exist_ok=True)
    shutil.copytree(SOURCE / "website", OUTPUT, dirs_exist_ok=True, ignore=shutil.ignore_patterns("*.md"))
    (OUTPUT / "assets").mkdir(exist_ok=True)
    shutil.copyfile(ROOT / "web" / "public" / "favicon.svg", OUTPUT / "assets" / "webfile.svg")
    for file in OUTPUT.glob("*.html"):
        text = file.read_text(encoding="utf-8")
        text = text.replace("{{PUBLIC_BASE}}", PUBLIC_URL)
        file.write_text(text, encoding="utf-8")
    (OUTPUT / ".nojekyll").touch()

    with tempfile.TemporaryDirectory(prefix="webfile-docs-") as temporary:
        staging = Path(temporary)
        content = staging / "content"
        shutil.copytree(SOURCE / "content", content)
        shutil.copyfile(ROOT / "web" / "public" / "favicon.svg", content / "assets" / "webfile.svg")
        config = yaml.safe_load((SOURCE / "mkdocs.yml").read_text(encoding="utf-8"))
        config["hooks"] = [str(SOURCE / hook) for hook in config.get("hooks", [])]
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
    print(f"Built WebFile product page and user documentation: {OUTPUT}")


if __name__ == "__main__":
    main()
