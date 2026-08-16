-- name: CreateBookFile :one
INSERT INTO book_files (hash, path, title)
VALUES (?, ?, ?)
ON CONFLICT(hash) DO NOTHING
RETURNING id;

-- name: GetBookFileByHash :one
SELECT * FROM book_files WHERE hash = ? LIMIT 1;

-- name: CreateBookFileAuthor :exec
INSERT INTO book_file_authors (book_file_id, author_id) VALUES (?, ?);

-- name: CreateBookFileDuplicate :exec
INSERT INTO book_file_duplicates (hash, path) VALUES (?, ?);