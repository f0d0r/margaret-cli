package report

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/f0d0r/margaret-cli/internal/database"
	"github.com/f0d0r/margaret-cli/internal/db"
)

func openConsolidateDB(t *testing.T) (*sql.DB, *db.Queries, func()) {
	t.Helper()
	conn, err := database.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	return conn, db.New(conn), func() { _ = conn.Close() }
}

func consolidateDB(t *testing.T, conn *sql.DB, q *db.Queries) {
	t.Helper()
	if err := Consolidate(context.Background(), conn, q); err != nil {
		t.Fatal(err)
	}
}

func addConsolidateFile(t *testing.T, q *db.Queries, ctx context.Context, bookID int64, hash, path, title string, authors []string) {
	t.Helper()
	fileID, err := q.CreateBookFile(ctx, db.CreateBookFileParams{
		Hash:  hash,
		Path:  path,
		Title: title,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := q.CreateBookBookFile(ctx, db.CreateBookBookFileParams{
		BookID:     bookID,
		BookFileID: fileID,
	}); err != nil {
		t.Fatal(err)
	}
	for _, name := range authors {
		if err := q.CreateAuthor(ctx, name); err != nil {
			t.Fatal(err)
		}
		author, err := q.GetAuthorByName(ctx, name)
		if err != nil {
			t.Fatal(err)
		}
		if err := q.CreateBookFileAuthor(ctx, db.CreateBookFileAuthorParams{
			BookFileID: fileID,
			AuthorID:   author.ID,
		}); err != nil {
			t.Fatal(err)
		}
		// Simulate the raw scan state: every file author is also linked at
		// book level before consolidation.
		if err := q.CreateBookAuthor(ctx, db.CreateBookAuthorParams{
			BookID:   bookID,
			AuthorID: author.ID,
		}); err != nil {
			t.Fatal(err)
		}
	}
}

func bookAuthors(t *testing.T, q *db.Queries, bookID int64) []string {
	t.Helper()
	rows, err := q.ListAuthorsByBookID(context.Background(), bookID)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, a := range rows {
		out = append(out, a.Name)
	}
	return out
}

func TestConsolidatePersistsFilenameFallbackTitle(t *testing.T) {
	conn, q, cleanup := openConsolidateDB(t)
	defer cleanup()
	ctx := context.Background()

	bookID, err := q.CreateBook(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	addConsolidateFile(t, q, ctx, bookID,
		"hash-fallback", "/home/attila/books/ebooks/spring/Spring in Action 4th edition by Craig Walls.epub",
		"", nil)
	consolidateDB(t, conn, q)

	book, err := q.GetBookByID(ctx, bookID)
	if err != nil {
		t.Fatal(err)
	}
	// No " - " separator in the filename, so no author is recovered and the
	// full stem is kept as the title (mirrors WriteBooks fallback).
	if book.Title != "Spring in Action 4th edition by Craig Walls" {
		t.Errorf("expected filename fallback title, got %q", book.Title)
	}
	// The filename-derived title must be searchable via FTS.
	hits, err := q.SearchBooksByTitleFTS(ctx, "Spring", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 1 || hits[0].ID != bookID {
		t.Fatalf("expected FTS hit for consolidated title, got %+v", hits)
	}
}

func TestConsolidateCleansBookAuthors(t *testing.T) {
	conn, q, cleanup := openConsolidateDB(t)
	defer cleanup()
	ctx := context.Background()

	bookID, err := q.CreateBook(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	addConsolidateFile(t, q, ctx, bookID, "h1", "books/a.epub", "Dune", []string{"Ismeretlen"})
	addConsolidateFile(t, q, ctx, bookID, "h2", "books/b.epub", "Dune", []string{"Frank Herbert"})

	consolidateDB(t, conn, q)

	if got := bookAuthors(t, q, bookID); !reflect.DeepEqual(got, []string{"Frank Herbert"}) {
		t.Errorf("expected placeholder filtered from book_authors, got %q", got)
	}
	// Raw file-level links are preserved.
	rows, err := q.ListBooksWithFiles(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 {
		t.Fatalf("expected 2 file rows, got %d", len(rows))
	}
}

func TestConsolidateRecoversCalibreAuthor(t *testing.T) {
	conn, q, cleanup := openConsolidateDB(t)
	defer cleanup()
	ctx := context.Background()

	bookID, err := q.CreateBook(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	addConsolidateFile(t, q, ctx, bookID,
		"hash-livius", "/home/attila/books/Regények/L/Livius/A romai nep tortenete 1 - Livius, Titus.epub",
		"Untitled", nil)

	consolidateDB(t, conn, q)

	book, err := q.GetBookByID(ctx, bookID)
	if err != nil {
		t.Fatal(err)
	}
	if book.Title != "A romai nep tortenete 1" {
		t.Errorf("expected Calibre title, got %q", book.Title)
	}
	if got := bookAuthors(t, q, bookID); !reflect.DeepEqual(got, []string{"Livius, Titus"}) {
		t.Errorf("expected Calibre author, got %q", got)
	}
}

func TestConsolidateIsIdempotent(t *testing.T) {
	conn, q, cleanup := openConsolidateDB(t)
	defer cleanup()
	ctx := context.Background()

	bookID, err := q.CreateBook(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	addConsolidateFile(t, q, ctx, bookID, "h1", "books/a.epub",
		"Marcus Meadow - Könnyek városa", []string{"Marcus Meadow"})

	consolidateDB(t, conn, q)
	first, err := q.GetBookByID(ctx, bookID)
	if err != nil {
		t.Fatal(err)
	}
	firstAuthors := bookAuthors(t, q, bookID)

	consolidateDB(t, conn, q)
	second, err := q.GetBookByID(ctx, bookID)
	if err != nil {
		t.Fatal(err)
	}
	if second.Title != first.Title {
		t.Errorf("second run changed title: %q -> %q", first.Title, second.Title)
	}
	if got := bookAuthors(t, q, bookID); !reflect.DeepEqual(got, firstAuthors) {
		t.Errorf("second run changed authors: %q -> %q", firstAuthors, got)
	}
	if first.Title != "Könnyek városa" {
		t.Errorf("expected washed title, got %q", first.Title)
	}
}

func TestConsolidateResumeChangesConsensus(t *testing.T) {
	conn, q, cleanup := openConsolidateDB(t)
	defer cleanup()
	ctx := context.Background()

	bookID, err := q.CreateBook(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	addConsolidateFile(t, q, ctx, bookID, "h1", "books/a.epub", "Moby Dick", []string{"Herman Melville"})
	consolidateDB(t, conn, q)
	if book, _ := q.GetBookByID(ctx, bookID); book.Title != "Moby Dick" {
		t.Fatalf("expected initial title, got %q", book.Title)
	}

	// A later resume run attaches two more files voting for the longer title.
	addConsolidateFile(t, q, ctx, bookID, "h2", "books/b.epub", "Moby Dick; Or, The Whale", []string{"Herman Melville"})
	addConsolidateFile(t, q, ctx, bookID, "h3", "books/c.epub", "Moby Dick; Or, The Whale", []string{"Herman Melville"})
	consolidateDB(t, conn, q)

	if book, _ := q.GetBookByID(ctx, bookID); book.Title != "Moby Dick; Or, The Whale" {
		t.Errorf("expected consensus to follow new majority, got %q", book.Title)
	}
}

func TestConsolidateDeletesOnlyTrueOrphans(t *testing.T) {
	conn, q, cleanup := openConsolidateDB(t)
	defer cleanup()
	ctx := context.Background()

	bookID, err := q.CreateBook(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	addConsolidateFile(t, q, ctx, bookID, "h1", "books/a.epub", "Dune", []string{"Ismeretlen", "Frank Herbert"})

	// A truly unreferenced author: linked to nothing.
	if err := q.CreateAuthor(ctx, "Stray Author"); err != nil {
		t.Fatal(err)
	}

	consolidateDB(t, conn, q)

	if _, err := q.GetAuthorByName(ctx, "Stray Author"); err == nil {
		t.Errorf("expected true orphan author to be deleted")
	}
	// The placeholder is still referenced by book_file_authors (raw data is
	// preserved), so it survives even though book_authors no longer links it.
	if _, err := q.GetAuthorByName(ctx, "Ismeretlen"); err != nil {
		t.Errorf("expected file-referenced author to survive, got %v", err)
	}
	if got := bookAuthors(t, q, bookID); !reflect.DeepEqual(got, []string{"Frank Herbert"}) {
		t.Errorf("expected cleaned book authors, got %q", got)
	}
}

func TestGetBooksFilteredReadsConsolidated(t *testing.T) {
	conn, q, cleanup := openConsolidateDB(t)
	defer cleanup()
	ctx := context.Background()

	bookID, err := q.CreateBook(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	addConsolidateFile(t, q, ctx, bookID, "h1",
		"/home/attila/books/11 - Egy gladiátor csak egyszer hal meg - Steven Saylor.epub",
		"", []string{"Steven Saylor"})
	consolidateDB(t, conn, q)

	books, err := GetBooksFiltered(ctx, q, []int64{9999, bookID, bookID})
	if err != nil {
		t.Fatal(err)
	}
	if len(books) != 1 {
		t.Fatalf("expected 1 book (unknown skipped, dup collapsed), got %d", len(books))
	}
	b := books[0]
	if b.Title != "11 - Egy gladiátor csak egyszer hal meg" {
		t.Errorf("expected consolidated title, got %q", b.Title)
	}
	if !reflect.DeepEqual(b.Authors, []string{"Steven Saylor"}) {
		t.Errorf("expected consolidated authors, got %q", b.Authors)
	}
	if len(b.Files) != 1 || b.Files[0].Title != "" {
		t.Errorf("expected raw file entry to keep empty title, got %+v", b.Files)
	}

	if got, err := GetBooksFiltered(ctx, q, nil); err != nil || got != nil {
		t.Errorf("expected nil for empty ids, got %v, %v", got, err)
	}
}

func TestWriteBooksMatchesFilteredReads(t *testing.T) {
	conn, q, cleanup := openConsolidateDB(t)
	defer cleanup()
	ctx := context.Background()

	// One washed multi-file book plus one Calibre-recovered book.
	bookID, err := q.CreateBook(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	addConsolidateFile(t, q, ctx, bookID, "h1", "books/glad1.epub",
		"Egy gladiátor csak egyszer hal meg", []string{"Steven Saylor"})
	addConsolidateFile(t, q, ctx, bookID, "h2", "books/glad2.epub",
		"Steven Saylor - Egy gladiátor csak egyszer hal meg", []string{"Steven Saylor"})
	calibreID, err := q.CreateBook(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	addConsolidateFile(t, q, ctx, calibreID,
		"hash-livius", "/books/A romai nep tortenete 1 - Livius, Titus.epub",
		"Untitled", nil)
	if err := q.CreateBookFileDuplicate(ctx, db.CreateBookFileDuplicateParams{
		Hash: "h1",
		Path: "backup/glad1.epub",
	}); err != nil {
		t.Fatal(err)
	}
	consolidateDB(t, conn, q)

	path := filepath.Join(t.TempDir(), "books.json")
	if err := WriteBooks(ctx, q, path); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var full []BookReport
	if err := json.Unmarshal(data, &full); err != nil {
		t.Fatal(err)
	}
	all, err := GetBooks(ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(full, all) {
		t.Fatalf("WriteBooks output differs from GetBooks:\n%+v\n%+v", full, all)
	}
	filtered, err := GetBooksFiltered(ctx, q, []int64{calibreID, bookID})
	if err != nil {
		t.Fatal(err)
	}
	if len(filtered) != 2 || filtered[0].Title != "A romai nep tortenete 1" ||
		filtered[1].Title != "Egy gladiátor csak egyszer hal meg" {
		t.Fatalf("unexpected filtered order/content: %+v", filtered)
	}
	// Same books in book-ID order must equal the full report.
	ordered, err := GetBooksFiltered(ctx, q, []int64{bookID, calibreID})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(full, ordered) {
		t.Fatalf("full report differs from ordered filtered read:\n%+v\n%+v", full, ordered)
	}
}
