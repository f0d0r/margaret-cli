package report

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/f0d0r/margaret-cli/internal/db"
)

// MatchInfo is the match metadata embedded in enriched and review reports.
type MatchInfo struct {
	Status         string  `json:"status"`
	Confidence     float64 `json:"confidence"`
	Method         string  `json:"method"`
	EditionKey     string  `json:"edition_key"`
	WorkKey        string  `json:"work_key"`
	ISBN           string  `json:"isbn"`
	ISBNSource     string  `json:"isbn_source"`
	ReviewReason   string  `json:"review_reason"`
	ScoreGap       float64 `json:"score_gap"`
	CandidateCount int64   `json:"candidate_count"`
	WorkCount      int64   `json:"work_count"`
	MatcherVersion string  `json:"matcher_version"`
	DumpVersion    string  `json:"dump_version"`
}

// EnrichedBook is one confirmed book with its OpenLibrary match metadata.
type EnrichedBook struct {
	BookReport
	Match MatchInfo `json:"match"`
}

// WriteEnrichedBooks writes confirmed books with their match metadata. When
// no book is confirmed nothing is written.
func WriteEnrichedBooks(ctx context.Context, q *db.Queries, path string) error {
	matches, err := q.ListConfirmedBookMatches(ctx)
	if err != nil {
		return fmt.Errorf("list confirmed matches: %w", err)
	}
	if len(matches) == 0 {
		return nil
	}

	books := make([]EnrichedBook, 0, len(matches))
	for _, match := range matches {
		book, ok, err := getBookReport(ctx, q, match.BookID)
		if err != nil {
			return fmt.Errorf("read confirmed book %d: %w", match.BookID, err)
		}
		if !ok {
			continue
		}
		books = append(books, EnrichedBook{
			BookReport: book,
			Match:      matchInfo(match),
		})
	}
	if len(books) == 0 {
		return nil
	}
	data, err := json.MarshalIndent(books, "", "  ")
	if err != nil {
		return err
	}
	if err := writeFile(path, data); err != nil {
		return fmt.Errorf("write enriched books report: %w", err)
	}
	return nil
}

func matchInfo(match db.BookMatch) MatchInfo {
	return MatchInfo{
		Status:         match.Status,
		Confidence:     match.Confidence,
		Method:         match.Method,
		EditionKey:     match.EditionKey,
		WorkKey:        match.WorkKey,
		ISBN:           match.Isbn,
		ISBNSource:     match.IsbnSource,
		ReviewReason:   match.ReviewReason,
		ScoreGap:       match.ScoreGap,
		CandidateCount: match.CandidateCount,
		WorkCount:      match.WorkCount,
		MatcherVersion: match.MatcherVersion,
		DumpVersion:    match.DumpVersion,
	}
}
