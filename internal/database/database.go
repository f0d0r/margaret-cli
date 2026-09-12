// Package database opens the SQLite database used during a scan and applies
// pending schema migrations with goose, so an existing database file is
// reused and only migrated forward.
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
