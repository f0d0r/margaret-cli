package matcher

import (
	"errors"
	"sort"

	"github.com/f0d0r/margaret-cli/internal/identifier"
)

const (
	defaultConfirmScore      = 0.90
	defaultProbableScore     = 0.75
	defaultReviewScore       = 0.60
	defaultConfirmGap        = 0.05
	defaultAmbiguityDelta    = 0.05
	defaultMaxRankCandidates = 10

	scoreEpsilon = 1e-9

	defaultContentConfirmScore = 0.60
	defaultContentTitleConfirm = 0.60
)

// Status is the final matching classification of one scanned book.
type Status string

const (
	StatusConfirmed   Status = "confirmed"
	StatusProbable    Status = "probable"
	StatusAmbiguous   Status = "ambiguous"
	StatusNeedsReview Status = "needs_review"
	StatusNotFound    Status = "not_found"
	StatusNoData      Status = "no_data"
)

// RankOptions configures the ranking and classification step.
type RankOptions struct {
	ConfirmScore   float64
	ProbableScore  float64
	ReviewScore    float64
	ConfirmGap     float64
	AmbiguityDelta float64
	MaxCandidates  int
	// ContentConfirmScore is the minimum content confirmation score that can
	// confirm an ISBN match whose scanned metadata disagrees with OpenLibrary.
	ContentConfirmScore float64
	// ContentTitleConfirm is the minimum fraction of the candidate title that
	// must appear in the scanned head for content confirmation.
	ContentTitleConfirm float64
}

func (o RankOptions) withDefaults() RankOptions {
	if o.ConfirmScore <= 0 {
		o.ConfirmScore = defaultConfirmScore
	}
	if o.ProbableScore <= 0 {
		o.ProbableScore = defaultProbableScore
	}
	if o.ReviewScore <= 0 {
		o.ReviewScore = defaultReviewScore
	}
	if o.ConfirmGap <= 0 {
		o.ConfirmGap = defaultConfirmGap
	}
	if o.AmbiguityDelta <= 0 {
		o.AmbiguityDelta = defaultAmbiguityDelta
	}
	if o.MaxCandidates <= 0 {
		o.MaxCandidates = defaultMaxRankCandidates
	}
	if o.ContentConfirmScore <= 0 {
		o.ContentConfirmScore = defaultContentConfirmScore
	}
	if o.ContentTitleConfirm <= 0 {
		o.ContentTitleConfirm = defaultContentTitleConfirm
	}
	return o
}

// RankedCandidate is one work group in a book's ranked candidate list.
type RankedCandidate struct {
	Rank         int
	Selected     bool
	EditionCount int
	Candidate
}

// BookMatch is the ranking decision for one scanned book.
type BookMatch struct {
	BookID int64

	Status     Status
	Confidence float64
	// ContentScore records how strongly the scanned text confirms the selected
	// OpenLibrary title and author.
	ContentScore float64
	Method       string
	EditionKey   string
	WorkKey      string

	ISBN       string
	ISBNSource identifier.Source

	ReviewReason string
	ScoreGap     float64

	CandidateCount int
	WorkCount      int
	Candidates     []RankedCandidate
}

// RankStats counts books by final status.
type RankStats struct {
	Confirmed   int
	Probable    int
	Ambiguous   int
	NeedsReview int
	NotFound    int
	NoData      int
}

// RankResult is the output of the ranking step.
type RankResult struct {
	Books map[int64]BookMatch
	Stats RankStats
}

// Rank groups candidates by work, selects the best edition per work and
// classifies every cached book.
func Rank(stream *StreamResult, cache *Cache, opts RankOptions) (*RankResult, error) {
	if stream == nil {
		return nil, errors.New("nil stream result")
	}
	if cache == nil {
		return nil, errors.New("nil cache")
	}
	opts = opts.withDefaults()

	result := &RankResult{Books: make(map[int64]BookMatch, len(cache.Books))}
	for bookID, book := range cache.Books {
		match := rankBook(book, stream.Candidates[bookID], opts)
		match.BookID = bookID
		result.Books[bookID] = match
		switch match.Status {
		case StatusConfirmed:
			result.Stats.Confirmed++
		case StatusProbable:
			result.Stats.Probable++
		case StatusAmbiguous:
			result.Stats.Ambiguous++
		case StatusNeedsReview:
			result.Stats.NeedsReview++
		case StatusNotFound:
			result.Stats.NotFound++
		case StatusNoData:
			result.Stats.NoData++
		}
	}
	return result, nil
}

type candidateGroup struct {
	key      string
	best     Candidate
	editions []Candidate
}

func rankBook(book *Book, candidates []Candidate, opts RankOptions) BookMatch {
	match := BookMatch{BookID: book.ID, CandidateCount: len(candidates)}
	if len(candidates) == 0 {
		if book.TitleKey == "" && len(book.ISBNs) == 0 {
			match.Status = StatusNoData
		} else {
			match.Status = StatusNotFound
		}
		return match
	}

	groups := groupCandidates(candidates)
	sortGroups(groups)
	match.WorkCount = len(groups)

	best := groups[0]
	var second *Candidate
	if len(groups) > 1 {
		second = &groups[1].best
	}

	status, reason := classify(book, best.best, second, candidates, opts)
	match.Status = status
	match.ReviewReason = reason
	match.EditionKey = best.best.EditionKey
	match.WorkKey = best.best.WorkKey
	match.ISBN = best.best.ISBN
	match.ISBNSource = best.best.ISBNSource
	match.ContentScore = best.best.ContentScore
	match.Method = candidateMethod(best.best)
	if contentConfirms(best.best, opts) {
		match.Method = "isbn+content"
	}
	match.Confidence = best.best.Score

	if second != nil {
		match.ScoreGap = best.best.Score - second.Score
	} else {
		match.ScoreGap = 1
	}

	switch status {
	case StatusConfirmed:
		if match.Confidence < opts.ConfirmScore {
			match.Confidence = opts.ConfirmScore
		}
	case StatusProbable:
		if match.Confidence < opts.ProbableScore {
			match.Confidence = opts.ProbableScore
		}
	}

	count := opts.MaxCandidates
	if count > len(groups) {
		count = len(groups)
	}
	match.Candidates = make([]RankedCandidate, 0, count)
	for i := 0; i < count; i++ {
		group := groups[i]
		match.Candidates = append(match.Candidates, RankedCandidate{
			Rank:         i + 1,
			Selected:     i == 0,
			EditionCount: len(group.editions),
			Candidate:    group.best,
		})
	}
	return match
}

func classify(book *Book, best Candidate, second *Candidate, candidates []Candidate, opts RankOptions) (Status, string) {
	strongWorks := make(map[string]struct{})
	for _, candidate := range candidates {
		if candidate.ISBN != "" && candidate.ISBNPriority <= 1 {
			strongWorks[groupKey(candidate)] = struct{}{}
		}
	}
	if len(strongWorks) > 1 {
		return StatusNeedsReview, "conflicting_isbns"
	}

	// An ISBN match whose scanned title/author disagree with OpenLibrary is
	// confirmed when the scanned text itself contains the OpenLibrary title
	// and author: the metadata lied, not the ISBN.
	if contentConfirms(best, opts) {
		return StatusConfirmed, ""
	}

	gap := 1.0
	if second != nil {
		gap = best.Score - second.Score
	}
	ambiguityFloor := opts.ProbableScore - opts.AmbiguityDelta
	if ambiguityFloor < opts.ReviewScore {
		ambiguityFloor = opts.ReviewScore
	}
	if second != nil && atLeast(second.Score, ambiguityFloor) && lessThan(gap, opts.AmbiguityDelta) {
		return StatusAmbiguous, "ambiguous_work"
	}

	titleAvailable := len(book.TitleTokens) > 0
	authorAvailable := len(book.AuthorTokens) > 0
	contradiction := titleAvailable && authorAvailable &&
		lessThan(best.TitleScore, 0.20) && lessThan(best.AuthorScore, 0.20)

	titleOnly := best.ISBN == "" && lessThan(best.AuthorScore, 0.5)
	if titleOnly {
		if atLeast(best.ContentScore, 0.5) && atLeast(best.TitleScore, 0.9) {
			return StatusProbable, ""
		}
		return StatusNeedsReview, "title_only"
	}

	if best.ISBN != "" {
		switch best.ISBNPriority {
		case 0:
			if contradiction {
				return StatusNeedsReview, "isbn_title_mismatch"
			}
			return StatusConfirmed, ""
		case 1:
			if contradiction {
				return StatusNeedsReview, "isbn_title_mismatch"
			}
			if !titleAvailable && !authorAvailable {
				return StatusProbable, ""
			}
		}
	}

	if atLeast(best.Score, opts.ConfirmScore) && atLeast(gap, opts.ConfirmGap) {
		return StatusConfirmed, ""
	}
	if atLeast(best.Score, opts.ProbableScore) && atLeast(gap, opts.AmbiguityDelta) {
		return StatusProbable, ""
	}
	if atLeast(best.Score, opts.ReviewScore) {
		return StatusNeedsReview, "low_confidence"
	}
	return StatusNotFound, ""
}

func contentConfirms(candidate Candidate, opts RankOptions) bool {
	return candidate.ISBN != "" &&
		candidate.ISBNPriority <= 2 &&
		atLeast(candidate.ContentScore, opts.ContentConfirmScore) &&
		atLeast(candidate.ContentTitleHit, opts.ContentTitleConfirm)
}

func atLeast(value, threshold float64) bool {
	return value+scoreEpsilon >= threshold
}

func lessThan(value, threshold float64) bool {
	return value+scoreEpsilon < threshold
}

func groupCandidates(candidates []Candidate) []candidateGroup {
	index := make(map[string]int, len(candidates))
	groups := make([]candidateGroup, 0, len(candidates))
	for _, candidate := range candidates {
		key := groupKey(candidate)
		position, ok := index[key]
		if !ok {
			index[key] = len(groups)
			groups = append(groups, candidateGroup{
				key:      key,
				best:     candidate,
				editions: []Candidate{candidate},
			})
			continue
		}
		groups[position].editions = append(groups[position].editions, candidate)
		if betterCandidate(candidate, groups[position].best) {
			groups[position].best = candidate
		}
	}
	return groups
}

func sortGroups(groups []candidateGroup) {
	sort.SliceStable(groups, func(i, j int) bool {
		if betterCandidate(groups[i].best, groups[j].best) {
			return true
		}
		if betterCandidate(groups[j].best, groups[i].best) {
			return false
		}
		return groups[i].key < groups[j].key
	})
}

func groupKey(candidate Candidate) string {
	if candidate.WorkKey != "" {
		return candidate.WorkKey
	}
	return "edition:" + candidate.EditionKey
}

func betterCandidate(a, b Candidate) bool {
	return candidateWorse(b, a)
}

func candidateWorse(a, b Candidate) bool {
	if a.Score != b.Score {
		return a.Score < b.Score
	}
	aHasISBN := a.ISBN != ""
	bHasISBN := b.ISBN != ""
	if aHasISBN != bHasISBN {
		return !aHasISBN
	}
	if aHasISBN && a.ISBNPriority != b.ISBNPriority {
		return a.ISBNPriority > b.ISBNPriority
	}
	if a.TitleScore != b.TitleScore {
		return a.TitleScore < b.TitleScore
	}
	if a.AuthorScore != b.AuthorScore {
		return a.AuthorScore < b.AuthorScore
	}
	return a.EditionKey > b.EditionKey
}

func candidateMethod(candidate Candidate) string {
	hasISBN := candidate.ISBN != ""
	hasExact := containsString(candidate.Methods, "title_exact")
	hasFuzzy := containsString(candidate.Methods, "title_fuzzy")
	switch {
	case hasISBN && hasExact:
		return "isbn+title_exact"
	case hasISBN && hasFuzzy:
		return "isbn+title_fuzzy"
	case hasISBN:
		return "isbn"
	case hasExact:
		return "title_exact"
	case hasFuzzy:
		return "title_fuzzy"
	default:
		return "unknown"
	}
}

func containsString(values []string, value string) bool {
	for _, item := range values {
		if item == value {
			return true
		}
	}
	return false
}
