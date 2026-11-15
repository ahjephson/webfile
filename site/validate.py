#!/usr/bin/env python3
"""Check generated links and anchors at the GitHub Pages project path."""

from html.parser import HTMLParser
from pathlib import Path
import sys
from urllib.parse import unquote, urljoin, urlsplit

BASE = "https://ahjephson.dev/webfile/"


class Page(HTMLParser):
    def __init__(self, text):
        super().__init__()
        self.links = []
        self.ids = set()
        self.feed(text)

    def handle_starttag(self, tag, attributes):
        attributes = dict(attributes)
        if attributes.get("id"):
            self.ids.add(attributes["id"])
        for key in ("href", "src"):
            if attributes.get(key):
                self.links.append(attributes[key])


def validate(root):
    for required in (root / "index.html", root / "docs" / "index.html"):
        if not required.is_file():
            raise SystemExit(f"Missing required entry page: {required}")
    pages = {file.resolve(): Page(file.read_text(encoding="utf-8")) for file in root.rglob("*.html")}
    errors = []
    for file, page in pages.items():
        text = file.read_text(encoding="utf-8")
        if "{{VERSION}}" in text or "{{PUBLIC_BASE}}" in text:
            errors.append(f"{file}: unresolved template value")
        location = urljoin(BASE, file.relative_to(root).as_posix())
        for link in page.links:
            target = urlsplit(urljoin(location, link))
            if target.netloc != urlsplit(BASE).netloc or target.scheme not in ("http", "https"):
                continue
            if not target.path.startswith("/webfile/"):
                errors.append(f"{file}: link escapes the Pages project path: {link}")
                continue
            destination = (root / unquote(target.path.removeprefix("/webfile/"))).resolve()
            if not destination.is_relative_to(root):
                errors.append(f"{file}: link escapes site output: {link}")
                continue
            if destination.is_dir():
                destination /= "index.html"
            if not destination.is_file():
                errors.append(f"{file}: missing target: {link}")
            elif target.fragment and destination in pages and unquote(target.fragment) not in pages[destination].ids:
                errors.append(f"{file}: missing anchor: {link}")
    if errors:
        raise SystemExit("\n".join(errors))
    print(f"Validated {len(pages)} HTML pages, local assets, links and fragment targets under /webfile/.")


if __name__ == "__main__":
    validate(Path(sys.argv[1]).resolve())
