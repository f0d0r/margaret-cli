-- name: ListBooksNeedingMatching :many
SELECT b.id, b.title
FROM books b
LEFT JOIN book_matches m ON m.book_id = b.id
WHERE m.book_id IS NULL
   OR m.matcher_version <> ?
   OR m.dump_version <> ?
ORDER BY b.id;

-- name: UpsertBookMatch :exec
INSERT INTO book_matches (
    book_id,
    status,
    confidence,
    method,
    edition_key,
    work_key,
    isbn,
    isbn_source,
    review_reason,
    score_gap,
    candidate_count,
    work_count,
    matcher_version,
    dump_version,
    matched_at
)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(book_id) DO UPDATE SET
    status = excluded.status,
    confidence = excluded.confidence,
    method = excluded.method,
    edition_key = excluded.edition_key,
    work_key = excluded.work_key,
    isbn = excluded.isbn,
    isbn_source = excluded.isbn_source,
    review_reason = excluded.review_reason,
    score_gap = excluded.score_gap,
    candidate_count = excluded.candidate_count,
    work_count = excluded.work_count,
    matcher_version = excluded.matcher_version,
    dump_version = excluded.dump_version,
    matched_at = excluded.matched_at;

-- name: UpdateBookMatchStatus :exec
UPDATE books SET match_status = ? WHERE id = ?;

-- name: DeleteBookMatchCandidatesByBookID :exec
DELETE FROM book_match_candidates WHERE book_id = ?;

-- name: CreateBookMatchCandidate :exec
INSERT INTO book_match_candidates (
    book_id,
    rank,
    selected,
    edition_count,
    edition_key,
    work_key,
    edition_title,
    edition_authors,
    author_keys,
    author_names,
    isbn,
    isbn_source,
    isbn_priority,
    method,
    title_score,
    author_score,
    content_score,
    score,
    evidence,
    matcher_version,
    dump_version,
    created_at
)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?);

-- name: GetBookMatchByBookID :one
SELECT * FROM book_matches WHERE book_id = ?;

-- name: ListBookMatchCandidatesByBookID :many
SELECT * FROM book_match_candidates WHERE book_id = ? ORDER BY rank;

-- name: CountBooksByMatchStatus :many
SELECT match_status AS status, count(*) AS count FROM books GROUP BY match_status;
