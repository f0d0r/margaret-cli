package matcher

import (
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/f0d0r/margaret-cli/internal/db"
	"github.com/f0d0r/margaret-cli/internal/openlibrary"
)

func writeStreamDump(t *testing.T, dir, name, body string, kind openlibrary.Kind) *openlibrary.File {
	t.Helper()
	path := filepath.Join(dir, name)
	compressed := strings.HasSuffix(name, ".gz")
	if compressed {
		file, err := os.Create(path)
		if err != nil {
			t.Fatal(err)
		}
		writer := gzip.NewWriter(file)
		if _, err := writer.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
		if err := writer.Close(); err != nil {
			t.Fatal(err)
		}
		if err := file.Close(); err != nil {
			t.Fatal(err)
		}
	} else if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return &openlibrary.File{
		Kind:       kind,
		Path:       path,
		Version:    "2024-01-31",
		Compressed: compressed,
	}
}

func dumpLine(recordType, key, data string) string {
	return fmt.Sprintf("%s\t%s\t1\t2024-01-31\t%s\n", recordType, key, data)
}

func keys(values []string) []map[string]string {
	out := make([]map[string]string, len(values))
	for i, value := range values {
		out[i] = map[string]string{"key": value}
	}
	return out
}

func editionLine(key, title string, isbn13, authorKeys, workKeys []string) string {
	record := map[string]any{"key": key, "title": title}
	if len(isbn13) > 0 {
		record["isbn_13"] = isbn13
	}
	if len(authorKeys) > 0 {
		record["authors"] = keys(authorKeys)
	}
	if len(workKeys) > 0 {
		record["works"] = keys(workKeys)
	}
	data, err := json.Marshal(record)
	if err != nil {
		panic(err)
	}
	return dumpLine("/type/edition", key, string(data))
}

func authorLine(key, name string) string {
	data, err := json.Marshal(map[string]any{"key": key, "name": name})
	if err != nil {
		panic(err)
	}
	return dumpLine("/type/author", key, string(data))
}

func workLine(key, title, description string, subjects []string) string {
	data, err := json.Marshal(map[string]any{
		"key":                key,
		"title":              title,
		"description":        map[string]string{"value": description},
		"subjects":           subjects,
		"first_publish_date": "1937",
	})
	if err != nil {
		panic(err)
	}
	return dumpLine("/type/work", key, string(data))
}

func streamCache(t *testing.T) (*Cache, int64, int64, int64) {
	t.Helper()
	queries := newTestQueries(t)
	first := addBook(t, queries, "The Hobbit", []string{"J. R. R. Tolkien"}, []db.CreateBookIdentifierParams{
		{Scheme: "ISBN", Value: "9780306406157", Source: "METADATA_AND_TEXT"},
	})
	second := addBook(t, queries, "The Hobbit", []string{"Someone Else"}, []db.CreateBookIdentifierParams{
		{Scheme: "ISBN", Value: "9780375704024", Source: "TEXT"},
	})
	third := addBook(t, queries, "A Title Without ISBN", []string{"Jane Doe"}, nil)
	cache, err := Build(context.Background(), queries, Options{})
	if err != nil {
		t.Fatal(err)
	}
	return cache, first, second, third
}

func TestStreamDataset(t *testing.T) {
	cache, first, second, third := streamCache(t)
	dir := t.TempDir()
	editions := writeStreamDump(t, dir, "editions.txt.gz", strings.Join([]string{
		editionLine("/books/OL1M", "The Hobbit", []string{"9780306406157"}, []string{"/authors/OL1A"}, []string{"/works/OL1W"}),
		editionLine("/books/OL2M", "The Hobbit", []string{"9780375704024"}, []string{"/authors/OL2A"}, []string{"/works/OL2W"}),
		editionLine("/books/OL3M", "A Title Without ISBN", nil, []string{"/authors/OL3A"}, []string{"/works/OL3W"}),
	}, ""), openlibrary.KindEditions)
	authors := writeStreamDump(t, dir, "authors.txt.gz", strings.Join([]string{
		authorLine("/authors/OL1A", "J. R. R. Tolkien"),
		authorLine("/authors/OL2A", "Someone Else"),
		authorLine("/authors/OL3A", "Jane Doe"),
	}, ""), openlibrary.KindAuthors)
	works := writeStreamDump(t, dir, "works.txt.gz", strings.Join([]string{
		workLine("/works/OL1W", "The Hobbit", "A hobbit goes there and back again.", []string{"Fantasy"}),
		workLine("/works/OL2W", "The Hobbit", "Another edition.", []string{"Fantasy"}),
		workLine("/works/OL3W", "A Title Without ISBN", "A third work.", []string{"Fiction"}),
	}, ""), openlibrary.KindWorks)

	result, err := StreamDataset(context.Background(), &openlibrary.Dataset{
		Dir:      dir,
		Editions: editions,
		Authors:  authors,
		Works:    works,
	}, cache, StreamOptions{})
	if err != nil {
		t.Fatal(err)
	}

	if result.Stats.Editions != 3 || result.Stats.Authors != 3 || result.Stats.Works != 3 {
		t.Fatalf("stats = %+v", result.Stats)
	}
	if result.Stats.ISBNMatches != 2 || result.Stats.TitleExactMatches != 3 {
		t.Fatalf("match stats = %+v", result.Stats)
	}
	if len(result.Warnings) != 0 {
		t.Fatalf("warnings = %v", result.Warnings)
	}

	firstCandidate := findCandidate(t, result.Candidates[first], "/books/OL1M")
	if firstCandidate.ISBN != "9780306406157" || firstCandidate.AuthorScore != 1 {
		t.Fatalf("first candidate = %+v", firstCandidate)
	}
	if !slices.Contains(firstCandidate.Methods, "isbn") {
		t.Fatalf("first methods = %v", firstCandidate.Methods)
	}

	secondCandidate := findCandidate(t, result.Candidates[second], "/books/OL2M")
	if secondCandidate.ISBN != "9780375704024" || secondCandidate.AuthorScore != 1 {
		t.Fatalf("second candidate = %+v", secondCandidate)
	}
	if !slices.Contains(secondCandidate.Methods, "isbn") || !slices.Contains(secondCandidate.Methods, "title_exact") {
		t.Fatalf("second methods = %v", secondCandidate.Methods)
	}

	thirdCandidate := findCandidate(t, result.Candidates[third], "/books/OL3M")
	if thirdCandidate.AuthorScore != 1 || !slices.Contains(thirdCandidate.Methods, "title_exact") {
		t.Fatalf("third candidate = %+v", thirdCandidate)
	}
	if result.Works["/works/OL1W"].Title != "The Hobbit" {
		t.Fatalf("works = %+v", result.Works)
	}
}

func TestStreamEarlyExit(t *testing.T) {
	queries := newTestQueries(t)
	first := addBook(t, queries, "Book One", nil, []db.CreateBookIdentifierParams{
		{Scheme: "ISBN", Value: "9780306406157", Source: "METADATA_AND_TEXT"},
	})
	second := addBook(t, queries, "Book Two", nil, []db.CreateBookIdentifierParams{
		{Scheme: "ISBN", Value: "9780375704024", Source: "METADATA_AND_TEXT"},
	})
	cache, err := Build(context.Background(), queries, Options{})
	if err != nil {
		t.Fatal(err)
	}

	dir := t.TempDir()
	editions := writeStreamDump(t, dir, "editions.txt", strings.Join([]string{
		editionLine("/books/OL1M", "Book One", []string{"9780306406157"}, nil, nil),
		editionLine("/books/OL2M", "Book Two", []string{"9780375704024"}, nil, nil),
		editionLine("/books/OL3M", "Book Three", nil, nil, nil),
	}, ""), openlibrary.KindEditions)

	result, err := StreamDataset(context.Background(), &openlibrary.Dataset{
		Dir:      dir,
		Editions: editions,
	}, cache, StreamOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if result.Stats.Editions != 2 {
		t.Fatalf("editions = %d, want 2 (early exit)", result.Stats.Editions)
	}
	findCandidate(t, result.Candidates[first], "/books/OL1M")
	findCandidate(t, result.Candidates[second], "/books/OL2M")
	if len(result.Warnings) != 2 {
		t.Fatalf("warnings = %v, want missing authors/works", result.Warnings)
	}
}

func TestStreamFuzzyTitle(t *testing.T) {
	queries := newTestQueries(t)
	bookID := addBook(t, queries, "Hobbit, The", nil, nil)
	cache, err := Build(context.Background(), queries, Options{})
	if err != nil {
		t.Fatal(err)
	}

	dir := t.TempDir()
	editions := writeStreamDump(t, dir, "editions.txt", editionLine("/books/OL1M", "The Hobbit", nil, nil, nil), openlibrary.KindEditions)
	result, err := StreamDataset(context.Background(), &openlibrary.Dataset{
		Dir:      dir,
		Editions: editions,
	}, cache, StreamOptions{})
	if err != nil {
		t.Fatal(err)
	}
	candidate := findCandidate(t, result.Candidates[bookID], "/books/OL1M")
	if !slices.Contains(candidate.Methods, "title_fuzzy") {
		t.Fatalf("methods = %v, want title_fuzzy", candidate.Methods)
	}
	if candidate.TitleScore < 0.99 {
		t.Fatalf("title score = %f, want 1", candidate.TitleScore)
	}
}

func TestStreamMalformedRows(t *testing.T) {
	queries := newTestQueries(t)
	bookID := addBook(t, queries, "The Hobbit", nil, nil)
	cache, err := Build(context.Background(), queries, Options{})
	if err != nil {
		t.Fatal(err)
	}

	dir := t.TempDir()
	body := "bad\trow\n" +
		dumpLine("/type/edition", "/books/OLBAD", `{"title":`) +
		editionLine("/books/OL1M", "The Hobbit", nil, nil, nil)
	editions := writeStreamDump(t, dir, "editions.txt", body, openlibrary.KindEditions)
	result, err := StreamDataset(context.Background(), &openlibrary.Dataset{
		Dir:      dir,
		Editions: editions,
	}, cache, StreamOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if result.Stats.Malformed < 2 {
		t.Fatalf("malformed = %d, want at least 2", result.Stats.Malformed)
	}
	findCandidate(t, result.Candidates[bookID], "/books/OL1M")
}

func TestStreamCombinedRejected(t *testing.T) {
	file := &openlibrary.File{Path: "combined.txt", Kind: openlibrary.KindAll}
	_, err := StreamDataset(context.Background(), &openlibrary.Dataset{
		Dir:      ".",
		Editions: file,
		Works:    file,
		Authors:  file,
		Combined: file,
	}, &Cache{Books: map[int64]*Book{}}, StreamOptions{})
	if err == nil || !strings.Contains(err.Error(), "combined") {
		t.Fatalf("error = %v, want combined dump rejection", err)
	}
}

func findCandidate(t *testing.T, candidates []Candidate, editionKey string) Candidate {
	t.Helper()
	for _, candidate := range candidates {
		if candidate.EditionKey == editionKey {
			return candidate
		}
	}
	t.Fatalf("candidate %q not found in %+v", editionKey, candidates)
	return Candidate{}
}
