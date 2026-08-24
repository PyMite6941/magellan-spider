-- Enhanced database schema for infection research crawler

CREATE TABLE IF NOT EXISTS pages (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	url TEXT UNIQUE,
	title TEXT,
	domain TEXT,
	path TEXT,
	content_hash TEXT,
	last_crawled TIMESTAMP,
	status_code INTEGER,
	response_time_ms INTEGER,
	content_type TEXT,
	content_length INTEGER,
	keywords TEXT,
	is_infection_related BOOLEAN DEFAULT 0,
	infection_keywords TEXT,
	data_quality_score INTEGER DEFAULT 0
);

CREATE TABLE IF NOT EXISTS pages_content (
	page_id INTEGER PRIMARY KEY,
	content TEXT,
	cleaned_content TEXT,
	FOREIGN KEY(page_id) REFERENCES pages(id)
);

CREATE TABLE IF NOT EXISTS images (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	page_id INTEGER,
	url TEXT,
	alt_text TEXT,
	is_infection_related BOOLEAN DEFAULT 0,
	is_scientific_image BOOLEAN DEFAULT 0,
	FOREIGN KEY(page_id) REFERENCES pages(id)
);

CREATE TABLE IF NOT EXISTS files (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	page_id INTEGER,
	url TEXT,
	file_type TEXT,
	is_infection_related BOOLEAN DEFAULT 0,
	is_scientific_data BOOLEAN DEFAULT 0,
	file_size INTEGER,
	FOREIGN KEY(page_id) REFERENCES pages(id)
);

CREATE TABLE IF NOT EXISTS crawl_queue (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	url TEXT UNIQUE,
	depth INTEGER,
	keywords TEXT,
	is_infection_related BOOLEAN DEFAULT 0,
	priority INTEGER DEFAULT 0,
	added_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS infection_research_data (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	page_id INTEGER,
	pathogen_type TEXT,
	pathogen_name TEXT,
	disease_name TEXT,
	research_focus TEXT,
	bioengineering_approach TEXT,
	key_findings TEXT,
	publication_date TEXT,
	authors TEXT,
	institution TEXT,
	journal TEXT,
	doi TEXT,
	methods_used TEXT,
	clinical_relevance TEXT,
	data_quality TEXT,
	last_updated TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
	FOREIGN KEY(page_id) REFERENCES pages(id)
);

CREATE TABLE IF NOT EXISTS infection_research_metadata (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	page_id INTEGER,
	genetic_sequence_available BOOLEAN DEFAULT 0,
	protein_structure_available BOOLEAN DEFAULT 0,
	experimental_data_available BOOLEAN DEFAULT 0,
	clinical_trial_data_available BOOLEAN DEFAULT 0,
	has_bioengineering_content BOOLEAN DEFAULT 0,
	has_ai_ml_content BOOLEAN DEFAULT 0,
	has_nanotechnology_content BOOLEAN DEFAULT 0,
	has_synthetic_biology_content BOOLEAN DEFAULT 0,
	data_completeness_score INTEGER DEFAULT 0,
	data_reliability_score INTEGER DEFAULT 0,
	FOREIGN KEY(page_id) REFERENCES pages(id)
);

CREATE TABLE IF NOT EXISTS infection_sources (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	domain TEXT UNIQUE,
	authority_score INTEGER DEFAULT 0,
	is_government_source BOOLEAN DEFAULT 0,
	is_academic_source BOOLEAN DEFAULT 0,
	is_health_organization BOOLEAN DEFAULT 0,
	is_bioengineering_source BOOLEAN DEFAULT 0,
	is_scientific_journal BOOLEAN DEFAULT 0,
	last_updated TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS infection_keywords (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	keyword TEXT UNIQUE,
	category TEXT,
	search_volume INTEGER DEFAULT 0,
	last_used TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS crawl_stats (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	crawl_session TEXT,
	pages_crawled INTEGER DEFAULT 0,
	infection_pages_crawled INTEGER DEFAULT 0,
	start_time TIMESTAMP,
	end_time TIMESTAMP,
	data_volume INTEGER DEFAULT 0
);