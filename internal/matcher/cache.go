// Package matcher builds the in-memory lookup structures used to match
// scanned books against OpenLibrary records.
package matcher

import (
	"context"
	"fmt"
	"sort"

	"github.com/f0d0r/margaret-cli/internal/db"
	"github.com/f0d0r/margaret-cli/internal/identifier"
	"github.com/f0d0r/margaret-cli/internal/textmatch"
	"github.com/f0d0r/margaret-ebook-library/tools"
)

// Options configures Build.
type Options struct {
	// BookIDs limits the cache to the given books. A nil slice includes all
	// books.
	BookIDs []int64
	// Pending limits the cache to books without a match, or with a match
	// produced by a different matcher or dump version.
	Pending        bool
	MatcherVersion string
	DumpVersion    string
	// Content loads the per-file content head fingerprints used to confirm
	// matches against the scanned text.
	Content bool
}

// ISBN is one normalized ISBN of a scanned book with its provenance.
type ISBN struct {
	Value    string
	Source   identifier.Source
	Priority int
}

// Book is the matching view of one consolidated scanned book.
type Book struct {
	ID           int64
	Index        int
	Title        string
	TitleKey     string
	TitleTokens  []string
	TitleHashes  []uint64
	Authors      []string
	AuthorTokens [][]string
	ISBNs        []ISBN
	HeadBlooms   [][]byte
	// OrigTitleHit and OrigAuthorHit show how strongly the scanned metadata
	// itself appears in the leading text. They are filled by the content pass.
	OrigTitleHit  float64
	OrigAuthorHit float64
}

// Cache holds the in-memory indexes built from the scanned books.
type Cache struct {
	Books   map[int64]*Book
	ByISBN  map[string][]int64
	ByTitle map[string][]int64
	ByToken map[string][]int64
	// ByTokenHash indexes books by hashed title tokens; the editions pass uses
	// it to avoid string comparisons in the fuzzy candidate loop.
	ByTokenHash map[uint64][]int64

	Stats Stats
}

// Stats summarizes the loaded cache.
type Stats struct {
	Books       int
	WithTitle   int
	WithISBN    int
	WithAuthors int
	TitleKeys   int
	Tokens      int
	Postings    int
}

// Build loads the scanned books and builds the matching indexes.
func Build(ctx context.Context, q *db.Queries, opts Options) (*Cache, error) {
	type bookRow struct {
		ID    int64
		Title string
	}
	var rows []bookRow
	if opts.Pending && opts.BookIDs == nil {
		pending, err := q.ListBooksNeedingMatching(ctx, db.ListBooksNeedingMatchingParams{
			MatcherVersion: opts.MatcherVersion,
			DumpVersion:    opts.DumpVersion,
		})
		if err != nil {
			return nil, fmt.Errorf("list books needing matching: %w", err)
		}
		rows = make([]bookRow, len(pending))
		for i, row := range pending {
			rows[i] = bookRow{ID: row.ID, Title: row.Title}
		}
	} else {
		all, err := q.ListBooksForMatching(ctx)
		if err != nil {
			return nil, fmt.Errorf("list books for matching: %w", err)
		}
		rows = make([]bookRow, len(all))
		for i, row := range all {
			rows[i] = bookRow{ID: row.ID, Title: row.Title}
		}
	}

	var include map[int64]struct{}
	if opts.BookIDs != nil {
		include = make(map[int64]struct{}, len(opts.BookIDs))
		for _, id := range opts.BookIDs {
			include[id] = struct{}{}
		}
	}

	cache := &Cache{
		Books:       make(map[int64]*Book, len(rows)),
		ByISBN:      make(map[string][]int64),
		ByTitle:     make(map[string][]int64),
		ByToken:     make(map[string][]int64),
		ByTokenHash: make(map[uint64][]int64),
	}
	order := make([]int64, 0, len(rows))
	for _, row := range rows {
		if include != nil {
			if _, ok := include[row.ID]; !ok {
				continue
			}
		}
		cache.Books[row.ID] = &Book{ID: row.ID, Index: len(order), Title: row.Title}
		order = append(order, row.ID)
	}
	if len(order) == 0 {
		return cache, nil
	}

	authors, err := q.ListBookAuthorsForMatching(ctx)
	if err != nil {
		return nil, fmt.Errorf("list book authors for matching: %w", err)
	}
	for _, row := range authors {
		book := cache.Books[row.BookID]
		if book == nil {
			continue
		}
		book.Authors = append(book.Authors, row.Name)
	}

	identifiers, err := q.ListBookIdentifiersForMatching(ctx)
	if err != nil {
		return nil, fmt.Errorf("list book identifiers for matching: %w", err)
	}
	for _, row := range identifiers {
		book := cache.Books[row.BookID]
		if book == nil || !identifier.IsISBNScheme(row.Scheme) {
			continue
		}
		isbn, ok := tools.ValidateBookISBN(row.Value)
		if !ok {
			continue
		}
		isbn13, ok := isbn.ToISBN13()
		if !ok {
			continue
		}
		source := identifier.Source(row.Source)
		priority := isbnPriority(source)
		value := isbn13.String()

		updated := false
		for i := range book.ISBNs {
			if book.ISBNs[i].Value != value {
				continue
			}
			if priority < book.ISBNs[i].Priority {
				book.ISBNs[i].Source = source
				book.ISBNs[i].Priority = priority
			}
			updated = true
			break
		}
		if !updated {
			book.ISBNs = append(book.ISBNs, ISBN{Value: value, Source: source, Priority: priority})
		}
	}

	if opts.Content {
		blooms, err := q.ListBookFileHeadBlooms(ctx)
		if err != nil {
			return nil, fmt.Errorf("list content heads: %w", err)
		}
		for _, row := range blooms {
			if book := cache.Books[row.BookID]; book != nil {
				book.HeadBlooms = append(book.HeadBlooms, row.HeadBloom)
			}
		}
	}

	for _, id := range order {
		book := cache.Books[id]
		sort.SliceStable(book.ISBNs, func(i, j int) bool {
			if book.ISBNs[i].Priority != book.ISBNs[j].Priority {
				return book.ISBNs[i].Priority < book.ISBNs[j].Priority
			}
			return book.ISBNs[i].Value < book.ISBNs[j].Value
		})

		book.TitleKey = textmatch.TitleKey(book.Title)
		book.TitleTokens = textmatch.TokensFromKey(book.TitleKey)
		book.TitleHashes = textmatch.HashTokens(book.TitleTokens)
		for _, author := range book.Authors {
			tokens := textmatch.AuthorTokens(author)
			if len(tokens) == 0 {
				continue
			}
			book.AuthorTokens = append(book.AuthorTokens, tokens)
		}

		if book.TitleKey != "" {
			cache.ByTitle[book.TitleKey] = append(cache.ByTitle[book.TitleKey], id)
			cache.Stats.WithTitle++
		}
		for _, token := range book.TitleTokens {
			cache.ByToken[token] = append(cache.ByToken[token], id)
		}
		for _, hash := range book.TitleHashes {
			cache.ByTokenHash[hash] = append(cache.ByTokenHash[hash], id)
		}
		for _, isbn := range book.ISBNs {
			cache.ByISBN[isbn.Value] = append(cache.ByISBN[isbn.Value], id)
		}
		if len(book.ISBNs) > 0 {
			cache.Stats.WithISBN++
		}
		if len(book.Authors) > 0 {
			cache.Stats.WithAuthors++
		}
	}

	cache.Stats.Books = len(cache.Books)
	cache.Stats.TitleKeys = len(cache.ByTitle)
	cache.Stats.Tokens = len(cache.ByToken)
	for _, ids := range cache.ByToken {
		cache.Stats.Postings += len(ids)
	}
	return cache, nil
}

// RareTokenCandidates returns the union of books sharing the rarest title
// tokens, considering only tokens whose postings list is at most maxPosting
// long. It is used to generate a small candidate set for an OpenLibrary
// edition without comparing it to every book.
func (c *Cache) RareTokenCandidates(tokens []string, maxPosting int) []int64 {
	if len(tokens) == 0 || maxPosting <= 0 {
		return nil
	}

	first, second := rarestTokens(tokens, func(token string) int {
		return len(c.ByToken[token])
	}, maxPosting)
	if first < 0 {
		return nil
	}
	postings := c.ByToken
	if second >= 0 {
		return unionPostings(postings[tokens[first]], postings[tokens[second]])
	}
	return unionPostings(postings[tokens[first]])
}

func (c *Cache) RareTokenCandidatesByHash(tokens []uint64, maxPosting int) []int64 {
	if len(tokens) == 0 || maxPosting <= 0 {
		return nil
	}

	first, second := rarestTokens(tokens, func(token uint64) int {
		return len(c.ByTokenHash[token])
	}, maxPosting)
	if first < 0 {
		return nil
	}
	if second >= 0 {
		return unionPostings(c.ByTokenHash[tokens[first]], c.ByTokenHash[tokens[second]])
	}
	return unionPostings(c.ByTokenHash[tokens[first]])
}

// rarestTokens returns the indexes of the two rarest eligible tokens in scan
// order. A token is eligible when its postings count is in (0, maxPosting].
// Order matches sorting by (count, token): the first index is the smallest,
// the second the next smallest. A -1 index means no token qualified.
func rarestTokens[T comparable](tokens []T, count func(T) int, maxPosting int) (int, int) {
	first, second := -1, -1
	firstCount, secondCount := 0, 0
	for i, token := range tokens {
		n := count(token)
		if n == 0 || n > maxPosting {
			continue
		}
		if first < 0 || n < firstCount || (n == firstCount && tokenLess(token, tokens[first])) {
			second, secondCount = first, firstCount
			first, firstCount = i, n
			continue
		}
		if second < 0 || n < secondCount || (n == secondCount && tokenLess(token, tokens[second])) {
			second, secondCount = i, n
		}
	}
	return first, second
}

// tokenLess orders tokens the way the former sort.Slice comparators did.
func tokenLess[T comparable](a, b T) bool {
	switch av := any(a).(type) {
	case string:
		bv, _ := any(b).(string)
		return av < bv
	case uint64:
		bv, _ := any(b).(uint64)
		return av < bv
	default:
		return false
	}
}

// unionPostings merges one or two postings lists in order, dropping
// duplicates. It returns nil when every list is empty.
func unionPostings(lists ...[]int64) []int64 {
	total := 0
	for _, list := range lists {
		total += len(list)
	}
	if total == 0 {
		return nil
	}
	seen := make(map[int64]struct{}, total)
	out := make([]int64, 0, total)
	for _, list := range lists {
		for _, id := range list {
			if _, ok := seen[id]; ok {
				continue
			}
			seen[id] = struct{}{}
			out = append(out, id)
		}
	}
	return out
}

func isbnPriority(source identifier.Source) int {
	switch source {
	case identifier.SourceMetadataAndText:
		return 0
	case identifier.SourceMetadata:
		return 1
	case identifier.SourceText:
		return 2
	default:
		return 3
	}
}
