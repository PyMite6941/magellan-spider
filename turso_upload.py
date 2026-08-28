"""Push a crawl from local SQLite into Turso.

Replaces the MySQL half of `upload_db.py`, which is dead — the Oracle MySQL DB
system was deleted when the trial expired (see ../MIGRATION.md).

What is kept from the MySQL version, unchanged in behaviour:
  * per-row upsert, never TRUNCATE — a crawl covering one topic must not delete
    every other topic
  * empty values from a fresh crawl never overwrite good stored values
  * the junk filters (`is_junk_page`, `is_junk_image`), imported from upload_db

What changed: transport. Statements go over Turso's HTTP pipeline API in batches
instead of through a MySQL driver over an SSH tunnel, and the upsert is written in
SQLite syntax (`ON CONFLICT … DO UPDATE SET x = excluded.x`) rather than MySQL's
`ON DUPLICATE KEY UPDATE … VALUES(x)`.

The FTS5 indexes need no attention here: the triggers in turso_schema.sql fire on
insert and on the DO UPDATE path, so `pages_fts` and `content_fts` stay in sync.

Usage:
    set TURSO_URL / TURSO_TOKEN in spider/.env, then
    python turso_upload.py [--dry-run] [--only pages,news]
"""

import argparse
import html as html_mod
import os
import re
import sqlite3
import sys
import time
from typing import Any, Dict, List, Sequence, Tuple

import requests
from dotenv import load_dotenv

from filters import better_row, canonical_url, is_junk_image, is_junk_page
from export_for_migration import make_snippet, recover_title

sys.stdout.reconfigure(encoding="utf-8", errors="replace")

HERE = os.path.dirname(os.path.abspath(__file__))
load_dotenv(os.path.join(HERE, ".env"))

# Which database to publish. The batch crawler writes to a scratch file so it can
# wipe it each run, and must not be made to upload the multi-GB archive by accident
# — that would re-send the entire corpus and burn the monthly write quota.
SQ3_PATH = os.environ.get("MAGELLAN_DB") or os.path.join(HERE, "magellan.sq3")
STATEMENTS_PER_REQUEST = 100  # keeps each HTTP body well under Turso's limit
CONTENT_CAP = 8 * 1024        # chars of cleaned text kept per page


# ── Turso transport ───────────────────────────────────────────────────────────

def turso_url() -> str:
    raw = os.getenv("TURSO_URL", "")
    if not raw:
        sys.exit("ERROR: TURSO_URL is not set (spider/.env)")
    return raw.replace("libsql://", "https://").rstrip("/")


def turso_token() -> str:
    token = os.getenv("TURSO_TOKEN", "")
    if not token:
        sys.exit("ERROR: TURSO_TOKEN is not set (spider/.env)")
    return token


def _encode(value: Any) -> Dict[str, Any]:
    if value is None:
        return {"type": "null", "value": None}
    if isinstance(value, int) and not isinstance(value, bool):
        return {"type": "integer", "value": str(value)}
    return {"type": "text", "value": str(value)}


def pipeline(statements: Sequence[Tuple[str, Sequence[Any]]],
             retries: int = 3) -> List[Dict[str, Any]]:
    """Send a batch of statements as one request. Returns the raw results."""
    requests_body = [
        {"type": "execute", "stmt": {"sql": sql, "args": [_encode(a) for a in args]}}
        for sql, args in statements
    ]
    requests_body.append({"type": "close"})

    last_error = None
    for attempt in range(retries):
        try:
            response = requests.post(
                f"{turso_url()}/v2/pipeline",
                headers={"Authorization": f"Bearer {turso_token()}",
                         "Content-Type": "application/json"},
                json={"requests": requests_body},
                timeout=120,
            )
        except requests.RequestException as e:
            last_error = e
            print(f"  network error (attempt {attempt + 1}/{retries}): {e} — retrying")
            time.sleep(2 * (attempt + 1))
            continue

        if response.status_code != 200:
            last_error = RuntimeError(f"HTTP {response.status_code}: {response.text[:300]}")
            print(f"  {last_error} (attempt {attempt + 1}/{retries}) — retrying")
            time.sleep(2 * (attempt + 1))
            continue

        results = response.json()["results"]
        for result in results:
            if result.get("type") == "error":
                # A SQL error will not fix itself on retry — fail loudly.
                raise RuntimeError("SQL error from Turso: %s" % result["error"]["message"])
        return results

    raise RuntimeError(f"giving up after {retries} attempts: {last_error}")


def execute(sql: str, args: Sequence[Any] = ()) -> List[Dict[str, Any]]:
    """Run one statement and return rows as dicts."""
    result = pipeline([(sql, args)])[0]
    inner = result["response"]["result"]
    columns = [c["name"] for c in inner["cols"]]
    return [dict(zip(columns, (cell.get("value") for cell in row))) for row in inner["rows"]]


def row_count(table: str) -> int:
    rows = execute(f"SELECT COUNT(*) AS n FROM {table}")
    return int(rows[0]["n"]) if rows else 0


# ── SQL ───────────────────────────────────────────────────────────────────────

def upsert_sql(table: str, columns: Sequence[str], key: str = "url") -> str:
    """INSERT … ON CONFLICT DO UPDATE — the newest crawl wins, row by row.

    `COALESCE(NULLIF(excluded.c, ''), table.c)` keeps the stored value when the
    fresh row carries an empty string, so a page that came back blank this time
    cannot wipe a good title from last time. SQLite's `excluded.` is the
    equivalent of MySQL's `VALUES()`.
    """
    placeholders = ", ".join("?" for _ in columns)
    updates = ", ".join(
        f"{c} = COALESCE(NULLIF(excluded.{c}, ''), {table}.{c})"
        for c in columns if c != key
    )
    return (
        f"INSERT INTO {table} ({', '.join(columns)}) VALUES ({placeholders})"
        f" ON CONFLICT({key}) DO UPDATE SET {updates}, updated_at = datetime('now')"
    )


def send_rows(sql: str, rows: Sequence[Sequence[Any]], label: str,
              dry_run: bool = False) -> None:
    if dry_run:
        print(f"  [dry-run] would upsert {len(rows):,} rows into {label}")
        return
    sent = 0
    for start in range(0, len(rows), STATEMENTS_PER_REQUEST):
        chunk = rows[start:start + STATEMENTS_PER_REQUEST]
        pipeline([(sql, row) for row in chunk])
        sent += len(chunk)
        if sent % (STATEMENTS_PER_REQUEST * 10) == 0 or sent == len(rows):
            print(f"    {sent:,}/{len(rows):,}")


# ── Local reads ───────────────────────────────────────────────────────────────

def local() -> sqlite3.Connection:
    if not os.path.exists(SQ3_PATH):
        sys.exit(f"ERROR: {SQ3_PATH} not found")
    con = sqlite3.connect("file:" + SQ3_PATH.replace("\\", "/") + "?mode=ro", uri=True)
    con.text_factory = lambda b: b.decode("utf-8", "replace")
    return con


_TAG_RE = re.compile(r"<[^>]+>")
_SCRIPT_RE = re.compile(r"<(script|style)[^>]*>.*?</(script|style)>", re.I | re.S)


def clean_html(raw: str) -> str:
    text = _SCRIPT_RE.sub(" ", raw)
    text = _TAG_RE.sub(" ", text)
    return " ".join(html_mod.unescape(text).split())


def norm(value: Any) -> str:
    return (value or "").strip()


# ── Uploads ───────────────────────────────────────────────────────────────────

def upload_pages(con: sqlite3.Connection, dry_run: bool) -> None:
    # Pull the stored HTML alongside each page: the crawler leaves title empty
    # when its regex extraction fails, but the title is usually still in the HTML.
    # Without this the filter drops ~460 real pages that the migration export kept.
    # Streamed rather than fetchall() — the join carries every page's full HTML
    # and materialising it all at once exhausts memory on a multi-GB corpus.
    cursor = con.execute("""
        SELECT p.url, p.title, p.snippet, pc.content
        FROM pages p LEFT JOIN pages_content pc ON pc.page_id = p.id
    """)

    deduped, recovered, total = {}, 0, 0
    for url, title, snippet, content in cursor:
        total += 1
        title, snippet = norm(title), norm(snippet)
        if not title and content:
            found = recover_title(content)
            if found:
                title = found
                recovered += 1
                if not snippet:
                    snippet = make_snippet(content)
        url = canonical_url(url)
        row = (url, title, snippet)
        deduped[url] = better_row(deduped[url], row) if url in deduped else row

    rows = [r for r in deduped.values() if not is_junk_page(*r)]
    print(f"[pages]        {total:,} local -> {len(deduped):,} dedup -> "
          f"{len(rows):,} after junk filter ({recovered:,} titles recovered)", flush=True)

    before = 0 if dry_run else row_count("pages")
    send_rows(upsert_sql("pages", ["url", "title", "snippet"]), rows, "pages", dry_run)
    if not dry_run:
        after = row_count("pages")
        print(f"[pages]        {after - before:,} new, "
              f"{len(rows) - (after - before):,} updated (remote total {after:,})")


def upload_images(con: sqlite3.Connection, dry_run: bool) -> None:
    try:
        raw = con.execute("SELECT url, alt, source FROM images").fetchall()
    except sqlite3.OperationalError:
        print("[images]       no local table")
        return
    rows = [(u, norm(a), norm(s)) for u, a, s in raw if not is_junk_image(u, a or "")]
    print(f"[images]       {len(raw):,} local -> {len(rows):,} after junk filter")

    before = 0 if dry_run else row_count("images")
    send_rows(upsert_sql("images", ["url", "alt", "source"]), rows, "images", dry_run)
    if not dry_run:
        after = row_count("images")
        print(f"[images]       {after - before:,} new, "
              f"{len(rows) - (after - before):,} updated (remote total {after:,})")


def upload_files(con: sqlite3.Connection, dry_run: bool) -> None:
    try:
        raw = con.execute("SELECT url, filename, filetype, source FROM files").fetchall()
    except sqlite3.OperationalError:
        print("[files]        no local table")
        return
    rows = [(u, norm(f), norm(t)[:100], norm(s)) for u, f, t, s in raw]
    print(f"[files]        {len(rows):,} local")

    before = 0 if dry_run else row_count("files")
    send_rows(upsert_sql("files", ["url", "filename", "filetype", "source"]),
              rows, "files", dry_run)
    if not dry_run:
        after = row_count("files")
        print(f"[files]        {after - before:,} new, "
              f"{len(rows) - (after - before):,} updated (remote total {after:,})")


def upload_news(con: sqlite3.Connection, dry_run: bool) -> None:
    try:
        raw = con.execute(
            "SELECT url, title, snippet, content, source, source_domain, bias,"
            " published_at, crawled_at FROM news").fetchall()
    except sqlite3.OperationalError:
        print("[news]         no local table — run the spider with -news first")
        return
    rows = [tuple(norm(c) if isinstance(c, str) or c is None else c for c in row)
            for row in raw]
    print(f"[news]         {len(rows):,} local")

    before = 0 if dry_run else row_count("news")
    send_rows(
        upsert_sql("news", ["url", "title", "snippet", "content", "source",
                            "source_domain", "bias", "published_at", "crawled_at"]),
        rows, "news", dry_run)
    if not dry_run:
        after = row_count("news")
        print(f"[news]         {after - before:,} new, "
              f"{len(rows) - (after - before):,} updated (remote total {after:,})")


def upload_page_content(con: sqlite3.Connection, dry_run: bool) -> None:
    try:
        raw = con.execute("""
            SELECT p.url, pc.content
            FROM pages p JOIN pages_content pc ON pc.page_id = p.id
            WHERE pc.content IS NOT NULL AND LENGTH(pc.content) > 100
        """).fetchall()
    except sqlite3.OperationalError as e:
        print(f"[page_content] skipping — {e}")
        return

    rows, seen = [], set()
    for url, content in raw:
        url = canonical_url(url)  # must match the key used for pages
        if url in seen:
            continue
        text = clean_html(content)[:CONTENT_CAP]
        if len(text) > 50:
            seen.add(url)
            rows.append((url, text))
    print(f"[page_content] {len(raw):,} local -> {len(rows):,} after cleaning")

    before = 0 if dry_run else row_count("page_content")
    # Content rows are large, so fewer statements per request.
    global STATEMENTS_PER_REQUEST
    original = STATEMENTS_PER_REQUEST
    STATEMENTS_PER_REQUEST = 25
    try:
        send_rows(upsert_sql("page_content", ["url", "content"]),
                  rows, "page_content", dry_run)
    finally:
        STATEMENTS_PER_REQUEST = original
    if not dry_run:
        after = row_count("page_content")
        print(f"[page_content] {after - before:,} new, "
              f"{len(rows) - (after - before):,} updated (remote total {after:,})")


TASKS = {
    "pages": upload_pages,
    "images": upload_images,
    "files": upload_files,
    "news": upload_news,
    "page_content": upload_page_content,
}


def main() -> None:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--dry-run", action="store_true",
                        help="read and filter locally, send nothing")
    parser.add_argument("--only", default="",
                        help="comma-separated subset of: " + ", ".join(TASKS))
    parser.add_argument("--db", default="",
                        help="SQLite file to publish (default: magellan.sq3)")
    args = parser.parse_args()

    if args.db:
        global SQ3_PATH
        SQ3_PATH = os.path.abspath(args.db)
    print(f"source: {SQ3_PATH}")

    selected = [t.strip() for t in args.only.split(",") if t.strip()] or list(TASKS)
    unknown = [t for t in selected if t not in TASKS]
    if unknown:
        sys.exit(f"ERROR: unknown table(s): {', '.join(unknown)}")

    if not args.dry_run:
        print(f"target: {turso_url()}")
        # Fail before reading 4 GB of SQLite if the database is unreachable.
        execute("SELECT 1")

    con = local()
    started = time.time()
    try:
        for name in selected:
            TASKS[name](con, args.dry_run)
    finally:
        con.close()
    print(f"\ndone in {time.time() - started:.1f}s")


if __name__ == "__main__":
    main()
