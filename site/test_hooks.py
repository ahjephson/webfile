from types import SimpleNamespace
import unittest

from hooks import on_page_context


class CanonicalTests(unittest.TestCase):
    def test_homepage_uses_directory_canonical(self):
        page = SimpleNamespace(is_homepage=True, canonical_url="https://ahjephson.dev/webfile/docs/index.html")
        config = SimpleNamespace(site_url="https://ahjephson.dev/webfile/docs/")
        context = {"page": page}
        self.assertIs(on_page_context(context, page=page, config=config, nav=None), context)
        self.assertEqual(page.canonical_url, config.site_url)

    def test_other_pages_keep_their_html_canonical(self):
        url = "https://ahjephson.dev/webfile/docs/installation.html"
        page = SimpleNamespace(is_homepage=False, canonical_url=url)
        on_page_context({}, page=page, config=SimpleNamespace(site_url="https://ahjephson.dev/webfile/docs/"), nav=None)
        self.assertEqual(page.canonical_url, url)
