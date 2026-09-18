-- name: CreateBookFile :one
INSERT INTO book_files (hash, path, title, minhash)
VALUES (?, ?, ?, ?)
ON CONFLICT(hash) DO NOTHING
RETURNING id;

-- name: GetBookFileByHash :one
SELECT * FROM book_files WHERE hash = ? LIMIT 1;

-- name: GetBookFileByPath :one
SELECT * FROM book_files WHERE path = ? LIMIT 1;

-- name: CountBookFiles :one
SELECT count(*) FROM book_files;

-- name: CreateBookFileAuthor :exec
INSERT INTO book_file_authors (book_file_id, author_id) VALUES (?, ?);

-- name: CreateBookFileDuplicate :exec
INSERT INTO book_file_duplicates (hash, path)
VALUES (?, ?)
ON CONFLICT(hash, path) DO NOTHING;

-- name: GetBookFileDuplicateByPath :one
SELECT hash, path FROM book_file_duplicates WHERE path = ? LIMIT 1;

-- name: ListBookFileDuplicates :many
SELECT
    bf.path AS path,
    bf.title AS title,
    CAST(IFNULL(GROUP_CONCAT(a.name, ', '), '') AS TEXT) AS authors,
    d.path AS duplicate_path
FROM book_file_duplicates d
JOIN book_files bf ON bf.hash = d.hash
LEFT JOIN book_file_authors bfa ON bfa.book_file_id = bf.id
LEFT JOIN authors a ON a.id = bfa.author_id
GROUP BY d.hash, d.path
ORDER BY bf.path;

-- name: ListBookFileByBookID :many
SELECT bf.*
FROM book_files bf
JOIN book_book_files bbf ON bbf.book_file_id = bf.id
WHERE bbf.book_id = ?
ORDER BY bf.id;