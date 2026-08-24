#!/bin/bash
# Crawl only the education topics added to crawl_all.sh, using the expanded
# seed list. Kept separate so education coverage can be refreshed without
# re-running all 260 topics.
SPIDER="./magellan-spider.exe"
LIMIT="${LIMIT:-60}"
grep -A100 '# --- Education and academic topics ---' crawl_all.sh \
  | grep -oE '^run "[^"]+"' | sed 's/^run "//; s/"$//' \
  | while read -r kw; do
      echo "=== $kw ==="
      "$SPIDER" -keyword "$kw" -seeds starter_websites.json -limit "$LIMIT" -db magellan.sq3 2>&1 \
        | grep -ciE "database is locked" | sed 's/^/  busy_warnings: /'
      echo "$(date +%F) | $kw" >> crawled_topics.txt
    done
echo "EDUCATION CRAWL COMPLETE"
