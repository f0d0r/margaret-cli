package matcher

import (
	"context"
	"database/sql"
	"encoding/json"
	"testing"
	"time"

	"github.com/f0d0r/margaret-cli/internal/database"
	"github.com/f0d0r/margaret-cli/internal/db"
	"github.com/f0d0r/margaret-cli/internal/identifier"
	"github.com/f0d0r/margaret-cli/internal/openlibrary"
)

func openPersistDB(t *testing.T) (*sql.DB, *db.Queries) {
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

func testMatch(bookID int64) BookMatch {
	candidate := Candidate{
		BookID:       bookID,
		EditionKey:   "/books/OL1M",
		WorkKey:      "/works/OL1W",
		EditionTitle: "Book",
		AuthorKeys:   []string{"/authors/OL1A"},
		AuthorNames:  []string{"Author"},
		ISBN:         "9780306406157",
		ISBNSource:   identifier.SourceMetadataAndText,
		ISBNPriority: 0,
		TitleScore:   1,
		AuthorScore:  1,
		Score:        1,
		Methods:      []string{"isbn"},
	}
	return BookMatch{
		BookID:         bookID,
		Status:         StatusConfirmed,
		Confidence:     1,
		Method:         "isbn",
		EditionKey:     candidate.EditionKey,
		WorkKey:        candidate.WorkKey,
		ISBN:           candidate.ISBN,
		ISBNSource:     candidate.ISBNSource,
		ScoreGap:       1,
		CandidateCount: 1,
		WorkCount:      1,
		Candidates: []RankedCandidate{{
			Rank:         1,
			Selected:     true,
			EditionCount: 1,
			Candidate:    candidate,
		}},
	}
}

func TestPersistAndLoad(t *testing.T) {
	conn, q := openPersistDB(t)
	ctx := context.Background()
	bookID := addBook(t, q, "Book", []string{"Author"}, nil)

	result := &RankResult{Books: map[int64]BookMatch{
		bookID: testMatch(bookID),
	}}
	meta := PersistMeta{
		MatcherVersion: "test-matcher",
		DumpVersion:    "test-dump",
		Now:            time.Unix(123, 0),
	}
	if err := Persist(ctx, conn, q, result, meta, PersistOptions{}); err != nil {
		t.Fatal(err)
	}

	match, err := q.GetBookMatchByBookID(ctx, bookID)
	if err != nil {
		t.Fatal(err)
	}
	if match.Status != "confirmed" || match.MatcherVersion != "test-matcher" || match.DumpVersion != "test-dump" {
		t.Fatalf("match = %+v", match)
	}
	if match.MatchedAt != 123 {
		t.Fatalf("matched_at = %d, want 123", match.MatchedAt)
	}

	var status string
	if err := conn.QueryRow("SELECT match_status FROM books WHERE id = ?", bookID).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "confirmed" {
		t.Fatalf("books.match_status = %q, want confirmed", status)
	}

	candidates, err := q.ListBookMatchCandidatesByBookID(ctx, bookID)
	if err != nil {
		t.Fatal(err)
	}
	if len(candidates) != 1 {
		t.Fatalf("candidates = %d, want 1", len(candidates))
	}
	candidate := candidates[0]
	if candidate.Rank != 1 || candidate.Selected != 1 || candidate.EditionKey != "/books/OL1M" {
		t.Fatalf("candidate = %+v", candidate)
	}
	if want := `["Author"]`; candidate.AuthorNames != want {
		t.Fatalf("author names = %q, want %q", candidate.AuthorNames, want)
	}
	if want := `["/authors/OL1A"]`; candidate.AuthorKeys != want {
		t.Fatalf("author keys = %q, want %q", candidate.AuthorKeys, want)
	}
	var evidence map[string]any
	if err := json.Unmarshal([]byte(candidate.Evidence), &evidence); err != nil {
		t.Fatalf("evidence is not JSON: %v", err)
	}
	if evidence["work_group_editions"] != float64(1) {
		t.Fatalf("evidence = %v", evidence)
	}

	// Persisting again must replace, not duplicate, the candidate.
	if err := Persist(ctx, conn, q, result, meta, PersistOptions{}); err != nil {
		t.Fatal(err)
	}
	candidates, err = q.ListBookMatchCandidatesByBookID(ctx, bookID)
	if err != nil {
		t.Fatal(err)
	}
	if len(candidates) != 1 {
		t.Fatalf("candidates after re-persist = %d, want 1", len(candidates))
	}
}

func TestPersistBatches(t *testing.T) {
	conn, q := openPersistDB(t)
	ctx := context.Background()
	books := []int64{
		addBook(t, q, "One", nil, nil),
		addBook(t, q, "Two", nil, nil),
		addBook(t, q, "Three", nil, nil),
	}
	result := &RankResult{Books: map[int64]BookMatch{}}
	for _, id := range books {
		result.Books[id] = testMatch(id)
	}
	if err := Persist(ctx, conn, q, result, PersistMeta{}, PersistOptions{BatchSize: 2}); err != nil {
		t.Fatal(err)
	}
	for _, id := range books {
		if _, err := q.GetBookMatchByBookID(ctx, id); err != nil {
			t.Fatalf("book %d: %v", id, err)
		}
	}
}

func TestPersistNoCandidates(t *testing.T) {
	conn, q := openPersistDB(t)
	ctx := context.Background()
	bookID := addBook(t, q, "Book", nil, nil)
	result := &RankResult{Books: map[int64]BookMatch{
		bookID: {BookID: bookID, Status: StatusNotFound},
	}}
	if err := Persist(ctx, conn, q, result, PersistMeta{}, PersistOptions{}); err != nil {
		t.Fatal(err)
	}
	match, err := q.GetBookMatchByBookID(ctx, bookID)
	if err != nil {
		t.Fatal(err)
	}
	if match.Status != "not_found" || match.CandidateCount != 0 {
		t.Fatalf("match = %+v", match)
	}
	candidates, err := q.ListBookMatchCandidatesByBookID(ctx, bookID)
	if err != nil {
		t.Fatal(err)
	}
	if len(candidates) != 0 {
		t.Fatalf("candidates = %d, want 0", len(candidates))
	}
}

func TestBuildPendingSkipsCurrentMatches(t *testing.T) {
	conn, q := openPersistDB(t)
	ctx := context.Background()
	first := addBook(t, q, "One", nil, nil)
	second := addBook(t, q, "Two", nil, nil)

	meta := PersistMeta{MatcherVersion: "m1", DumpVersion: "d1", Now: time.Unix(1, 0)}
	result := &RankResult{Books: map[int64]BookMatch{
		first: testMatch(first),
	}}
	if err := Persist(ctx, conn, q, result, meta, PersistOptions{}); err != nil {
		t.Fatal(err)
	}

	cache, err := Build(ctx, q, Options{
		Pending:        true,
		MatcherVersion: "m1",
		DumpVersion:    "d1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(cache.Books) != 1 || cache.Books[second] == nil {
		t.Fatalf("pending cache = %v, want only book %d", cache.Books, second)
	}

	cache, err = Build(ctx, q, Options{
		Pending:        true,
		MatcherVersion: "m2",
		DumpVersion:    "d1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(cache.Books) != 2 {
		t.Fatalf("new matcher version cache = %d books, want 2", len(cache.Books))
	}
}

func TestDatasetVersion(t *testing.T) {
	dataset := &openlibrary.Dataset{
		Editions: &openlibrary.File{Version: "2024-01-31"},
		Works:    &openlibrary.File{Version: "2024-01-31"},
		Authors:  &openlibrary.File{Version: "2024-01-31"},
	}
	if got, want := DatasetVersion(dataset), "editions=2024-01-31;works=2024-01-31;authors=2024-01-31"; got != want {
		t.Fatalf("DatasetVersion = %q, want %q", got, want)
	}

	combined := &openlibrary.File{Path: "/tmp/ol_dump_latest.txt.gz", Version: "2024-02-29"}
	dataset = &openlibrary.Dataset{
		Editions: combined,
		Works:    combined,
		Authors:  combined,
		Combined: combined,
	}
	if got, want := DatasetVersion(dataset), "combined=2024-02-29"; got != want {
		t.Fatalf("DatasetVersion = %q, want %q", got, want)
	}
	if got := DatasetVersion(nil); got != "" {
		t.Fatalf("DatasetVersion(nil) = %q, want empty", got)
	}
}
