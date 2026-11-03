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

-- Plain B-tree indexes. A leading-wildcard LIKE can't use them; the trigram
-- indexes below handle image and file search.
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

-- ── Substring search for images and files ───────────────────────────────────
-- The API searches alt text and filenames with LIKE '%term%'. On the base tables
-- that reads every row (~290k for images) per search, and Turso bills by rows read.
-- A trigram FTS5 index answers the same LIKE from the index, with identical
-- results; terms shorter than 3 characters still fall back to a scan.

CREATE VIRTUAL TABLE IF NOT EXISTS images_fts USING fts5(
    alt,
    content='images',
    content_rowid='rowid',
    tokenize='trigram'
);

CREATE VIRTUAL TABLE IF NOT EXISTS files_fts USING fts5(
    filename,
    content='files',
    content_rowid='rowid',
    tokenize='trigram'
);

CREATE TRIGGER IF NOT EXISTS images_ai AFTER INSERT ON images BEGIN
    INSERT INTO images_fts(rowid, alt) VALUES (new.rowid, new.alt);
END;

CREATE TRIGGER IF NOT EXISTS images_ad AFTER DELETE ON images BEGIN
    INSERT INTO images_fts(images_fts, rowid, alt) VALUES ('delete', old.rowid, old.alt);
END;

CREATE TRIGGER IF NOT EXISTS images_au AFTER UPDATE ON images BEGIN
    INSERT INTO images_fts(images_fts, rowid, alt) VALUES ('delete', old.rowid, old.alt);
    INSERT INTO images_fts(rowid, alt) VALUES (new.rowid, new.alt);
END;

CREATE TRIGGER IF NOT EXISTS files_ai AFTER INSERT ON files BEGIN
    INSERT INTO files_fts(rowid, filename) VALUES (new.rowid, new.filename);
END;

CREATE TRIGGER IF NOT EXISTS files_ad AFTER DELETE ON files BEGIN
    INSERT INTO files_fts(files_fts, rowid, filename) VALUES ('delete', old.rowid, old.filename);
END;

CREATE TRIGGER IF NOT EXISTS files_au AFTER UPDATE ON files BEGIN
    INSERT INTO files_fts(files_fts, rowid, filename) VALUES ('delete', old.rowid, old.filename);
    INSERT INTO files_fts(rowid, filename) VALUES (new.rowid, new.filename);
END;

-- ── Spelling vocabulary ──────────────────────────────────────────────────────
-- Every word in the indexed text (title, snippet, cleaned body), unstemmed and
-- lowercased, with the number of pages it appears on. The search API uses it to
-- correct typos ("Showing results for ...") and for autocomplete. FTS5's own
-- vocabulary (fts5vocab) would be the natural source, but Turso refuses to create
-- fts5vocab tables, and its terms are Porter stems ("physic"), not words a user
-- would recognise. build_terms.py fills this table and keeps it current.
CREATE TABLE IF NOT EXISTS terms (
    term TEXT PRIMARY KEY,
    df   INTEGER NOT NULL
) WITHOUT ROWID;

-- build_terms.py's watermark: the highest pages.rowid already counted.
CREATE TABLE IF NOT EXISTS terms_state (
    key   TEXT PRIMARY KEY,
    value TEXT NOT NULL
);
