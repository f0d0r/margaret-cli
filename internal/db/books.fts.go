package db

import (
	"context"
)

// SearchBooksByTitleFTS performs a full-text search on the books_fts table
func (q *Queries) SearchBooksByTitleFTS(ctx context.Context, searchQuery string) ([]Book, error) {
	const query = `
		SELECT b.id, b.title
		FROM books b
		JOIN books_fts fts ON fts.rowid = b.id
		WHERE books_fts MATCH ?
		ORDER BY fts.rank
	`

	rows, err := q.db.QueryContext(ctx, query, searchQuery)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var items []Book
	for rows.Next() {
		var i Book
		if err := rows.Scan(
			&i.ID,
			&i.Title,
		); err != nil {
			return nil, err
		}
		items = append(items, i)
	}

	if err := rows.Close(); err != nil {
		return nil, err
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	return items, nil
}

func (q *Queries) SearchBooksByAuthorFTS(ctx context.Context, searchQuery string) ([]Book, error) {
	const query = `
		SELECT b.id, b.title
		FROM books b
		JOIN book_authors ba ON ba.book_id = b.id
		JOIN authors a ON a.id = ba.author_id
		JOIN authors_fts fts ON fts.rowid = a.id
		WHERE authors_fts MATCH ?
		GROUP BY b.id, b.title
		ORDER BY MIN(fts.rank)
	`

	rows, err := q.db.QueryContext(ctx, query, searchQuery)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var items []Book
	for rows.Next() {
		var i Book
		if err := rows.Scan(
			&i.ID,
			&i.Title,
		); err != nil {
			return nil, err
		}
		items = append(items, i)
	}

	if err := rows.Close(); err != nil {
		return nil, err
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	return items, nil
}

func (q *Queries) SearchBooksByAuthorOrTitleFTS(ctx context.Context, searchQuery string) ([]Book, error) {
	const query = `
		SELECT id, title FROM (
			SELECT b.id as id, b.title as title, books_fts.rank as r FROM books b JOIN books_fts ON books_fts.rowid = b.id WHERE books_fts MATCH ?
			UNION ALL
			SELECT b.id as id, b.title as title, authors_fts.rank as r FROM books b
			JOIN book_authors ba ON ba.book_id = b.id
			JOIN authors a ON a.id = ba.author_id
			JOIN authors_fts ON authors_fts.rowid = a.id
			WHERE authors_fts MATCH ?
		)
	 	GROUP BY id, title
		ORDER BY MIN(r)
	`

	rows, err := q.db.QueryContext(ctx, query, searchQuery, searchQuery)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var items []Book
	for rows.Next() {
		var i Book
		if err := rows.Scan(
			&i.ID,
			&i.Title,
		); err != nil {
			return nil, err
		}
		items = append(items, i)
	}

	if err := rows.Close(); err != nil {
		return nil, err
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	return items, nil
}
