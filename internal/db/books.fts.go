package db

import (
	"context"
)

// appendLimit appends a "LIMIT ?" clause to an FTS query only when limit > 0.
// A limit <= 0 means unlimited: the query is returned unchanged.
func appendLimit(query string, args []any, limit int) (string, []any) {
	if limit > 0 {
		query += "\nLIMIT ?"
		args = append(args, limit)
	}
	return query, args
}

// SearchBooksByTitleFTS performs a full-text search on the books_fts table.
// A limit > 0 caps the number of results; limit <= 0 means unlimited.
func (q *Queries) SearchBooksByTitleFTS(ctx context.Context, searchQuery string, limit int) ([]Book, error) {
	const baseQuery = `
		SELECT b.id, b.title
		FROM books b
		JOIN books_fts fts ON fts.rowid = b.id
		WHERE books_fts MATCH ?
		ORDER BY fts.rank
	`

	query, args := appendLimit(baseQuery, []any{searchQuery}, limit)
	rows, err := q.db.QueryContext(ctx, query, args...)
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

// SearchBooksByAuthorFTS returns books having at least one author matching
// the full-text query. Each book is returned once even when several of its
// authors match. A limit > 0 caps the number of results; limit <= 0 means
// unlimited.
func (q *Queries) SearchBooksByAuthorFTS(ctx context.Context, searchQuery string, limit int) ([]Book, error) {
	const baseQuery = `
		SELECT b.id, b.title
		FROM books b
		JOIN book_authors ba ON ba.book_id = b.id
		JOIN authors a ON a.id = ba.author_id
		JOIN authors_fts fts ON fts.rowid = a.id
		WHERE authors_fts MATCH ?
		GROUP BY b.id, b.title
		ORDER BY MIN(fts.rank)
	`

	query, args := appendLimit(baseQuery, []any{searchQuery}, limit)
	rows, err := q.db.QueryContext(ctx, query, args...)
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

// SearchBooksByAuthorOrTitleFTS returns books whose title or at least one
// author matches the full-text query, de-duplicated and ordered by best
// rank. A limit > 0 caps the number of results; limit <= 0 means unlimited.
func (q *Queries) SearchBooksByAuthorOrTitleFTS(ctx context.Context, searchQuery string, limit int) ([]Book, error) {
	const baseQuery = `
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

	query, args := appendLimit(baseQuery, []any{searchQuery, searchQuery}, limit)
	rows, err := q.db.QueryContext(ctx, query, args...)
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
