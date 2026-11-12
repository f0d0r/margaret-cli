package matcher

import (
	"context"
	"slices"
	"testing"

	"github.com/f0d0r/margaret-cli/internal/database"
	"github.com/f0d0r/margaret-cli/internal/db"
)

func newTestQueries(t *testing.T) *db.Queries {
	t.Helper()
	conn, err := database.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = conn.Close()
	})
	return db.New(conn)
}

func addBook(t *testing.T, queries *db.Queries, title string, authors []string, identifiers []db.CreateBookIdentifierParams) int64 {
	t.Helper()
	ctx := context.Background()
	bookID, err := queries.CreateBook(ctx, title)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range authors {
		if err := queries.CreateAuthor(ctx, name); err != nil {
			t.Fatal(err)
		}
		author, err := queries.GetAuthorByName(ctx, name)
		if err != nil {
			t.Fatal(err)
		}
		if err := queries.CreateBookAuthor(ctx, db.CreateBookAuthorParams{
			BookID:   bookID,
			AuthorID: author.ID,
		}); err != nil {
			t.Fatal(err)
		}
	}
	for _, identifier := range identifiers {
		identifier.BookID = bookID
		if err := queries.CreateBookIdentifier(ctx, identifier); err != nil {
			t.Fatal(err)
		}
	}
	return bookID
}

func TestBuildBasic(t *testing.T) {
	queries := newTestQueries(t)
	first := addBook(t, queries, "The Hobbit", []string{"J. R. R. Tolkien"}, []db.CreateBookIdentifierParams{
		{Scheme: "ISBN", Value: "9780306406157", Source: "METADATA_AND_TEXT"},
	})
	second := addBook(t, queries, "The Hobbit", []string{"Someone Else"}, []db.CreateBookIdentifierParams{
		{Scheme: "ISBN", Value: "9780375704024", Source: "TEXT"},
	})
	third := addBook(t, queries, "", nil, nil)

	cache, err := Build(context.Background(), queries, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if cache.Stats.Books != 3 {
		t.Fatalf("books = %d, want 3", cache.Stats.Books)
	}
	if cache.Stats.WithTitle != 2 {
		t.Fatalf("with title = %d, want 2", cache.Stats.WithTitle)
	}
	if cache.Stats.WithISBN != 2 {
		t.Fatalf("with ISBN = %d, want 2", cache.Stats.WithISBN)
	}
	if cache.Stats.WithAuthors != 2 {
		t.Fatalf("with authors = %d, want 2", cache.Stats.WithAuthors)
	}
	if cache.Stats.TitleKeys != 1 {
		t.Fatalf("title keys = %d, want 1", cache.Stats.TitleKeys)
	}
	if cache.Stats.Tokens != 2 {
		t.Fatalf("tokens = %d, want 2", cache.Stats.Tokens)
	}
	if cache.Stats.Postings != 4 {
		t.Fatalf("postings = %d, want 4", cache.Stats.Postings)
	}

	if got := cache.ByTitle["the hobbit"]; !slices.Equal(got, []int64{first, second}) {
		t.Fatalf("by title = %v, want [%d %d]", got, first, second)
	}
	if got := cache.ByISBN["9780306406157"]; !slices.Equal(got, []int64{first}) {
		t.Fatalf("by ISBN = %v, want [%d]", got, first)
	}
	if got := cache.ByToken["hobbit"]; !slices.Equal(got, []int64{first, second}) {
		t.Fatalf("by token = %v, want [%d %d]", got, first, second)
	}
	if cache.Books[third] == nil {
		t.Fatal("book without title or ISBN is missing from the cache")
	}
	if len(cache.Books[third].TitleTokens) != 0 || len(cache.Books[third].ISBNs) != 0 {
		t.Fatalf("book without title or ISBN has indexes: %+v", cache.Books[third])
	}
	if got := cache.Books[first].ISBNs[0]; got.Priority != 0 || got.Source != "METADATA_AND_TEXT" {
		t.Fatalf("first ISBN = %+v, want strongest priority", got)
	}
	if got := cache.Books[second].ISBNs[0]; got.Priority != 2 || got.Source != "TEXT" {
		t.Fatalf("second ISBN = %+v, want text priority", got)
	}
}

func TestBuildISBNPriorityAndDeduplication(t *testing.T) {
	queries := newTestQueries(t)
	bookID := addBook(t, queries, "Book", nil, []db.CreateBookIdentifierParams{
		{Scheme: "ISBN-10", Value: "0-306-40615-2", Source: "METADATA"},
		{Scheme: "ISBN-13", Value: "9780306406157", Source: "METADATA_AND_TEXT"},
		{Scheme: "ISBN", Value: "9780375704024", Source: "TEXT"},
		{Scheme: "ISBN", Value: "not-an-isbn", Source: "TEXT"},
	})

	cache, err := Build(context.Background(), queries, Options{})
	if err != nil {
		t.Fatal(err)
	}
	isbns := cache.Books[bookID].ISBNs
	if len(isbns) != 2 {
		t.Fatalf("ISBNs = %+v, want 2", isbns)
	}
	if isbns[0].Value != "9780306406157" || isbns[0].Priority != 0 {
		t.Fatalf("first ISBN = %+v, want the merged strongest ISBN", isbns[0])
	}
	if isbns[1].Value != "9780375704024" || isbns[1].Priority != 2 {
		t.Fatalf("second ISBN = %+v, want the text ISBN", isbns[1])
	}
}

func TestBuildBookIDsFilter(t *testing.T) {
	queries := newTestQueries(t)
	first := addBook(t, queries, "First", nil, []db.CreateBookIdentifierParams{
		{Scheme: "ISBN", Value: "9780306406157", Source: "METADATA"},
	})
	second := addBook(t, queries, "Second", nil, []db.CreateBookIdentifierParams{
		{Scheme: "ISBN", Value: "9780375704024", Source: "METADATA"},
	})

	cache, err := Build(context.Background(), queries, Options{BookIDs: []int64{second}})
	if err != nil {
		t.Fatal(err)
	}
	if len(cache.Books) != 1 || cache.Books[second] == nil {
		t.Fatalf("books = %v, want only %d", cache.Books, second)
	}
	if cache.ByTitle["first"] != nil {
		t.Fatal("filtered book leaked into the title index")
	}
	if got := cache.ByTitle["second"]; !slices.Equal(got, []int64{second}) {
		t.Fatalf("by title = %v, want [%d]", got, second)
	}
	if cache.ByISBN["9780306406157"] != nil {
		t.Fatal("filtered ISBN leaked into the index")
	}
	if cache.ByISBN["9780375704024"] == nil {
		t.Fatal("selected ISBN is missing from the index")
	}
	_ = first
}

func TestBuildEmpty(t *testing.T) {
	queries := newTestQueries(t)
	cache, err := Build(context.Background(), queries, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if cache.Stats.Books != 0 || len(cache.Books) != 0 {
		t.Fatalf("cache = %+v, want empty", cache)
	}
	if cache.ByISBN == nil || cache.ByTitle == nil || cache.ByToken == nil {
		t.Fatal("indexes must be initialized")
	}
}

func TestRareTokenCandidates(t *testing.T) {
	queries := newTestQueries(t)
	first := addBook(t, queries, "Alpha Beta", nil, nil)
	second := addBook(t, queries, "Alpha Gamma", nil, nil)
	addBook(t, queries, "Delta Epsilon", nil, nil)

	cache, err := Build(context.Background(), queries, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if got := cache.RareTokenCandidates([]string{"alpha", "beta"}, 10); !slices.Equal(got, []int64{first, second}) {
		t.Fatalf("candidates = %v, want [%d %d]", got, first, second)
	}
	if got := cache.RareTokenCandidates([]string{"alpha", "beta"}, 1); !slices.Equal(got, []int64{first}) {
		t.Fatalf("candidates = %v, want [%d]", got, first)
	}
	if got := cache.RareTokenCandidates([]string{"missing"}, 10); got != nil {
		t.Fatalf("candidates = %v, want nil", got)
	}
	if got := cache.RareTokenCandidates(nil, 10); got != nil {
		t.Fatalf("candidates = %v, want nil", got)
	}
	if got := cache.RareTokenCandidates([]string{"alpha"}, 0); got != nil {
		t.Fatalf("candidates = %v, want nil for maxPosting 0", got)
	}
}

func TestBuildAuthorTokens(t *testing.T) {
	queries := newTestQueries(t)
	bookID := addBook(t, queries, "Book", []string{"J. K. Rowling", "George R. R. Martin"}, nil)

	cache, err := Build(context.Background(), queries, Options{})
	if err != nil {
		t.Fatal(err)
	}
	book := cache.Books[bookID]
	if len(book.AuthorTokens) != 2 {
		t.Fatalf("author token sets = %v, want 2", book.AuthorTokens)
	}
	if !slices.Equal(book.AuthorTokens[0], []string{"george", "martin"}) {
		t.Fatalf("first author tokens = %v", book.AuthorTokens[0])
	}
	if !slices.Equal(book.AuthorTokens[1], []string{"rowling"}) {
		t.Fatalf("second author tokens = %v", book.AuthorTokens[1])
	}
}
