"""Regression checks for deployment-path and link-validation failures."""

from contextlib import redirect_stdout
import io
from pathlib import Path
import tempfile
import unittest

from validate import validate


class SiteValidationTests(unittest.TestCase):
    def setUp(self):
        self.directory = tempfile.TemporaryDirectory()
        self.addCleanup(self.directory.cleanup)
        self.root = Path(self.directory.name).resolve()
        (self.root / "docs").mkdir()
        (self.root / "docs" / "index.html").write_text('<h1 id="start">Docs</h1>')

    def page(self, content):
        (self.root / "index.html").write_text(content)

    def test_nested_relative_and_canonical_project_links(self):
        self.page('<a href="docs/#start">Docs</a><a href="https://ahjephson.dev/webfile/docs/index.html#start">Docs</a>')
        with redirect_stdout(io.StringIO()):
            validate(self.root)

    def test_root_relative_asset_is_rejected(self):
        self.page('<script src="/site.js"></script>')
        with self.assertRaisesRegex(SystemExit, "escapes the Pages project path"):
            validate(self.root)

    def test_missing_anchor_is_rejected(self):
        self.page('<a href="docs/#missing">Docs</a>')
        with self.assertRaisesRegex(SystemExit, "missing anchor"):
            validate(self.root)

    def test_missing_asset_is_rejected(self):
        self.page('<img src="assets/missing.svg" alt="">')
        with self.assertRaisesRegex(SystemExit, "missing target"):
            validate(self.root)

    def test_missing_entry_point_is_rejected(self):
        with self.assertRaisesRegex(SystemExit, "Missing required entry page"):
            validate(self.root)

    def test_unresolved_version_is_rejected(self):
        self.page('<p>{{VERSION}}</p>')
        with self.assertRaisesRegex(SystemExit, "unresolved template value"):
            validate(self.root)

    def test_repository_only_guidance_is_rejected(self):
        self.page('<p>WebFile</p>')
        (self.root / "docs" / "development.html").write_text('<h1>Maintainer instructions</h1>')
        with self.assertRaisesRegex(SystemExit, "Repository-only guidance must not be published"):
            validate(self.root)
