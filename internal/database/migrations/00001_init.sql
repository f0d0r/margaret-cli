-- +goose Up
CREATE TABLE books (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    title TEXT NOT NULL DEFAULT ''
);

CREATE VIRTUAL TABLE books_fts USING FTS5(
    title,
    content='books',
    content_rowid='id',
    tokenize='trigram remove_diacritics 1'
);

-- +goose StatementBegin
CREATE TRIGGER books_fts_insert AFTER INSERT ON books BEGIN
    INSERT INTO books_fts(rowid, title) VALUES (new.id, new.title);
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER books_fts_update AFTER UPDATE ON books BEGIN
    INSERT INTO books_fts(books_fts, rowid, title) VALUES ('delete', old.id, old.title);
    INSERT INTO books_fts(rowid, title) VALUES (new.id, new.title);
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER books_fts_delete AFTER DELETE ON books BEGIN
    INSERT INTO books_fts(books_fts, rowid, title) VALUES ('delete', old.id, old.title);
END;
-- +goose StatementEnd

CREATE TABLE book_files (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    hash TEXT NOT NULL UNIQUE,
    path TEXT NOT NULL UNIQUE,
    title TEXT NOT NULL DEFAULT '',
    minhash BLOB
);

CREATE TABLE authors (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    name TEXT NOT NULL UNIQUE
);

CREATE VIRTUAL TABLE authors_fts USING FTS5(
    name,
    content='authors',
    content_rowid='id',
    tokenize='trigram remove_diacritics 1'
);

-- +goose StatementBegin
CREATE TRIGGER authors_fts_insert AFTER INSERT ON authors BEGIN
    INSERT INTO authors_fts(rowid, name) VALUES (new.id, new.name);
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER authors_fts_update AFTER UPDATE ON authors BEGIN
    INSERT INTO authors_fts(authors_fts, rowid, name) VALUES ('delete', old.id, old.name);
    INSERT INTO authors_fts(rowid, name) VALUES (new.id, new.name);
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER authors_fts_delete AFTER DELETE ON authors BEGIN
    INSERT INTO authors_fts(authors_fts, rowid, name) VALUES ('delete', old.id, old.name);
END;
-- +goose StatementEnd

CREATE TABLE book_file_authors (
    book_file_id INTEGER NOT NULL,
    author_id INTEGER NOT NULL,
    PRIMARY KEY (book_file_id, author_id),
    FOREIGN KEY (book_file_id) REFERENCES book_files(id) ON DELETE CASCADE,
    FOREIGN KEY (author_id) REFERENCES authors(id) ON DELETE CASCADE
);

CREATE TABLE book_file_duplicates (
    hash TEXT NOT NULL,
    path TEXT NOT NULL,
    PRIMARY KEY (hash, path),
    FOREIGN KEY (hash) REFERENCES book_files(hash) ON DELETE CASCADE
);

CREATE TABLE book_authors (
    book_id INTEGER NOT NULL,
    author_id INTEGER NOT NULL,
    PRIMARY KEY (book_id, author_id),
    FOREIGN KEY (book_id) REFERENCES books(id) ON DELETE CASCADE,
    FOREIGN KEY (author_id) REFERENCES authors(id) ON DELETE CASCADE
);

CREATE TABLE book_book_files (
    book_id INTEGER NOT NULL,
    book_file_id INTEGER NOT NULL,
    PRIMARY KEY (book_id, book_file_id),
    FOREIGN KEY (book_id) REFERENCES books(id) ON DELETE CASCADE,
    FOREIGN KEY (book_file_id) REFERENCES book_files(id) ON DELETE CASCADE
);

CREATE TABLE book_file_lsh_buckets (
    band_idx INTEGER NOT NULL,
    bucket_hash INTEGER NOT NULL,
    book_file_id INTEGER NOT NULL,
    PRIMARY KEY (band_idx, bucket_hash, book_file_id),
    FOREIGN KEY (book_file_id) REFERENCES book_files(id) ON DELETE CASCADE
);
-- NOTE: no extra index on (band_idx, bucket_hash): the PRIMARY KEY already
-- covers that leftmost prefix, a second index would only add write cost.

-- scan_runs records one row per scan run: the scanned root, the mode the
-- run used and its outcome. The prompt shown before a re-run over a
-- non-empty database reads the latest row to display the previous root and
-- time and to detect a different-root re-run. A fresh scan wipes this table
-- together with the scanned content (see database.Clear); a resumed scan
-- adds a new row.
CREATE TABLE scan_runs (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    root TEXT NOT NULL,
    mode TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'running',
    started_at INTEGER NOT NULL,
    finished_at INTEGER
);

-- +goose Down
DROP TABLE IF EXISTS scan_runs;
DROP TABLE IF EXISTS book_file_lsh_buckets;
DROP TABLE IF EXISTS book_book_files;
DROP TABLE IF EXISTS book_authors;
DROP TABLE IF EXISTS book_file_duplicates;
DROP TABLE IF EXISTS book_file_authors;
DROP TRIGGER IF EXISTS authors_fts_delete;
DROP TRIGGER IF EXISTS authors_fts_update;
DROP TRIGGER IF EXISTS authors_fts_insert;
DROP TABLE IF EXISTS authors_fts;
DROP TABLE IF EXISTS authors;
DROP TABLE IF EXISTS book_files;
DROP TABLE IF EXISTS books;
