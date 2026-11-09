// Package conflict detects local books whose metadata claims they are the
// same book while their content fingerprints show otherwise.
package conflict

import (
	"context"
	"database/sql"
	"encoding/binary"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/f0d0r/margaret-cli/internal/db"
	"github.com/f0d0r/margaret-cli/internal/lsh"
	"github.com/f0d0r/margaret-cli/internal/textmatch"
	"github.com/f0d0r/margaret-cli/internal/tx"
	"github.com/f0d0r/margaret-ebook-library/tools"
)

const (
	// Reason marks a pair whose content similarity is low enough to be a real
	// metadata/content mismatch.
	Reason = "metadata_content_mismatch"
	// PossibleDuplicateReason marks a pair in the middle band: likely the same
	// book in a different edition or extraction, but not a metadata error.
	PossibleDuplicateReason = "possible_duplicate"
	// mismatchSimilarity is the upper bound of the real-mismatch band.
	mismatchSimilarity = 0.20
	// contentSupportScore is the content score above which a book is treated
	// as having independent content support for its match.
	contentSupportScore = 0.60
)

// Options configures conflict detection.
type Options struct {
	Now time.Time
}

// Peer is one conflicting book pair as seen from a book.
type Peer struct {
	PeerBookID int64
	Reason     string
	Score      float64
}

// Pair is one directed conflict edge ready for persistence.
type Pair struct {
	BookID     int64
	PeerBookID int64
	Reason     string
	Score      float64
}

// Stats summarizes a conflict detection run.
type Stats struct {
	Books              int
	TitleGroups        int
	DuplicateGroups    int
	PairsCompared      int
	Conflicts          int
	PossibleDuplicates int
	ConflictBooks      int
	// PossibleDuplicateBooks counts books with at least one possible-duplicate
	// peer; they keep their matcher status.
	PossibleDuplicateBooks int
	DegradedBooks          int
	SkippedNoMinhash       int
	SkippedAuthorPair      int
}

// Result is the output of conflict detection.
type Result struct {
	Pairs []Pair
	// Books lists every book that has at least one conflict or possible
	// duplicate peer.
	Books map[int64][]Peer
	// Degraded lists the books whose final status must become needs_review.
	Degraded map[int64]struct{}
	Stats    Stats
}

type bookInfo struct {
	id           int64
	titleKey     string
	authorKey    string
	authorNames  int
	contentScore float64
}

func (b *bookInfo) contentSupported() bool {
	return b.contentScore >= contentSupportScore
}

// DetectAndPersist finds local metadata/content conflicts and writes the
// final status overlay to the database.
func DetectAndPersist(ctx context.Context, conn *sql.DB, q *db.Queries, opts Options) (*Result, error) {
	if conn == nil {
		return nil, fmt.Errorf("nil database")
	}
	if q == nil {
		return nil, fmt.Errorf("nil queries")
	}
	result, err := Detect(ctx, q)
	if err != nil {
		return nil, err
	}
	if opts.Now.IsZero() {
		opts.Now = time.Now()
	}
	now := opts.Now.Unix()

	err = tx.WithTx(ctx, conn, q, func(txCtx context.Context) error {
		txq := tx.QueryFrom(txCtx, q)
		if err := txq.ResetBooksMatchStatus(txCtx); err != nil {
			return fmt.Errorf("reset match statuses: %w", err)
		}
		if err := txq.DeleteBookConflicts(txCtx); err != nil {
			return fmt.Errorf("clear conflicts: %w", err)
		}
		for _, pair := range result.Pairs {
			if err := txq.CreateBookConflict(txCtx, db.CreateBookConflictParams{
				BookID:     pair.BookID,
				PeerBookID: pair.PeerBookID,
				Reason:     pair.Reason,
				Score:      pair.Score,
				CreatedAt:  now,
			}); err != nil {
				return fmt.Errorf("create conflict %d/%d: %w", pair.BookID, pair.PeerBookID, err)
			}
		}
		for bookID := range result.Degraded {
			if err := txq.SetBookMatchStatus(txCtx, db.SetBookMatchStatusParams{
				MatchStatus: "needs_review",
				ID:          bookID,
			}); err != nil {
				return fmt.Errorf("set conflict status for book %d: %w", bookID, err)
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

// Detect finds conflicts without writing to the database.
func Detect(ctx context.Context, q *db.Queries) (*Result, error) {
	rows, err := q.ListBooksForConflictDetection(ctx)
	if err != nil {
		return nil, fmt.Errorf("list books: %w", err)
	}
	books := make(map[int64]*bookInfo, len(rows))
	for _, row := range rows {
		titleKey := textmatch.TitleKey(row.Title)
		if titleKey == "" {
			continue
		}
		books[row.ID] = &bookInfo{
			id:           row.ID,
			titleKey:     titleKey,
			contentScore: row.ContentScore,
		}
	}

	authorRows, err := q.ListBookAuthorsForConflictDetection(ctx)
	if err != nil {
		return nil, fmt.Errorf("list authors: %w", err)
	}
	authorTokens := make(map[int64]map[string]struct{})
	for _, row := range authorRows {
		book := books[row.BookID]
		if book == nil {
			continue
		}
		book.authorNames++
		tokens := authorTokens[row.BookID]
		if tokens == nil {
			tokens = make(map[string]struct{})
			authorTokens[row.BookID] = tokens
		}
		for _, token := range textmatch.AuthorTokens(row.Name) {
			tokens[token] = struct{}{}
		}
	}
	for id, tokens := range authorTokens {
		book := books[id]
		if book == nil || len(tokens) == 0 {
			continue
		}
		values := make([]string, 0, len(tokens))
		for token := range tokens {
			values = append(values, token)
		}
		sort.Strings(values)
		book.authorKey = strings.Join(values, "\x1f")
	}

	groups := make(map[string][]*bookInfo)
	for _, book := range books {
		groups[book.titleKey] = append(groups[book.titleKey], book)
	}

	result := &Result{
		Books:    make(map[int64][]Peer),
		Degraded: make(map[int64]struct{}),
	}
	result.Stats.Books = len(books)
	result.Stats.TitleGroups = len(groups)
	for _, group := range groups {
		if len(group) < 2 {
			continue
		}
		sort.Slice(group, func(i, j int) bool { return group[i].id < group[j].id })
		result.Stats.DuplicateGroups++
		signatures, err := loadSignatures(ctx, q, group)
		if err != nil {
			return nil, err
		}
		for i := 0; i < len(group); i++ {
			for j := i + 1; j < len(group); j++ {
				left, right := group[i], group[j]
				if !authorsCompatible(left, right) {
					result.Stats.SkippedAuthorPair++
					continue
				}
				leftSigs := signatures[left.id]
				rightSigs := signatures[right.id]
				if len(leftSigs) == 0 || len(rightSigs) == 0 {
					result.Stats.SkippedNoMinhash++
					continue
				}
				result.Stats.PairsCompared++
				score := maxSimilarity(leftSigs, rightSigs)
				if score >= lsh.MinSimilarityThreshold {
					continue
				}
				reason := PossibleDuplicateReason
				if score < mismatchSimilarity {
					reason = Reason
					result.Stats.Conflicts++
					degradeMismatch(result, left, right)
				} else {
					result.Stats.PossibleDuplicates++
				}
				addConflict(result, left.id, right.id, reason, score)
				addConflict(result, right.id, left.id, reason, score)
			}
		}
	}
	result.Stats.ConflictBooks = len(result.Books)
	result.Stats.DegradedBooks = len(result.Degraded)
	possibleBooks := make(map[int64]struct{})
	for _, pair := range result.Pairs {
		if pair.Reason == PossibleDuplicateReason {
			possibleBooks[pair.BookID] = struct{}{}
		}
	}
	result.Stats.PossibleDuplicateBooks = len(possibleBooks)
	sort.Slice(result.Pairs, func(i, j int) bool {
		if result.Pairs[i].BookID != result.Pairs[j].BookID {
			return result.Pairs[i].BookID < result.Pairs[j].BookID
		}
		return result.Pairs[i].PeerBookID < result.Pairs[j].PeerBookID
	})
	return result, nil
}

func loadSignatures(ctx context.Context, q *db.Queries, group []*bookInfo) (map[int64][][]uint64, error) {
	out := make(map[int64][][]uint64, len(group))
	for _, book := range group {
		files, err := q.ListBookFileByBookID(ctx, book.id)
		if err != nil {
			return nil, fmt.Errorf("list files of book %d: %w", book.id, err)
		}
		for _, file := range files {
			signature := decodeMinHash(file.Minhash)
			if len(signature) == 0 {
				continue
			}
			out[book.id] = append(out[book.id], signature)
		}
	}
	return out, nil
}

func authorsCompatible(a, b *bookInfo) bool {
	if a.authorNames == 0 && b.authorNames == 0 {
		return true
	}
	if a.authorNames == 0 || b.authorNames == 0 {
		return false
	}
	return a.authorKey == b.authorKey
}

func maxSimilarity(a, b [][]uint64) float64 {
	max := 0.0
	for _, left := range a {
		for _, right := range b {
			if similarity := tools.Jaccard(left, right); similarity > max {
				max = similarity
			}
		}
	}
	return max
}

// degradeMismatch marks the books whose final status must become needs_review.
// A book with independent content support keeps its matcher status; if both
// sides have support (or neither does), both are degraded.
func degradeMismatch(result *Result, left, right *bookInfo) {
	leftSupported := left.contentSupported()
	rightSupported := right.contentSupported()
	switch {
	case leftSupported && !rightSupported:
		result.Degraded[right.id] = struct{}{}
	case !leftSupported && rightSupported:
		result.Degraded[left.id] = struct{}{}
	default:
		result.Degraded[left.id] = struct{}{}
		result.Degraded[right.id] = struct{}{}
	}
}

func addConflict(result *Result, bookID, peerID int64, reason string, score float64) {
	result.Pairs = append(result.Pairs, Pair{
		BookID:     bookID,
		PeerBookID: peerID,
		Reason:     reason,
		Score:      score,
	})
	result.Books[bookID] = append(result.Books[bookID], Peer{
		PeerBookID: peerID,
		Reason:     reason,
		Score:      score,
	})
}

func decodeMinHash(blob []byte) []uint64 {
	if len(blob) == 0 || len(blob)%8 != 0 {
		return nil
	}
	signature := make([]uint64, len(blob)/8)
	for i := range signature {
		signature[i] = binary.LittleEndian.Uint64(blob[i*8:])
	}
	return signature
}
