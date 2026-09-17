package search

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/f0d0r/margaret-cli/internal/database"
	"github.com/f0d0r/margaret-cli/internal/db"
)

type searchFileJSON struct {
	Path    string   `json:"path"`
	Authors []string `json:"authors"`
	Title   string   `json:"title"`
}

type searchBookJSON struct {
	Authors []string         `json:"authors"`
	Title   string           `json:"title"`
	Files   []searchFileJSON `json:"files"`
}

func setupSearchDB(t *testing.T) *db.Queries {
	t.Helper()
	conn, err := database.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	q := db.New(conn)
	createTestBook(t, q, "Moby Dick", "Herman Melville", "deadbeef", "books/mobydick.epub")
	return q
}

func createTestBook(t *testing.T, q *db.Queries, title, authorName, hash, path string) int64 {
	t.Helper()
	ctx := context.Background()

	bookID, err := q.CreateBook(ctx, title)
	if err != nil {
		t.Fatal(err)
	}
	fileID, err := q.CreateBookFile(ctx, db.CreateBookFileParams{
		Hash:  hash,
		Path:  path,
		Title: title,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := q.CreateBookBookFile(ctx, db.CreateBookBookFileParams{
		BookID:     bookID,
		BookFileID: fileID,
	}); err != nil {
		t.Fatal(err)
	}
	if authorName != "" {
		if err := q.CreateAuthor(ctx, authorName); err != nil {
			t.Fatal(err)
		}
		author, err := q.GetAuthorByName(ctx, authorName)
		if err != nil {
			t.Fatal(err)
		}
		if err := q.CreateBookFileAuthor(ctx, db.CreateBookFileAuthorParams{
			BookFileID: fileID,
			AuthorID:   author.ID,
		}); err != nil {
			t.Fatal(err)
		}
		if err := q.CreateBookAuthor(ctx, db.CreateBookAuthorParams{
			BookID:   bookID,
			AuthorID: author.ID,
		}); err != nil {
			t.Fatal(err)
		}
	}
	return bookID
}

func captureStdout(t *testing.T, f func()) string {
	t.Helper()
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w

	outCh := make(chan string, 1)
	go func() {
		data, _ := io.ReadAll(r)
		outCh <- string(data)
	}()

	f()

	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	os.Stdout = old
	return <-outCh
}

func TestSearchJSONWritesBooksJSONShape(t *testing.T) {
	q := setupSearchDB(t)
	ctx := context.Background()

	out := filepath.Join(t.TempDir(), "search.json")
	if err := Search(ctx, q, false, false, 0, true, out, "Moby"); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	var report []searchBookJSON
	if err := json.Unmarshal(data, &report); err != nil {
		t.Fatal(err)
	}
	if len(report) != 1 {
		t.Fatalf("expected 1 book, got %d", len(report))
	}
	b := report[0]
	if b.Title != "Moby Dick" {
		t.Errorf("expected title %q, got %q", "Moby Dick", b.Title)
	}
	if !reflect.DeepEqual(b.Authors, []string{"Herman Melville"}) {
		t.Errorf("expected authors [Herman Melville], got %q", b.Authors)
	}
	if len(b.Files) != 1 || b.Files[0].Path != "books/mobydick.epub" {
		t.Errorf("expected mobydick file entry, got %+v", b.Files)
	}
}

func TestSearchExclusiveAuthorAndTitle(t *testing.T) {
	conn, err := database.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	q := db.New(conn)
	ctx := context.Background()

	createTestBook(t, q, "The Shining", "Stephen King", "h1", "books/shining.epub")
	createTestBook(t, q, "King Solomon", "Rider Haggard", "h2", "books/solomon.epub")

	// 1. Author only search: "King" matches Stephen King, not King Solomon
	authorOut := filepath.Join(t.TempDir(), "author.json")
	if err := Search(ctx, q, true, false, 0, true, authorOut, "King"); err != nil {
		t.Fatal(err)
	}
	var authorHits []searchBookJSON
	authorData, err := os.ReadFile(authorOut)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(authorData, &authorHits); err != nil {
		t.Fatal(err)
	}
	if len(authorHits) != 1 || authorHits[0].Title != "The Shining" {
		t.Fatalf("author-only search: want only [The Shining], got %+v", authorHits)
	}

	// 2. Title only search: "King" matches King Solomon, not Stephen King
	titleOut := filepath.Join(t.TempDir(), "title.json")
	if err := Search(ctx, q, false, true, 0, true, titleOut, "King"); err != nil {
		t.Fatal(err)
	}
	var titleHits []searchBookJSON
	titleData, err := os.ReadFile(titleOut)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(titleData, &titleHits); err != nil {
		t.Fatal(err)
	}
	if len(titleHits) != 1 || titleHits[0].Title != "King Solomon" {
		t.Fatalf("title-only search: want only [King Solomon], got %+v", titleHits)
	}

	// 3. Both (default): matches both books
	bothOut := filepath.Join(t.TempDir(), "both.json")
	if err := Search(ctx, q, false, false, 0, true, bothOut, "King"); err != nil {
		t.Fatal(err)
	}
	var bothHits []searchBookJSON
	bothData, err := os.ReadFile(bothOut)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(bothData, &bothHits); err != nil {
		t.Fatal(err)
	}
	if len(bothHits) != 2 {
		t.Fatalf("default search: want 2 books, got %d", len(bothHits))
	}
}

func TestSearchConsoleFormattingDuplicatesAndAuthorless(t *testing.T) {
	conn, err := database.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	q := db.New(conn)
	ctx := context.Background()

	// Authorless book
	createTestBook(t, q, "Lonely Wanderer", "", "h_lonely", "books/lonely.epub")

	// Book with duplicate file
	createTestBook(t, q, "Dune", "Frank Herbert", "h_dune", "books/dune1.epub")
	if err := q.CreateBookFileDuplicate(ctx, db.CreateBookFileDuplicateParams{
		Hash: "h_dune",
		Path: "books/dune2.epub",
	}); err != nil {
		t.Fatal(err)
	}

	// 1. Authorless book output on screen must NOT have a leading colon: ": Lonely Wanderer"
	outLonely := captureStdout(t, func() {
		if err := Search(ctx, q, false, false, 0, false, "", "Lonely"); err != nil {
			t.Fatal(err)
		}
	})
	if strings.Contains(outLonely, ": Lonely Wanderer") {
		t.Fatalf("expected no leading colon for authorless book, got %q", outLonely)
	}
	if !strings.Contains(outLonely, "Lonely Wanderer\n  - books/lonely.epub") {
		t.Fatalf("expected title without author and file line, got %q", outLonely)
	}

	// 2. Book with duplicate must list BOTH file paths on screen
	outDune := captureStdout(t, func() {
		if err := Search(ctx, q, false, false, 0, false, "", "Dune"); err != nil {
			t.Fatal(err)
		}
	})
	if !strings.Contains(outDune, "Frank Herbert: Dune") {
		t.Fatalf("expected header 'Frank Herbert: Dune', got %q", outDune)
	}
	if !strings.Contains(outDune, "- books/dune1.epub") || !strings.Contains(outDune, "- books/dune2.epub") {
		t.Fatalf("expected both canonical and duplicate files in screen output, got %q", outDune)
	}
}

func TestSearchJSONNoHitsWritesNothing(t *testing.T) {
	q := setupSearchDB(t)
	ctx := context.Background()

	out := filepath.Join(t.TempDir(), "search.json")
	if err := Search(ctx, q, false, false, 0, true, out, "zzzqqqnonexistent"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(out); !os.IsNotExist(err) {
		t.Fatalf("expected no file to be written, stat err = %v", err)
	}
}

func TestSearchScreenDoesNotFail(t *testing.T) {
	q := setupSearchDB(t)
	if err := Search(context.Background(), q, true, true, 0, false, "", "Moby"); err != nil {
		t.Fatal(err)
	}
}

func TestSanitizeFTSQuery(t *testing.T) {
	for _, tc := range []struct {
		name  string
		raw   string
		want  string
		isErr bool
	}{
		{"single word", "Moby", `Moby`, false},
		{"multi word is AND", "Harry Potter", `Harry Potter`, false},
		{"extra whitespace collapsed", "  Harry   Potter  ", `Harry Potter`, false},
		{"double quotes dropped", `say "hi"`, `say hi`, false},
		{"parens dropped", "(Dune)", `Dune`, false},
		{"or dropped", "King OR Queen", `King Queen`, false},
		{"and not dropped", "War AND NOT Peace", `War Peace`, false},
		{"star dropped", "Dune*", `Dune`, false},
		{"column filter dropped", "title:Dune", `title Dune`, false},
		{"apostrophe splits", "O'Brien", `O Brien`, false},
		{"digits kept", "1984", `1984`, false},
		{"diacritics kept", "Márai Sándor", `Márai Sándor`, false},
		{"only operators", "OR", "", true},
		{"empty", "", "", true},
		{"whitespace only", "   ", "", true},
		{"only punctuation", "((()))", "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := sanitizeFTSQuery(tc.raw)
			if tc.isErr {
				if err == nil {
					t.Fatalf("sanitizeFTSQuery(%q) = %q, want error", tc.raw, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("sanitizeFTSQuery(%q) error: %v", tc.raw, err)
			}
			if got != tc.want {
				t.Errorf("sanitizeFTSQuery(%q) = %q, want %q", tc.raw, got, tc.want)
			}
		})
	}
}

func TestSearchSpecialCharsDoNotFail(t *testing.T) {
	q := setupSearchDB(t)
	ctx := context.Background()

	// Previously any of these broke the FTS5 MATCH expression with
	// "SQL logic error". They must now run and find Moby Dick by title.
	for _, raw := range []string{`Moby "Dick"`, "(Moby)", "Moby OR Dick", "Moby*"} {
		books, err := q.SearchBooksByTitleFTS(ctx, mustSanitize(t, raw), 0)
		if err != nil {
			t.Fatalf("SearchBooksByTitleFTS(%q): %v", raw, err)
		}
		if len(books) != 1 || books[0].Title != "Moby Dick" {
			t.Fatalf("SearchBooksByTitleFTS(%q) = %+v, want [Moby Dick]", raw, books)
		}
	}

	// Sanitized tokens that don't all occur ("title" is not in the book
	// title) must still run without a MATCH syntax error.
	if _, err := q.SearchBooksByTitleFTS(ctx, mustSanitize(t, "title:Moby"), 0); err != nil {
		t.Fatalf("SearchBooksByTitleFTS(%q): %v", "title:Moby", err)
	}

	// End to end through Search: input with syntax characters.
	out := filepath.Join(t.TempDir(), "search.json")
	if err := Search(ctx, q, false, false, 0, true, out, `Moby ("Dick")`); err != nil {
		t.Fatalf("Search with special chars: %v", err)
	}
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	var report []searchBookJSON
	if err := json.Unmarshal(data, &report); err != nil {
		t.Fatal(err)
	}
	if len(report) != 1 {
		t.Fatalf("expected 1 book, got %d", len(report))
	}
}

func mustSanitize(t *testing.T, raw string) string {
	t.Helper()
	q, err := sanitizeFTSQuery(raw)
	if err != nil {
		t.Fatal(err)
	}
	return q
}

func TestSearchEmptyQueryFails(t *testing.T) {
	q := setupSearchDB(t)
	if err := Search(context.Background(), q, false, false, 0, false, "", "   "); err == nil {
		t.Fatal("expected error for whitespace-only query, got nil")
	}
}
