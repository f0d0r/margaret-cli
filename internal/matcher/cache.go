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

// maxBlockingTokens bounds how many rare title tokens are used to generate
// candidate books for one OpenLibrary edition.
const maxBlockingTokens = 2

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
	Title        string
	TitleKey     string
	TitleTokens  []string
	Authors      []string
	AuthorTokens [][]string
	ISBNs        []ISBN
}

// Cache holds the in-memory indexes built from the scanned books.
type Cache struct {
	Books   map[int64]*Book
	ByISBN  map[string][]int64
	ByTitle map[string][]int64
	ByToken map[string][]int64

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
		Books:   make(map[int64]*Book, len(rows)),
		ByISBN:  make(map[string][]int64),
		ByTitle: make(map[string][]int64),
		ByToken: make(map[string][]int64),
	}
	order := make([]int64, 0, len(rows))
	for _, row := range rows {
		if include != nil {
			if _, ok := include[row.ID]; !ok {
				continue
			}
		}
		cache.Books[row.ID] = &Book{ID: row.ID, Title: row.Title}
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

	for _, id := range order {
		book := cache.Books[id]
		sort.SliceStable(book.ISBNs, func(i, j int) bool {
			if book.ISBNs[i].Priority != book.ISBNs[j].Priority {
				return book.ISBNs[i].Priority < book.ISBNs[j].Priority
			}
			return book.ISBNs[i].Value < book.ISBNs[j].Value
		})

		book.TitleKey = textmatch.TitleKey(book.Title)
		book.TitleTokens = textmatch.TitleTokens(book.Title)
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

	type tokenCount struct {
		token string
		count int
	}
	stats := make([]tokenCount, 0, len(tokens))
	for _, token := range tokens {
		count := len(c.ByToken[token])
		if count == 0 || count > maxPosting {
			continue
		}
		stats = append(stats, tokenCount{token: token, count: count})
	}
	if len(stats) == 0 {
		return nil
	}
	sort.Slice(stats, func(i, j int) bool {
		if stats[i].count != stats[j].count {
			return stats[i].count < stats[j].count
		}
		return stats[i].token < stats[j].token
	})
	if len(stats) > maxBlockingTokens {
		stats = stats[:maxBlockingTokens]
	}

	seen := make(map[int64]struct{})
	var candidates []int64
	for _, stat := range stats {
		for _, id := range c.ByToken[stat.token] {
			if _, ok := seen[id]; ok {
				continue
			}
			seen[id] = struct{}{}
			candidates = append(candidates, id)
		}
	}
	return candidates
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
