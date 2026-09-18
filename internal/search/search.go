package search

import (
	"context"
	"fmt"
	"strings"
	"unicode"

	"github.com/f0d0r/margaret-cli/internal/db"
	"github.com/f0d0r/margaret-cli/internal/report"
)

func Search(ctx context.Context, q *db.Queries, author bool, title bool, limit int, json bool, jsonOut string, searchQuery string) error {
	query, err := sanitizeFTSQuery(searchQuery)
	if err != nil {
		return err
	}

	var books []db.Book
	if author && title {
		books, err = q.SearchBooksByAuthorOrTitleFTS(ctx, query, limit)
	} else if author {
		books, err = q.SearchBooksByAuthorFTS(ctx, query, limit)
	} else if title {
		books, err = q.SearchBooksByTitleFTS(ctx, query, limit)
	} else {
		books, err = q.SearchBooksByAuthorOrTitleFTS(ctx, query, limit)
	}

	if err != nil {
		return err
	}

	if len(books) == 0 {
		return nil
	}

	if json {
		return printBooksJSON(ctx, books, q, jsonOut)
	}

	return printOnScreen(ctx, books, q)
}

// sanitizeFTSQuery turns raw user input into a safe FTS5 MATCH expression.
// Only letter/number runs are kept (everything else becomes a separator),
// so FTS5 syntax characters in the input ('"', '*', parentheses, ':', ...)
// can neither break the query nor silently change its meaning. Tokens are
// joined with spaces, i.e. implicit AND, which preserves the semantics of
// plain multi-word searches. Bare AND/OR/NOT tokens are dropped: unquoted
// they would act as boolean operators (or fail the query outright), and
// quoted they would require a literal "or"/"and"/"not" trigram in the text.
// It returns an error when nothing searchable remains.
func sanitizeFTSQuery(raw string) (string, error) {
	words := strings.Fields(strings.Map(func(r rune) rune {
		if unicode.IsLetter(r) || unicode.IsNumber(r) {
			return r
		}
		return ' '
	}, raw))
	kept := words[:0]
	for _, w := range words {
		switch strings.ToUpper(w) {
		case "AND", "OR", "NOT":
			continue
		}
		kept = append(kept, w)
	}
	if len(kept) == 0 {
		return "", fmt.Errorf("empty search query")
	}
	return strings.Join(kept, " "), nil
}

// printBooksJSON writes the FTS hits in the books.json shape (same pipeline
// as report.WriteBooks, restricted to the hit IDs in relevance order) to
// path. With no hits nothing is written.
func printBooksJSON(ctx context.Context, books []db.Book, q *db.Queries, path string) error {
	if len(books) == 0 {
		return nil
	}
	ids := make([]int64, 0, len(books))
	for _, b := range books {
		ids = append(ids, b.ID)
	}
	return report.WriteBooksFiltered(ctx, q, ids, path)
}

func printOnScreen(ctx context.Context, books []db.Book, q *db.Queries) error {
	if len(books) == 0 {
		return nil
	}
	ids := make([]int64, 0, len(books))
	for _, b := range books {
		ids = append(ids, b.ID)
	}
	bookReports, err := report.GetBooksFiltered(ctx, q, ids)
	if err != nil {
		return err
	}
	for _, b := range bookReports {
		if len(b.Authors) == 0 {
			fmt.Println(b.Title)
		} else {
			fmt.Printf("%s: %s\n", strings.Join(b.Authors, ", "), b.Title)
		}
		for _, file := range b.Files {
			fmt.Printf("  - %s\n", file.Path)
		}
		fmt.Println()
	}
	return nil
}
