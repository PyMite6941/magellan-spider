"""Build a compact, portable copy of the index for migrating off Oracle MySQL.

The working database (`magellan.sq3`, 4.66 GB) is mostly things that should not
migrate: `crawl_queue` (1.3M rows of frontier state) and raw page HTML. This
produces `magellan-export.sq3` containing only what a search backend needs.

Along the way it:
  * drops junk rows using the same `is_junk_page` rules as the upload path
  * recovers titles the crawler failed to extract — 535 of 616 empty-title pages
    still have a usable <title> in their stored HTML
  * strips HTML from page_content and truncates it, so full-text-over-content
    stays possible without carrying gigabytes

Usage:
    python export_for_migration.py [--content-kb 8] [--out magellan-export.sq3]

The result is plain SQLite: load it into Turso/libSQL directly, or use it as the
source for a Postgres/D1 load. No index is built here — each target builds its
own (FTS5, tsvector, MySQL FULLTEXT).
"""

import argparse
import html as html_mod
import os
import re
import sqlite3
import sys

from filters import better_row, canonical_url, is_junk_page, is_junk_image

sys.stdout.reconfigure(encoding="utf-8", errors="replace")

SRC_PATH = os.path.join(os.path.dirname(os.path.abspath(__file__)), "magellan.sq3")

# ── Title / snippet recovery ──────────────────────────────────────────────────
# The Go crawler stores title='' when extraction fails, but the page HTML is kept
# in pages_content, so the title is usually still there to be found. Tried in
# order of trustworthiness.

RE_TITLE = re.compile(r"<title[^>]*>(.*?)</title>", re.I | re.S)
RE_OG_TITLE = re.compile(
    r'<meta[^>]+property=["\']og:title["\'][^>]+content=["\'](.*?)["\']', re.I | re.S)
RE_SCRIPT_STYLE = re.compile(r"<(script|style)[^>]*>.*?</(script|style)>", re.I | re.S)
RE_TAG = re.compile(r"<[^>]+>")


def clean_text(raw: str) -> str:
    """Strip scripts, tags and entities down to readable text."""
    text = RE_SCRIPT_STYLE.sub(" ", raw)
    text = RE_TAG.sub(" ", text)
    text = html_mod.unescape(text)
    return " ".join(text.split())


# Nav chrome that shows up when a page has no real title. Recovering one of these
# is worse than leaving the title empty — it pollutes search results with a row
# titled "Open menu".
NON_TITLES = {
    "email", "menu", "open menu", "close menu", "search", "home", "skip to content",
    "navigation", "main menu", "toggle navigation", "share", "login", "sign in",
}


def recover_title(page_html: str) -> str:
    """Rebuild a title from stored HTML. <h1> is deliberately not consulted — it
    contributed ~1% of recoveries and produced nav junk ("Open menu") for most."""
    for pattern in (RE_TITLE, RE_OG_TITLE):
        match = pattern.search(page_html)
        if not match:
            continue
        candidate = clean_text(match.group(1))
        if len(candidate) >= 3 and candidate.lower() not in NON_TITLES:
            return candidate[:300]
    return ""


def make_snippet(page_html: str, limit: int = 300) -> str:
    text = clean_text(page_html)
    if len(text) <= limit:
        return text
    cut = text[:limit]
    space = cut.rfind(" ")
    if space > limit * 0.6:
        cut = cut[:space]
    return cut + "..."


# ── Export ────────────────────────────────────────────────────────────────────

SCHEMA = """
CREATE TABLE pages (
    url      TEXT PRIMARY KEY,
    title    TEXT NOT NULL DEFAULT '',
    snippet  TEXT NOT NULL DEFAULT ''
);
CREATE TABLE page_content (
    url      TEXT PRIMARY KEY,
    content  TEXT NOT NULL
);
CREATE TABLE images (
    url      TEXT PRIMARY KEY,
    alt      TEXT NOT NULL DEFAULT '',
    source   TEXT NOT NULL DEFAULT ''
);
CREATE TABLE files (
    url      TEXT PRIMARY KEY,
    filename TEXT NOT NULL DEFAULT '',
    filetype TEXT NOT NULL DEFAULT '',
    source   TEXT NOT NULL DEFAULT ''
);
CREATE TABLE news (
    url           TEXT PRIMARY KEY,
    title         TEXT NOT NULL DEFAULT '',
    snippet       TEXT NOT NULL DEFAULT '',
    content       TEXT,
    source        TEXT NOT NULL DEFAULT '',
    source_domain TEXT NOT NULL DEFAULT '',
    bias          TEXT NOT NULL DEFAULT '',
    published_at  TEXT,
    crawled_at    TEXT
);
"""


def open_source(path: str) -> sqlite3.Connection:
    con = sqlite3.connect("file:" + path.replace("\\", "/") + "?mode=ro", uri=True)
    con.text_factory = lambda b: b.decode("utf-8", "replace")
    return con


def export_pages(src: sqlite3.Connection, dst: sqlite3.Connection) -> None:
    # Pull the stored HTML alongside each page so a failed title can be rebuilt.
    # Streamed, not fetchall(): this join carries every page's full HTML, and
    # materialising it all at once exhausted memory once the corpus passed ~4.7 GB
    # (the process died mid-transaction and left a hot journal). Only the small
    # (url, title, snippet) tuples are retained.
    cursor = src.execute("""
        SELECT p.url, p.title, p.snippet, pc.content
        FROM pages p
        LEFT JOIN pages_content pc ON pc.page_id = p.id
    """)

    seen, recovered, junk, total = {}, 0, 0, 0
    for url, title, snippet, content in cursor:
        total += 1
        title = (title or "").strip()
        snippet = (snippet or "").strip()

        if not title and content:
            found = recover_title(content)
            if found:
                title = found
                recovered += 1
                if not snippet:
                    snippet = make_snippet(content)

        if is_junk_page(url, title, snippet):
            junk += 1
            continue

        # Collapse URLs that address the same document (#fragments above all).
        url = canonical_url(url)
        row = (url, title, snippet)
        seen[url] = better_row(seen[url], row) if url in seen else row

    dst.executemany("INSERT OR REPLACE INTO pages VALUES (?, ?, ?)", seen.values())
    dst.commit()
    print(f"[pages]        {total:,} source -> {len(seen):,} exported "
          f"({recovered:,} titles recovered, {junk:,} junk dropped)", flush=True)


def export_page_content(src: sqlite3.Connection, dst: sqlite3.Connection,
                        limit_bytes: int) -> None:
    kept = 0
    batch = []
    cursor = src.execute("""
        SELECT p.url, pc.content
        FROM pages p JOIN pages_content pc ON pc.page_id = p.id
        WHERE pc.content IS NOT NULL AND LENGTH(pc.content) > 100
    """)
    # Only keep content for pages that survived the junk filter.
    live = {row[0] for row in dst.execute("SELECT url FROM pages")}

    seen = set()
    for url, raw in cursor:
        url = canonical_url(url)  # must match the key used for pages
        if url not in live or url in seen:
            continue  # fragment variants of one page share a canonical url
        text = clean_text(raw)[:limit_bytes]
        if len(text) < 50:
            continue
        seen.add(url)
        batch.append((url, text))
        kept += 1
        if len(batch) >= 500:
            dst.executemany("INSERT OR REPLACE INTO page_content VALUES (?, ?)", batch)
            batch.clear()
    if batch:
        dst.executemany("INSERT OR REPLACE INTO page_content VALUES (?, ?)", batch)
    dst.commit()
    print(f"[page_content] {kept:,} exported (cleaned, capped at "
          f"{limit_bytes // 1024} KB each)")


def export_images(src: sqlite3.Connection, dst: sqlite3.Connection) -> None:
    try:
        rows = src.execute("SELECT url, alt, source FROM images").fetchall()
    except sqlite3.OperationalError:
        print("[images]       no table in source")
        return
    kept = {r[0]: (r[0], r[1] or "", r[2] or "")
            for r in rows if not is_junk_image(r[0], r[1] or "")}
    dst.executemany("INSERT OR REPLACE INTO images VALUES (?, ?, ?)", kept.values())
    dst.commit()
    print(f"[images]       {len(rows):,} source -> {len(kept):,} exported "
          f"({len(rows) - len(kept):,} junk dropped)")


def export_files(src: sqlite3.Connection, dst: sqlite3.Connection) -> None:
    try:
        rows = src.execute("SELECT url, filename, filetype, source FROM files").fetchall()
    except sqlite3.OperationalError:
        print("[files]        no table in source")
        return
    kept = {r[0]: (r[0], r[1] or "", (r[2] or "")[:100], r[3] or "") for r in rows}
    dst.executemany("INSERT OR REPLACE INTO files VALUES (?, ?, ?, ?)", kept.values())
    dst.commit()
    print(f"[files]        {len(kept):,} exported")


def export_news(src: sqlite3.Connection, dst: sqlite3.Connection) -> None:
    try:
        rows = src.execute(
            "SELECT url, title, snippet, content, source, source_domain, bias,"
            " published_at, crawled_at FROM news").fetchall()
    except sqlite3.OperationalError:
        print("[news]         no table in source")
        return
    kept = {r[0]: r for r in rows}
    dst.executemany("INSERT OR REPLACE INTO news VALUES (?,?,?,?,?,?,?,?,?)", kept.values())
    dst.commit()
    print(f"[news]         {len(kept):,} exported")


def main() -> None:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--out", default="magellan-export.sq3")
    parser.add_argument("--content-kb", type=int, default=8,
                        help="max KB of cleaned text kept per page (default 8)")
    args = parser.parse_args()

    out_path = os.path.join(os.path.dirname(os.path.abspath(__file__)), args.out)
    if os.path.exists(out_path):
        sys.exit(f"ERROR: {out_path} already exists — delete it or pass --out")

    print(f"source: {SRC_PATH}  ({os.path.getsize(SRC_PATH) / 2**30:.2f} GB)")
    print(f"target: {out_path}\n")

    src = open_source(SRC_PATH)
    dst = sqlite3.connect(out_path)
    dst.executescript(SCHEMA)

    try:
        export_pages(src, dst)
        export_page_content(src, dst, args.content_kb * 1024)
        export_images(src, dst)
        export_files(src, dst)
        export_news(src, dst)
        dst.execute("VACUUM")
    finally:
        src.close()
        dst.close()

    size = os.path.getsize(out_path)
    print(f"\ndone — {out_path}  ({size / 2**20:.1f} MB)")


if __name__ == "__main__":
    main()
