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

-- name: ListBookMatchesForReview :many
SELECT
    b.id AS book_id,
    b.match_status AS final_status,
    COALESCE(m.status, '') AS match_status,
    COALESCE(m.confidence, 0) AS confidence,
    COALESCE(m.method, '') AS method,
    COALESCE(m.edition_key, '') AS edition_key,
    COALESCE(m.work_key, '') AS work_key,
    COALESCE(m.isbn, '') AS isbn,
    COALESCE(m.isbn_source, '') AS isbn_source,
    COALESCE(m.review_reason, '') AS review_reason,
    COALESCE(m.score_gap, 0) AS score_gap,
    COALESCE(m.candidate_count, 0) AS candidate_count,
    COALESCE(m.work_count, 0) AS work_count,
    COALESCE(m.matcher_version, '') AS matcher_version,
    COALESCE(m.dump_version, '') AS dump_version
FROM books b
LEFT JOIN book_matches m ON m.book_id = b.id
WHERE b.match_status IN ('probable', 'ambiguous', 'needs_review', 'not_found', 'no_data')
ORDER BY
    CASE b.match_status
        WHEN 'probable' THEN 0
        WHEN 'ambiguous' THEN 1
        WHEN 'needs_review' THEN 2
        WHEN 'not_found' THEN 3
        ELSE 4
    END,
    b.id;

-- name: ListBookMatchCandidatesForReview :many
SELECT c.*
FROM book_match_candidates c
JOIN books b ON b.id = c.book_id
WHERE b.match_status IN ('probable', 'ambiguous', 'needs_review', 'not_found', 'no_data')
ORDER BY c.book_id, c.rank;

-- name: ListConfirmedBookMatches :many
SELECT m.*
FROM book_matches m
JOIN books b ON b.id = m.book_id
WHERE b.match_status = 'confirmed'
ORDER BY m.book_id;

-- name: ResetBooksMatchStatus :exec
UPDATE books
SET match_status = COALESCE(
    (SELECT status FROM book_matches WHERE book_id = books.id),
    'unmatched'
);

-- name: SetBookMatchStatus :exec
UPDATE books SET match_status = ? WHERE id = ?;

-- name: ListBooksForConflictDetection :many
SELECT id, title FROM books ORDER BY id;

-- name: ListBookAuthorsForConflictDetection :many
SELECT ba.book_id, a.name
FROM book_authors ba
JOIN authors a ON a.id = ba.author_id
ORDER BY ba.book_id, a.name;

-- name: DeleteBookConflicts :exec
DELETE FROM book_conflicts;

-- name: CreateBookConflict :exec
INSERT INTO book_conflicts (book_id, peer_book_id, reason, score, created_at)
VALUES (?, ?, ?, ?, ?)
ON CONFLICT(book_id, peer_book_id) DO UPDATE SET
    reason = excluded.reason,
    score = excluded.score,
    created_at = excluded.created_at;

-- name: ListBookConflictsByBookID :many
SELECT peer_book_id, reason, score
FROM book_conflicts
WHERE book_id = ?
ORDER BY peer_book_id;

-- name: ListBookConflictsForReview :many
SELECT c.book_id, c.peer_book_id, c.reason, c.score
FROM book_conflicts c
JOIN books b ON b.id = c.book_id
WHERE b.match_status IN ('probable', 'ambiguous', 'needs_review', 'not_found', 'no_data')
ORDER BY c.book_id, c.peer_book_id;
