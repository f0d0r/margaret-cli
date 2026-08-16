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