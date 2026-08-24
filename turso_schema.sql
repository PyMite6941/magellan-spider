-- Magellan schema for Turso / libSQL.
--
-- Applied by load_turso.py, or by hand:  turso db shell magellan < turso_schema.sql
--
-- Difference from the Oracle MySQL schema: search runs on FTS5 external-content
-- tables instead of MySQL FULLTEXT. FTS5 gives real BM25 ranking, phrase queries
-- and prefix matching, and the index lives beside the data instead of needing a
-- database server on a private VCN.

CREATE TABLE IF NOT EXISTS pages (
    url        TEXT PRIMARY KEY,
    title      TEXT NOT NULL DEFAULT '',
    snippet    TEXT NOT NULL DEFAULT '',
    updated_at TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE TABLE IF NOT EXISTS page_content (
    url        TEXT PRIMARY KEY,
    content    TEXT NOT NULL,
    updated_at TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE TABLE IF NOT EXISTS images (
    url        TEXT PRIMARY KEY,
    alt        TEXT NOT NULL DEFAULT '',
    source     TEXT NOT NULL DEFAULT '',
    updated_at TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE TABLE IF NOT EXISTS files (
    url        TEXT PRIMARY KEY,
    filename   TEXT NOT NULL DEFAULT '',
    filetype   TEXT NOT NULL DEFAULT '',
    source     TEXT NOT NULL DEFAULT '',
    updated_at TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE TABLE IF NOT EXISTS news (
    url           TEXT PRIMARY KEY,
    title         TEXT NOT NULL DEFAULT '',
    snippet       TEXT NOT NULL DEFAULT '',
    content       TEXT,
    source        TEXT NOT NULL DEFAULT '',
    source_domain TEXT NOT NULL DEFAULT '',
    bias          TEXT NOT NULL DEFAULT '',
    published_at  TEXT,
    crawled_at    TEXT,
    updated_at    TEXT NOT NULL DEFAULT (datetime('now'))
);

-- Images and files are searched with LIKE, which cannot use a normal index for a
-- leading-wildcard match. These help the ORDER BY and the primary-key lookups.
CREATE INDEX IF NOT EXISTS idx_images_alt ON images (alt);
CREATE INDEX IF NOT EXISTS idx_files_filename ON files (filename);
CREATE INDEX IF NOT EXISTS idx_news_bias_crawled ON news (bias, crawled_at DESC);

-- ── Full-text indexes ───────────────────────────────────────────────────────
-- External-content tables: FTS5 stores only the index and reads the columns from
-- the base table, so the text is not duplicated. `content_rowid` ties each FTS
-- row to the base table's implicit rowid.

CREATE VIRTUAL TABLE IF NOT EXISTS pages_fts USING fts5(
    title,
    snippet,
    content='pages',
    content_rowid='rowid',
    tokenize='porter unicode61'
);

CREATE VIRTUAL TABLE IF NOT EXISTS news_fts USING fts5(
    title,
    snippet,
    content='news',
    content_rowid='rowid',
    tokenize='porter unicode61'
);

-- External-content FTS5 tables do not update themselves; without these triggers
-- the index silently drifts from the table after the first upload.

CREATE TRIGGER IF NOT EXISTS pages_ai AFTER INSERT ON pages BEGIN
    INSERT INTO pages_fts(rowid, title, snippet) VALUES (new.rowid, new.title, new.snippet);
END;

CREATE TRIGGER IF NOT EXISTS pages_ad AFTER DELETE ON pages BEGIN
    INSERT INTO pages_fts(pages_fts, rowid, title, snippet)
    VALUES ('delete', old.rowid, old.title, old.snippet);
END;

CREATE TRIGGER IF NOT EXISTS pages_au AFTER UPDATE ON pages BEGIN
    INSERT INTO pages_fts(pages_fts, rowid, title, snippet)
    VALUES ('delete', old.rowid, old.title, old.snippet);
    INSERT INTO pages_fts(rowid, title, snippet) VALUES (new.rowid, new.title, new.snippet);
END;

-- Title and snippet alone give poor recall: "quantum entanglement" matched only
-- 3 of 22,845 pages. page_content holds the cleaned body text of 18,288 of them,
-- so it gets its own index and the API merges both result sets.
CREATE VIRTUAL TABLE IF NOT EXISTS content_fts USING fts5(
    content,
    content='page_content',
    content_rowid='rowid',
    tokenize='porter unicode61'
);

CREATE TRIGGER IF NOT EXISTS content_ai AFTER INSERT ON page_content BEGIN
    INSERT INTO content_fts(rowid, content) VALUES (new.rowid, new.content);
END;

CREATE TRIGGER IF NOT EXISTS content_ad AFTER DELETE ON page_content BEGIN
    INSERT INTO content_fts(content_fts, rowid, content) VALUES ('delete', old.rowid, old.content);
END;

CREATE TRIGGER IF NOT EXISTS content_au AFTER UPDATE ON page_content BEGIN
    INSERT INTO content_fts(content_fts, rowid, content) VALUES ('delete', old.rowid, old.content);
    INSERT INTO content_fts(rowid, content) VALUES (new.rowid, new.content);
END;

CREATE TRIGGER IF NOT EXISTS news_ai AFTER INSERT ON news BEGIN
    INSERT INTO news_fts(rowid, title, snippet) VALUES (new.rowid, new.title, new.snippet);
END;

CREATE TRIGGER IF NOT EXISTS news_ad AFTER DELETE ON news BEGIN
    INSERT INTO news_fts(news_fts, rowid, title, snippet)
    VALUES ('delete', old.rowid, old.title, old.snippet);
END;

CREATE TRIGGER IF NOT EXISTS news_au AFTER UPDATE ON news BEGIN
    INSERT INTO news_fts(news_fts, rowid, title, snippet)
    VALUES ('delete', old.rowid, old.title, old.snippet);
    INSERT INTO news_fts(rowid, title, snippet) VALUES (new.rowid, new.title, new.snippet);
END;
