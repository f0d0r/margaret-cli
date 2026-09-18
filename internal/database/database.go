// Package database opens the SQLite database used during a scan and applies
// pending schema migrations with goose, so an existing database file is
// reused and only migrated forward. Each scan starts with a clean slate via
// Clear, which deletes all scanned content while keeping the schema and the
// goose version history.
package database

import (
	"database/sql"
	"embed"
	"fmt"

	"github.com/pressly/goose/v3"
	_ "modernc.org/sqlite"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

// DefaultPath is the SQLite database file used when the user does not pass
// an explicit path. It is resolved relative to the current working directory.
const DefaultPath = "margaret.db"

// clearStatements deletes all scanned content in foreign-key-safe order
// (children before parents). The goose version table is intentionally left
// untouched, and deleting from books/authors lets the books_fts/authors_fts
// triggers clean up the full-text indexes. Scan run history is wiped as well: a fresh scan
// starts a new history with its own run row. When a migration adds a table, extend this list:
// TestClearCoversEntireSchema fails until you do.
var clearStatements = []string{
	`DELETE FROM book_file_lsh_buckets;`,
	`DELETE FROM book_book_files;`,
	`DELETE FROM book_authors;`,
	`DELETE FROM book_file_duplicates;`,
	`DELETE FROM book_file_authors;`,
	`DELETE FROM book_files;`,
	`DELETE FROM books;`,
	`DELETE FROM authors;`,
	`DELETE FROM scan_runs;`,
}

// Open opens a SQLite database at dataSource (a file path or ":memory:"),
// applies pending migrations and returns a connection with foreign keys
// enabled and a single connection so writes are serialized.
func Open(dataSource string) (*sql.DB, error) {
	db, err := sql.Open("sqlite", dataSource+"?_pragma=foreign_keys(1)")
	if err != nil {
		return nil, err
	}
	_, err = db.Exec("PRAGMA busy_timeout = 5000;")
	if err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("failed to set busy timeout: %w", err)
	}
	db.SetMaxOpenConns(1)

	if err := goose.SetDialect("sqlite3"); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("failed to set goose dialect: %w", err)
	}
	goose.SetBaseFS(migrationsFS)
	goose.SetLogger(goose.NopLogger())
	if err := goose.Up(db, "migrations"); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("failed to apply migrations: %w", err)
	}
	return db, nil
}

// Clear deletes all scanned content so a new scan starts with a clean slate.
// The schema and the goose version history are kept. It is safe to call on a
// fresh database, where it is a no-op.
func Clear(db *sql.DB) error {
	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("failed to begin clear transaction: %w", err)
	}
	for _, stmt := range clearStatements {
		if _, err := tx.Exec(stmt); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("failed to clear database: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("failed to clear database: %w", err)
	}
	return nil
}
