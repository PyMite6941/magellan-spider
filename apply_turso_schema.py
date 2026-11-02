"""Bring the live Turso database up to date with turso_schema.sql.

Every statement in the schema is IF NOT EXISTS, so this is safe to re-run: existing
tables, indexes and triggers are left alone, and no row is deleted or changed. Any
FTS5 index that did not exist before this run is filled from its base table with
FTS5's 'rebuild' command. That is a one-off read of the base table; afterwards
the triggers keep the index in sync.

Written for the trigram indexes on images and files (see the end of
turso_schema.sql), but it applies whatever the schema file adds.

Usage:
    set TURSO_URL / TURSO_TOKEN in spider/.env, then
    python apply_turso_schema.py --dry-run    # show what would be created
    python apply_turso_schema.py
"""

import argparse
import os
import re
import sqlite3
import time
from typing import List, Tuple

from turso_upload import execute, pipeline

HERE = os.path.dirname(os.path.abspath(__file__))

_CREATE_RE = re.compile(
    r"CREATE\s+(?:VIRTUAL\s+)?(TABLE|INDEX|TRIGGER)\s+IF\s+NOT\s+EXISTS\s+(\w+)", re.I)
_FTS_CONTENT_RE = re.compile(r"content\s*=\s*'(\w+)'", re.I)


def split_statements(sql: str) -> List[str]:
    """Split a schema file into statements, keeping trigger bodies intact."""
    statements, buffer = [], ""
    for line in sql.splitlines(keepends=True):
        if not buffer and line.strip().startswith("--"):
            continue
        buffer += line
        if sqlite3.complete_statement(buffer):
            if buffer.strip():
                statements.append(buffer.strip())
            buffer = ""
    if buffer.strip():
        raise ValueError(f"incomplete statement at end of schema: {buffer[:80]!r}")
    return statements


def plan(statements: List[str]) -> List[Tuple[str, str, str, str]]:
    """(kind, name, base table for FTS or '', sql) for every CREATE statement."""
    out = []
    for stmt in statements:
        m = _CREATE_RE.search(stmt)
        if not m:
            raise ValueError(f"not an IF NOT EXISTS create, refusing to run: {stmt[:80]!r}")
        kind, name = m.group(1).upper(), m.group(2)
        base = ""
        if kind == "TABLE" and re.search(r"USING\s+fts5", stmt, re.I):
            c = _FTS_CONTENT_RE.search(stmt)
            base = c.group(1) if c else ""
        out.append((kind, name, base, stmt))
    return out


def main() -> None:
    parser = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    parser.add_argument("--schema", default=os.path.join(HERE, "turso_schema.sql"))
    parser.add_argument("--dry-run", action="store_true", help="show what would be created; change nothing")
    args = parser.parse_args()

    items = plan(split_statements(open(args.schema, encoding="utf-8").read()))
    existing = {r["name"] for r in execute("SELECT name FROM sqlite_master")}
    missing = [i for i in items if i[1] not in existing]

    print(f"{len(items)} statements in schema, {len(items) - len(missing)} already present.")
    for kind, name, base, _ in missing:
        print(f"  will create {kind.lower():8} {name}" + (f"  (FTS5 over {base})" if base else ""))
    if not missing:
        print("Nothing to do.")
        return
    if args.dry_run:
        print("Dry run: nothing changed.")
        return

    # Tables first, then triggers that reference them. The schema file is already
    # in that order, and IF NOT EXISTS makes a partial earlier run harmless.
    pipeline([(stmt, ()) for _, _, _, stmt in missing])
    print("created.")

    for kind, name, base, _ in missing:
        if not base:
            continue
        t = time.time()
        print(f"filling {name} from {base}...", flush=True)
        pipeline([(f"INSERT INTO {name}({name}) VALUES('rebuild')", ())])
        print(f"  done in {time.time() - t:.1f}s")

    after = {r["name"] for r in execute("SELECT name FROM sqlite_master")}
    still_missing = [name for _, name, _, _ in items if name not in after]
    if still_missing:
        raise SystemExit(f"ERROR: still missing after apply: {', '.join(still_missing)}")
    print("schema is up to date.")


if __name__ == "__main__":
    main()
