package matcher

import (
	"testing"

	"github.com/f0d0r/margaret-cli/internal/headprint"
)

func contentCandidate(bookID int64) Candidate {
	candidate := Candidate{
		BookID:       bookID,
		EditionKey:   "OL1M",
		WorkKey:      "OL1W",
		EditionTitle: "The Hobbit",
		AuthorNames:  []string{"J.R.R. Tolkien"},
		ISBN:         "9780306406157",
		ISBNPriority: 0,
		TitleScore:   0.1,
		AuthorScore:  0.1,
		Methods:      []string{"isbn"},
	}
	candidate.Score = candidate.computeScore()
	return candidate
}

func rankWithContent(t *testing.T, book *Book, candidate Candidate) BookMatch {
	t.Helper()
	cache := rankCache(book)
	set := newCandidateSet()
	set.add(candidate, defaultMaxCandidatesPerBook)
	applyContentScores(set, cache)
	result, err := Rank(&StreamResult{Candidates: set.values()}, cache, RankOptions{})
	if err != nil {
		t.Fatal(err)
	}
	return result.Books[book.ID]
}

func TestContentConfirmsMismatchedISBN(t *testing.T) {
	book := rankTestBook(1, "Garbage Title", true, true)
	book.HeadBlooms = [][]byte{headprint.Build("The Hobbit by J.R.R. Tolkien")}

	match := rankWithContent(t, book, contentCandidate(1))
	if match.Status != StatusConfirmed {
		t.Fatalf("status = %q, want confirmed (reason %q, content %f)", match.Status, match.ReviewReason, match.ContentScore)
	}
	if match.Method != "isbn+content" {
		t.Fatalf("method = %q, want isbn+content", match.Method)
	}
	if match.ContentScore < defaultContentConfirmScore {
		t.Fatalf("content score = %f, want at least %f", match.ContentScore, defaultContentConfirmScore)
	}
}

func TestContentDoesNotConfirmWrongBook(t *testing.T) {
	book := rankTestBook(1, "Garbage Title", true, true)
	book.HeadBlooms = [][]byte{headprint.Build("A completely different book by Someone Else")}

	match := rankWithContent(t, book, contentCandidate(1))
	if match.Status != StatusNeedsReview {
		t.Fatalf("status = %q, want needs_review", match.Status)
	}
	if match.ReviewReason != "isbn_title_mismatch" {
		t.Fatalf("reason = %q, want isbn_title_mismatch", match.ReviewReason)
	}
}

func TestContentWithoutHeadKeepsMismatchReview(t *testing.T) {
	book := rankTestBook(1, "Garbage Title", true, true)

	match := rankWithContent(t, book, contentCandidate(1))
	if match.Status != StatusNeedsReview {
		t.Fatalf("status = %q, want needs_review", match.Status)
	}
	if match.ReviewReason != "isbn_title_mismatch" {
		t.Fatalf("reason = %q, want isbn_title_mismatch", match.ReviewReason)
	}
}

func TestContentUsesWorkTitle(t *testing.T) {
	book := rankTestBook(1, "Garbage Title", true, true)
	book.HeadBlooms = [][]byte{headprint.Build("The Hobbit by J.R.R. Tolkien")}
	candidate := contentCandidate(1)
	candidate.EditionTitle = "Der Hobbit"
	candidate.WorkTitle = "The Hobbit"

	match := rankWithContent(t, book, candidate)
	if match.Status != StatusConfirmed {
		t.Fatalf("status = %q, want confirmed", match.Status)
	}
}

func TestApplyWorkTitles(t *testing.T) {
	set := newCandidateSet()
	candidate := contentCandidate(1)
	candidate.WorkKey = "/works/OL1W"
	set.add(candidate, defaultMaxCandidatesPerBook)
	applyWorkTitles(set, map[string]WorkRecord{
		"/works/OL1W": {Key: "/works/OL1W", Title: "The Hobbit"},
	})
	if got := set.byBook[1][0].WorkTitle; got != "The Hobbit" {
		t.Fatalf("work title = %q, want The Hobbit", got)
	}
}

func TestContentConfirmsAuthorGate(t *testing.T) {
	opts := RankOptions{}.withDefaults()
	base := Candidate{
		ISBN:             "9781932394115",
		ISBNPriority:     2,
		AuthorNames:      []string{"Howard M. Lewis Ship"},
		ContentTitleHit:  1,
		ContentAuthorHit: 0,
		ContentScore:     0.75,
		AuthorScore:      0,
	}
	if contentConfirms(base, opts) {
		t.Fatal("content confirmed without author corroboration")
	}
	base.ContentAuthorHit = 0.5
	if !contentConfirms(base, opts) {
		t.Fatal("content did not confirm with author corroboration")
	}
	base.ContentAuthorHit = 0
	base.AuthorScore = 1
	if !contentConfirms(base, opts) {
		t.Fatal("metadata author should not require content author corroboration")
	}
}

func TestContentConflictMarksAmbiguous(t *testing.T) {
	book := rankTestBook(1, "JavaServer Faces JSF in Action Manning", true, false)
	book.OrigTitleHit = 0.9

	isbn := rankCandidate(1, "OL1M", "OL1W", "9781932394115", 2, 0.2, 0, 0.75, "isbn")
	isbn.ContentTitleHit = 1
	isbn.ContentAuthorHit = 0
	isbn.Score = isbn.computeScore()

	title := rankCandidate(1, "OL2M", "OL2W", "", 0, 0.4, 0, 1, "title_fuzzy")
	title.ContentTitleHit = 1
	title.ContentAuthorHit = 1
	title.Score = title.computeScore()

	match := rankOne(t, book, []Candidate{isbn, title}, RankOptions{})
	if match.Status != StatusAmbiguous {
		t.Fatalf("status = %q, want ambiguous", match.Status)
	}
	if match.ReviewReason != "content_conflict" {
		t.Fatalf("reason = %q, want content_conflict", match.ReviewReason)
	}
}

func TestContentConflictAdISBN(t *testing.T) {
	book := rankTestBook(1, "JavaServer Faces JSF in Action Manning", true, false)
	book.OrigTitleHit = 0.9

	jsf := rankCandidate(1, "OL2M", "OL2W", "", 0, 0.667, 0, 1, "title_fuzzy")
	jsf.ContentTitleHit = 1
	jsf.ContentAuthorHit = 1
	jsf.Score = 0.6

	isbn := rankCandidate(1, "OL1M", "OL1W", "9781932394115", 2, 0.286, 0, 0.75, "isbn")
	isbn.ContentTitleHit = 1
	isbn.ContentAuthorHit = 0
	isbn.Score = 0.49

	match := rankOne(t, book, []Candidate{jsf, isbn}, RankOptions{})
	if match.Status != StatusAmbiguous {
		t.Fatalf("status = %q, want ambiguous", match.Status)
	}
	if match.ReviewReason != "content_conflict" {
		t.Fatalf("reason = %q, want content_conflict", match.ReviewReason)
	}
	if match.EditionKey != "OL2M" {
		t.Fatalf("selected edition = %q, want OL2M", match.EditionKey)
	}
}

func TestContentConflictDoesNotOverrideStrongMetadata(t *testing.T) {
	book := rankTestBook(1, "Level Up! The Guide to Great Video Game Design", true, false)
	book.OrigTitleHit = 1
	book.OrigAuthorHit = 1

	best := rankCandidate(1, "OL1M", "OL1W", "", 0, 1, 1, 1, "title_exact")
	best.ContentTitleHit = 1
	best.ContentAuthorHit = 1
	best.Score = 1

	isbn := rankCandidate(1, "OL2M", "OL2W", "9780470688670", 2, 0.222, 0, 0.75, "isbn")
	isbn.ContentTitleHit = 1
	isbn.ContentAuthorHit = 0
	isbn.Score = 0.47

	match := rankOne(t, book, []Candidate{best, isbn}, RankOptions{})
	if match.Status != StatusConfirmed {
		t.Fatalf("status = %q, want confirmed", match.Status)
	}
	if match.EditionKey != "OL1M" {
		t.Fatalf("edition = %q, want OL1M", match.EditionKey)
	}
}
