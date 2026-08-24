# Magellan Spider

Go web crawler for the Magellan search engine, plus the Python pipeline that
publishes crawls to Turso.

## Crawling

```bash
go build -o magellan-spider .

./magellan-spider -keyword "quantum computing" -limit 200
./magellan-spider -news -news-limit 15
bash crawl_all.sh                    # all 260 topics
bash run_edu_crawl.sh                # the 25 education topics
BATCH_SIZE=10 LIMIT=60 bash crawl_batch.sh   # next batch from topics.txt
```

Results land in `magellan.sq3` (WAL mode — the `-wal` and `-shm` sidecars are part
of the database; copying the `.sq3` alone loses the newest pages).

## Publishing

```bash
python turso_upload.py --dry-run     # filter and count locally, send nothing
python turso_upload.py               # upsert into Turso
```

Needs `TURSO_URL` and `TURSO_TOKEN` in `.env`. Uploads upsert per row and never
truncate, so a crawl covering one topic cannot delete the others, and a page that
comes back blank cannot overwrite a good title.

For a full rebuild (after a large crawl, or to reclaim space):

```bash
python -u export_for_migration.py    # magellan.sq3 -> magellan-export.sq3 (~10 min)
python build_turso_db.py             # -> magellan-turso.sq3, FTS5 built (~60 s)
turso db create magellan-$(date +%Y%m%d) --from-file magellan-turso.sq3
```

Publishing a rebuilt file costs no row-write quota, unlike a row-by-row upload.
Flip `TURSO_URL` to the new database and destroy the old one when it looks right.

Run the Python scripts with `-u` — output is block-buffered when piped, which
makes a long run look like a hang.

## Automated crawling

`.github/workflows/crawl.yml` runs `crawl_batch.sh` six times a day. Each run
crawls the next slice of `topics.txt`, publishes it, and commits the cursor in
`.crawl-state` plus a line in `crawl-log.md`. At the default batch size the whole
260-topic list cycles roughly every four days, then starts again — which doubles
as a refresh of the oldest pages.

Required repository secrets (Settings → Secrets and variables → Actions):

| Secret | Value |
|---|---|
| `TURSO_URL` | `libsql://magellan-<org>.turso.io` |
| `TURSO_TOKEN` | from `turso db tokens create magellan` |

**This is scheduled batch crawling, not continuous crawling.** GitHub Actions is
for building, testing and deploying the project it belongs to; parking a
permanently-running crawler on it is outside that, and unlimited public-repo
minutes do not change the rule. Bounded batches are also politer to the sites
being crawled and give the same index growth.

For genuinely constant crawling, run `crawl_batch.sh` in a loop on a machine you
control. It reads its configuration from the environment and knows nothing about
CI, so a cron entry on any always-on box works unchanged:

```bash
*/30 * * * * cd /path/to/spider && BATCH_SIZE=5 LIMIT=40 bash crawl_batch.sh >> cron.log 2>&1
```

## Before making this repository public

The working directory holds credentials that `.gitignore` excludes. Verify rather
than assume:

```bash
git status --porcelain                     # what would be committed
git status --porcelain --ignored | grep '^!!'   # what is excluded
```

Excluded and must stay so: `.env`, `*.pem`, `*.key`, `legacy_mysql/` (retired
Oracle scripts containing hardcoded database credentials), and the `.sq3` files.

An audit on 2026-08-21 found the live API key hardcoded in `finish_setup.sh`;
that script has been moved into `legacy_mysql/`. It was never committed, and the
key appears in no git history.

## Layout

| File | Purpose |
|---|---|
| `main.go`, `helpers.go`, `news_spider.go` | the crawler |
| `filters.py` | junk-page/image filters and URL canonicalisation, shared by every path |
| `turso_upload.py` | incremental publish to Turso |
| `export_for_migration.py` | working DB → portable export |
| `build_turso_db.py` | export → upload-ready DB with FTS5 built |
| `turso_schema.sql` | schema, FTS5 indexes, sync triggers |
| `crawl_batch.sh` | crawl the next batch and publish it |
| `topics.txt` | the 260 topics, in crawl order |
| `legacy_mysql/` | retired Oracle-era scripts — do not run |
