package report

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/f0d0r/margaret-cli/internal/database"
	"github.com/f0d0r/margaret-cli/internal/db"
)

func openReportDB(t *testing.T) (*sql.DB, *db.Queries) {
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

func seedReportBook(t *testing.T, q *db.Queries, title, hash string) int64 {
	t.Helper()
	ctx := context.Background()
	fileID, err := q.CreateBookFile(ctx, db.CreateBookFileParams{
		Hash:  hash,
		Path:  "books/" + hash + ".epub",
		Title: title,
	})
	if err != nil {
		t.Fatal(err)
	}
	bookID, err := q.CreateBook(ctx, title)
	if err != nil {
		t.Fatal(err)
	}
	if err := q.CreateBookBookFile(ctx, db.CreateBookBookFileParams{
		BookID:     bookID,
		BookFileID: fileID,
	}); err != nil {
		t.Fatal(err)
	}
	if err := q.CreateAuthor(ctx, "Author "+title); err != nil {
		t.Fatal(err)
	}
	author, err := q.GetAuthorByName(ctx, "Author "+title)
	if err != nil {
		t.Fatal(err)
	}
	if err := q.CreateBookAuthor(ctx, db.CreateBookAuthorParams{
		BookID:   bookID,
		AuthorID: author.ID,
	}); err != nil {
		t.Fatal(err)
	}
	return bookID
}

func seedReportMatch(t *testing.T, q *db.Queries, bookID int64, status string) {
	t.Helper()
	if err := q.UpsertBookMatch(context.Background(), db.UpsertBookMatchParams{
		BookID:         bookID,
		Status:         status,
		Confidence:     0.8,
		ContentScore:   0.7,
		Method:         "isbn",
		EditionKey:     "/books/OL1M",
		WorkKey:        "/works/OL1W",
		Isbn:           "9780306406157",
		IsbnSource:     "METADATA_AND_TEXT",
		ReviewReason:   "reason",
		ScoreGap:       0.02,
		CandidateCount: 1,
		WorkCount:      1,
		MatcherVersion: "1",
		DumpVersion:    "dump",
		MatchedAt:      time.Now().Unix(),
	}); err != nil {
		t.Fatal(err)
	}
	if err := q.SetBookMatchStatus(context.Background(), db.SetBookMatchStatusParams{
		MatchStatus: status,
		ID:          bookID,
	}); err != nil {
		t.Fatal(err)
	}
}

func seedReportCandidate(t *testing.T, q *db.Queries, bookID int64) {
	t.Helper()
	if err := q.CreateBookMatchCandidate(context.Background(), db.CreateBookMatchCandidateParams{
		BookID:         bookID,
		Rank:           1,
		Selected:       1,
		EditionCount:   1,
		EditionKey:     "/books/OL1M",
		WorkKey:        "/works/OL1W",
		EditionTitle:   "Title",
		EditionAuthors: `["Author"]`,
		AuthorKeys:     `["/authors/OL1A"]`,
		AuthorNames:    `["Author"]`,
		Isbn:           "9780306406157",
		IsbnSource:     "METADATA_AND_TEXT",
		IsbnPriority:   0,
		Method:         "isbn",
		TitleScore:     1,
		AuthorScore:    1,
		Score:          0.97,
		Evidence:       `{"methods":["isbn"],"work_group_editions":1}`,
		MatcherVersion: "1",
		DumpVersion:    "dump",
		CreatedAt:      time.Now().Unix(),
	}); err != nil {
		t.Fatal(err)
	}
}

func TestWriteEnrichedBooks(t *testing.T) {
	_, q := openReportDB(t)
	bookID := seedReportBook(t, q, "Title", "hash-enriched")
	seedReportMatch(t, q, bookID, "confirmed")

	path := filepath.Join(t.TempDir(), "nested", "books_enriched.json")
	if err := WriteEnrichedBooks(context.Background(), q, path); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var books []EnrichedBook
	if err := json.Unmarshal(data, &books); err != nil {
		t.Fatal(err)
	}
	if len(books) != 1 {
		t.Fatalf("books = %d, want 1", len(books))
	}
	if books[0].Title != "Title" || books[0].Match.Status != "confirmed" {
		t.Fatalf("book = %+v", books[0])
	}
	if books[0].Match.ContentScore != 0.7 {
		t.Fatalf("content score = %f, want 0.7", books[0].Match.ContentScore)
	}
	if len(books[0].Files) != 1 {
		t.Fatalf("files = %+v", books[0].Files)
	}
}

func TestWriteEnrichedBooksEmptyWritesNothing(t *testing.T) {
	_, q := openReportDB(t)
	path := filepath.Join(t.TempDir(), "books_enriched.json")
	if err := WriteEnrichedBooks(context.Background(), q, path); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("expected no enriched report, stat err = %v", err)
	}
}

func TestWriteReviewCategories(t *testing.T) {
	_, q := openReportDB(t)
	probable := seedReportBook(t, q, "Probable", "hash-probable")
	ambiguous := seedReportBook(t, q, "Ambiguous", "hash-ambiguous")
	needsReview := seedReportBook(t, q, "Needs Review", "hash-review")
	notFound := seedReportBook(t, q, "Not Found", "hash-notfound")
	noData := seedReportBook(t, q, "No Data", "hash-nodata")
	seedReportMatch(t, q, probable, "probable")
	seedReportMatch(t, q, ambiguous, "ambiguous")
	seedReportMatch(t, q, needsReview, "needs_review")
	seedReportMatch(t, q, notFound, "not_found")
	seedReportMatch(t, q, noData, "no_data")
	seedReportCandidate(t, q, probable)

	path := filepath.Join(t.TempDir(), "nested", "review.json")
	if err := WriteReview(context.Background(), q, path); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var report ReviewReport
	if err := json.Unmarshal(data, &report); err != nil {
		t.Fatal(err)
	}
	if len(report.Probable) != 1 || len(report.Ambiguous) != 1 || len(report.NeedsReview) != 1 ||
		len(report.NotFound) != 1 || len(report.NoData) != 1 {
		t.Fatalf("report = %+v", report)
	}
	if len(report.Probable[0].Candidates) != 1 {
		t.Fatalf("candidates = %+v", report.Probable[0].Candidates)
	}
	candidate := report.Probable[0].Candidates[0]
	if candidate.EditionKey != "/books/OL1M" || candidate.Selected != true {
		t.Fatalf("candidate = %+v", candidate)
	}
	var evidence map[string]any
	if err := json.Unmarshal(candidate.Evidence, &evidence); err != nil {
		t.Fatalf("evidence is not JSON: %v", err)
	}
	if evidence["work_group_editions"] != float64(1) {
		t.Fatalf("evidence = %v", evidence)
	}
}

func TestWriteReviewConflict(t *testing.T) {
	_, q := openReportDB(t)
	bookID := seedReportBook(t, q, "Title", "hash-conflict")
	peerID := seedReportBook(t, q, "Title", "hash-conflict-peer")
	seedReportMatch(t, q, bookID, "confirmed")
	if err := q.SetBookMatchStatus(context.Background(), db.SetBookMatchStatusParams{
		MatchStatus: "needs_review",
		ID:          bookID,
	}); err != nil {
		t.Fatal(err)
	}
	if err := q.CreateBookConflict(context.Background(), db.CreateBookConflictParams{
		BookID:     bookID,
		PeerBookID: peerID,
		Reason:     "metadata_content_mismatch",
		Score:      0.12,
		CreatedAt:  1,
	}); err != nil {
		t.Fatal(err)
	}

	path := filepath.Join(t.TempDir(), "review.json")
	if err := WriteReview(context.Background(), q, path); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var report ReviewReport
	if err := json.Unmarshal(data, &report); err != nil {
		t.Fatal(err)
	}
	if len(report.NeedsReview) != 1 {
		t.Fatalf("needs_review = %d, want 1", len(report.NeedsReview))
	}
	item := report.NeedsReview[0]
	if item.Status != "needs_review" || item.MatchStatus != "confirmed" {
		t.Fatalf("statuses = %q / %q", item.Status, item.MatchStatus)
	}
	if item.ReviewReason != "metadata_content_mismatch" {
		t.Fatalf("review reason = %q", item.ReviewReason)
	}
	if len(item.Conflicts) != 1 || item.Conflicts[0].PeerBookID != peerID {
		t.Fatalf("conflicts = %+v", item.Conflicts)
	}
}

func TestWriteReviewEmptyWritesNothing(t *testing.T) {
	_, q := openReportDB(t)
	path := filepath.Join(t.TempDir(), "review.json")
	if err := WriteReview(context.Background(), q, path); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("expected no review report, stat err = %v", err)
	}
}

func TestWriteEnrichedBooksMergesOpenLibraryMetadata(t *testing.T) {
	_, q := openReportDB(t)
	bookID := seedReportBook(t, q, "Raw Title", "hash-enriched-metadata")
	seedReportMatch(t, q, bookID, "confirmed")
	ctx := context.Background()
	if err := q.UpsertBookEnrichment(ctx, db.UpsertBookEnrichmentParams{
		BookID:           bookID,
		EditionKey:       "/books/OL1M",
		WorkKey:          "/works/OL1W",
		Source:           "OPENLIBRARY",
		Title:            "OL Title",
		Subtitle:         "OL Subtitle",
		Description:      "OL description",
		Series:           "OL Series",
		SeriesIndex:      "2",
		FirstPublishDate: "1937",
		MatcherVersion:   "1",
		DumpVersion:      "dump",
		EnrichedAt:       123,
	}); err != nil {
		t.Fatal(err)
	}
	if err := q.CreateBookEnrichmentSubject(ctx, db.CreateBookEnrichmentSubjectParams{BookID: bookID, Name: "Fantasy"}); err != nil {
		t.Fatal(err)
	}
	if err := q.CreateBookEnrichmentLanguage(ctx, db.CreateBookEnrichmentLanguageParams{BookID: bookID, Name: "en"}); err != nil {
		t.Fatal(err)
	}
	if err := q.CreateBookEnrichmentPublication(ctx, db.CreateBookEnrichmentPublicationParams{
		BookID: bookID, Publisher: "OL Press", PublicationDate: "1937",
	}); err != nil {
		t.Fatal(err)
	}
	if err := q.CreateBookEnrichmentIdentifier(ctx, db.CreateBookEnrichmentIdentifierParams{
		BookID: bookID, Scheme: "OCLC", Value: "12345", Source: "OPENLIBRARY",
	}); err != nil {
		t.Fatal(err)
	}
	if err := q.CreateBookEnrichmentAuthor(ctx, db.CreateBookEnrichmentAuthorParams{
		BookID: bookID, AuthorKey: "/authors/OL1A", Name: "OL Author",
		AlternateNames: `["Alt"]`, BirthDate: "1900", DeathDate: "1970", Bio: "Bio",
	}); err != nil {
		t.Fatal(err)
	}

	path := filepath.Join(t.TempDir(), "books_enriched.json")
	if err := WriteEnrichedBooks(ctx, q, path); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var books []EnrichedBook
	if err := json.Unmarshal(data, &books); err != nil {
		t.Fatal(err)
	}
	if len(books) != 1 {
		t.Fatalf("books = %d, want 1", len(books))
	}
	book := books[0]
	if book.Title != "OL Title" || book.Subtitle != "OL Subtitle" || book.Description != "OL description" {
		t.Fatalf("scalar merge failed: %+v", book)
	}
	if book.Series != "OL Series" || book.SeriesIndex != "2" || book.FirstPublishDate != "1937" {
		t.Fatalf("series merge failed: %+v", book)
	}
	if len(book.Subjects) != 1 || book.Subjects[0] != "Fantasy" {
		t.Fatalf("subjects = %v", book.Subjects)
	}
	if len(book.Languages) != 1 || book.Languages[0] != "en" {
		t.Fatalf("languages = %v", book.Languages)
	}
	if len(book.Publications) != 1 || book.Publications[0].Publisher != "OL Press" {
		t.Fatalf("publications = %+v", book.Publications)
	}
	if len(book.Authors) != 1 || book.Authors[0] != "OL Author" {
		t.Fatalf("authors = %v", book.Authors)
	}
	if len(book.AuthorDetails) != 1 || book.AuthorDetails[0].BirthDate != "1900" {
		t.Fatalf("author details = %+v", book.AuthorDetails)
	}
	if book.Enrichment == nil || book.Enrichment.Source != "OPENLIBRARY" || book.Enrichment.EnrichedAt != 123 {
		t.Fatalf("enrichment = %+v", book.Enrichment)
	}
	foundOCLC := false
	for _, id := range book.Identifiers {
		if id.Scheme == "OCLC" && id.Source == "OPENLIBRARY" {
			foundOCLC = true
		}
	}
	if !foundOCLC {
		t.Fatalf("identifiers = %+v", book.Identifiers)
	}
}
