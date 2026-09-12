package database

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/f0d0r/margaret-tools/internal/db"
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
