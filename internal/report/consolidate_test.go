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
	ident "github.com/f0d0r/margaret-cli/internal/identifier"
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
	addConsolidateFileWithMetadata(t, q, ctx, bookID, db.CreateBookFileParams{
		Hash:  hash,
		Path:  path,
		Title: title,
	}, authors, nil, nil, nil)
}

func addConsolidateFileWithMetadata(
	t *testing.T,
	q *db.Queries,
	ctx context.Context,
	bookID int64,
	params db.CreateBookFileParams,
	authors, subjects, languages []string,
	identifiers []ident.Identifier,
) {
	t.Helper()
	fileID, err := q.CreateBookFile(ctx, params)
	if err != nil {
		t.Fatal(err)
	}
	if err := q.CreateBookBookFile(ctx, db.CreateBookBookFileParams{
		BookID:     bookID,
		BookFileID: fileID,
	}); err != nil {
		t.Fatal(err)
	}
	for i, subject := range subjects {
		if err := q.CreateBookFileSubject(ctx, db.CreateBookFileSubjectParams{
			BookFileID: fileID,
			Name:       subject,
			Ordinal:    int64(i),
		}); err != nil {
			t.Fatal(err)
		}
	}
	for i, language := range languages {
		if err := q.CreateBookFileLanguage(ctx, db.CreateBookFileLanguageParams{
			BookFileID: fileID,
			Name:       language,
			Ordinal:    int64(i),
		}); err != nil {
			t.Fatal(err)
		}
	}
	for i, identifier := range identifiers {
		if err := q.CreateBookFileIdentifier(ctx, db.CreateBookFileIdentifierParams{
			BookFileID: fileID,
			Scheme:     identifier.Scheme,
			Value:      identifier.Value,
			Source:     string(identifier.Source),
			Ordinal:    int64(i),
		}); err != nil {
			t.Fatal(err)
		}
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
		"hash-fallback", "/books/ebooks/spring/Spring in Action 4th edition by Craig Walls.epub",
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

func TestConsolidatePersistsBookMetadata(t *testing.T) {
	conn, q, cleanup := openConsolidateDB(t)
	defer cleanup()
	ctx := context.Background()

	bookID, err := q.CreateBook(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	addConsolidateFileWithMetadata(t, q, ctx, bookID, db.CreateBookFileParams{
		Hash:            "h1",
		Path:            "books/dune.epub",
		Title:           "Dune",
		Description:     "<p>Short</p>",
		Publisher:       "Penguin",
		PublicationDate: "1999-05-04T00:00:00Z",
		Series:          "Saga",
		SeriesIndex:     "1",
	}, []string{"Frank Herbert"},
		[]string{"Fiction", "Adventure"},
		[]string{"en"},
		[]ident.Identifier{
			{Scheme: "ISBN", Value: "9780306406157", Source: ident.SourceMetadata},
			{Scheme: "DOI", Value: "10.1000/test", Source: ident.SourceMetadata},
			{Scheme: "ISBN", Value: "9780375704024", Source: ident.SourceMetadata},
		})
	addConsolidateFileWithMetadata(t, q, ctx, bookID, db.CreateBookFileParams{
		Hash:            "h2",
		Path:            "books/dune.mobi",
		Title:           "Dune",
		Description:     "<div>A much longer description</div>",
		Publisher:       "Penguin",
		PublicationDate: "1999",
		Series:          "Saga",
		SeriesIndex:     "2",
	}, []string{"Frank Herbert"},
		[]string{"fiction", "Science"},
		[]string{"en-US", "english", "und"},
		[]ident.Identifier{
			{Scheme: "ISBN", Value: "9780306406157", Source: ident.SourceText},
			{Scheme: "ISBN", Value: "9780375704024", Source: ident.SourceText},
			{Scheme: "ISBN", Value: "9780306406158", Source: ident.SourceMetadata},
		})

	consolidateDB(t, conn, q)

	book, err := q.GetBookByID(ctx, bookID)
	if err != nil {
		t.Fatal(err)
	}
	if book.Title != "Dune" {
		t.Errorf("title = %q", book.Title)
	}
	if book.Description != "A much longer description" {
		t.Errorf("description = %q", book.Description)
	}
	if book.Series != "Saga" || book.SeriesIndex != "1" {
		t.Errorf("series = %q, index = %q", book.Series, book.SeriesIndex)
	}
	if got := bookAuthors(t, q, bookID); !reflect.DeepEqual(got, []string{"Frank Herbert"}) {
		t.Errorf("authors = %q", got)
	}

	var rawDescription string
	if err := conn.QueryRow(
		`SELECT description FROM book_files WHERE hash = 'h1'`,
	).Scan(&rawDescription); err != nil {
		t.Fatal(err)
	}
	if rawDescription != "<p>Short</p>" {
		t.Errorf("raw description not preserved: %q", rawDescription)
	}

	subjects, err := q.ListBookSubjectsByBookID(ctx, bookID)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"Fiction", "Adventure", "Science"}; !reflect.DeepEqual(subjects, want) {
		t.Errorf("subjects = %v, want %v", subjects, want)
	}
	languages, err := q.ListBookLanguagesByBookID(ctx, bookID)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"en", "en-US"}; !reflect.DeepEqual(languages, want) {
		t.Errorf("languages = %v, want %v", languages, want)
	}
	identifiers, err := q.ListBookIdentifiersByBookID(ctx, bookID)
	if err != nil {
		t.Fatal(err)
	}
	wantIdentifiers := []db.ListBookIdentifiersByBookIDRow{
		{Scheme: "ISBN", Value: "9780306406157", Source: string(ident.SourceMetadataAndText)},
		{Scheme: "ISBN", Value: "9780375704024", Source: string(ident.SourceMetadataAndText)},
	}
	if !reflect.DeepEqual(identifiers, wantIdentifiers) {
		t.Errorf("identifiers = %v, want %v", identifiers, wantIdentifiers)
	}
	publications, err := q.ListBookPublicationsByBookID(ctx, bookID)
	if err != nil {
		t.Fatal(err)
	}
	wantPublications := []db.ListBookPublicationsByBookIDRow{
		{Publisher: "Penguin", PublicationDate: "1999-05-04"},
		{Publisher: "Penguin", PublicationDate: "1999"},
	}
	if !reflect.DeepEqual(publications, wantPublications) {
		t.Errorf("publications = %v, want %v", publications, wantPublications)
	}

	var rawDOI int
	if err := conn.QueryRow(
		`SELECT count(*) FROM book_file_identifiers WHERE scheme = 'DOI'`,
	).Scan(&rawDOI); err != nil {
		t.Fatal(err)
	}
	if rawDOI != 1 {
		t.Errorf("raw DOI identifier not preserved: %d", rawDOI)
	}

	reports, err := GetBooks(ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	if len(reports) != 1 {
		t.Fatalf("expected 1 report, got %d", len(reports))
	}
	report := reports[0]
	if report.Description != book.Description ||
		report.Series != book.Series ||
		report.SeriesIndex != book.SeriesIndex {
		t.Errorf("report metadata does not match book: %+v", report)
	}
	if !reflect.DeepEqual(report.Subjects, subjects) ||
		!reflect.DeepEqual(report.Languages, languages) {
		t.Errorf("report subjects/languages do not match book: %+v", report)
	}
	if want := []BookIdentifier{
		{Scheme: "ISBN", Value: "9780306406157", Source: ident.SourceMetadataAndText},
		{Scheme: "ISBN", Value: "9780375704024", Source: ident.SourceMetadataAndText},
	}; !reflect.DeepEqual(report.Identifiers, want) {
		t.Errorf("report identifiers = %v, want %v", report.Identifiers, want)
	}
	if len(report.Files) != 2 {
		t.Fatalf("expected 2 report files, got %d", len(report.Files))
	}
	wantFileIdentifiers := [][]BookIdentifier{
		{
			{Scheme: "ISBN", Value: "9780306406157", Source: ident.SourceMetadata},
			{Scheme: "DOI", Value: "10.1000/test", Source: ident.SourceMetadata},
			{Scheme: "ISBN", Value: "9780375704024", Source: ident.SourceMetadata},
		},
		{
			{Scheme: "ISBN", Value: "9780306406157", Source: ident.SourceText},
			{Scheme: "ISBN", Value: "9780375704024", Source: ident.SourceText},
			{Scheme: "ISBN", Value: "9780306406158", Source: ident.SourceMetadata},
		},
	}
	for i, want := range wantFileIdentifiers {
		if !reflect.DeepEqual(report.Files[i].Identifiers, want) {
			t.Errorf("file %d identifiers = %v, want %v", i, report.Files[i].Identifiers, want)
		}
	}
	if want := []BookPublication{
		{Publisher: "Penguin", PublicationDate: "1999-05-04"},
		{Publisher: "Penguin", PublicationDate: "1999"},
	}; !reflect.DeepEqual(report.Publications, want) {
		t.Errorf("report publications = %v, want %v", report.Publications, want)
	}

	data, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(data, &object); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{
		"description", "series", "series_index", "subjects", "languages",
		"identifiers", "publications",
	} {
		if _, ok := object[key]; !ok {
			t.Errorf("report JSON missing %q: %s", key, data)
		}
	}
	for _, key := range []string{"publisher", "publication_date"} {
		if _, ok := object[key]; ok {
			t.Errorf("report JSON still has scalar %q: %s", key, data)
		}
	}

	consolidateDB(t, conn, q)
	if got, err := q.ListBookSubjectsByBookID(ctx, bookID); err != nil {
		t.Fatal(err)
	} else if !reflect.DeepEqual(got, subjects) {
		t.Errorf("second run changed subjects: %v -> %v", subjects, got)
	}
	if got, err := q.ListBookLanguagesByBookID(ctx, bookID); err != nil {
		t.Fatal(err)
	} else if !reflect.DeepEqual(got, languages) {
		t.Errorf("second run changed languages: %v -> %v", languages, got)
	}
	if got, err := q.ListBookIdentifiersByBookID(ctx, bookID); err != nil {
		t.Fatal(err)
	} else if !reflect.DeepEqual(got, identifiers) {
		t.Errorf("second run changed identifiers: %v -> %v", identifiers, got)
	}
	if got, err := q.ListBookPublicationsByBookID(ctx, bookID); err != nil {
		t.Fatal(err)
	} else if !reflect.DeepEqual(got, publications) {
		t.Errorf("second run changed publications: %v -> %v", publications, got)
	}
}

func TestConsolidateRefreshesBookMetadata(t *testing.T) {
	conn, q, cleanup := openConsolidateDB(t)
	defer cleanup()
	ctx := context.Background()

	bookID, err := q.CreateBook(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := q.UpdateBookMetadata(ctx, db.UpdateBookMetadataParams{
		ID:          bookID,
		Title:       "stale",
		Description: "stale",
		Series:      "stale",
		SeriesIndex: "9",
	}); err != nil {
		t.Fatal(err)
	}
	if err := q.CreateBookSubject(ctx, db.CreateBookSubjectParams{
		BookID: bookID, Name: "stale", Ordinal: 0,
	}); err != nil {
		t.Fatal(err)
	}
	if err := q.CreateBookLanguage(ctx, db.CreateBookLanguageParams{
		BookID: bookID, Name: "und", Ordinal: 0,
	}); err != nil {
		t.Fatal(err)
	}
	if err := q.CreateBookIdentifier(ctx, db.CreateBookIdentifierParams{
		BookID: bookID, Scheme: "ISBN", Value: "stale", Source: string(ident.SourceMetadata), Ordinal: 0,
	}); err != nil {
		t.Fatal(err)
	}
	if err := q.CreateBookPublication(ctx, db.CreateBookPublicationParams{
		BookID: bookID, Publisher: "stale", PublicationDate: "1999", Ordinal: 0,
	}); err != nil {
		t.Fatal(err)
	}

	addConsolidateFile(t, q, ctx, bookID, "h1", "books/dune.epub", "Dune", nil)
	consolidateDB(t, conn, q)

	book, err := q.GetBookByID(ctx, bookID)
	if err != nil {
		t.Fatal(err)
	}
	if book.Title != "Dune" || book.Description != "" ||
		book.Series != "" || book.SeriesIndex != "" {
		t.Errorf("stale metadata not cleared: %+v", book)
	}
	if subjects, err := q.ListBookSubjectsByBookID(ctx, bookID); err != nil {
		t.Fatal(err)
	} else if len(subjects) != 0 {
		t.Errorf("stale subjects not cleared: %v", subjects)
	}
	if languages, err := q.ListBookLanguagesByBookID(ctx, bookID); err != nil {
		t.Fatal(err)
	} else if len(languages) != 0 {
		t.Errorf("stale languages not cleared: %v", languages)
	}
	if identifiers, err := q.ListBookIdentifiersByBookID(ctx, bookID); err != nil {
		t.Fatal(err)
	} else if len(identifiers) != 0 {
		t.Errorf("stale identifiers not cleared: %v", identifiers)
	}
	if publications, err := q.ListBookPublicationsByBookID(ctx, bookID); err != nil {
		t.Fatal(err)
	} else if len(publications) != 0 {
		t.Errorf("stale publications not cleared: %v", publications)
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
		"hash-livius", "/books/Regények/L/Livius/A romai nep tortenete 1 - Livius, Titus.epub",
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
		"/books/11 - Egy gladiátor csak egyszer hal meg - Steven Saylor.epub",
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
