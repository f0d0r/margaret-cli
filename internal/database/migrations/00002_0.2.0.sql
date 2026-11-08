-- +goose Up
ALTER TABLE book_files ADD COLUMN description TEXT NOT NULL DEFAULT '';
ALTER TABLE book_files ADD COLUMN publisher TEXT NOT NULL DEFAULT '';
-- Raw publication date as returned by the ebook metadata.
ALTER TABLE book_files ADD COLUMN publication_date TEXT NOT NULL DEFAULT '';
ALTER TABLE book_files ADD COLUMN series TEXT NOT NULL DEFAULT '';
ALTER TABLE book_files ADD COLUMN series_index TEXT NOT NULL DEFAULT '';

ALTER TABLE books ADD COLUMN description TEXT NOT NULL DEFAULT '';
ALTER TABLE books ADD COLUMN series TEXT NOT NULL DEFAULT '';
ALTER TABLE books ADD COLUMN series_index TEXT NOT NULL DEFAULT '';

CREATE TABLE book_file_subjects (
    book_file_id INTEGER NOT NULL,
    name TEXT NOT NULL,
    ordinal INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (book_file_id, name),
    FOREIGN KEY (book_file_id) REFERENCES book_files(id) ON DELETE CASCADE
);

CREATE TABLE book_file_languages (
    book_file_id INTEGER NOT NULL,
    name TEXT NOT NULL,
    ordinal INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (book_file_id, name),
    FOREIGN KEY (book_file_id) REFERENCES book_files(id) ON DELETE CASCADE
);

CREATE TABLE book_file_identifiers (
    book_file_id INTEGER NOT NULL,
    scheme TEXT NOT NULL DEFAULT '',
    value TEXT NOT NULL,
    source TEXT NOT NULL DEFAULT 'METADATA'
        CHECK (source IN ('METADATA', 'TEXT', 'METADATA_AND_TEXT')),
    ordinal INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (book_file_id, scheme, value),
    FOREIGN KEY (book_file_id) REFERENCES book_files(id) ON DELETE CASCADE
);

CREATE TABLE book_subjects (
    book_id INTEGER NOT NULL,
    name TEXT NOT NULL,
    ordinal INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (book_id, name),
    FOREIGN KEY (book_id) REFERENCES books(id) ON DELETE CASCADE
);

CREATE TABLE book_languages (
    book_id INTEGER NOT NULL,
    name TEXT NOT NULL,
    ordinal INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (book_id, name),
    FOREIGN KEY (book_id) REFERENCES books(id) ON DELETE CASCADE
);

CREATE TABLE book_identifiers (
    book_id INTEGER NOT NULL,
    scheme TEXT NOT NULL DEFAULT '',
    value TEXT NOT NULL,
    source TEXT NOT NULL DEFAULT 'METADATA'
        CHECK (source IN ('METADATA', 'TEXT', 'METADATA_AND_TEXT')),
    ordinal INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (book_id, scheme, value),
    FOREIGN KEY (book_id) REFERENCES books(id) ON DELETE CASCADE
);

CREATE TABLE book_publications (
    book_id INTEGER NOT NULL,
    publisher TEXT NOT NULL DEFAULT '',
    publication_date TEXT NOT NULL DEFAULT ''
        CHECK (
            publication_date = ''
            OR publication_date GLOB '[0-9][0-9][0-9][0-9]'
            OR publication_date GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]'
            OR publication_date GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]'
        ),
    ordinal INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (book_id, publisher, publication_date),
    FOREIGN KEY (book_id) REFERENCES books(id) ON DELETE CASCADE
);

ALTER TABLE books ADD COLUMN match_status TEXT NOT NULL DEFAULT 'unmatched'
    CHECK (match_status IN (
        'unmatched', 'confirmed', 'probable', 'ambiguous',
        'needs_review', 'not_found', 'no_data'
    ));

CREATE INDEX books_match_status_idx ON books(match_status);

-- book_matches stores the final ranking decision for one scanned book.
CREATE TABLE book_matches (
    book_id INTEGER PRIMARY KEY,
    status TEXT NOT NULL CHECK (status IN (
        'confirmed', 'probable', 'ambiguous',
        'needs_review', 'not_found', 'no_data'
    )),
    confidence REAL NOT NULL DEFAULT 0,
    method TEXT NOT NULL DEFAULT '',
    edition_key TEXT NOT NULL DEFAULT '',
    work_key TEXT NOT NULL DEFAULT '',
    isbn TEXT NOT NULL DEFAULT '',
    isbn_source TEXT NOT NULL DEFAULT ''
        CHECK (isbn_source IN ('', 'METADATA', 'TEXT', 'METADATA_AND_TEXT')),
    review_reason TEXT NOT NULL DEFAULT '',
    score_gap REAL NOT NULL DEFAULT 0,
    candidate_count INTEGER NOT NULL DEFAULT 0,
    work_count INTEGER NOT NULL DEFAULT 0,
    source TEXT NOT NULL DEFAULT 'OPENLIBRARY',
    matcher_version TEXT NOT NULL DEFAULT '',
    dump_version TEXT NOT NULL DEFAULT '',
    matched_at INTEGER NOT NULL,
    FOREIGN KEY (book_id) REFERENCES books(id) ON DELETE CASCADE
);

CREATE INDEX book_matches_status_idx ON book_matches(status);
CREATE INDEX book_matches_version_idx
    ON book_matches(matcher_version, dump_version);

-- book_match_candidates holds the top work groups considered for a book.
CREATE TABLE book_match_candidates (
    book_id INTEGER NOT NULL,
    rank INTEGER NOT NULL CHECK (rank >= 1),
    selected INTEGER NOT NULL DEFAULT 0 CHECK (selected IN (0, 1)),
    edition_count INTEGER NOT NULL DEFAULT 1,

    edition_key TEXT NOT NULL,
    work_key TEXT NOT NULL DEFAULT '',
    edition_title TEXT NOT NULL DEFAULT '',
    edition_authors TEXT NOT NULL DEFAULT '',
    author_keys TEXT NOT NULL DEFAULT '',
    author_names TEXT NOT NULL DEFAULT '',

    isbn TEXT NOT NULL DEFAULT '',
    isbn_source TEXT NOT NULL DEFAULT ''
        CHECK (isbn_source IN ('', 'METADATA', 'TEXT', 'METADATA_AND_TEXT')),
    isbn_priority INTEGER NOT NULL DEFAULT 3,

    method TEXT NOT NULL DEFAULT '',
    title_score REAL NOT NULL DEFAULT 0,
    author_score REAL NOT NULL DEFAULT 0,
    content_score REAL NOT NULL DEFAULT 0,
    score REAL NOT NULL DEFAULT 0,
    evidence TEXT NOT NULL DEFAULT '{}',

    matcher_version TEXT NOT NULL DEFAULT '',
    dump_version TEXT NOT NULL DEFAULT '',
    created_at INTEGER NOT NULL,

    PRIMARY KEY (book_id, rank),
    FOREIGN KEY (book_id) REFERENCES books(id) ON DELETE CASCADE
);

CREATE INDEX book_match_candidates_book_score_idx
    ON book_match_candidates(book_id, score DESC);
CREATE INDEX book_match_candidates_edition_idx
    ON book_match_candidates(edition_key);

-- Enriched values are stored separately so the raw scan metadata stays
-- auditable and nothing is silently overwritten.
CREATE TABLE book_enrichment (
    book_id INTEGER PRIMARY KEY,
    edition_key TEXT NOT NULL DEFAULT '',
    work_key TEXT NOT NULL DEFAULT '',
    source TEXT NOT NULL DEFAULT 'OPENLIBRARY',
    title TEXT NOT NULL DEFAULT '',
    description TEXT NOT NULL DEFAULT '',
    series TEXT NOT NULL DEFAULT '',
    series_index TEXT NOT NULL DEFAULT '',
    first_publish_date TEXT NOT NULL DEFAULT '',
    matcher_version TEXT NOT NULL DEFAULT '',
    dump_version TEXT NOT NULL DEFAULT '',
    enriched_at INTEGER NOT NULL,
    FOREIGN KEY (book_id) REFERENCES books(id) ON DELETE CASCADE
);

CREATE TABLE book_enrichment_subjects (
    book_id INTEGER NOT NULL,
    name TEXT NOT NULL,
    ordinal INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (book_id, name),
    FOREIGN KEY (book_id) REFERENCES books(id) ON DELETE CASCADE
);

CREATE TABLE book_enrichment_languages (
    book_id INTEGER NOT NULL,
    name TEXT NOT NULL,
    ordinal INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (book_id, name),
    FOREIGN KEY (book_id) REFERENCES books(id) ON DELETE CASCADE
);

CREATE TABLE book_enrichment_publications (
    book_id INTEGER NOT NULL,
    publisher TEXT NOT NULL DEFAULT '',
    publication_date TEXT NOT NULL DEFAULT '',
    ordinal INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (book_id, publisher, publication_date),
    FOREIGN KEY (book_id) REFERENCES books(id) ON DELETE CASCADE
);

CREATE TABLE book_enrichment_identifiers (
    book_id INTEGER NOT NULL,
    scheme TEXT NOT NULL DEFAULT '',
    value TEXT NOT NULL,
    source TEXT NOT NULL DEFAULT 'OPENLIBRARY',
    ordinal INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (book_id, scheme, value),
    FOREIGN KEY (book_id) REFERENCES books(id) ON DELETE CASCADE
);

CREATE TABLE book_enrichment_authors (
    book_id INTEGER NOT NULL,
    author_key TEXT NOT NULL,
    name TEXT NOT NULL DEFAULT '',
    alternate_names TEXT NOT NULL DEFAULT '',
    birth_date TEXT NOT NULL DEFAULT '',
    death_date TEXT NOT NULL DEFAULT '',
    bio TEXT NOT NULL DEFAULT '',
    ordinal INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (book_id, author_key),
    FOREIGN KEY (book_id) REFERENCES books(id) ON DELETE CASCADE
);

-- +goose Down
DROP TABLE IF EXISTS book_enrichment_authors;
DROP TABLE IF EXISTS book_enrichment_identifiers;
DROP TABLE IF EXISTS book_enrichment_publications;
DROP TABLE IF EXISTS book_enrichment_languages;
DROP TABLE IF EXISTS book_enrichment_subjects;
DROP TABLE IF EXISTS book_enrichment;
DROP TABLE IF EXISTS book_match_candidates;
DROP TABLE IF EXISTS book_matches;
DROP INDEX IF EXISTS books_match_status_idx;
ALTER TABLE books DROP COLUMN match_status;
DROP TABLE IF EXISTS book_publications;
DROP TABLE IF EXISTS book_identifiers;
DROP TABLE IF EXISTS book_languages;
DROP TABLE IF EXISTS book_subjects;
DROP TABLE IF EXISTS book_file_identifiers;
DROP TABLE IF EXISTS book_file_languages;
DROP TABLE IF EXISTS book_file_subjects;

ALTER TABLE books DROP COLUMN series_index;
ALTER TABLE books DROP COLUMN series;
ALTER TABLE books DROP COLUMN description;

ALTER TABLE book_files DROP COLUMN series_index;
ALTER TABLE book_files DROP COLUMN series;
ALTER TABLE book_files DROP COLUMN publication_date;
ALTER TABLE book_files DROP COLUMN publisher;
ALTER TABLE book_files DROP COLUMN description;
