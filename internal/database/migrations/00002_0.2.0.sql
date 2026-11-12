-- +goose Up
ALTER TABLE book_files ADD COLUMN description TEXT NOT NULL DEFAULT '';
ALTER TABLE book_files ADD COLUMN publisher TEXT NOT NULL DEFAULT '';
-- Raw publication date as returned by the ebook metadata.
ALTER TABLE book_files ADD COLUMN publication_date TEXT NOT NULL DEFAULT '';
ALTER TABLE book_files ADD COLUMN series TEXT NOT NULL DEFAULT '';
ALTER TABLE book_files ADD COLUMN series_index TEXT NOT NULL DEFAULT '';
ALTER TABLE book_files ADD COLUMN head_bloom BLOB;
-- Normalized title-page window (JSON list of lines) used for file- and
-- book-level attestation without keeping the raw head.
ALTER TABLE book_files ADD COLUMN title_window TEXT NOT NULL DEFAULT '';

-- The title and author fields are resolved from the claims below. The legacy
-- fields keep the value the pre-claim parser produced, so the change can be
-- audited and rolled back while the transition lasts.
ALTER TABLE book_files ADD COLUMN legacy_title TEXT NOT NULL DEFAULT '';
ALTER TABLE book_files ADD COLUMN title_status TEXT NOT NULL DEFAULT 'none'
    CHECK (title_status IN ('', 'none', 'weak', 'single', 'corroborated', 'conflict'));
ALTER TABLE book_files ADD COLUMN author_status TEXT NOT NULL DEFAULT 'none'
    CHECK (author_status IN ('', 'none', 'weak', 'single', 'corroborated', 'conflict'));
ALTER TABLE book_files ADD COLUMN title_set TEXT NOT NULL DEFAULT '';
ALTER TABLE book_files ADD COLUMN author_set TEXT NOT NULL DEFAULT '';
-- claim_version covers extraction and attestation; resolve_version covers
-- resolution rules that can be re-run from the stored claims.
ALTER TABLE book_files ADD COLUMN claim_version INTEGER NOT NULL DEFAULT 0;
ALTER TABLE book_files ADD COLUMN resolve_version INTEGER NOT NULL DEFAULT 0;

UPDATE book_files SET legacy_title = title WHERE legacy_title = '';

ALTER TABLE books ADD COLUMN description TEXT NOT NULL DEFAULT '';
ALTER TABLE books ADD COLUMN series TEXT NOT NULL DEFAULT '';
ALTER TABLE books ADD COLUMN series_index TEXT NOT NULL DEFAULT '';
ALTER TABLE books ADD COLUMN title_status TEXT NOT NULL DEFAULT 'none'
    CHECK (title_status IN ('', 'none', 'weak', 'single', 'cross_file', 'corroborated', 'conflict'));
ALTER TABLE books ADD COLUMN author_status TEXT NOT NULL DEFAULT 'none'
    CHECK (author_status IN ('', 'none', 'weak', 'single', 'cross_file', 'corroborated', 'conflict'));
ALTER TABLE books ADD COLUMN grouping_suspect INTEGER NOT NULL DEFAULT 0
    CHECK (grouping_suspect IN (0, 1));
ALTER TABLE books ADD COLUMN grouping_reason TEXT NOT NULL DEFAULT '';
ALTER TABLE books ADD COLUMN book_resolve_version INTEGER NOT NULL DEFAULT 0;

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

-- book_file_claims stores every title and author statement extracted from a
-- file, together with where it came from and how the leading text supports it.
CREATE TABLE book_file_claims (
    book_file_id INTEGER NOT NULL,
    path TEXT NOT NULL DEFAULT '',
    set_key TEXT NOT NULL,
    source TEXT NOT NULL CHECK (source IN ('METADATA', 'TEXT', 'FILENAME')),
    origin TEXT NOT NULL DEFAULT '',
    field TEXT NOT NULL CHECK (field IN ('title', 'author')),
    value TEXT NOT NULL,
    ordinal INTEGER NOT NULL DEFAULT 0,
    junk INTEGER NOT NULL DEFAULT 0 CHECK (junk IN (0, 1)),
    trust TEXT NOT NULL DEFAULT 'WEAK' CHECK (trust IN ('STRONG', 'WEAK')),
    witness TEXT NOT NULL DEFAULT 'SUPPORTING'
        CHECK (witness IN ('INDEPENDENT', 'SUPPORTING', 'DERIVED')),
    attested INTEGER NOT NULL DEFAULT 0 CHECK (attested IN (0, 1)),
    attestation REAL NOT NULL DEFAULT 0
        CHECK (attestation >= 0 AND attestation <= 1),
    attestation_method TEXT NOT NULL DEFAULT 'none',
    attestation_level TEXT NOT NULL DEFAULT 'none',
    attestation_residual TEXT NOT NULL DEFAULT '[]',
    suspicious_signals INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (book_file_id, path, set_key, field, value),
    FOREIGN KEY (book_file_id) REFERENCES book_files(id) ON DELETE CASCADE
);

CREATE INDEX book_file_claims_lookup_idx
    ON book_file_claims(book_file_id, field, path);

-- The pre-claim author list, kept alongside legacy_title while the transition
-- lasts. Names are stored verbatim; no FK because the old list may contain
-- names that are no longer present in authors.
CREATE TABLE book_file_legacy_authors (
    book_file_id INTEGER NOT NULL,
    name TEXT NOT NULL,
    ordinal INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (book_file_id, name, ordinal),
    FOREIGN KEY (book_file_id) REFERENCES book_files(id) ON DELETE CASCADE
);

INSERT INTO book_file_legacy_authors (book_file_id, name, ordinal)
SELECT
    bfa.book_file_id,
    a.name,
    ROW_NUMBER() OVER (PARTITION BY bfa.book_file_id ORDER BY a.id) - 1
FROM book_file_authors bfa
JOIN authors a ON a.id = bfa.author_id;

-- Book-level title/author hypotheses, including alternatives kept for the
-- matcher and for audit.
CREATE TABLE book_claim_candidates (
    book_id INTEGER NOT NULL,
    field TEXT NOT NULL CHECK (field IN ('title', 'author')),
    rank INTEGER NOT NULL CHECK (rank >= 1),
    value TEXT NOT NULL,
    authors TEXT NOT NULL DEFAULT '[]',
    status TEXT NOT NULL CHECK (status IN (
        'none', 'weak', 'single', 'cross_file', 'corroborated', 'conflict'
    )),
    disposition TEXT NOT NULL CHECK (disposition IN (
        'selected', 'compatible_truncation', 'overruled', 'conflict'
    )),
    reason TEXT NOT NULL DEFAULT '',
    supporting_witnesses INTEGER NOT NULL DEFAULT 0,
    strict_supporting_claims INTEGER NOT NULL DEFAULT 0,
    source_groups INTEGER NOT NULL DEFAULT 0,
    independent_witnesses INTEGER NOT NULL DEFAULT 0,
    attested INTEGER NOT NULL DEFAULT 0 CHECK (attested IN (0, 1)),
    suspicious INTEGER NOT NULL DEFAULT 0 CHECK (suspicious IN (0, 1)),
    attestation_level TEXT NOT NULL DEFAULT 'none',
    max_attestation REAL NOT NULL DEFAULT 0,
    found_in TEXT NOT NULL DEFAULT '[]',
    residual TEXT NOT NULL DEFAULT '[]',
    use_for_matching INTEGER NOT NULL DEFAULT 1 CHECK (use_for_matching IN (0, 1)),
    use_for_agreement INTEGER NOT NULL DEFAULT 1 CHECK (use_for_agreement IN (0, 1)),
    evidence TEXT NOT NULL DEFAULT '{}',
    PRIMARY KEY (book_id, field, rank),
    FOREIGN KEY (book_id) REFERENCES books(id) ON DELETE CASCADE
);

CREATE INDEX book_claim_candidates_book_field_idx
    ON book_claim_candidates(book_id, field, rank);

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
    content_score REAL NOT NULL DEFAULT 0,
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

-- Book pairs whose local metadata claims they are the same book but whose
-- content MinHash similarity is below the near-duplicate threshold.
CREATE TABLE book_conflicts (
    book_id INTEGER NOT NULL,
    peer_book_id INTEGER NOT NULL,
    reason TEXT NOT NULL,
    score REAL NOT NULL DEFAULT 0,
    created_at INTEGER NOT NULL,
    PRIMARY KEY (book_id, peer_book_id),
    FOREIGN KEY (book_id) REFERENCES books(id) ON DELETE CASCADE,
    FOREIGN KEY (peer_book_id) REFERENCES books(id) ON DELETE CASCADE
);

CREATE INDEX book_conflicts_peer_idx ON book_conflicts(peer_book_id);

-- Enriched values are stored separately so the raw scan metadata stays
-- auditable and nothing is silently overwritten.
CREATE TABLE book_enrichment (
    book_id INTEGER PRIMARY KEY,
    edition_key TEXT NOT NULL DEFAULT '',
    work_key TEXT NOT NULL DEFAULT '',
    source TEXT NOT NULL DEFAULT 'OPENLIBRARY',
    title TEXT NOT NULL DEFAULT '',
    subtitle TEXT NOT NULL DEFAULT '',
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
DROP TABLE IF EXISTS book_claim_candidates;
DROP TABLE IF EXISTS book_file_claims;
DROP TABLE IF EXISTS book_file_legacy_authors;
DROP TABLE IF EXISTS book_enrichment_authors;
DROP TABLE IF EXISTS book_enrichment_identifiers;
DROP TABLE IF EXISTS book_enrichment_publications;
DROP TABLE IF EXISTS book_enrichment_languages;
DROP TABLE IF EXISTS book_enrichment_subjects;
DROP TABLE IF EXISTS book_enrichment;
DROP TABLE IF EXISTS book_conflicts;
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

ALTER TABLE books DROP COLUMN book_resolve_version;
ALTER TABLE books DROP COLUMN grouping_reason;
ALTER TABLE books DROP COLUMN grouping_suspect;
ALTER TABLE books DROP COLUMN author_status;
ALTER TABLE books DROP COLUMN title_status;
ALTER TABLE books DROP COLUMN series_index;
ALTER TABLE books DROP COLUMN series;
ALTER TABLE books DROP COLUMN description;

ALTER TABLE book_files DROP COLUMN title_window;
ALTER TABLE book_files DROP COLUMN series_index;
ALTER TABLE book_files DROP COLUMN series;
ALTER TABLE book_files DROP COLUMN head_bloom;
ALTER TABLE book_files DROP COLUMN publication_date;
ALTER TABLE book_files DROP COLUMN publisher;
ALTER TABLE book_files DROP COLUMN description;
ALTER TABLE book_files DROP COLUMN legacy_title;
ALTER TABLE book_files DROP COLUMN title_status;
ALTER TABLE book_files DROP COLUMN author_status;
ALTER TABLE book_files DROP COLUMN title_set;
ALTER TABLE book_files DROP COLUMN author_set;
ALTER TABLE book_files DROP COLUMN claim_version;
ALTER TABLE book_files DROP COLUMN resolve_version;
