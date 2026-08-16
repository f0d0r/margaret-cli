// Package database opens the SQLite database used during a scan and applies
// the schema. The database is ephemeral: it is created per run and removed
// when the scan finishes, so there is nothing to migrate.
package database

import (
	"database/sql"
	"embed"
	"fmt"
	"os"

	_ "modernc.org/sqlite"
)

//go:embed schema.sql
var schemaFS embed.FS

// Open opens a SQLite database at dataSource (a file path or ":memory:"),
// applies the schema and returns a connection with foreign keys enabled and
// a single connection so writes are serialized.
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

	schema, err := schemaFS.ReadFile("schema.sql")
	if err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("failed to read embedded schema: %w", err)
	}
	if _, err := db.Exec(string(schema)); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("failed to apply schema: %w", err)
	}
	return db, nil
}

// OpenTemp creates a per-run temporary database file, opens it with [Open]
// and returns the connection plus a cleanup function that closes it and
// removes the file.
func OpenTemp() (*sql.DB, func(), error) {
	f, err := os.CreateTemp("", "margaret-tools-db-*")
	if err != nil {
		return nil, nil, err
	}
	name := f.Name()
	if err := f.Close(); err != nil {
		_ = os.Remove(name)
		return nil, nil, err
	}
	db, err := Open(name)
	if err != nil {
		_ = os.Remove(name)
		return nil, nil, err
	}
	return db, func() {
		_ = db.Close()
		_ = os.Remove(name)
	}, nil
}
