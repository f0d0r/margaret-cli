package db

import (
	"context"
)

// SearchAuthorsFTS performs a full-text search on the authors_fts table
func (q *Queries) SearchAuthorsFTS(ctx context.Context, searchQuery string) ([]Author, error) {
	const query = `
		SELECT a.id, a.name
		FROM authors a
		JOIN authors_fts fts ON fts.rowid = a.id
		WHERE authors_fts MATCH ?
		ORDER BY fts.rank
		LIMIT 5
	`

	rows, err := q.db.QueryContext(ctx, query, searchQuery)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var items []Author
	for rows.Next() {
		var i Author
		if err := rows.Scan(
			&i.ID,
			&i.Name,
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
