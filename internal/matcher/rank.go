package matcher

import (
	"errors"
	"sort"
	"strings"

	"github.com/f0d0r/margaret-cli/internal/identifier"
	"github.com/f0d0r/margaret-cli/internal/textmatch"
)

const (
	defaultConfirmScore      = 0.90
	defaultProbableScore     = 0.75
	defaultReviewScore       = 0.60
	defaultConfirmGap        = 0.05
	defaultAmbiguityDelta    = 0.05
	defaultMaxRankCandidates = 10

	scoreEpsilon = 1e-9

	defaultContentConfirmScore  = 0.60
	defaultContentTitleConfirm  = 0.60
	defaultContentAuthorConfirm = 0.40
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
	// ContentAuthorConfirm is the minimum fraction of the candidate author
	// that must appear in the scanned head when the metadata author is weak.
	ContentAuthorConfirm float64
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
	if o.ContentAuthorConfirm <= 0 {
		o.ContentAuthorConfirm = defaultContentAuthorConfirm
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
	groups, canonical := mergeDuplicateGroups(groups)
	sortGroups(groups)
	match.WorkCount = len(groups)

	best := groups[0]
	var second *Candidate
	if len(groups) > 1 {
		second = &groups[1].best
	}

	status, reason := classify(book, best.best, second, candidates, canonical, opts)
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

func classify(book *Book, best Candidate, second *Candidate, candidates []Candidate, canonical map[string]string, opts RankOptions) (Status, string) {
	strongWorks := make(map[string]struct{})
	for _, candidate := range candidates {
		if candidate.ISBN != "" && candidate.ISBNPriority <= 1 {
			strongWorks[candidateGroupKey(candidate, canonical)] = struct{}{}
		}
	}
	if len(strongWorks) > 1 {
		return StatusNeedsReview, "conflicting_isbns"
	}

	// An ISBN match whose scanned title/author disagree with OpenLibrary is
	// confirmed when the scanned text itself contains the OpenLibrary title
	// and author: the metadata lied, not the ISBN. If the scanned metadata is
	// itself present in the text and another candidate is corroborated, the
	// evidence is contradictory and the book goes to review instead.
	if contentConflict(book, best, candidates, canonical) {
		return StatusAmbiguous, "content_conflict"
	}
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
	if candidate.ISBN == "" || candidate.ISBNPriority > 2 {
		return false
	}
	if !atLeast(candidate.ContentScore, opts.ContentConfirmScore) {
		return false
	}
	if !atLeast(candidate.ContentTitleHit, opts.ContentTitleConfirm) {
		return false
	}
	if len(candidate.AuthorNames) > 0 && lessThan(candidate.AuthorScore, 0.5) &&
		lessThan(candidate.TitleScore, 0.5) &&
		!atLeast(candidate.ContentAuthorHit, opts.ContentAuthorConfirm) {
		return false
	}
	return true
}

func contentConflict(book *Book, best Candidate, candidates []Candidate, canonical map[string]string) bool {
	if book == nil || lessThan(book.OrigTitleHit, contentCredibleTitle) {
		return false
	}
	bestGroup := candidateGroupKey(best, canonical)
	// A weak-title ISBN candidate that contradicts a title+author-confirmed
	// alternative is a conflict.
	if best.ISBN != "" && lessThan(best.TitleScore, 0.5) {
		for _, candidate := range candidates {
			if candidate.EditionKey == best.EditionKey || candidateGroupKey(candidate, canonical) == bestGroup {
				continue
			}
			if atLeast(candidate.ContentTitleHit, contentTitleConfirmed) &&
				atLeast(candidate.ContentAuthorHit, contentAuthorConfirmedHit) {
				return true
			}
		}
	}
	// A title+author-confirmed title candidate with a conflicting weak-title
	// ISBN candidate is the ad-ISBN case (e.g. JSF in Action vs Tapestry).
	if best.ISBN == "" && atLeast(best.ContentAuthorHit, contentAuthorConfirmedHit) &&
		(lessThan(best.TitleScore, 0.9) || lessThan(best.AuthorScore, 0.5)) {
		for _, candidate := range candidates {
			if candidate.ISBN == "" || candidate.EditionKey == best.EditionKey ||
				candidateGroupKey(candidate, canonical) == bestGroup {
				continue
			}
			if lessThan(candidate.TitleScore, 0.5) &&
				atLeast(candidate.ContentTitleHit, contentTitleConfirmed) &&
				lessThan(candidate.ContentAuthorHit, contentAuthorConfirmedHit) {
				return true
			}
		}
	}
	return false
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
		key := baseGroupKey(candidate)
		candidate.GroupKey = key
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

// mergeDuplicateGroups folds groups that describe the same work but carry
// different OpenLibrary work keys. OpenLibrary contains many duplicate work
// records; without this step their near-identical scores produce false
// ambiguous_work results.
func mergeDuplicateGroups(groups []candidateGroup) ([]candidateGroup, map[string]string) {
	canonical := make(map[string]string, len(groups))
	used := make([]bool, len(groups))
	merged := make([]candidateGroup, 0, len(groups))

	for i := range groups {
		if used[i] {
			continue
		}
		cluster := []int{i}
		used[i] = true
		for j := i + 1; j < len(groups); j++ {
			if used[j] || !sameWorkIdentity(groups[i].best, groups[j].best) {
				continue
			}
			cluster = append(cluster, j)
			used[j] = true
		}

		canonicalIdx := cluster[0]
		for _, idx := range cluster[1:] {
			if len(groups[idx].editions) > len(groups[canonicalIdx].editions) ||
				(len(groups[idx].editions) == len(groups[canonicalIdx].editions) &&
					betterCandidate(groups[idx].best, groups[canonicalIdx].best)) {
				canonicalIdx = idx
			}
		}
		canonicalKey := groups[canonicalIdx].key

		mergedGroup := candidateGroup{key: canonicalKey}
		mergedKeys := make([]string, 0, len(cluster)-1)
		for _, idx := range cluster {
			mergedGroup.editions = append(mergedGroup.editions, groups[idx].editions...)
			if mergedGroup.best.EditionKey == "" || betterCandidate(groups[idx].best, mergedGroup.best) {
				mergedGroup.best = groups[idx].best
			}
			if groups[idx].key != canonicalKey {
				mergedKeys = append(mergedKeys, groups[idx].key)
			}
			canonical[groups[idx].key] = canonicalKey
		}
		mergedGroup.best.GroupKey = canonicalKey
		mergedGroup.best.MergedWorkKeys = uniqueStrings(mergedKeys)
		merged = append(merged, mergedGroup)
	}
	return merged, canonical
}

func sameWorkIdentity(a, b Candidate) bool {
	return sameWorkTitle(a, b) && authorsCompatible(a, b)
}

func sameWorkTitle(a, b Candidate) bool {
	titlesA := candidateTitleKeys(a)
	titlesB := candidateTitleKeys(b)
	for _, titleA := range titlesA {
		for _, titleB := range titlesB {
			if titleA != "" && titleA == titleB {
				return true
			}
		}
	}
	return false
}

func candidateTitleKeys(candidate Candidate) []string {
	values := make([]string, 0, 2)
	if candidate.EditionTitle != "" {
		values = append(values, textmatch.TitleKey(candidate.EditionTitle))
	}
	if candidate.WorkTitle != "" {
		values = append(values, textmatch.TitleKey(candidate.WorkTitle))
	}
	return values
}

func authorsCompatible(a, b Candidate) bool {
	keysA := stringSet(a.AuthorKeys)
	keysB := stringSet(b.AuthorKeys)
	if len(keysA) > 0 && len(keysB) > 0 {
		for key := range keysA {
			if _, ok := keysB[key]; ok {
				return true
			}
		}
	}
	namesA := normalizedAuthorNames(a.AuthorNames)
	namesB := normalizedAuthorNames(b.AuthorNames)
	if len(namesA) > 0 && len(namesB) > 0 {
		return stringSlicesEqual(namesA, namesB)
	}
	// Merge only when neither side has any author information; one-sided
	// author evidence is not enough to assume the works are the same.
	return len(a.AuthorNames) == 0 && len(b.AuthorNames) == 0
}

func normalizedAuthorNames(names []string) []string {
	out := make([]string, 0, len(names))
	for _, name := range names {
		tokens := textmatch.AuthorTokens(name)
		if len(tokens) == 0 {
			continue
		}
		value := strings.Join(tokens, " ")
		if value == "" {
			continue
		}
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}

func stringSet(values []string) map[string]struct{} {
	out := make(map[string]struct{}, len(values))
	for _, value := range values {
		if value != "" {
			out[value] = struct{}{}
		}
	}
	return out
}

func stringSlicesEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
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

func baseGroupKey(candidate Candidate) string {
	if candidate.WorkKey != "" {
		return candidate.WorkKey
	}
	return "edition:" + candidate.EditionKey
}

// candidateGroupKey returns the canonical ranking group for a candidate.
// Candidates produced by groupCandidates already carry their base key; the
// canonical map folds duplicate work keys into one group.
func candidateGroupKey(candidate Candidate, canonical map[string]string) string {
	key := candidate.GroupKey
	if key == "" {
		key = baseGroupKey(candidate)
	}
	if merged, ok := canonical[key]; ok {
		return merged
	}
	return key
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
