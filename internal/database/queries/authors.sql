-- name: CreateAuthor :exec
INSERT INTO authors (name)
VALUES (?)
ON CONFLICT(name) DO NOTHING;

-- name: GetAuthorByName :one
SELECT * FROM authors WHERE name = ?;
