package database

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/f0d0r/margaret-cli/internal/db"
	"github.com/pressly/goose/v3"
)

func TestOpenAppliesSchema(t *testing.T) {
	conn, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = conn.Close()
	}()

	q := db.New(conn)
	ctx := context.Background()

	id, err := q.CreateBookFile(ctx, db.CreateBookFileParams{
		Hash:  "deadbeef",
		Path:  "books/mobydick.epub",
		Title: "Moby Dick",
	})
	if err != nil {
		t.Fatal(err)
	}

	if err := q.CreateAuthor(ctx, "Herman Melville"); err != nil {
		t.Fatal(err)
	}
	author, err := q.GetAuthorByName(ctx, "Herman Melville")
	if err != nil {
		t.Fatal(err)
	}
	if err := q.CreateBookFileAuthor(ctx, db.CreateBookFileAuthorParams{
		BookFileID: id,
		AuthorID:   author.ID,
	}); err != nil {
		t.Fatal(err)
	}

	got, err := q.GetBookFileByHash(ctx, "deadbeef")
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != id || got.Title != "Moby Dick" || got.Path != "books/mobydick.epub" {
		t.Errorf("unexpected book file: %+v", got)
	}
}

func TestCreateBookFileDuplicate(t *testing.T) {
	conn, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = conn.Close()
	}()

	q := db.New(conn)
	ctx := context.Background()

	if _, err := q.CreateBookFile(ctx, db.CreateBookFileParams{
		Hash: "deadbeef",
		Path: "books/mobydick.epub",
	}); err != nil {
		t.Fatal(err)
	}

	_, err = q.CreateBookFile(ctx, db.CreateBookFileParams{
		Hash: "deadbeef",
		Path: "backup/mobydick.epub",
	})
	if !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("expected sql.ErrNoRows for an existing hash, got %v", err)
	}

	if err := q.CreateBookFileDuplicate(ctx, db.CreateBookFileDuplicateParams{
		Hash: "deadbeef",
		Path: "backup/mobydick.epub",
	}); err != nil {
		t.Fatal(err)
	}

	var n int
	if err := conn.QueryRow("SELECT count(*) FROM book_file_duplicates").Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("expected 1 duplicate, got %d", n)
	}
}

func TestOpenRejectsUnknownTable(t *testing.T) {
	conn, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = conn.Close()
	}()

	if _, err := conn.Query("SELECT * FROM does_not_exist"); err == nil {
		t.Fatal("expected an error for a missing table")
	}
}

func TestListBookFileDuplicates(t *testing.T) {
	conn, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = conn.Close()
	}()

	q := db.New(conn)
	ctx := context.Background()

	// Original book with two authors.
	origID, err := q.CreateBookFile(ctx, db.CreateBookFileParams{
		Hash:  "deadbeef",
		Path:  "books/mobydick.epub",
		Title: "Moby Dick",
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"Herman Melville", "H. Melville"} {
		if err := q.CreateAuthor(ctx, name); err != nil {
			t.Fatal(err)
		}
		author, err := q.GetAuthorByName(ctx, name)
		if err != nil {
			t.Fatal(err)
		}
		if err := q.CreateBookFileAuthor(ctx, db.CreateBookFileAuthorParams{
			BookFileID: origID,
			AuthorID:   author.ID,
		}); err != nil {
			t.Fatal(err)
		}
	}

	// An unrelated book without duplicates must not show up.
	if _, err := q.CreateBookFile(ctx, db.CreateBookFileParams{
		Hash: "cafebabe",
		Path: "books/other.epub",
	}); err != nil {
		t.Fatal(err)
	}

	for _, p := range []string{"backup/mobydick.epub", "mirror/mobydick.epub"} {
		if err := q.CreateBookFileDuplicate(ctx, db.CreateBookFileDuplicateParams{
			Hash: "deadbeef",
			Path: p,
		}); err != nil {
			t.Fatal(err)
		}
	}

	rows, err := q.ListBookFileDuplicates(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 {
		t.Fatalf("expected 2 rows, got %d", len(rows))
	}
	for _, r := range rows {
		if r.Path != "books/mobydick.epub" {
			t.Errorf("expected original path, got %q", r.Path)
		}
		if r.Title != "Moby Dick" {
			t.Errorf("expected title, got %q", r.Title)
		}
		parts := strings.Split(r.Authors, ", ")
		if len(parts) != 2 {
			t.Errorf("expected 2 authors, got %q", r.Authors)
		} else {
			sort.Strings(parts)
			if parts[0] != "H. Melville" || parts[1] != "Herman Melville" {
				t.Errorf("unexpected authors %q", r.Authors)
			}
		}
		if r.DuplicatePath != "backup/mobydick.epub" && r.DuplicatePath != "mirror/mobydick.epub" {
			t.Errorf("unexpected duplicate path %q", r.DuplicatePath)
		}
	}
}

func TestListBookFileDuplicatesEmpty(t *testing.T) {
	conn, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = conn.Close()
	}()

	rows, err := db.New(conn).ListBookFileDuplicates(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 0 {
		t.Fatalf("expected no rows, got %d", len(rows))
	}
}

func TestClearDeletesContentButKeepsSchema(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.db")
	conn, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	q := db.New(conn)
	id, err := q.CreateBookFile(ctx, db.CreateBookFileParams{
		Hash:  "deadbeef",
		Path:  "books/mobydick.epub",
		Title: "Moby Dick",
	})
	if err != nil {
		_ = conn.Close()
		t.Fatal(err)
	}
	if err := q.CreateAuthor(ctx, "Herman Melville"); err != nil {
		_ = conn.Close()
		t.Fatal(err)
	}
	author, err := q.GetAuthorByName(ctx, "Herman Melville")
	if err != nil {
		_ = conn.Close()
		t.Fatal(err)
	}
	if err := q.CreateBookFileAuthor(ctx, db.CreateBookFileAuthorParams{
		BookFileID: id,
		AuthorID:   author.ID,
	}); err != nil {
		_ = conn.Close()
		t.Fatal(err)
	}
	bookID, err := q.CreateBook(ctx, "Moby Dick")
	if err != nil {
		_ = conn.Close()
		t.Fatal(err)
	}
	if err := q.CreateBookBookFile(ctx, db.CreateBookBookFileParams{
		BookID:     bookID,
		BookFileID: id,
	}); err != nil {
		_ = conn.Close()
		t.Fatal(err)
	}
	if err := q.CreateBookFileDuplicate(ctx, db.CreateBookFileDuplicateParams{
		Hash: "deadbeef",
		Path: "backup/mobydick.epub",
	}); err != nil {
		_ = conn.Close()
		t.Fatal(err)
	}

	if err := Clear(conn); err != nil {
		_ = conn.Close()
		t.Fatal(err)
	}

	for _, table := range []string{
		"book_files",
		"books",
		"books_fts",
		"authors",
		"authors_fts",
		"book_file_authors",
		"book_file_duplicates",
		"book_book_files",
		"book_authors",
		"book_file_lsh_buckets",
	} {
		var n int
		if err := conn.QueryRow("SELECT count(*) FROM " + table).Scan(&n); err != nil {
			_ = conn.Close()
			t.Fatalf("count %s: %v", table, err)
		}
		if n != 0 {
			_ = conn.Close()
			t.Fatalf("expected %s to be empty, got %d rows", table, n)
		}
	}

	// Clearing a second time must be a no-op, not an error.
	if err := Clear(conn); err != nil {
		_ = conn.Close()
		t.Fatal(err)
	}
	if err := conn.Close(); err != nil {
		t.Fatal(err)
	}

	// Reopening the cleared file must migrate cleanly and stay writable.
	reopened, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = reopened.Close()
	}()
	if _, err := db.New(reopened).CreateBookFile(ctx, db.CreateBookFileParams{
		Hash: "cafebabe",
		Path: "books/other.epub",
	}); err != nil {
		t.Fatal(err)
	}
}

// expectedSchemaTables lists every table the migrations create. If a future
// migration adds a table, this test fails and reminds the author to extend
// clearStatements in database.go as well. Tables intentionally excluded:
// goose_db_version (migration history, kept by Clear) and sqlite_sequence
// (AUTOINCREMENT counters, harmless).
var expectedSchemaTables = []string{
	"authors",
	"authors_fts",
	"authors_fts_config",
	"authors_fts_data",
	"authors_fts_docsize",
	"authors_fts_idx",
	"book_authors",
	"book_book_files",
	"book_file_authors",
	"book_file_duplicates",
	"book_file_lsh_buckets",
	"book_files",
	"books",
	"books_fts",
	"books_fts_config",
	"books_fts_data",
	"books_fts_docsize",
	"books_fts_idx",
	"scan_runs",
}

func listUserTables(t *testing.T, conn *sql.DB) []string {
	t.Helper()
	rows, err := conn.Query(`SELECT name FROM sqlite_master WHERE type = 'table'
		AND name NOT LIKE 'sqlite_%' AND name <> 'goose_db_version' ORDER BY name`)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	var tables []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatal(err)
		}
		tables = append(tables, name)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return tables
}

// TestClearCoversEntireSchema guards clearStatements against schema drift in
// both directions: every user table must be known (so a new migration cannot
// silently add a table Clear misses), and after populating every content
// table, Clear must leave all of them empty.
func TestClearCoversEntireSchema(t *testing.T) {
	conn, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()

	if got := listUserTables(t, conn); !equalStrings(got, expectedSchemaTables) {
		t.Fatalf("schema drift: sqlite_master holds %v, expected %v; "+
			"if a migration added a table, extend clearStatements in database.go",
			got, expectedSchemaTables)
	}

	ctx := context.Background()
	q := db.New(conn)
	fileID, err := q.CreateBookFile(ctx, db.CreateBookFileParams{
		Hash:  "deadbeef",
		Path:  "books/mobydick.epub",
		Title: "Moby Dick",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := q.CreateAuthor(ctx, "Herman Melville"); err != nil {
		t.Fatal(err)
	}
	author, err := q.GetAuthorByName(ctx, "Herman Melville")
	if err != nil {
		t.Fatal(err)
	}
	if err := q.CreateBookFileAuthor(ctx, db.CreateBookFileAuthorParams{
		BookFileID: fileID,
		AuthorID:   author.ID,
	}); err != nil {
		t.Fatal(err)
	}
	bookID, err := q.CreateBook(ctx, "Moby Dick")
	if err != nil {
		t.Fatal(err)
	}
	if err := q.CreateBookBookFile(ctx, db.CreateBookBookFileParams{
		BookID:     bookID,
		BookFileID: fileID,
	}); err != nil {
		t.Fatal(err)
	}
	if err := q.CreateBookAuthor(ctx, db.CreateBookAuthorParams{
		BookID:   bookID,
		AuthorID: author.ID,
	}); err != nil {
		t.Fatal(err)
	}
	if err := q.CreateBookFileDuplicate(ctx, db.CreateBookFileDuplicateParams{
		Hash: "deadbeef",
		Path: "backup/mobydick.epub",
	}); err != nil {
		t.Fatal(err)
	}
	if err := q.CreateLSHBucket(ctx, db.CreateLSHBucketParams{
		BandIdx:    0,
		BucketHash: 12345,
		BookFileID: fileID,
	}); err != nil {
		t.Fatal(err)
	}

	// Sanity check: every content table really holds a row, so the emptiness
	// assertions below are meaningful.
	for _, table := range []string{
		"book_files",
		"books",
		"books_fts",
		"authors",
		"authors_fts",
		"book_file_authors",
		"book_file_duplicates",
		"book_book_files",
		"book_authors",
		"book_file_lsh_buckets",
	} {
		var n int
		if err := conn.QueryRow("SELECT count(*) FROM " + table).Scan(&n); err != nil {
			t.Fatalf("count %s: %v", table, err)
		}
		if n == 0 {
			t.Fatalf("test setup broken: expected rows in %s before Clear", table)
		}
	}

	if err := Clear(conn); err != nil {
		t.Fatal(err)
	}

	for _, table := range listUserTables(t, conn) {
		// FTS5 shadow tables are maintained by the books_fts/authors_fts
		// triggers; the FTS tables themselves are the observable index state.
		if strings.HasPrefix(table, "authors_fts_") || strings.HasPrefix(table, "books_fts_") {
			continue
		}
		var n int
		if err := conn.QueryRow("SELECT count(*) FROM " + table).Scan(&n); err != nil {
			t.Fatalf("count %s: %v", table, err)
		}
		if n != 0 {
			t.Fatalf("expected %s to be empty after Clear, got %d rows", table, n)
		}
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// TestDownUpRoundTrip guards the migration Down branch against drift: after
// goose down no user table may remain (this caught the missing books_fts
// drops), and goose up must restore the full schema afterwards.
func TestDownUpRoundTrip(t *testing.T) {
	conn, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = conn.Close()
	}()

	if err := goose.SetDialect("sqlite3"); err != nil {
		t.Fatal(err)
	}
	goose.SetBaseFS(migrationsFS)
	if err := goose.Down(conn, "migrations"); err != nil {
		t.Fatalf("goose down: %v", err)
	}
	if got := listUserTables(t, conn); len(got) != 0 {
		t.Fatalf("expected no user tables after down, got %v", got)
	}
	if err := goose.Up(conn, "migrations"); err != nil {
		t.Fatalf("goose up: %v", err)
	}
	if got := listUserTables(t, conn); !equalStrings(got, expectedSchemaTables) {
		t.Fatalf("schema after down+up holds %v, expected %v", got, expectedSchemaTables)
	}
}
