package matcher

import (
	"slices"
	"testing"

	"github.com/f0d0r/margaret-cli/internal/identifier"
	"github.com/f0d0r/margaret-cli/internal/textmatch"
)

func rankTestBook(id int64, title string, withAuthor, withISBN bool) *Book {
	book := &Book{ID: id, Title: title}
	if title != "" {
		book.TitleKey = textmatch.TitleKey(title)
		book.TitleTokens = textmatch.TitleTokens(title)
	}
	if withAuthor {
		book.Authors = []string{"Test Author"}
		book.AuthorTokens = [][]string{textmatch.AuthorTokens("Test Author")}
	}
	if withISBN {
		book.ISBNs = []ISBN{{
			Value:    "9780306406157",
			Source:   identifier.SourceMetadataAndText,
			Priority: 0,
		}}
	}
	return book
}

func rankCache(books ...*Book) *Cache {
	cache := &Cache{Books: make(map[int64]*Book, len(books))}
	for _, book := range books {
		cache.Books[book.ID] = book
	}
	return cache
}

func rankCandidate(bookID int64, edition, work, isbn string, priority int, title, author, content float64, methods ...string) Candidate {
	candidate := Candidate{
		BookID:       bookID,
		EditionKey:   edition,
		WorkKey:      work,
		ISBN:         isbn,
		ISBNPriority: priority,
		TitleScore:   title,
		AuthorScore:  author,
		ContentScore: content,
		Methods:      methods,
	}
	candidate.Score = candidate.computeScore()
	return candidate
}

func rankOne(t *testing.T, book *Book, candidates []Candidate, opts RankOptions) BookMatch {
	t.Helper()
	result, err := Rank(&StreamResult{
		Candidates: map[int64][]Candidate{book.ID: candidates},
	}, rankCache(book), opts)
	if err != nil {
		t.Fatal(err)
	}
	return result.Books[book.ID]
}

func TestRankStrongISBNConfirmed(t *testing.T) {
	book := rankTestBook(1, "The Hobbit", true, true)
	match := rankOne(t, book, []Candidate{
		rankCandidate(1, "OL1M", "OL1W", "9780306406157", 0, 1, 1, 0, "isbn", "title_exact"),
	}, RankOptions{})

	if match.Status != StatusConfirmed {
		t.Fatalf("status = %q, want confirmed", match.Status)
	}
	if match.Method != "isbn+title_exact" {
		t.Fatalf("method = %q", match.Method)
	}
	if match.Confidence < defaultConfirmScore {
		t.Fatalf("confidence = %f, want at least %f", match.Confidence, defaultConfirmScore)
	}
	if match.ScoreGap != 1 {
		t.Fatalf("score gap = %f, want 1", match.ScoreGap)
	}
	if len(match.Candidates) != 1 || !match.Candidates[0].Selected {
		t.Fatalf("candidates = %+v", match.Candidates)
	}
}

func TestRankStrongISBNAloneConfirmed(t *testing.T) {
	book := rankTestBook(1, "", false, true)
	match := rankOne(t, book, []Candidate{
		rankCandidate(1, "OL1M", "OL1W", "9780306406157", 0, 0, 0, 0, "isbn"),
	}, RankOptions{})

	if match.Status != StatusConfirmed {
		t.Fatalf("status = %q, want confirmed", match.Status)
	}
	if match.Confidence < defaultConfirmScore {
		t.Fatalf("confidence = %f, want at least %f", match.Confidence, defaultConfirmScore)
	}
}

func TestRankStrongISBNMismatch(t *testing.T) {
	book := rankTestBook(1, "The Hobbit", true, true)
	match := rankOne(t, book, []Candidate{
		rankCandidate(1, "OL1M", "OL1W", "9780306406157", 0, 0.1, 0.1, 0, "isbn"),
	}, RankOptions{})

	if match.Status != StatusNeedsReview {
		t.Fatalf("status = %q, want needs_review", match.Status)
	}
	if match.ReviewReason != "isbn_title_mismatch" {
		t.Fatalf("reason = %q", match.ReviewReason)
	}
}

func TestRankMetadataISBNAloneProbable(t *testing.T) {
	book := rankTestBook(1, "", false, true)
	match := rankOne(t, book, []Candidate{
		rankCandidate(1, "OL1M", "OL1W", "9780306406157", 1, 0, 0, 0, "isbn"),
	}, RankOptions{})

	if match.Status != StatusProbable {
		t.Fatalf("status = %q, want probable", match.Status)
	}
	if match.Confidence < defaultProbableScore {
		t.Fatalf("confidence = %f, want at least %f", match.Confidence, defaultProbableScore)
	}
}

func TestRankTextISBNWithExactTextProbable(t *testing.T) {
	book := rankTestBook(1, "The Hobbit", true, true)
	match := rankOne(t, book, []Candidate{
		rankCandidate(1, "OL1M", "OL1W", "9780306406157", 2, 1, 1, 0, "isbn", "title_exact"),
	}, RankOptions{})

	if match.Status != StatusProbable {
		t.Fatalf("status = %q, want probable", match.Status)
	}
}

func TestRankAmbiguousWorks(t *testing.T) {
	book := rankTestBook(1, "The Hobbit", true, false)
	match := rankOne(t, book, []Candidate{
		rankCandidate(1, "OL1M", "OL1W", "", 0, 1, 1, 0, "title_exact"),
		rankCandidate(1, "OL2M", "OL2W", "", 0, 0.97, 0.93, 0, "title_fuzzy"),
	}, RankOptions{})

	if match.Status != StatusAmbiguous {
		t.Fatalf("status = %q, want ambiguous", match.Status)
	}
	if match.ReviewReason != "ambiguous_work" {
		t.Fatalf("reason = %q", match.ReviewReason)
	}
}

func TestRankSameWorkEditions(t *testing.T) {
	book := rankTestBook(1, "The Hobbit", true, false)
	match := rankOne(t, book, []Candidate{
		rankCandidate(1, "OL1M", "OL1W", "", 0, 1, 1, 0, "title_exact"),
		rankCandidate(1, "OL2M", "OL1W", "", 0, 0.9, 0.9, 0, "title_fuzzy"),
	}, RankOptions{})

	if match.Status != StatusConfirmed {
		t.Fatalf("status = %q, want confirmed", match.Status)
	}
	if match.WorkCount != 1 {
		t.Fatalf("work count = %d, want 1", match.WorkCount)
	}
	if len(match.Candidates) != 1 || match.Candidates[0].EditionCount != 2 {
		t.Fatalf("candidates = %+v", match.Candidates)
	}
	if match.EditionKey != "OL1M" {
		t.Fatalf("edition = %q, want OL1M", match.EditionKey)
	}
}

func TestRankConflictingStrongISBNs(t *testing.T) {
	book := rankTestBook(1, "The Hobbit", true, true)
	match := rankOne(t, book, []Candidate{
		rankCandidate(1, "OL1M", "OL1W", "9780306406157", 0, 1, 1, 0, "isbn"),
		rankCandidate(1, "OL2M", "OL2W", "9780375704024", 0, 1, 1, 0, "isbn"),
	}, RankOptions{})

	if match.Status != StatusNeedsReview {
		t.Fatalf("status = %q, want needs_review", match.Status)
	}
	if match.ReviewReason != "conflicting_isbns" {
		t.Fatalf("reason = %q", match.ReviewReason)
	}
}

func TestRankTitleOnlyNeedsReview(t *testing.T) {
	book := rankTestBook(1, "The Hobbit", false, false)
	match := rankOne(t, book, []Candidate{
		rankCandidate(1, "OL1M", "OL1W", "", 0, 1, 0, 0, "title_exact"),
	}, RankOptions{})

	if match.Status != StatusNeedsReview {
		t.Fatalf("status = %q, want needs_review", match.Status)
	}
	if match.ReviewReason != "title_only" {
		t.Fatalf("reason = %q", match.ReviewReason)
	}
}

func TestRankTitleOnlyWithContentProbable(t *testing.T) {
	book := rankTestBook(1, "The Hobbit", false, false)
	match := rankOne(t, book, []Candidate{
		rankCandidate(1, "OL1M", "OL1W", "", 0, 1, 0, 1, "title_exact"),
	}, RankOptions{})

	if match.Status != StatusProbable {
		t.Fatalf("status = %q, want probable", match.Status)
	}
}

func TestRankNoCandidates(t *testing.T) {
	withTitle := rankTestBook(1, "The Hobbit", false, false)
	empty := rankTestBook(2, "", false, false)
	result, err := Rank(&StreamResult{Candidates: map[int64][]Candidate{}}, rankCache(withTitle, empty), RankOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if got := result.Books[1].Status; got != StatusNotFound {
		t.Fatalf("status = %q, want not_found", got)
	}
	if got := result.Books[2].Status; got != StatusNoData {
		t.Fatalf("status = %q, want no_data", got)
	}
}

func TestRankDeterministicEditionTie(t *testing.T) {
	book := rankTestBook(1, "The Hobbit", true, false)
	match := rankOne(t, book, []Candidate{
		rankCandidate(1, "B", "OL1W", "", 0, 1, 1, 0, "title_exact"),
		rankCandidate(1, "A", "OL1W", "", 0, 1, 1, 0, "title_exact"),
	}, RankOptions{})

	if match.EditionKey != "A" {
		t.Fatalf("edition = %q, want A", match.EditionKey)
	}
	if match.Candidates[0].EditionKey != "A" {
		t.Fatalf("selected edition = %q, want A", match.Candidates[0].EditionKey)
	}
}

func TestRankMaxCandidates(t *testing.T) {
	book := rankTestBook(1, "The Hobbit", true, false)
	candidates := []Candidate{
		rankCandidate(1, "OL1M", "OL1W", "", 0, 1, 1, 0, "title_exact"),
		rankCandidate(1, "OL2M", "OL2W", "", 0, 0.9, 0.9, 0, "title_exact"),
		rankCandidate(1, "OL3M", "OL3W", "", 0, 0.9, 0.8, 0, "title_exact"),
		rankCandidate(1, "OL4M", "OL4W", "", 0, 0.9, 0.7, 0, "title_exact"),
		rankCandidate(1, "OL5M", "OL5W", "", 0, 0.9, 0.6, 0, "title_exact"),
	}
	match := rankOne(t, book, candidates, RankOptions{MaxCandidates: 3})
	if len(match.Candidates) != 3 {
		t.Fatalf("candidates = %d, want 3", len(match.Candidates))
	}
	for i, candidate := range match.Candidates {
		if candidate.Rank != i+1 {
			t.Fatalf("rank = %d, want %d", candidate.Rank, i+1)
		}
		if candidate.Selected != (i == 0) {
			t.Fatalf("selected = %v at rank %d", candidate.Selected, i+1)
		}
	}
}

func TestRankStats(t *testing.T) {
	confirmedBook := rankTestBook(1, "Confirmed", true, true)
	notFoundBook := rankTestBook(2, "Not Found", false, false)
	noDataBook := rankTestBook(3, "", false, false)

	result, err := Rank(&StreamResult{
		Candidates: map[int64][]Candidate{
			1: {rankCandidate(1, "OL1M", "OL1W", "9780306406157", 0, 1, 1, 0, "isbn")},
		},
	}, rankCache(confirmedBook, notFoundBook, noDataBook), RankOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if result.Stats.Confirmed != 1 || result.Stats.NotFound != 1 || result.Stats.NoData != 1 {
		t.Fatalf("stats = %+v", result.Stats)
	}
}

func duplicateCandidate(bookID int64, edition, work, authorKey, authorName, title string) Candidate {
	candidate := rankCandidate(bookID, edition, work, "", 0, 1, 1, 1, "title_exact")
	candidate.EditionTitle = title
	candidate.AuthorKeys = []string{authorKey}
	candidate.AuthorNames = []string{authorName}
	candidate.ContentTitleHit = 1
	candidate.ContentAuthorHit = 1
	candidate.Score = 1
	return candidate
}

func TestRankMergesDuplicateWorks(t *testing.T) {
	book := rankTestBook(52, "Level Up! The Guide to Great Video Game Design", true, false)
	const title = "Level up! the Guide to Great Video Game Design"
	candidates := []Candidate{
		duplicateCandidate(52, "/books/OL29228914M", "/works/OL21547168W", "/authors/OL1A", "Test Author", title),
		duplicateCandidate(52, "/books/OL53267304M", "/works/OL39131835W", "/authors/OL1A", "Test Author", title),
		duplicateCandidate(52, "/books/OL53328422M", "/works/OL39174303W", "/authors/OL1A", "Test Author", title),
	}
	other := rankCandidate(52, "/books/OL26040246M", "/works/OL17455507W", "", 0, 1, 0, 0.75, "title_exact")
	other.EditionTitle = "Level Up The Guide To Great Video Game Design"
	other.Score = 0.675
	candidates = append(candidates, other)

	match := rankOne(t, book, candidates, RankOptions{})
	if match.Status != StatusConfirmed {
		t.Fatalf("status = %q, want confirmed (reason %q)", match.Status, match.ReviewReason)
	}
	if match.WorkCount != 2 {
		t.Fatalf("work count = %d, want 2", match.WorkCount)
	}
	if len(match.Candidates) != 2 || match.Candidates[0].EditionCount != 3 {
		t.Fatalf("candidates = %+v", match.Candidates)
	}
	if match.EditionKey != "/books/OL29228914M" {
		t.Fatalf("edition = %q, want /books/OL29228914M", match.EditionKey)
	}
	merged := match.Candidates[0].MergedWorkKeys
	if len(merged) != 2 || !slices.Contains(merged, "/works/OL39131835W") ||
		!slices.Contains(merged, "/works/OL39174303W") {
		t.Fatalf("merged work keys = %v", merged)
	}
}

func TestRankDoesNotMergeSameTitleDifferentAuthors(t *testing.T) {
	book := rankTestBook(1, "Collected Works", true, false)
	first := duplicateCandidate(1, "OL1M", "OL1W", "/authors/OL1A", "Alice Smith", "Collected Works")
	second := duplicateCandidate(1, "OL2M", "OL2W", "/authors/OL2A", "Bob Jones", "Collected Works")
	second.Score = 0.99

	match := rankOne(t, book, []Candidate{first, second}, RankOptions{})
	if match.WorkCount != 2 {
		t.Fatalf("work count = %d, want 2", match.WorkCount)
	}
	if match.Status != StatusAmbiguous || match.ReviewReason != "ambiguous_work" {
		t.Fatalf("status = %q reason = %q, want ambiguous/ambiguous_work", match.Status, match.ReviewReason)
	}
}
