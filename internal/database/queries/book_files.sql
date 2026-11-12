-- name: CreateBookFile :one
INSERT INTO book_files (
    hash,
    path,
    title,
    minhash,
    head_bloom,
    title_window,
    description,
    publisher,
    publication_date,
    series,
    series_index,
    legacy_title,
    title_status,
    author_status,
    title_set,
    author_set,
    claim_version,
    resolve_version
)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
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

-- name: DeleteBookFileAuthorsByBookFileID :exec
DELETE FROM book_file_authors WHERE book_file_id = ?;

-- name: CreateBookFileLegacyAuthor :exec
INSERT INTO book_file_legacy_authors (book_file_id, name, ordinal)
VALUES (?, ?, ?)
ON CONFLICT(book_file_id, name, ordinal) DO NOTHING;

-- name: DeleteBookFileLegacyAuthorsByBookFileID :exec
DELETE FROM book_file_legacy_authors WHERE book_file_id = ?;

-- name: ListBookFileLegacyAuthorsByBookFileID :many
SELECT name, ordinal
FROM book_file_legacy_authors
WHERE book_file_id = ?
ORDER BY ordinal, name;

-- name: CreateBookFileClaim :exec
INSERT INTO book_file_claims (
    book_file_id,
    path,
    set_key,
    source,
    origin,
    field,
    value,
    ordinal,
    junk,
    trust,
    witness,
    attested,
    attestation,
    attestation_method,
    attestation_level,
    attestation_residual,
    suspicious_signals
)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(book_file_id, path, set_key, field, value) DO NOTHING;

-- name: DeleteBookFileClaimsByBookFileID :exec
DELETE FROM book_file_claims WHERE book_file_id = ?;

-- name: ListBookFileClaimsByBookFileID :many
SELECT *
FROM book_file_claims
WHERE book_file_id = ?
ORDER BY source, set_key, field, ordinal, value;

-- name: ListBookFileClaimsWithBook :many
SELECT
    bbf.book_id AS book_id,
    c.book_file_id,
    c.path,
    c.set_key,
    c.source,
    c.origin,
    c.field,
    c.value,
    c.ordinal,
    c.junk,
    c.trust,
    c.witness,
    c.attested,
    c.attestation,
    c.attestation_method,
    c.attestation_level,
    c.attestation_residual,
    c.suspicious_signals
FROM book_file_claims c
JOIN book_book_files bbf ON bbf.book_file_id = c.book_file_id
ORDER BY bbf.book_id, c.book_file_id, c.source, c.set_key, c.field, c.ordinal, c.value;

-- name: UpdateBookFileResolution :exec
UPDATE book_files
SET title = ?,
    title_status = ?,
    author_status = ?,
    title_set = ?,
    author_set = ?,
    claim_version = ?,
    resolve_version = ?
WHERE id = ?;

-- name: UpdateBookFileLegacyTitle :exec
UPDATE book_files SET legacy_title = ? WHERE id = ? AND legacy_title = '';

-- name: ListBookFilesForClaimResolution :many
SELECT *
FROM book_files
WHERE claim_version < ? OR resolve_version < ?;

-- name: ListBookFilesNeedingResolution :many
SELECT *
FROM book_files
WHERE claim_version >= ? AND resolve_version < ?
ORDER BY id;

-- name: CreateBookFileSubject :exec
INSERT INTO book_file_subjects (book_file_id, name, ordinal)
VALUES (?, ?, ?)
ON CONFLICT(book_file_id, name) DO NOTHING;

-- name: CreateBookFileLanguage :exec
INSERT INTO book_file_languages (book_file_id, name, ordinal)
VALUES (?, ?, ?)
ON CONFLICT(book_file_id, name) DO NOTHING;

-- name: CreateBookFileIdentifier :exec
INSERT INTO book_file_identifiers (book_file_id, scheme, value, source, ordinal)
VALUES (?, ?, ?, ?, ?)
ON CONFLICT(book_file_id, scheme, value) DO NOTHING;

-- name: CreateBookFileDuplicate :exec
INSERT INTO book_file_duplicates (hash, path)
VALUES (?, ?)
ON CONFLICT(hash, path) DO NOTHING;

-- name: GetBookFileDuplicateByPath :one
SELECT hash, path FROM book_file_duplicates WHERE path = ? LIMIT 1;

-- name: ListBookFileHashesNeedingCanonicalization :many
SELECT
    bf.hash AS hash,
    bf.path AS canonical_path,
    CAST(MIN(d.path) AS TEXT) AS smallest_duplicate_path
FROM book_files bf
JOIN book_file_duplicates d ON d.hash = bf.hash
GROUP BY bf.hash, bf.path
HAVING MIN(d.path) < bf.path;

-- name: UpdateBookFilePath :exec
UPDATE book_files SET path = ? WHERE hash = ?;

-- name: UpdateBookFileHeadBloom :exec
UPDATE book_files SET head_bloom = ? WHERE id = ?;

-- name: UpdateBookFileTitleWindow :exec
UPDATE book_files SET title_window = ? WHERE id = ?;

-- name: ListBookFileHeadBlooms :many
SELECT bbf.book_id, bf.head_bloom
FROM book_book_files bbf
JOIN book_files bf ON bf.id = bbf.book_file_id
WHERE length(bf.head_bloom) > 0
ORDER BY bbf.book_id, bf.id;

-- name: CountBookFilesMissingHeadBloom :one
SELECT count(*) FROM book_files WHERE head_bloom IS NULL OR length(head_bloom) = 0;

-- name: DeleteBookFileDuplicate :exec
DELETE FROM book_file_duplicates WHERE hash = ? AND path = ?;

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