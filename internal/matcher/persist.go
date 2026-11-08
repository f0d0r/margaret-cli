package matcher

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/f0d0r/margaret-cli/internal/db"
	"github.com/f0d0r/margaret-cli/internal/tx"
)

// PersistMeta carries the versions recorded with every match.
type PersistMeta struct {
	MatcherVersion string
	DumpVersion    string
	Now            time.Time
}

// PersistOptions configures the match writer.
type PersistOptions struct {
	BatchSize int
}

// Persist writes a RankResult to the database in batches. Re-running it for
// the same books is idempotent: old candidates are replaced.
func Persist(ctx context.Context, sqldb *sql.DB, q *db.Queries, result *RankResult, meta PersistMeta, opts PersistOptions) error {
	if result == nil {
		return errors.New("nil rank result")
	}
	if sqldb == nil {
		return errors.New("nil database")
	}
	if q == nil {
		return errors.New("nil queries")
	}
	if meta.MatcherVersion == "" {
		meta.MatcherVersion = Version
	}
	if meta.Now.IsZero() {
		meta.Now = time.Now()
	}
	if opts.BatchSize <= 0 {
		opts.BatchSize = 500
	}

	ids := make([]int64, 0, len(result.Books))
	for id := range result.Books {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool {
		return ids[i] < ids[j]
	})

	now := meta.Now.Unix()
	for start := 0; start < len(ids); start += opts.BatchSize {
		end := start + opts.BatchSize
		if end > len(ids) {
			end = len(ids)
		}
		batch := ids[start:end]
		err := tx.WithTx(ctx, sqldb, q, func(txCtx context.Context) error {
			txq := tx.QueryFrom(txCtx, q)
			for _, id := range batch {
				if err := persistBook(txCtx, txq, result.Books[id], meta, now); err != nil {
					return err
				}
			}
			return nil
		})
		if err != nil {
			return fmt.Errorf("persist matches: %w", err)
		}
	}
	return nil
}

func persistBook(ctx context.Context, q *db.Queries, match BookMatch, meta PersistMeta, now int64) error {
	if err := q.DeleteBookMatchCandidatesByBookID(ctx, match.BookID); err != nil {
		return fmt.Errorf("delete old candidates for book %d: %w", match.BookID, err)
	}
	if err := q.UpsertBookMatch(ctx, db.UpsertBookMatchParams{
		BookID:         match.BookID,
		Status:         string(match.Status),
		Confidence:     match.Confidence,
		Method:         match.Method,
		EditionKey:     match.EditionKey,
		WorkKey:        match.WorkKey,
		Isbn:           match.ISBN,
		IsbnSource:     string(match.ISBNSource),
		ReviewReason:   match.ReviewReason,
		ScoreGap:       match.ScoreGap,
		CandidateCount: int64(match.CandidateCount),
		WorkCount:      int64(match.WorkCount),
		MatcherVersion: meta.MatcherVersion,
		DumpVersion:    meta.DumpVersion,
		MatchedAt:      now,
	}); err != nil {
		return fmt.Errorf("upsert match for book %d: %w", match.BookID, err)
	}

	for _, candidate := range match.Candidates {
		authorNames, err := jsonStrings(candidate.AuthorNames)
		if err != nil {
			return fmt.Errorf("encode authors for book %d: %w", match.BookID, err)
		}
		authorKeys, err := jsonStrings(candidate.AuthorKeys)
		if err != nil {
			return fmt.Errorf("encode author keys for book %d: %w", match.BookID, err)
		}
		evidence, err := json.Marshal(candidateEvidence{
			Methods:           candidate.Methods,
			WorkGroupEditions: candidate.EditionCount,
		})
		if err != nil {
			return fmt.Errorf("encode evidence for book %d: %w", match.BookID, err)
		}
		if err := q.CreateBookMatchCandidate(ctx, db.CreateBookMatchCandidateParams{
			BookID:         match.BookID,
			Rank:           int64(candidate.Rank),
			Selected:       boolInt(candidate.Selected),
			EditionCount:   int64(candidate.EditionCount),
			EditionKey:     candidate.EditionKey,
			WorkKey:        candidate.WorkKey,
			EditionTitle:   candidate.EditionTitle,
			EditionAuthors: authorNames,
			AuthorKeys:     authorKeys,
			AuthorNames:    authorNames,
			Isbn:           candidate.ISBN,
			IsbnSource:     string(candidate.ISBNSource),
			IsbnPriority:   int64(candidate.ISBNPriority),
			Method:         candidateMethod(candidate.Candidate),
			TitleScore:     candidate.TitleScore,
			AuthorScore:    candidate.AuthorScore,
			ContentScore:   candidate.ContentScore,
			Score:          candidate.Score,
			Evidence:       string(evidence),
			MatcherVersion: meta.MatcherVersion,
			DumpVersion:    meta.DumpVersion,
			CreatedAt:      now,
		}); err != nil {
			return fmt.Errorf("create candidate for book %d: %w", match.BookID, err)
		}
	}

	if err := q.UpdateBookMatchStatus(ctx, db.UpdateBookMatchStatusParams{
		MatchStatus: string(match.Status),
		ID:          match.BookID,
	}); err != nil {
		return fmt.Errorf("update match status for book %d: %w", match.BookID, err)
	}
	return nil
}

type candidateEvidence struct {
	Methods           []string `json:"methods,omitempty"`
	WorkGroupEditions int      `json:"work_group_editions,omitempty"`
}

func jsonStrings(values []string) (string, error) {
	if len(values) == 0 {
		return "[]", nil
	}
	data, err := json.Marshal(values)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func boolInt(value bool) int64 {
	if value {
		return 1
	}
	return 0
}
