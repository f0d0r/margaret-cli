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
