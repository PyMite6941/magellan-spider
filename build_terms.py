"""Keep the `terms` table (the spelling vocabulary) in step with `pages`.

The search API reads `terms` for typo correction and autocomplete: every word in
the indexed text, lowercased and unstemmed, with the number of pages it appears on
(df). See the end of turso_schema.sql.

Incremental by default. `terms_state.pages_rowid` records the highest
`pages.rowid` already counted. Upserts keep a page's rowid, so only pages added
since the last run are read, and a crawl that refreshes existing pages adds
nothing. Words on a page that is re-crawled with new text are not recounted;
`--rebuild` recounts everything from scratch when that drift matters.

Usage (TURSO_URL / TURSO_TOKEN in spider/.env):
    python build_terms.py              # count pages added since the last run
    python build_terms.py --rebuild    # clear the table and recount every page
    python build_terms.py --dry-run    # count, write nothing
"""

import argparse
import re
import sys
import time
from collections import Counter
from typing import Iterable, List, Sequence, Tuple

from turso_upload import execute as _execute, pipeline as _pipeline

# Must match WORD_RE in the website's magellan/spell.py: the API looks words up
# exactly as they are stored here.
WORD_RE = re.compile(r"[^\W\d_]+")
MIN_LEN, MAX_LEN = 2, 24

READ_CHUNK = 400          # pages per SELECT; each carries up to 8 KB of body text
ROWS_PER_INSERT = 400     # terms per multi-row INSERT (2 params each)
STATEMENTS_PER_REQUEST = 50
WATERMARK = "pages_rowid"

UPSERT_HEAD = "INSERT INTO terms (term, df) VALUES "
# Incremental runs add to the stored count. A rebuild writes totals, so it sets
# them; that also makes a retried rebuild batch harmless.
UPSERT_ADD = " ON CONFLICT(term) DO UPDATE SET df = df + excluded.df"
UPSERT_SET = " ON CONFLICT(term) DO UPDATE SET df = excluded.df"
SET_WATERMARK = ("INSERT INTO terms_state (key, value) VALUES (?, ?)"
                 " ON CONFLICT(key) DO UPDATE SET value = excluded.value")


def _patient(fn, *args, attempts: int = 6, wait: float = 15.0):
    """turso_upload retries for about 12 s. A rebuild reads for minutes, so ride
    out longer network blips (DNS, resets). SQL errors are never retried."""
    for attempt in range(1, attempts + 1):
        try:
            return fn(*args)
        except RuntimeError as e:
            if str(e).startswith("SQL error") or attempt == attempts:
                raise
            print(f"  network trouble ({attempt}/{attempts}), waiting {wait:.0f}s", flush=True)
            time.sleep(wait)


def execute(sql, args=()):
    return _patient(_execute, sql, args)


def pipeline(statements):
    return _patient(_pipeline, statements)


def page_words(*texts: str) -> set:
    """The distinct words on one page, as stored in `terms`."""
    words = set()
    for text in texts:
        if text:
            words.update(w for w in WORD_RE.findall(text.lower())
                         if MIN_LEN <= len(w) <= MAX_LEN)
    return words


def upsert_statements(counts: Counter, add: bool = True) -> List[Tuple[str, Sequence]]:
    items = sorted(counts.items())
    statements = []
    for i in range(0, len(items), ROWS_PER_INSERT):
        chunk = items[i:i + ROWS_PER_INSERT]
        sql = (UPSERT_HEAD + ", ".join(["(?, ?)"] * len(chunk))
               + (UPSERT_ADD if add else UPSERT_SET))
        statements.append((sql, [v for pair in chunk for v in pair]))
    return statements


def get_watermark() -> int:
    rows = execute("SELECT value FROM terms_state WHERE key = ?", (WATERMARK,))
    return int(rows[0]["value"]) if rows else 0


def read_pages(after: int) -> Iterable[Tuple[int, set]]:
    """Yield (rowid, words) for every page with rowid > after, in rowid order."""
    while True:
        rows = execute(
            "SELECT p.rowid AS rid, p.title, p.snippet, c.content"
            " FROM pages p LEFT JOIN page_content c ON c.url = p.url"
            " WHERE p.rowid > ? ORDER BY p.rowid LIMIT ?",
            (after, READ_CHUNK))
        if not rows:
            return
        for r in rows:
            yield int(r["rid"]), page_words(r["title"], r["snippet"], r["content"])
        after = int(rows[-1]["rid"])


def main() -> None:
    parser = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    parser.add_argument("--rebuild", action="store_true", help="clear terms and recount every page")
    parser.add_argument("--dry-run", action="store_true", help="count, write nothing")
    args = parser.parse_args()
    sys.stdout.reconfigure(encoding="utf-8", errors="replace")

    started = time.time()
    after = 0 if args.rebuild else get_watermark()
    counts: Counter = Counter()
    pages, last = 0, after
    for rid, words in read_pages(after):
        counts.update(words)
        pages += 1
        last = rid
        if pages % 4000 == 0:
            print(f"  read {pages:,} pages, {len(counts):,} distinct terms", flush=True)

    print(f"[terms] {pages:,} pages after rowid {after:,} -> {len(counts):,} distinct terms")
    if args.dry_run:
        for term, df in counts.most_common(10):
            print(f"  {term:<20} {df:,}")
        print("dry run: nothing written")
        return
    if not pages and not args.rebuild:
        print("[terms] up to date")
        return

    statements = upsert_statements(counts, add=not args.rebuild)
    finish = [(SET_WATERMARK, (WATERMARK, str(last)))]
    if args.rebuild:
        pipeline([("DELETE FROM terms", ())])

    if len(statements) <= STATEMENTS_PER_REQUEST:
        # Small (every routine crawl): one transaction, so the counts and the
        # watermark land together or not at all.
        pipeline([("BEGIN", ())] + statements + finish + [("COMMIT", ())])
    else:
        # Large (a rebuild or a first run): batched. The watermark is written
        # last, so a failure part-way leaves it unmoved; rerun with --rebuild.
        for i in range(0, len(statements), STATEMENTS_PER_REQUEST):
            pipeline(statements[i:i + STATEMENTS_PER_REQUEST])
            done = min(i + STATEMENTS_PER_REQUEST, len(statements))
            print(f"  wrote {done:,}/{len(statements):,} statements", flush=True)
        pipeline(finish)

    total = execute("SELECT COUNT(*) AS n FROM terms")[0]["n"]
    print(f"[terms] watermark {after:,} -> {last:,}; table now holds {int(total):,} terms"
          f" ({time.time() - started:.1f}s)")


if __name__ == "__main__":
    main()
