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
    content_score,
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
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(book_id) DO UPDATE SET
    status = excluded.status,
    confidence = excluded.confidence,
    content_score = excluded.content_score,
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

-- name: ListBookEnrichmentTargets :many
SELECT
    b.id AS book_id,
    b.title,
    CAST(COALESCE((
        SELECT a.name
        FROM book_authors ba
        JOIN authors a ON a.id = ba.author_id
        WHERE ba.book_id = b.id
        ORDER BY a.name
        LIMIT 1
    ), '') AS TEXT) AS author,
    m.edition_key,
    m.work_key,
    COALESCE(c.author_keys, '[]') AS author_keys
FROM books b
JOIN book_matches m ON m.book_id = b.id
LEFT JOIN book_enrichment e ON e.book_id = b.id
LEFT JOIN book_match_candidates c ON c.book_id = b.id AND c.selected = 1
WHERE b.match_status = 'confirmed'
  AND (
      sqlc.arg(force) = 1
      OR e.book_id IS NULL
      OR e.matcher_version <> sqlc.arg(matcher_version)
      OR e.dump_version <> sqlc.arg(dump_version)
  )
ORDER BY b.id;

-- name: GetBookEnrichmentByBookID :one
SELECT * FROM book_enrichment WHERE book_id = ?;

-- name: ListBookEnrichments :many
SELECT * FROM book_enrichment ORDER BY book_id;

-- name: UpsertBookEnrichment :exec
INSERT INTO book_enrichment (
    book_id,
    edition_key,
    work_key,
    source,
    title,
    subtitle,
    description,
    series,
    series_index,
    first_publish_date,
    matcher_version,
    dump_version,
    enriched_at
)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(book_id) DO UPDATE SET
    edition_key = excluded.edition_key,
    work_key = excluded.work_key,
    source = excluded.source,
    title = excluded.title,
    subtitle = excluded.subtitle,
    description = excluded.description,
    series = excluded.series,
    series_index = excluded.series_index,
    first_publish_date = excluded.first_publish_date,
    matcher_version = excluded.matcher_version,
    dump_version = excluded.dump_version,
    enriched_at = excluded.enriched_at;

-- name: DeleteBookEnrichmentByBookID :exec
DELETE FROM book_enrichment WHERE book_id = ?;

-- name: DeleteBookEnrichmentSubjectsByBookID :exec
DELETE FROM book_enrichment_subjects WHERE book_id = ?;

-- name: DeleteBookEnrichmentLanguagesByBookID :exec
DELETE FROM book_enrichment_languages WHERE book_id = ?;

-- name: DeleteBookEnrichmentPublicationsByBookID :exec
DELETE FROM book_enrichment_publications WHERE book_id = ?;

-- name: DeleteBookEnrichmentIdentifiersByBookID :exec
DELETE FROM book_enrichment_identifiers WHERE book_id = ?;

-- name: DeleteBookEnrichmentAuthorsByBookID :exec
DELETE FROM book_enrichment_authors WHERE book_id = ?;

-- name: CreateBookEnrichmentSubject :exec
INSERT INTO book_enrichment_subjects (book_id, name, ordinal)
VALUES (?, ?, ?)
ON CONFLICT(book_id, name) DO NOTHING;

-- name: CreateBookEnrichmentLanguage :exec
INSERT INTO book_enrichment_languages (book_id, name, ordinal)
VALUES (?, ?, ?)
ON CONFLICT(book_id, name) DO NOTHING;

-- name: CreateBookEnrichmentPublication :exec
INSERT INTO book_enrichment_publications (book_id, publisher, publication_date, ordinal)
VALUES (?, ?, ?, ?)
ON CONFLICT(book_id, publisher, publication_date) DO NOTHING;

-- name: CreateBookEnrichmentIdentifier :exec
INSERT INTO book_enrichment_identifiers (book_id, scheme, value, source, ordinal)
VALUES (?, ?, ?, ?, ?)
ON CONFLICT(book_id, scheme, value) DO NOTHING;

-- name: CreateBookEnrichmentAuthor :exec
INSERT INTO book_enrichment_authors (
    book_id,
    author_key,
    name,
    alternate_names,
    birth_date,
    death_date,
    bio,
    ordinal
)
VALUES (?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(book_id, author_key) DO NOTHING;

-- name: ListBookEnrichmentSubjects :many
SELECT book_id, name, ordinal FROM book_enrichment_subjects ORDER BY book_id, ordinal, name;

-- name: ListBookEnrichmentLanguages :many
SELECT book_id, name, ordinal FROM book_enrichment_languages ORDER BY book_id, ordinal, name;

-- name: ListBookEnrichmentPublications :many
SELECT book_id, publisher, publication_date, ordinal FROM book_enrichment_publications ORDER BY book_id, ordinal, publisher, publication_date;

-- name: ListBookEnrichmentIdentifiers :many
SELECT book_id, scheme, value, source, ordinal FROM book_enrichment_identifiers ORDER BY book_id, ordinal, scheme, value;

-- name: ListBookEnrichmentAuthors :many
SELECT book_id, author_key, name, alternate_names, birth_date, death_date, bio, ordinal FROM book_enrichment_authors ORDER BY book_id, ordinal, author_key;

-- name: DeleteNonConfirmedEnrichmentSubjects :exec
DELETE FROM book_enrichment_subjects
WHERE book_id NOT IN (SELECT id FROM books WHERE match_status = 'confirmed');

-- name: DeleteNonConfirmedEnrichmentLanguages :exec
DELETE FROM book_enrichment_languages
WHERE book_id NOT IN (SELECT id FROM books WHERE match_status = 'confirmed');

-- name: DeleteNonConfirmedEnrichmentPublications :exec
DELETE FROM book_enrichment_publications
WHERE book_id NOT IN (SELECT id FROM books WHERE match_status = 'confirmed');

-- name: DeleteNonConfirmedEnrichmentIdentifiers :exec
DELETE FROM book_enrichment_identifiers
WHERE book_id NOT IN (SELECT id FROM books WHERE match_status = 'confirmed');

-- name: DeleteNonConfirmedEnrichmentAuthors :exec
DELETE FROM book_enrichment_authors
WHERE book_id NOT IN (SELECT id FROM books WHERE match_status = 'confirmed');

-- name: DeleteNonConfirmedEnrichment :exec
DELETE FROM book_enrichment
WHERE book_id NOT IN (SELECT id FROM books WHERE match_status = 'confirmed');
