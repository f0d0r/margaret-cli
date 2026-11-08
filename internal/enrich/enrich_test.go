package enrich

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/f0d0r/margaret-cli/internal/database"
	"github.com/f0d0r/margaret-cli/internal/db"
	"github.com/f0d0r/margaret-cli/internal/openlibrary"
)

func openEnrichDB(t *testing.T) (*sql.DB, *db.Queries) {
	t.Helper()
	conn, err := database.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = conn.Close()
	})
	return conn, db.New(conn)
}

func seedEnrichBook(t *testing.T, q *db.Queries, hash, editionKey, workKey, authorKeys string) int64 {
	t.Helper()
	ctx := context.Background()
	fileID, err := q.CreateBookFile(ctx, db.CreateBookFileParams{
		Hash:  hash,
		Path:  "books/" + hash + ".epub",
		Title: "Raw Title",
	})
	if err != nil {
		t.Fatal(err)
	}
	bookID, err := q.CreateBook(ctx, "Raw Title")
	if err != nil {
		t.Fatal(err)
	}
	if err := q.CreateBookBookFile(ctx, db.CreateBookBookFileParams{
		BookID:     bookID,
		BookFileID: fileID,
	}); err != nil {
		t.Fatal(err)
	}
	if err := q.UpsertBookMatch(ctx, db.UpsertBookMatchParams{
		BookID:         bookID,
		Status:         "confirmed",
		Confidence:     1,
		EditionKey:     editionKey,
		WorkKey:        workKey,
		CandidateCount: 1,
		WorkCount:      1,
		MatcherVersion: "1",
		DumpVersion:    "dump",
		MatchedAt:      1,
	}); err != nil {
		t.Fatal(err)
	}
	if err := q.SetBookMatchStatus(ctx, db.SetBookMatchStatusParams{
		MatchStatus: "confirmed",
		ID:          bookID,
	}); err != nil {
		t.Fatal(err)
	}
	if err := q.CreateBookMatchCandidate(ctx, db.CreateBookMatchCandidateParams{
		BookID:         bookID,
		Rank:           1,
		Selected:       1,
		EditionCount:   1,
		EditionKey:     editionKey,
		WorkKey:        workKey,
		AuthorKeys:     authorKeys,
		MatcherVersion: "1",
		DumpVersion:    "dump",
		CreatedAt:      1,
	}); err != nil {
		t.Fatal(err)
	}
	return bookID
}

func writeDumpFile(t *testing.T, dir, name, body string, kind openlibrary.Kind) *openlibrary.File {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return &openlibrary.File{Kind: kind, Path: path, Version: "2024-01-31"}
}

const testEdition = `{"key":"/books/OL1M","title":"OL Title","subtitle":"OL Subtitle",` +
	`"publishers":["OL Press"],"publish_date":"1937","series":["OL Series"],` +
	`"languages":[{"key":"/languages/eng"}],"isbn_13":["9780306406157"],` +
	`"oclc_numbers":["12345"],"lccn":["lc123"],"dewey_decimal_class":["823"],` +
	`"lc_classifications":["PR6039"],"ocaid":"ocaid1",` +
	`"authors":[{"key":"/authors/OL1A"}],"works":[{"key":"/works/OL1W"}],` +
	`"subjects":["Edition Subject"],"description":{"value":"edition description"}}`

const testWork = `{"key":"/works/OL1W","title":"OL Title","description":{"value":"work description"},` +
	`"subjects":["Work Subject"],"first_publish_date":"1937","authors":[{"key":"/authors/OL1A"}]}`

const testAuthor = `{"key":"/authors/OL1A","name":"OL Author","personal_name":"OL Author",` +
	`"alternate_names":["Alt Author"],"birth_date":"1900","death_date":"1970","bio":{"value":"bio text"}}`

func testDataset(t *testing.T) *openlibrary.Dataset {
	t.Helper()
	dir := t.TempDir()
	editions := writeDumpFile(t, dir, "editions.txt",
		"/type/edition\t/books/OL1M\t1\t2024-01-31\t"+testEdition+"\n", openlibrary.KindEditions)
	works := writeDumpFile(t, dir, "works.txt",
		"/type/work\t/works/OL1W\t1\t2024-01-31\t"+testWork+"\n", openlibrary.KindWorks)
	authors := writeDumpFile(t, dir, "authors.txt",
		"/type/author\t/authors/OL1A\t1\t2024-01-31\t"+testAuthor+"\n", openlibrary.KindAuthors)
	return &openlibrary.Dataset{Dir: dir, Editions: editions, Works: works, Authors: authors}
}

func TestEnrichFamily(t *testing.T) {
	conn, q := openEnrichDB(t)
	bookID := seedEnrichBook(t, q, "hash-family", "/books/OL1M", "/works/OL1W", `["/authors/OL1A"]`)
	result, err := Enrich(context.Background(), testDataset(t), conn, q, Options{
		MatcherVersion: "1",
		DumpVersion:    "dump",
		Now:            time.Unix(1, 0),
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Targets != 1 || result.Enriched != 1 {
		t.Fatalf("result = %+v", result)
	}
	enrichment, err := q.GetBookEnrichmentByBookID(context.Background(), bookID)
	if err != nil {
		t.Fatal(err)
	}
	if enrichment.Title != "OL Title" || enrichment.Subtitle != "OL Subtitle" {
		t.Fatalf("enrichment = %+v", enrichment)
	}
	if enrichment.Description != "work description" || enrichment.FirstPublishDate != "1937" {
		t.Fatalf("work fields = %+v", enrichment)
	}
	subjects, err := q.ListBookEnrichmentSubjects(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(subjects) != 2 {
		t.Fatalf("subjects = %+v", subjects)
	}
	languages, err := q.ListBookEnrichmentLanguages(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(languages) != 1 || languages[0].Name != "en" {
		t.Fatalf("languages = %+v", languages)
	}
	identifiers, err := q.ListBookEnrichmentIdentifiers(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(identifiers) != 6 {
		t.Fatalf("identifiers = %+v", identifiers)
	}
	authors, err := q.ListBookEnrichmentAuthors(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(authors) != 1 || authors[0].Name != "OL Author" || authors[0].BirthDate != "1900" {
		t.Fatalf("authors = %+v", authors)
	}
	raw, err := q.GetBookByID(context.Background(), bookID)
	if err != nil {
		t.Fatal(err)
	}
	if raw.Title != "Raw Title" {
		t.Fatalf("raw title changed: %q", raw.Title)
	}

	// Same versions: no targets on the next run.
	result, err = Enrich(context.Background(), testDataset(t), conn, q, Options{
		MatcherVersion: "1",
		DumpVersion:    "dump",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Targets != 0 {
		t.Fatalf("targets = %d, want 0", result.Targets)
	}

	// Force re-enriches.
	result, err = Enrich(context.Background(), testDataset(t), conn, q, Options{
		MatcherVersion: "1",
		DumpVersion:    "dump",
		Force:          true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Enriched != 1 {
		t.Fatalf("force enriched = %d, want 1", result.Enriched)
	}
}

func TestEnrichCombined(t *testing.T) {
	conn, q := openEnrichDB(t)
	bookID := seedEnrichBook(t, q, "hash-combined", "/books/OL1M", "/works/OL1W", `["/authors/OL1A"]`)
	dir := t.TempDir()
	combined := writeDumpFile(t, dir, "ol_dump_latest.txt",
		"/type/author\t/authors/OL1A\t1\t2024-01-31\t"+testAuthor+"\n"+
			"/type/edition\t/books/OL1M\t1\t2024-01-31\t"+testEdition+"\n"+
			"/type/work\t/works/OL1W\t1\t2024-01-31\t"+testWork+"\n", openlibrary.KindAll)
	dataset := &openlibrary.Dataset{
		Dir:      dir,
		Editions: combined,
		Works:    combined,
		Authors:  combined,
		Combined: combined,
	}
	result, err := Enrich(context.Background(), dataset, conn, q, Options{
		MatcherVersion: "1",
		DumpVersion:    "dump",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Enriched != 1 {
		t.Fatalf("result = %+v", result)
	}
	enrichment, err := q.GetBookEnrichmentByBookID(context.Background(), bookID)
	if err != nil {
		t.Fatal(err)
	}
	if enrichment.Title != "OL Title" {
		t.Fatalf("enrichment = %+v", enrichment)
	}
}

func TestMappingHelpers(t *testing.T) {
	if got := normalizeDate("1937"); got != "1937" {
		t.Fatalf("normalizeDate = %q", got)
	}
	if got := normalizeDate("c1937"); got != "" {
		t.Fatalf("normalizeDate invalid = %q", got)
	}
	if got := languageTag("/languages/eng"); got != "en" {
		t.Fatalf("languageTag = %q", got)
	}
	if got := languageTag("/languages/unknown"); got != "" {
		t.Fatalf("languageTag unknown = %q", got)
	}
}

func TestEnrichRemovesNonConfirmedEnrichment(t *testing.T) {
	conn, q := openEnrichDB(t)
	bookID := seedEnrichBook(t, q, "hash-cleanup", "/books/OL1M", "/works/OL1W", `["/authors/OL1A"]`)
	if _, err := Enrich(context.Background(), testDataset(t), conn, q, Options{
		MatcherVersion: "1",
		DumpVersion:    "dump",
	}); err != nil {
		t.Fatal(err)
	}
	if err := q.SetBookMatchStatus(context.Background(), db.SetBookMatchStatusParams{
		MatchStatus: "needs_review",
		ID:          bookID,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := Enrich(context.Background(), testDataset(t), conn, q, Options{
		MatcherVersion: "1",
		DumpVersion:    "dump",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := q.GetBookEnrichmentByBookID(context.Background(), bookID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("enrichment still present: %v", err)
	}
	var count int
	if err := conn.QueryRow("SELECT count(*) FROM book_enrichment_subjects").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("subjects = %d, want 0", count)
	}
}
