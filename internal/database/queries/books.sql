-- name: CreateBook :one
INSERT INTO books (title) VALUES (?) RETURNING id;

-- name: CountBooks :one
SELECT count(*) FROM books;

-- name: UpdateBookMetadata :exec
UPDATE books
SET title = ?,
    description = ?,
    series = ?,
    series_index = ?
WHERE id = ?;

-- name: CreateBookBookFile :exec
INSERT INTO book_book_files (book_id, book_file_id)
VALUES (?, ?)
ON CONFLICT(book_id, book_file_id) DO NOTHING;

-- name: GetBookIDByBookFileID :one
SELECT book_id FROM book_book_files WHERE book_file_id = ? LIMIT 1;

-- name: GetBookByID :one
SELECT * FROM books WHERE id = ?;

-- name: ListBookIDs :many
SELECT id FROM books ORDER BY id;

-- name: ListBookFilesWithAuthorsByBookID :many
SELECT
    bf.id AS file_id,
    bf.path AS file_path,
    bf.title AS file_title,
    CAST(IFNULL(GROUP_CONCAT(a.name, CHAR(31)), '') AS TEXT) AS file_authors
FROM book_book_files bbf
JOIN book_files bf ON bf.id = bbf.book_file_id
LEFT JOIN book_file_authors bfa ON bfa.book_file_id = bf.id
LEFT JOIN authors a ON a.id = bfa.author_id
WHERE bbf.book_id = ?
GROUP BY bf.id
ORDER BY bf.id;

-- name: ListDuplicatesByBookID :many
SELECT d.path AS duplicate_path
FROM book_file_duplicates d
JOIN book_files bf ON bf.hash = d.hash
JOIN book_book_files bbf ON bbf.book_file_id = bf.id
WHERE bbf.book_id = ?
ORDER BY d.path;

-- name: CreateBookAuthor :exec
INSERT INTO book_authors (book_id, author_id)
VALUES (?, ?)
ON CONFLICT(book_id, author_id) DO NOTHING;

-- name: DeleteBookAuthorsByBookID :exec
DELETE FROM book_authors WHERE book_id = ?;

-- name: DeleteBookSubjectsByBookID :exec
DELETE FROM book_subjects WHERE book_id = ?;

-- name: DeleteBookLanguagesByBookID :exec
DELETE FROM book_languages WHERE book_id = ?;

-- name: DeleteBookIdentifiersByBookID :exec
DELETE FROM book_identifiers WHERE book_id = ?;

-- name: CreateBookSubject :exec
INSERT INTO book_subjects (book_id, name, ordinal)
VALUES (?, ?, ?)
ON CONFLICT(book_id, name) DO NOTHING;

-- name: CreateBookLanguage :exec
INSERT INTO book_languages (book_id, name, ordinal)
VALUES (?, ?, ?)
ON CONFLICT(book_id, name) DO NOTHING;

-- name: CreateBookIdentifier :exec
INSERT INTO book_identifiers (book_id, scheme, value, source, ordinal)
VALUES (?, ?, ?, ?, ?)
ON CONFLICT(book_id, scheme, value) DO NOTHING;

-- name: DeleteBookPublicationsByBookID :exec
DELETE FROM book_publications WHERE book_id = ?;

-- name: CreateBookPublication :exec
INSERT INTO book_publications (book_id, publisher, publication_date, ordinal)
VALUES (?, ?, ?, ?)
ON CONFLICT(book_id, publisher, publication_date) DO NOTHING;

-- name: CreateLSHBucket :exec
INSERT INTO book_file_lsh_buckets (band_idx, bucket_hash, book_file_id)
VALUES (?, ?, ?)
ON CONFLICT(band_idx, bucket_hash, book_file_id) DO NOTHING;

-- name: FindCandidateMinHashesByBand :many
SELECT bf.id AS book_file_id, bf.minhash AS minhash
FROM book_file_lsh_buckets l
JOIN book_files bf ON bf.id = l.book_file_id
WHERE l.band_idx = ? AND l.bucket_hash = ?;

-- name: ListBooksWithFiles :many
SELECT
    b.id AS book_id,
    b.title AS book_title,
    bf.id AS file_id,
    bf.path AS file_path,
    bf.hash AS file_hash,
    bf.title AS file_title,
    bf.description AS file_description,
    bf.publisher AS file_publisher,
    bf.publication_date AS file_publication_date,
    bf.series AS file_series,
    bf.series_index AS file_series_index,
    CAST(IFNULL(GROUP_CONCAT(a.name, CHAR(31)), '') AS TEXT) AS file_authors
FROM books b
JOIN book_book_files bbf ON bbf.book_id = b.id
JOIN book_files bf ON bf.id = bbf.book_file_id
LEFT JOIN book_file_authors bfa ON bfa.book_file_id = bf.id
LEFT JOIN authors a ON a.id = bfa.author_id
GROUP BY b.id, bf.id
ORDER BY b.id, bf.id;

-- name: ListBookFileSubjectsWithBook :many
SELECT
    bbf.book_id AS book_id,
    bfs.name AS name
FROM book_file_subjects bfs
JOIN book_book_files bbf ON bbf.book_file_id = bfs.book_file_id
ORDER BY bbf.book_id, bbf.book_file_id, bfs.ordinal, bfs.name;

-- name: ListBookFileLanguagesWithBook :many
SELECT
    bbf.book_id AS book_id,
    bfl.name AS name
FROM book_file_languages bfl
JOIN book_book_files bbf ON bbf.book_file_id = bfl.book_file_id
ORDER BY bbf.book_id, bbf.book_file_id, bfl.ordinal, bfl.name;

-- name: ListBookFileIdentifiersWithBook :many
SELECT
    bbf.book_id AS book_id,
    bbf.book_file_id AS book_file_id,
    bfi.scheme AS scheme,
    bfi.value AS value,
    bfi.source AS source
FROM book_file_identifiers bfi
JOIN book_book_files bbf ON bbf.book_file_id = bfi.book_file_id
ORDER BY bbf.book_id, bbf.book_file_id, bfi.ordinal, bfi.scheme, bfi.value;

-- name: ListBookFileIdentifiersByBookID :many
SELECT
    bbf.book_file_id AS book_file_id,
    bfi.scheme AS scheme,
    bfi.value AS value,
    bfi.source AS source
FROM book_file_identifiers bfi
JOIN book_book_files bbf ON bbf.book_file_id = bfi.book_file_id
WHERE bbf.book_id = ?
ORDER BY bbf.book_file_id, bfi.ordinal, bfi.scheme, bfi.value;

-- name: ListBookSubjectsByBookID :many
SELECT name FROM book_subjects WHERE book_id = ? ORDER BY ordinal, name;

-- name: ListBookLanguagesByBookID :many
SELECT name FROM book_languages WHERE book_id = ? ORDER BY ordinal, name;

-- name: ListBookIdentifiersByBookID :many
SELECT scheme, value, source FROM book_identifiers WHERE book_id = ? ORDER BY ordinal, value;

-- name: ListBookPublicationsByBookID :many
SELECT publisher, publication_date FROM book_publications WHERE book_id = ? ORDER BY ordinal, publisher, publication_date;

-- name: ListBookFileDuplicatesWithBook :many
SELECT
    bbf.book_id AS book_id,
    d.path AS duplicate_path
FROM book_file_duplicates d
JOIN book_files bf ON bf.hash = d.hash
JOIN book_book_files bbf ON bbf.book_file_id = bf.id
ORDER BY bbf.book_id, d.path;
