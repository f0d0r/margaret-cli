CREATE TABLE books (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    title TEXT NOT NULL DEFAULT ''
);

CREATE TABLE book_files (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    hash TEXT NOT NULL UNIQUE,
    path TEXT NOT NULL,
    title TEXT NOT NULL DEFAULT '',
    minhash BLOB,
    simhash INTEGER
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

CREATE TRIGGER authors_fts_insert AFTER INSERT ON authors BEGIN
    INSERT INTO authors_fts(rowid, name) VALUES (new.id, new.name);
END;

CREATE TRIGGER authors_fts_update AFTER UPDATE ON authors BEGIN
    INSERT INTO authors_fts(authors_fts, rowid, name) VALUES ('delete', old.id, old.name);
    INSERT INTO authors_fts(rowid, name) VALUES (new.id, new.name);
END;

CREATE TRIGGER authors_fts_delete AFTER DELETE ON authors BEGIN
    INSERT INTO authors_fts(authors_fts, rowid, name) VALUES ('delete', old.id, old.name);
END;

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