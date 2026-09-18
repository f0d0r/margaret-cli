-- name: CreateAuthor :exec
INSERT INTO authors (name)
VALUES (?)
ON CONFLICT(name) DO NOTHING;

-- name: GetAuthorByName :one
SELECT * FROM authors WHERE name = ?;

-- name: ListAuthorsByBookID :many
SELECT a.*
FROM authors a
JOIN book_authors ba ON ba.author_id = a.id
WHERE ba.book_id = ?
ORDER BY a.name;

-- name: DeleteOrphanAuthors :exec
DELETE FROM authors WHERE id NOT IN
    (SELECT author_id FROM book_authors UNION SELECT author_id FROM book_file_authors);
