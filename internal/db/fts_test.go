package db

import (
	"context"
	"database/sql"
	"testing"

	_ "modernc.org/sqlite"
)

// setupFTSDB builds an in-memory database with the FTS schema and triggers
// mirroring internal/database/migrations/00001_init.sql, seeded with:
//   - 4 "Quest ..." books (title matches "Quest") plus one authorless
//     "Lonely Book"
//   - 6 "Rowling ..." authors; "Quest One" has two of them so author
//     queries can prove per-book de-duplication
func setupFTSDB(t *testing.T) *Queries {
	t.Helper()
	sqldb, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sqldb.Close() })
	ctx := context.Background()
	stmts := []string{
		`CREATE TABLE books (id INTEGER PRIMARY KEY AUTOINCREMENT, title TEXT NOT NULL DEFAULT '')`,
		`CREATE VIRTUAL TABLE books_fts USING FTS5(title, content='books', content_rowid='id', tokenize='trigram remove_diacritics 1')`,
		`CREATE TRIGGER books_fts_insert AFTER INSERT ON books BEGIN INSERT INTO books_fts(rowid, title) VALUES (new.id, new.title); END`,
		`CREATE TRIGGER books_fts_update AFTER UPDATE ON books BEGIN INSERT INTO books_fts(books_fts, rowid, title) VALUES ('delete', old.id, old.title); INSERT INTO books_fts(rowid, title) VALUES (new.id, new.title); END`,
		`CREATE TRIGGER books_fts_delete AFTER DELETE ON books BEGIN INSERT INTO books_fts(books_fts, rowid, title) VALUES ('delete', old.id, old.title); END`,
		`CREATE TABLE authors (id INTEGER PRIMARY KEY AUTOINCREMENT, name TEXT NOT NULL UNIQUE)`,
		`CREATE VIRTUAL TABLE authors_fts USING FTS5(name, content='authors', content_rowid='id', tokenize='trigram remove_diacritics 1')`,
		`CREATE TRIGGER authors_fts_insert AFTER INSERT ON authors BEGIN INSERT INTO authors_fts(rowid, name) VALUES (new.id, new.name); END`,
		`CREATE TRIGGER authors_fts_update AFTER UPDATE ON authors BEGIN INSERT INTO authors_fts(authors_fts, rowid, name) VALUES ('delete', old.id, old.name); INSERT INTO authors_fts(rowid, name) VALUES (new.id, new.name); END`,
		`CREATE TRIGGER authors_fts_delete AFTER DELETE ON authors BEGIN INSERT INTO authors_fts(authors_fts, rowid, name) VALUES ('delete', old.id, old.name); END`,
		`CREATE TABLE book_authors (book_id INTEGER NOT NULL, author_id INTEGER NOT NULL, PRIMARY KEY (book_id, author_id))`,
		`INSERT INTO books (title) VALUES ('Quest One'), ('Quest Two'), ('Quest Three'), ('Quest Four'), ('Lonely Book')`,
		`INSERT INTO authors (name) VALUES ('Rowling Ann'), ('Rowling Bob'), ('Rowling Cat'), ('Rowling Dan'), ('Rowling Eve'), ('Rowling Fay')`,
		`INSERT INTO book_authors VALUES (1, 1), (1, 2), (2, 3), (3, 4), (4, 5)`,
	}
	for _, s := range stmts {
		if _, err := sqldb.ExecContext(ctx, s); err != nil {
			t.Fatalf("exec %q: %v", s, err)
		}
	}
	return &Queries{db: sqldb}
}

func TestAppendLimit(t *testing.T) {
	q, args := appendLimit("SELECT 1 WHERE x MATCH ?", []any{"q"}, 5)
	if q != "SELECT 1 WHERE x MATCH ?\nLIMIT ?" {
		t.Fatalf("limit>0: query = %q, want LIMIT clause appended", q)
	}
	if len(args) != 2 || args[1] != 5 {
		t.Fatalf("limit>0: args = %v, want [q 5]", args)
	}
	for _, limit := range []int{0, -1} {
		q, args := appendLimit("SELECT 1 WHERE x MATCH ?", []any{"q"}, limit)
		if q != "SELECT 1 WHERE x MATCH ?" || len(args) != 1 {
			t.Fatalf("limit=%d: query or args changed: %q %v", limit, q, args)
		}
	}
}

func TestSearchBooksByTitleFTSLimit(t *testing.T) {
	ctx := context.Background()
	q := setupFTSDB(t)

	got, err := q.SearchBooksByTitleFTS(ctx, "Quest", 2)
	if err != nil {
		t.Fatalf("limit=2: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("limit=2: got %d books, want 2", len(got))
	}
	for _, limit := range []int{0, -1, 100} {
		got, err := q.SearchBooksByTitleFTS(ctx, "Quest", limit)
		if err != nil {
			t.Fatalf("limit=%d: %v", limit, err)
		}
		if len(got) != 4 {
			t.Fatalf("limit=%d: got %d books, want 4", limit, len(got))
		}
	}
}

func TestSearchBooksByAuthorFTSLimit(t *testing.T) {
	ctx := context.Background()
	q := setupFTSDB(t)

	got, err := q.SearchBooksByAuthorFTS(ctx, "Rowling", 0)
	if err != nil {
		t.Fatalf("unlimited: %v", err)
	}
	if len(got) != 4 {
		t.Fatalf("unlimited: got %d books, want 4", len(got))
	}
	seen := map[int64]int{}
	for _, b := range got {
		seen[b.ID]++
	}
	for id, n := range seen {
		if n != 1 {
			t.Fatalf("book %d returned %d times, want exactly once", id, n)
		}
	}

	got, err = q.SearchBooksByAuthorFTS(ctx, "Rowling", 2)
	if err != nil {
		t.Fatalf("limit=2: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("limit=2: got %d books, want 2", len(got))
	}
}

func TestSearchBooksByAuthorOrTitleFTSLimit(t *testing.T) {
	ctx := context.Background()
	q := setupFTSDB(t)

	got, err := q.SearchBooksByAuthorOrTitleFTS(ctx, "Rowling", 0)
	if err != nil {
		t.Fatalf("author query: %v", err)
	}
	if len(got) != 4 {
		t.Fatalf("author query: got %d books, want 4", len(got))
	}

	// An authorless book must still be found by title.
	got, err = q.SearchBooksByAuthorOrTitleFTS(ctx, "Lonely", 0)
	if err != nil {
		t.Fatalf("title query: %v", err)
	}
	if len(got) != 1 || got[0].Title != "Lonely Book" {
		t.Fatalf("title query: got %+v, want [Lonely Book]", got)
	}

	got, err = q.SearchBooksByAuthorOrTitleFTS(ctx, "Rowling", 1)
	if err != nil {
		t.Fatalf("limit=1: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("limit=1: got %d books, want 1", len(got))
	}
}
