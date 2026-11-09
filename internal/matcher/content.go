package matcher

import (
	"github.com/f0d0r/margaret-cli/internal/headprint"
	"github.com/f0d0r/margaret-cli/internal/textmatch"
)

// applyWorkTitles fills the work-level title of candidates whose work record
// was streamed. Combined-dump runs without a separate works dump leave it empty.
func applyWorkTitles(candidates *candidateSet, works map[string]WorkRecord) {
	if len(works) == 0 {
		return
	}
	for _, list := range candidates.byBook {
		for _, candidate := range list {
			if candidate.WorkTitle != "" {
				continue
			}
			if work, ok := works[candidate.WorkKey]; ok {
				candidate.WorkTitle = work.Title
			}
		}
	}
}

// applyContentScores checks whether the candidate title and author appear in
// the scanned book's leading text. It also records how strongly the scanned
// metadata itself appears, so a candidate that contradicts credible scanned
// metadata can be penalized and routed to review.
func applyContentScores(candidates *candidateSet, cache *Cache) {
	for bookID, list := range candidates.byBook {
		book := cache.Books[bookID]
		if book == nil || len(book.HeadBlooms) == 0 {
			continue
		}
		book.OrigTitleHit = maxContainment(book.HeadBlooms, textmatch.ContentTokens(book.Title))
		book.OrigAuthorHit = 0
		for _, author := range book.Authors {
			tokens := textmatch.AuthorTokens(author)
			if len(tokens) == 0 {
				continue
			}
			if hit := maxContainment(book.HeadBlooms, tokens); hit > book.OrigAuthorHit {
				book.OrigAuthorHit = hit
			}
		}
		for _, candidate := range list {
			candidate.ContentTitleHit = contentTitleHit(book, candidate)
			candidate.ContentAuthorHit = contentAuthorHit(book, candidate)
			candidate.ContentScore = contentScore(candidate.ContentTitleHit, candidate.ContentAuthorHit)
			candidate.OrigTitleHit = book.OrigTitleHit
			candidate.OrigAuthorHit = book.OrigAuthorHit
			candidate.Score = adjustedContentScore(book, candidate)
		}
	}
}

const (
	contentAuthorBoost        = 0.10
	contentConflictPenalty    = 0.15
	contentCredibleTitle      = 0.70
	contentDisagreementFloor  = 0.50
	contentTitleConfirmed     = 0.60
	contentAuthorConfirmedHit = 0.40
)

func adjustedContentScore(book *Book, candidate *Candidate) float64 {
	score := candidate.computeScore()
	if candidate.ContentTitleHit >= contentTitleConfirmed &&
		candidate.ContentAuthorHit >= contentAuthorConfirmedHit {
		score += contentAuthorBoost
	}
	if book.OrigTitleHit >= contentCredibleTitle {
		disagreement := 1 - max(candidate.TitleScore, candidate.AuthorScore)
		if disagreement > contentDisagreementFloor {
			score -= contentConflictPenalty * book.OrigTitleHit * disagreement
		}
	}
	if score < 0 {
		return 0
	}
	if score > 1 {
		return 1
	}
	return score
}

func maxContainment(blooms [][]byte, tokens []string) float64 {
	best := 0.0
	for _, bloom := range blooms {
		if hit := headprint.Containment(bloom, tokens); hit > best {
			best = hit
		}
	}
	return best
}

func contentTitleHit(book *Book, candidate *Candidate) float64 {
	best := 0.0
	if candidate.EditionTitle != "" {
		best = maxContainment(book.HeadBlooms, textmatch.ContentTokens(candidate.EditionTitle))
	}
	if candidate.WorkTitle != "" {
		if hit := maxContainment(book.HeadBlooms, textmatch.ContentTokens(candidate.WorkTitle)); hit > best {
			best = hit
		}
	}
	return best
}

func contentAuthorHit(book *Book, candidate *Candidate) float64 {
	best := 0.0
	for _, name := range candidate.AuthorNames {
		tokens := textmatch.AuthorTokens(name)
		if len(tokens) == 0 {
			continue
		}
		if hit := maxContainment(book.HeadBlooms, tokens); hit > best {
			best = hit
		}
	}
	return best
}

func contentScore(titleHit, authorHit float64) float64 {
	if titleHit == 0 && authorHit == 0 {
		return 0
	}
	return 0.75*titleHit + 0.25*authorHit
}
