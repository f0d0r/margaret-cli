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
