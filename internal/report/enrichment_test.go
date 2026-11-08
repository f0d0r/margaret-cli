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
