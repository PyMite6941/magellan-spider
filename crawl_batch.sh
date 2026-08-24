#!/usr/bin/env bash
# Crawl the next batch of topics and publish them to Turso.
#
# Host-agnostic on purpose: this is what the GitHub Actions workflow runs, and it
# is also what you would put in a cron job or a loop on an always-on machine.
# Nothing in here knows about CI.
#
#   BATCH_SIZE=10 LIMIT=60 bash crawl_batch.sh
#
# State lives in .crawl-state — a single integer, the index into topics.txt of the
# next topic to crawl. The batch wraps around at the end of the file, so running
# it forever cycles the whole corpus and refreshes the oldest pages first.
#
# Each run starts from an empty magellan.sq3, so the upload only ever carries the
# rows this batch produced. That keeps Turso row-writes proportional to new work
# rather than to the size of the index.

set -euo pipefail

cd "$(dirname "$0")"

BATCH_SIZE="${BATCH_SIZE:-10}"
LIMIT="${LIMIT:-60}"
STATE_FILE="${STATE_FILE:-.crawl-state}"
TOPICS_FILE="${TOPICS_FILE:-topics.txt}"
DB="${DB:-magellan.sq3}"
SPIDER="${SPIDER:-./magellan-spider}"
LOG="${LOG:-crawl-log.md}"

[ -f "$TOPICS_FILE" ] || { echo "ERROR: $TOPICS_FILE not found"; exit 1; }

TOTAL=$(grep -c . "$TOPICS_FILE")
CURSOR=0
[ -f "$STATE_FILE" ] && CURSOR=$(tr -dc '0-9' < "$STATE_FILE" || echo 0)
[ -z "$CURSOR" ] && CURSOR=0
[ "$CURSOR" -ge "$TOTAL" ] && CURSOR=0

echo "topics $((CURSOR + 1))-$((CURSOR + BATCH_SIZE)) of $TOTAL, $LIMIT pages each"

# ── Build ─────────────────────────────────────────────────────────────────────
if [ ! -x "$SPIDER" ]; then
    echo "building spider..."
    go build -o "$(basename "$SPIDER")" .
fi

# ── Crawl ─────────────────────────────────────────────────────────────────────
# A fresh database per batch: the crawler's dedupe is per-run, and the remote
# index is the real source of truth (uploads upsert, so re-crawling is harmless).
rm -f "$DB" "$DB-wal" "$DB-shm"

CRAWLED=0
BATCH_TOPICS=$(sed -n "$((CURSOR + 1)),$((CURSOR + BATCH_SIZE))p" "$TOPICS_FILE")
while IFS= read -r topic; do
    [ -z "$topic" ] && continue
    echo "=== $topic ==="
    # Never let one bad topic abort the batch — the upload still publishes the rest.
    if "$SPIDER" -keyword "$topic" -seeds starter_websites.json -limit "$LIMIT" -db "$DB" \
        > "/tmp/crawl-topic.log" 2>&1; then
        echo "  ok ($(grep -c 'Saved' /tmp/crawl-topic.log || echo 0) saved)"
    else
        echo "  FAILED (continuing) — last lines:"
        tail -3 /tmp/crawl-topic.log | sed 's/^/    /'
    fi
    CRAWLED=$((CRAWLED + 1))
done <<< "$BATCH_TOPICS"

if [ ! -f "$DB" ]; then
    echo "no database produced — nothing crawled, skipping upload"
    exit 0
fi

PAGES=$(python -c "
import sqlite3
con = sqlite3.connect('$DB')
try:
    print(con.execute('SELECT COUNT(*) FROM pages').fetchone()[0])
except Exception:
    print(0)
" 2>/dev/null || echo 0)
echo "crawled $CRAWLED topics -> $PAGES pages"

# ── Publish ───────────────────────────────────────────────────────────────────
if [ -z "${TURSO_URL:-}" ] || [ -z "${TURSO_TOKEN:-}" ]; then
    echo "TURSO_URL / TURSO_TOKEN not set — crawl kept locally, nothing published"
    exit 0
fi

python turso_upload.py

# ── Advance ───────────────────────────────────────────────────────────────────
NEXT=$(( (CURSOR + BATCH_SIZE) % TOTAL ))
echo "$NEXT" > "$STATE_FILE"

{
    printf '| %s | %d-%d of %d | %d topics | %s pages |\n' \
        "$(date -u +%Y-%m-%dT%H:%MZ)" "$((CURSOR + 1))" "$((CURSOR + BATCH_SIZE))" \
        "$TOTAL" "$CRAWLED" "$PAGES"
} >> "$LOG"

echo "done — next batch starts at topic $((NEXT + 1))"
