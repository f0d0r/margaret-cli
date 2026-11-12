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
	`DELETE FROM book_conflicts;`,
	`DELETE FROM book_match_candidates;`,
	`DELETE FROM book_matches;`,
	`DELETE FROM book_enrichment_authors;`,
	`DELETE FROM book_enrichment_identifiers;`,
	`DELETE FROM book_enrichment_publications;`,
	`DELETE FROM book_enrichment_languages;`,
	`DELETE FROM book_enrichment_subjects;`,
	`DELETE FROM book_enrichment;`,
	`DELETE FROM book_file_lsh_buckets;`,
	`DELETE FROM book_book_files;`,
	`DELETE FROM book_authors;`,
	`DELETE FROM book_identifiers;`,
	`DELETE FROM book_languages;`,
	`DELETE FROM book_subjects;`,
	`DELETE FROM book_publications;`,
	`DELETE FROM book_file_duplicates;`,
	`DELETE FROM book_file_legacy_authors;`,
	`DELETE FROM book_file_claims;`,
	`DELETE FROM book_claim_candidates;`,
	`DELETE FROM book_file_authors;`,
	`DELETE FROM book_file_identifiers;`,
	`DELETE FROM book_file_languages;`,
	`DELETE FROM book_file_subjects;`,
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
	if err := upgradeClaimSchema(db); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("upgrade claim schema: %w", err)
	}
	if err := ensureConflictSchema(db); err != nil {
		_ = db.Close()
		return nil, err
	}
	if err := ensureEnrichmentSchema(db); err != nil {
		_ = db.Close()
		return nil, err
	}
	if err := ensureContentSchema(db); err != nil {
		_ = db.Close()
		return nil, err
	}
	return db, nil
}

func ensureContentSchema(db *sql.DB) error {
	fileColumns, err := tableColumns(db, "book_files")
	if err != nil {
		return fmt.Errorf("inspect book file schema: %w", err)
	}
	if len(fileColumns) > 0 && !fileColumns["head_bloom"] {
		if _, err := db.Exec(`ALTER TABLE book_files ADD COLUMN head_bloom BLOB`); err != nil {
			return fmt.Errorf("ensure book file content head: %w", err)
		}
	}
	matchColumns, err := tableColumns(db, "book_matches")
	if err != nil {
		return fmt.Errorf("inspect book match schema: %w", err)
	}
	if len(matchColumns) > 0 && !matchColumns["content_score"] {
		if _, err := db.Exec(`ALTER TABLE book_matches ADD COLUMN content_score REAL NOT NULL DEFAULT 0`); err != nil {
			return fmt.Errorf("ensure match content score: %w", err)
		}
	}
	return nil
}

func ensureEnrichmentSchema(db *sql.DB) error {
	columns, err := tableColumns(db, "book_enrichment")
	if err != nil {
		return fmt.Errorf("inspect enrichment schema: %w", err)
	}
	if len(columns) == 0 || columns["subtitle"] {
		return nil
	}
	if _, err := db.Exec(`ALTER TABLE book_enrichment ADD COLUMN subtitle TEXT NOT NULL DEFAULT ''`); err != nil {
		return fmt.Errorf("ensure enrichment subtitle: %w", err)
	}
	return nil
}

func tableColumns(db *sql.DB, table string) (map[string]bool, error) {
	rows, err := db.Query(`PRAGMA table_info(` + table + `)`)
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = rows.Close()
	}()
	columns := make(map[string]bool)
	for rows.Next() {
		var cid, notNull, primaryKey int
		var name, columnType string
		var defaultValue sql.NullString
		if err := rows.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &primaryKey); err != nil {
			return nil, err
		}
		columns[name] = true
	}
	return columns, rows.Err()
}

// upgradeClaimSchema is a compatibility shim for databases created before the
// title/author claim columns were added to the 0.2.0 migration. The migration
// only runs once, so an existing version-2 database needs the same DDL here.
func upgradeClaimSchema(db *sql.DB) error {
	columns, err := tableColumns(db, "book_files")
	if err != nil {
		return fmt.Errorf("inspect book file schema: %w", err)
	}
	addColumns := []struct {
		name string
		ddl  string
	}{
		{"legacy_title", `ALTER TABLE book_files ADD COLUMN legacy_title TEXT NOT NULL DEFAULT ''`},
		{"title_status", `ALTER TABLE book_files ADD COLUMN title_status TEXT NOT NULL DEFAULT 'none'
			CHECK (title_status IN ('', 'none', 'weak', 'single', 'corroborated', 'conflict'))`},
		{"author_status", `ALTER TABLE book_files ADD COLUMN author_status TEXT NOT NULL DEFAULT 'none'
			CHECK (author_status IN ('', 'none', 'weak', 'single', 'corroborated', 'conflict'))`},
		{"title_set", `ALTER TABLE book_files ADD COLUMN title_set TEXT NOT NULL DEFAULT ''`},
		{"author_set", `ALTER TABLE book_files ADD COLUMN author_set TEXT NOT NULL DEFAULT ''`},
		{"claim_version", `ALTER TABLE book_files ADD COLUMN claim_version INTEGER NOT NULL DEFAULT 0`},
		{"resolve_version", `ALTER TABLE book_files ADD COLUMN resolve_version INTEGER NOT NULL DEFAULT 0`},
		{"title_window", `ALTER TABLE book_files ADD COLUMN title_window TEXT NOT NULL DEFAULT ''`},
	}
	addedLegacy := false
	for _, column := range addColumns {
		if columns[column.name] {
			continue
		}
		if _, err := db.Exec(column.ddl); err != nil {
			return fmt.Errorf("add %s: %w", column.name, err)
		}
		if column.name == "legacy_title" {
			addedLegacy = true
		}
	}
	if addedLegacy {
		if _, err := db.Exec(`UPDATE book_files SET legacy_title = title WHERE legacy_title = ''`); err != nil {
			return fmt.Errorf("backfill legacy title: %w", err)
		}
	}
	const claimsTable = `CREATE TABLE IF NOT EXISTS book_file_claims (
		book_file_id INTEGER NOT NULL,
		path TEXT NOT NULL DEFAULT '',
		set_key TEXT NOT NULL,
		source TEXT NOT NULL CHECK (source IN ('METADATA', 'TEXT', 'FILENAME')),
		origin TEXT NOT NULL DEFAULT '',
		field TEXT NOT NULL CHECK (field IN ('title', 'author')),
		value TEXT NOT NULL,
		ordinal INTEGER NOT NULL DEFAULT 0,
		junk INTEGER NOT NULL DEFAULT 0 CHECK (junk IN (0, 1)),
		trust TEXT NOT NULL DEFAULT 'WEAK' CHECK (trust IN ('STRONG', 'WEAK')),
		witness TEXT NOT NULL DEFAULT 'SUPPORTING'
			CHECK (witness IN ('INDEPENDENT', 'SUPPORTING', 'DERIVED')),
		attested INTEGER NOT NULL DEFAULT 0 CHECK (attested IN (0, 1)),
		attestation REAL NOT NULL DEFAULT 0
			CHECK (attestation >= 0 AND attestation <= 1),
		attestation_method TEXT NOT NULL DEFAULT 'none',
		PRIMARY KEY (book_file_id, path, set_key, field, value),
		FOREIGN KEY (book_file_id) REFERENCES book_files(id) ON DELETE CASCADE
	)`
	if _, err := db.Exec(claimsTable); err != nil {
		return fmt.Errorf("ensure claims table: %w", err)
	}
	if _, err := db.Exec(`CREATE INDEX IF NOT EXISTS book_file_claims_lookup_idx
		ON book_file_claims(book_file_id, field, path)`); err != nil {
		return fmt.Errorf("ensure claims index: %w", err)
	}
	claimColumns, err := tableColumns(db, "book_file_claims")
	if err != nil {
		return fmt.Errorf("inspect claims schema: %w", err)
	}
	if !claimColumns["attestation_level"] {
		if _, err := db.Exec(`ALTER TABLE book_file_claims ADD COLUMN attestation_level TEXT NOT NULL DEFAULT 'none'`); err != nil {
			return fmt.Errorf("add claim attestation level: %w", err)
		}
	}
	if !claimColumns["attestation_residual"] {
		if _, err := db.Exec(`ALTER TABLE book_file_claims ADD COLUMN attestation_residual TEXT NOT NULL DEFAULT '[]'`); err != nil {
			return fmt.Errorf("add claim attestation residual: %w", err)
		}
	}
	if !claimColumns["suspicious_signals"] {
		if _, err := db.Exec(`ALTER TABLE book_file_claims ADD COLUMN suspicious_signals INTEGER NOT NULL DEFAULT 0`); err != nil {
			return fmt.Errorf("add claim suspicious signals: %w", err)
		}
	}

	bookColumns, err := tableColumns(db, "books")
	if err != nil {
		return fmt.Errorf("inspect books schema: %w", err)
	}
	bookAdds := []struct {
		name string
		ddl  string
	}{
		{"title_status", `ALTER TABLE books ADD COLUMN title_status TEXT NOT NULL DEFAULT 'none'
			CHECK (title_status IN ('', 'none', 'weak', 'single', 'cross_file', 'corroborated', 'conflict'))`},
		{"author_status", `ALTER TABLE books ADD COLUMN author_status TEXT NOT NULL DEFAULT 'none'
			CHECK (author_status IN ('', 'none', 'weak', 'single', 'cross_file', 'corroborated', 'conflict'))`},
		{"grouping_suspect", `ALTER TABLE books ADD COLUMN grouping_suspect INTEGER NOT NULL DEFAULT 0
			CHECK (grouping_suspect IN (0, 1))`},
		{"grouping_reason", `ALTER TABLE books ADD COLUMN grouping_reason TEXT NOT NULL DEFAULT ''`},
		{"book_resolve_version", `ALTER TABLE books ADD COLUMN book_resolve_version INTEGER NOT NULL DEFAULT 0`},
	}
	for _, column := range bookAdds {
		if bookColumns[column.name] {
			continue
		}
		if _, err := db.Exec(column.ddl); err != nil {
			return fmt.Errorf("add books.%s: %w", column.name, err)
		}
	}
	const candidatesTable = `CREATE TABLE IF NOT EXISTS book_claim_candidates (
		book_id INTEGER NOT NULL,
		field TEXT NOT NULL CHECK (field IN ('title', 'author')),
		rank INTEGER NOT NULL CHECK (rank >= 1),
		value TEXT NOT NULL,
		authors TEXT NOT NULL DEFAULT '[]',
		status TEXT NOT NULL CHECK (status IN (
			'none', 'weak', 'single', 'cross_file', 'corroborated', 'conflict'
		)),
		disposition TEXT NOT NULL CHECK (disposition IN (
			'selected', 'compatible_truncation', 'overruled', 'conflict'
		)),
		reason TEXT NOT NULL DEFAULT '',
		supporting_witnesses INTEGER NOT NULL DEFAULT 0,
		strict_supporting_claims INTEGER NOT NULL DEFAULT 0,
		source_groups INTEGER NOT NULL DEFAULT 0,
		independent_witnesses INTEGER NOT NULL DEFAULT 0,
		attested INTEGER NOT NULL DEFAULT 0 CHECK (attested IN (0, 1)),
		suspicious INTEGER NOT NULL DEFAULT 0 CHECK (suspicious IN (0, 1)),
		attestation_level TEXT NOT NULL DEFAULT 'none',
		max_attestation REAL NOT NULL DEFAULT 0,
		found_in TEXT NOT NULL DEFAULT '[]',
		residual TEXT NOT NULL DEFAULT '[]',
		use_for_matching INTEGER NOT NULL DEFAULT 1 CHECK (use_for_matching IN (0, 1)),
		use_for_agreement INTEGER NOT NULL DEFAULT 1 CHECK (use_for_agreement IN (0, 1)),
		evidence TEXT NOT NULL DEFAULT '{}',
		PRIMARY KEY (book_id, field, rank),
		FOREIGN KEY (book_id) REFERENCES books(id) ON DELETE CASCADE
	)`
	if _, err := db.Exec(candidatesTable); err != nil {
		return fmt.Errorf("ensure book candidates table: %w", err)
	}
	if _, err := db.Exec(`CREATE INDEX IF NOT EXISTS book_claim_candidates_book_field_idx
		ON book_claim_candidates(book_id, field, rank)`); err != nil {
		return fmt.Errorf("ensure book candidates index: %w", err)
	}
	candidateColumns, err := tableColumns(db, "book_claim_candidates")
	if err != nil {
		return fmt.Errorf("inspect book candidates schema: %w", err)
	}
	if !candidateColumns["suspicious"] {
		if _, err := db.Exec(`ALTER TABLE book_claim_candidates ADD COLUMN suspicious INTEGER NOT NULL DEFAULT 0 CHECK (suspicious IN (0, 1))`); err != nil {
			return fmt.Errorf("add candidate suspicious: %w", err)
		}
	}
	if !candidateColumns["use_for_agreement"] {
		if _, err := db.Exec(`ALTER TABLE book_claim_candidates ADD COLUMN use_for_agreement INTEGER NOT NULL DEFAULT 1 CHECK (use_for_agreement IN (0, 1))`); err != nil {
			return fmt.Errorf("add candidate use_for_agreement: %w", err)
		}
	}
	var legacyTableExists int
	if err := db.QueryRow(`SELECT count(*) FROM sqlite_master
		WHERE type = 'table' AND name = 'book_file_legacy_authors'`).Scan(&legacyTableExists); err != nil {
		return fmt.Errorf("inspect legacy authors table: %w", err)
	}
	const legacyAuthorsTable = `CREATE TABLE IF NOT EXISTS book_file_legacy_authors (
		book_file_id INTEGER NOT NULL,
		name TEXT NOT NULL,
		ordinal INTEGER NOT NULL DEFAULT 0,
		PRIMARY KEY (book_file_id, name, ordinal),
		FOREIGN KEY (book_file_id) REFERENCES book_files(id) ON DELETE CASCADE
	)`
	if _, err := db.Exec(legacyAuthorsTable); err != nil {
		return fmt.Errorf("ensure legacy authors table: %w", err)
	}
	// Only backfill on the first upgrade. Re-running this on every Open would
	// copy the resolved authors into the legacy snapshot.
	if legacyTableExists == 0 {
		if _, err := db.Exec(`INSERT OR IGNORE INTO book_file_legacy_authors (book_file_id, name, ordinal)
			SELECT bfa.book_file_id, a.name,
			       ROW_NUMBER() OVER (PARTITION BY bfa.book_file_id ORDER BY a.id) - 1
			FROM book_file_authors bfa
			JOIN authors a ON a.id = bfa.author_id`); err != nil {
			return fmt.Errorf("backfill legacy authors: %w", err)
		}
	}
	return nil
}

// ensureConflictSchema is a compatibility shim for databases created before
// book_conflicts was added to the migration. Fresh databases get the table
// from the migration.
func ensureConflictSchema(db *sql.DB) error {
	const table = `CREATE TABLE IF NOT EXISTS book_conflicts (
		book_id INTEGER NOT NULL,
		peer_book_id INTEGER NOT NULL,
		reason TEXT NOT NULL,
		score REAL NOT NULL DEFAULT 0,
		created_at INTEGER NOT NULL,
		PRIMARY KEY (book_id, peer_book_id),
		FOREIGN KEY (book_id) REFERENCES books(id) ON DELETE CASCADE,
		FOREIGN KEY (peer_book_id) REFERENCES books(id) ON DELETE CASCADE
	)`
	if _, err := db.Exec(table); err != nil {
		return fmt.Errorf("ensure conflict schema: %w", err)
	}
	if _, err := db.Exec(`CREATE INDEX IF NOT EXISTS book_conflicts_peer_idx ON book_conflicts(peer_book_id)`); err != nil {
		return fmt.Errorf("ensure conflict index: %w", err)
	}
	return nil
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
