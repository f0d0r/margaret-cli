package report

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/f0d0r/margaret-cli/internal/db"
)

// ReviewCandidate is one stored ranking candidate in a review report.
type ReviewCandidate struct {
	Rank           int64           `json:"rank"`
	Selected       bool            `json:"selected"`
	EditionCount   int64           `json:"edition_count"`
	EditionKey     string          `json:"edition_key"`
	WorkKey        string          `json:"work_key"`
	EditionTitle   string          `json:"edition_title"`
	EditionAuthors []string        `json:"edition_authors"`
	AuthorKeys     []string        `json:"author_keys"`
	AuthorNames    []string        `json:"author_names"`
	ISBN           string          `json:"isbn"`
	ISBNSource     string          `json:"isbn_source"`
	ISBNPriority   int64           `json:"isbn_priority"`
	Method         string          `json:"method"`
	TitleScore     float64         `json:"title_score"`
	AuthorScore    float64         `json:"author_score"`
	ContentScore   float64         `json:"content_score"`
	Score          float64         `json:"score"`
	Evidence       json.RawMessage `json:"evidence"`
}

// ConflictInfo is one peer in a metadata/content conflict.
type ConflictInfo struct {
	PeerBookID int64   `json:"peer_book_id"`
	Reason     string  `json:"reason"`
	Score      float64 `json:"score"`
}

// ReviewBook is one uncertain book with its stored candidates.
type ReviewBook struct {
	BookReport
	MatchInfo
	MatchStatus       string            `json:"match_status"`
	MatchReviewReason string            `json:"match_review_reason"`
	Conflicts         []ConflictInfo    `json:"conflicts"`
	Candidates        []ReviewCandidate `json:"candidates"`
}

// ReviewReport groups uncertain books by match status.
type ReviewReport struct {
	Probable    []ReviewBook `json:"probable"`
	Ambiguous   []ReviewBook `json:"ambiguous"`
	NeedsReview []ReviewBook `json:"needs_review"`
	NotFound    []ReviewBook `json:"not_found"`
	NoData      []ReviewBook `json:"no_data"`
}

// WriteReview writes uncertain books grouped by status. When no book needs
// review nothing is written.
func WriteReview(ctx context.Context, q *db.Queries, path string) error {
	matches, err := q.ListBookMatchesForReview(ctx)
	if err != nil {
		return fmt.Errorf("list review matches: %w", err)
	}
	if len(matches) == 0 {
		return nil
	}
	candidateRows, err := q.ListBookMatchCandidatesForReview(ctx)
	if err != nil {
		return fmt.Errorf("list review candidates: %w", err)
	}
	candidates := make(map[int64][]ReviewCandidate)
	for _, row := range candidateRows {
		candidates[row.BookID] = append(candidates[row.BookID], reviewCandidate(row))
	}
	conflictRows, err := q.ListBookConflictsForReview(ctx)
	if err != nil {
		return fmt.Errorf("list review conflicts: %w", err)
	}
	conflicts := make(map[int64][]ConflictInfo)
	for _, row := range conflictRows {
		conflicts[row.BookID] = append(conflicts[row.BookID], ConflictInfo{
			PeerBookID: row.PeerBookID,
			Reason:     row.Reason,
			Score:      row.Score,
		})
	}

	report := ReviewReport{
		Probable:    []ReviewBook{},
		Ambiguous:   []ReviewBook{},
		NeedsReview: []ReviewBook{},
		NotFound:    []ReviewBook{},
		NoData:      []ReviewBook{},
	}
	for _, match := range matches {
		book, ok, err := getBookReport(ctx, q, match.BookID)
		if err != nil {
			return fmt.Errorf("read review book %d: %w", match.BookID, err)
		}
		if !ok {
			continue
		}
		reviewReason := match.ReviewReason
		bookConflicts := conflicts[match.BookID]
		if len(bookConflicts) > 0 {
			reviewReason = bookConflicts[0].Reason
		}
		item := ReviewBook{
			BookReport: book,
			MatchInfo: MatchInfo{
				Status:         match.FinalStatus,
				Confidence:     match.Confidence,
				ContentScore:   match.ContentScore,
				Method:         match.Method,
				EditionKey:     match.EditionKey,
				WorkKey:        match.WorkKey,
				ISBN:           match.Isbn,
				ISBNSource:     match.IsbnSource,
				ReviewReason:   reviewReason,
				ScoreGap:       match.ScoreGap,
				CandidateCount: match.CandidateCount,
				WorkCount:      match.WorkCount,
				MatcherVersion: match.MatcherVersion,
				DumpVersion:    match.DumpVersion,
			},
			MatchStatus:       match.MatchStatus,
			MatchReviewReason: match.ReviewReason,
			Conflicts:         bookConflicts,
			Candidates:        candidates[match.BookID],
		}
		if item.Conflicts == nil {
			item.Conflicts = []ConflictInfo{}
		}
		if item.Candidates == nil {
			item.Candidates = []ReviewCandidate{}
		}
		switch match.FinalStatus {
		case "probable":
			report.Probable = append(report.Probable, item)
		case "ambiguous":
			report.Ambiguous = append(report.Ambiguous, item)
		case "needs_review":
			report.NeedsReview = append(report.NeedsReview, item)
		case "not_found":
			report.NotFound = append(report.NotFound, item)
		case "no_data":
			report.NoData = append(report.NoData, item)
		}
	}

	if len(report.Probable) == 0 && len(report.Ambiguous) == 0 &&
		len(report.NeedsReview) == 0 && len(report.NotFound) == 0 &&
		len(report.NoData) == 0 {
		return nil
	}
	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return err
	}
	if err := writeFile(path, data); err != nil {
		return fmt.Errorf("write review report: %w", err)
	}
	return nil
}

func reviewCandidate(row db.BookMatchCandidate) ReviewCandidate {
	return ReviewCandidate{
		Rank:           row.Rank,
		Selected:       row.Selected != 0,
		EditionCount:   row.EditionCount,
		EditionKey:     row.EditionKey,
		WorkKey:        row.WorkKey,
		EditionTitle:   row.EditionTitle,
		EditionAuthors: decodeJSONStrings(row.EditionAuthors),
		AuthorKeys:     decodeJSONStrings(row.AuthorKeys),
		AuthorNames:    decodeJSONStrings(row.AuthorNames),
		ISBN:           row.Isbn,
		ISBNSource:     row.IsbnSource,
		ISBNPriority:   row.IsbnPriority,
		Method:         row.Method,
		TitleScore:     row.TitleScore,
		AuthorScore:    row.AuthorScore,
		ContentScore:   row.ContentScore,
		Score:          row.Score,
		Evidence:       rawJSON(row.Evidence),
	}
}

func decodeJSONStrings(raw string) []string {
	if raw == "" {
		return []string{}
	}
	var values []string
	if err := json.Unmarshal([]byte(raw), &values); err != nil {
		return []string{}
	}
	if values == nil {
		return []string{}
	}
	return values
}

func rawJSON(raw string) json.RawMessage {
	if raw == "" {
		return json.RawMessage("{}")
	}
	var value any
	if err := json.Unmarshal([]byte(raw), &value); err != nil {
		return json.RawMessage("{}")
	}
	return json.RawMessage(raw)
}
