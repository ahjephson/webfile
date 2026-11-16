"""Keep the documentation homepage canonical consistent with the sitemap."""


def on_page_context(context, *, page, config, nav):
    if page.is_homepage:
        page.canonical_url = config.site_url
    return context
