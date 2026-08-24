import re
from urllib.parse import parse_qsl, urlencode, urlsplit, urlunsplit

"""Quality filters shared by every path that moves crawl data.

Extracted from the MySQL-era `upload_db.py` so the filters outlive it: the same
rules must apply whether rows are going into the migration export
(`export_for_migration.py`) or straight to Turso (`turso_upload.py`).

The crawler seeds itself with search-result URLs and follows whatever it finds, so
the local database collects rows that are not documents: the seed search pages
themselves, Cloudflare interstitials, error pages, and social/icon images.
Filtering happens on the way out rather than in the crawler, so the local .sq3
keeps everything and the rules can change without a re-crawl.
"""

JUNK_URL_PATTERNS = [
    "facebook", "twitter", "instagram", "linkedin", "youtube", "tiktok",
    "pinterest", "snapchat", "reddit", "whatsapp", "telegram", "discord",
    "share", "follow", "social",
    "favicon", "icon", "logo", "sprite", "badge", "avatar",
    "arrow", "bullet", "btn", "button", "banner", "ad-",
    "placeholder", "blank", "spacer", "pixel", "tracking",
    "/assets/icons/", "/img/icons/", "/images/icons/",
    "/static/icons/", "/media/icons/",
]

JUNK_ALT_PATTERNS = [
    "facebook", "twitter", "instagram", "linkedin", "tiktok",
    "pinterest", "share", "follow us", "like us",
    "close", "menu", "search icon", "arrow", "chevron",
    "play button", "pause", "loading",
]


def is_junk_image(url: str, alt: str) -> bool:
    url_lower = url.lower()
    alt_lower = alt.lower()
    for pattern in JUNK_URL_PATTERNS:
        if pattern in url_lower:
            return True
    for pattern in JUNK_ALT_PATTERNS:
        if pattern in alt_lower:
            return True
    filename = url_lower.rsplit("/", 1)[-1].split("?")[0].split(".")[0]
    if not alt and re.fullmatch(r"[a-f0-9\-_]{8,}", filename):
        return True
    return False


# ── Page quality filter ───────────────────────────────────────────────────────
# The crawler seeds itself with search-result URLs and follows whatever it finds,
# so the local DB collects pages that are not documents: the seed search pages
# themselves, Cloudflare interstitials, and error pages. They dilute every
# FULLTEXT query. Filtered on upload rather than in the crawler so the local
# .sq3 keeps everything and the rules can be changed without a re-crawl.

# Bot walls and interstitials — never real content.
JUNK_TITLE_MARKERS = [
    "just a moment", "client challenge", "attention required",
    "are you a robot", "please enable javascript", "enable cookies",
    "access denied", "403 forbidden", "404 not found", "page not found",
    "site unavailable", "service unavailable", "too many requests",
    "checking your browser", "security check",
]

# Search-result pages: the seeds, and pagination off them.
SEARCH_TITLE_MARKERS = [
    "search |", "| search", "search results", "results for",
    "search archives", "search - ", "- search", "search:",
]

SEARCH_URL_MARKERS = [
    "?q=", "&q=", "?query=", "&query=", "?search=", "&search=",
    "searchterm=", "?keyword=", "&keyword=", "/search?", "/search/?",
    "?s=", "&s=", "searchtype=",
    # Path-style search pages: popsci.com/search/quantum+entanglement
    "/search/", "/searches/", "/find/", "/results/",
]


def is_junk_page(url: str, title: str, snippet: str) -> bool:
    """True when a row is not a document worth returning as a search result."""
    title_lower = (title or "").strip().lower()
    url_lower = (url or "").lower()
    snippet_text = (snippet or "").strip()

    for marker in JUNK_TITLE_MARKERS:
        if marker in title_lower:
            return True

    # A search page is junk whether it announces itself in the title or the URL.
    for marker in SEARCH_TITLE_MARKERS:
        if marker in title_lower:
            return True
    for marker in SEARCH_URL_MARKERS:
        if marker in url_lower:
            return True

    # No title AND no usable snippet: nothing for FULLTEXT to match on, so the
    # row can never be returned. A missing title alone is not enough — the
    # snippet can still carry the page.
    if not title_lower and len(snippet_text) < 80:
        return True

    return False

# ── URL canonicalisation ──────────────────────────────────────────────────────
# The crawler treats every distinct URL string as a distinct page, so the same
# document gets indexed once per anchor link. Measured on the 22,742-page export:
# fragments alone accounted for 1,249 duplicate rows, everything else 108.
#
# Dropping the fragment is provably safe — a fragment is never sent to the server,
# so `page#a` and `page#b` are byte-identical responses. `www.` is deliberately
# NOT collapsed: it can point somewhere different, and it would have merged only
# 6 rows.

TRACKING_PARAMS = {
    "fbclid", "gclid", "msclkid", "mc_cid", "mc_eid", "igshid", "ref_src",
    "_ga", "yclid", "dclid", "twclid", "vero_id", "wickedid",
}

_INDEX_FILE_RE = re.compile(r"/(index|default)\.(html?|php|aspx|shtml)$", re.I)


def canonical_url(url: str) -> str:
    """Collapse URLs that address the same document to one form.

    Lowercases scheme and host, strips the default port, drops the fragment and
    known tracking parameters, sorts what remains, and removes a trailing slash
    or an explicit index file. The result is still a working URL, so it is what
    gets stored and displayed.
    """
    if not url:
        return url
    try:
        parts = urlsplit(url.strip())
    except ValueError:
        return url
    if not parts.netloc:
        return url

    host = parts.netloc.lower()
    if host.endswith(":80") and parts.scheme.lower() == "http":
        host = host[:-3]
    elif host.endswith(":443") and parts.scheme.lower() == "https":
        host = host[:-4]

    path = _INDEX_FILE_RE.sub("/", parts.path)
    if len(path) > 1:
        path = path.rstrip("/")

    kept = [(k, v) for k, v in parse_qsl(parts.query, keep_blank_values=True)
            if not (k.lower().startswith("utm_") or k.lower() in TRACKING_PARAMS)]
    query = urlencode(sorted(kept))

    return urlunsplit((parts.scheme.lower(), host, path, query, ""))


def better_row(a: tuple, b: tuple) -> tuple:
    """Pick the richer of two rows that canonicalised to the same URL.

    A row with a title beats one without; otherwise the longer snippet wins. Last
    -write-wins would happily replace a good row with an empty duplicate.
    """
    a_title, a_snippet = (a[1] or "").strip(), (a[2] or "").strip()
    b_title, b_snippet = (b[1] or "").strip(), (b[2] or "").strip()
    if bool(a_title) != bool(b_title):
        return a if a_title else b
    return a if len(a_snippet) >= len(b_snippet) else b
