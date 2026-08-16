CREATE TABLE book_files (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    hash TEXT NOT NULL UNIQUE,
    path TEXT NOT NULL,
    title TEXT NOT NULL DEFAULT ''
);

CREATE TABLE authors (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    name TEXT NOT NULL UNIQUE
);

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