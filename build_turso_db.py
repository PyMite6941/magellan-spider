"""Build the upload-ready Turso database from the migration export.

    python export_for_migration.py     # magellan.sq3 (4.66 GB) -> magellan-export.sq3
    python build_turso_db.py           # magellan-export.sq3    -> magellan-turso.sq3

The result has the schema, the data, the FTS5 indexes and the sync triggers all in
place, so it can be handed straight to Turso:

    turso db create magellan --from-file magellan-turso.sq3
    turso db tokens create magellan

Building the indexes locally rather than remotely matters: `turso db create
--from-file` uploads the file as-is, and an empty FTS5 table would stay empty
(external-content triggers only fire on future writes, not on rows already there).
"""

import argparse
import os
import re
import sqlite3
import sys
import time

sys.stdout.reconfigure(encoding="utf-8", errors="replace")

HERE = os.path.dirname(os.path.abspath(__file__))

# Turso free tier ceiling, and the guard we actually fail at — deliberately below
# it so a build that is heading for trouble says so while there is room to act.
FREE_TIER_BYTES = 5 * 2 ** 30
GUARD_BYTES = int(4.5 * 2 ** 30)

COPY = [
    ("pages", "url, title, snippet"),
    ("images", "url, alt, source"),
    ("files", "url, filename, filetype, source"),
    ("news", "url, title, snippet, content, source, source_domain, bias,"
             " published_at, crawled_at"),
    ("page_content", "url, content"),
]


def build(export_path: str, out_path: str, schema_path: str) -> None:
    if not os.path.exists(export_path):
        sys.exit(f"ERROR: {export_path} not found — run export_for_migration.py first")
    if os.path.exists(out_path):
        sys.exit(f"ERROR: {out_path} already exists — delete it or pass --out")

    schema = open(schema_path, encoding="utf-8").read()
    db = sqlite3.connect(out_path)
    db.executescript(schema)
    print("schema + FTS5 indexes created")

    db.execute("ATTACH DATABASE ? AS exp", (export_path.replace("\\", "/"),))
    started = time.time()
    for table, columns in COPY:
        t0 = time.time()
        db.execute(f"INSERT INTO {table} ({columns}) SELECT {columns} FROM exp.{table}")
        db.commit()
        count = db.execute(f"SELECT COUNT(*) FROM {table}").fetchone()[0]
        print(f"  {table:<13} {count:>9,}  ({time.time() - t0:.1f}s)")
    db.execute("DETACH DATABASE exp")

    # The triggers index rows as they arrive, so these should already match their
    # base tables. Verified rather than assumed — a silent mismatch here means
    # every future search misses rows.
    print()
    for fts, base in (("pages_fts", "pages"), ("content_fts", "page_content"),
                      ("news_fts", "news")):
        indexed = db.execute(f"SELECT COUNT(*) FROM {fts}").fetchone()[0]
        total = db.execute(f"SELECT COUNT(*) FROM {base}").fetchone()[0]
        status = "ok" if indexed == total else "MISMATCH"
        print(f"  {fts:<13} {indexed:>9,} / {total:,}  {status}")
        if indexed != total:
            print(f"    rebuilding {fts}...")
            db.execute(f"INSERT INTO {fts}({fts}) VALUES('rebuild')")
            db.commit()

    db.execute("PRAGMA optimize")
    integrity = db.execute("PRAGMA quick_check").fetchone()[0]
    db.close()

    size_bytes = os.path.getsize(out_path)
    size_mb = size_bytes / 2 ** 20
    print(f"\nintegrity: {integrity}")
    print(f"built in {time.time() - started:.1f}s -> {out_path} ({size_mb:.1f} MB)")

    # Turso's free tier is 5 GB. At the measured ~12.5 KB per indexed page that is
    # roughly 400k pages — far off today, but an expanding crawl gets there, and the
    # failure mode otherwise is a rejected upload after a ten-minute build. Fail
    # below the ceiling so there is room to react.
    print(f"free-tier usage: {size_bytes / FREE_TIER_BYTES:.1%} of 5 GB")
    if size_bytes > GUARD_BYTES:
        sys.exit(
            f"\nERROR: {size_bytes / 2**30:.2f} GB exceeds the "
            f"{GUARD_BYTES / 2**30:.1f} GB guard.\n"
            "  Options: lower --content-kb (page_content is ~65% of the payload),\n"
            "  prune stale pages, or move to a paid tier.")
    if size_bytes / FREE_TIER_BYTES > 0.75:
        print("WARNING: past 75% of the free tier — plan pruning or a smaller --content-kb")
    print("\nNext:")
    print(f"  turso db create magellan --from-file {os.path.basename(out_path)}")
    print("  turso db tokens create magellan")
    print("  # then set TURSO_URL and TURSO_TOKEN in api/.env")


def smoke_test(out_path: str) -> None:
    """Prove the shipped file actually answers a search before it is uploaded."""
    db = sqlite3.connect("file:" + out_path.replace("\\", "/") + "?mode=ro", uri=True)
    match = " ".join('"%s"' % t for t in re.findall(r"\w+", "ebola outbreak"))
    rows = db.execute("""
        SELECT p.url, p.title FROM pages_fts f JOIN pages p ON p.rowid = f.rowid
        WHERE pages_fts MATCH ? ORDER BY bm25(pages_fts) LIMIT 3""", (match,)).fetchall()
    db.close()
    print("\nsmoke test — 'ebola outbreak':")
    for url, title in rows:
        print("   ", (title or url)[:66])
    if not rows:
        sys.exit("ERROR: the built database returns no results — do not upload it")


def main() -> None:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--export", default=os.path.join(HERE, "magellan-export.sq3"))
    parser.add_argument("--out", default=os.path.join(HERE, "magellan-turso.sq3"))
    parser.add_argument("--schema", default=os.path.join(HERE, "turso_schema.sql"))
    args = parser.parse_args()

    build(args.export, args.out, args.schema)
    smoke_test(args.out)


if __name__ == "__main__":
    main()
