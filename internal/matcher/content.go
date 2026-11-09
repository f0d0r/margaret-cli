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
// the scanned book's leading text. It is a positive-only signal: a missing
// head or a low score never rejects a candidate.
func applyContentScores(candidates *candidateSet, cache *Cache) {
	for bookID, list := range candidates.byBook {
		book := cache.Books[bookID]
		if book == nil || len(book.HeadBlooms) == 0 {
			continue
		}
		for _, candidate := range list {
			candidate.ContentTitleHit = contentTitleHit(book, candidate)
			candidate.ContentAuthorHit = contentAuthorHit(book, candidate)
			candidate.ContentScore = contentScore(candidate.ContentTitleHit, candidate.ContentAuthorHit)
			candidate.Score = candidate.computeScore()
		}
	}
}

func contentTitleHit(book *Book, candidate *Candidate) float64 {
	best := 0.0
	for _, bloom := range book.HeadBlooms {
		if candidate.EditionTitle != "" {
			if hit := headprint.Containment(bloom, textmatch.ContentTokens(candidate.EditionTitle)); hit > best {
				best = hit
			}
		}
		if candidate.WorkTitle != "" {
			if hit := headprint.Containment(bloom, textmatch.ContentTokens(candidate.WorkTitle)); hit > best {
				best = hit
			}
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
		for _, bloom := range book.HeadBlooms {
			if hit := headprint.Containment(bloom, tokens); hit > best {
				best = hit
			}
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
