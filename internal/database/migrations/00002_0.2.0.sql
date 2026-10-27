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

-- +goose Down
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
