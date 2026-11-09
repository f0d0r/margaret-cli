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
	"github.com/f0d0r/margaret-cli/internal/textmatch"
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

func streamDataset(t *testing.T) (*Cache, int64, int64, int64, *openlibrary.Dataset) {
	t.Helper()
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
	return cache, first, second, third, &openlibrary.Dataset{
		Dir:      dir,
		Editions: editions,
		Authors:  authors,
		Works:    works,
	}
}

func TestStreamDataset(t *testing.T) {
	cache, first, second, third, dataset := streamDataset(t)
	result, err := StreamDataset(context.Background(), dataset, cache, StreamOptions{})
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

func TestStreamAndRank(t *testing.T) {
	cache, first, second, third, dataset := streamDataset(t)
	stream, err := StreamDataset(context.Background(), dataset, cache, StreamOptions{})
	if err != nil {
		t.Fatal(err)
	}
	ranked, err := Rank(stream, cache, RankOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if got := ranked.Books[first].Status; got != StatusConfirmed {
		t.Fatalf("first status = %q, want confirmed", got)
	}
	if got := ranked.Books[second].Status; got != StatusProbable {
		t.Fatalf("second status = %q, want probable", got)
	}
	if got := ranked.Books[third].Status; got != StatusConfirmed {
		t.Fatalf("third status = %q, want confirmed", got)
	}
	if ranked.Books[first].WorkKey != "/works/OL1W" {
		t.Fatalf("first work = %q", ranked.Books[first].WorkKey)
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

func TestStreamCombined(t *testing.T) {
	cache, first, second, third := streamCache(t)
	dir := t.TempDir()
	tempDir := t.TempDir()
	file := writeStreamDump(t, dir, "ol_dump_latest.txt.gz", strings.Join([]string{
		authorLine("/authors/OL1A", "J. R. R. Tolkien"),
		editionLine("/books/OL1M", "The Hobbit", []string{"9780306406157"}, []string{"/authors/OL1A"}, []string{"/works/OL1W"}),
		workLine("/works/OL1W", "The Hobbit", "A hobbit goes there and back again.", []string{"Fantasy"}),
		authorLine("/authors/OL2A", "Someone Else"),
		editionLine("/books/OL2M", "The Hobbit", []string{"9780375704024"}, []string{"/authors/OL2A"}, []string{"/works/OL2W"}),
		workLine("/works/OL2W", "The Hobbit", "Another edition.", []string{"Fantasy"}),
		authorLine("/authors/OL3A", "Jane Doe"),
		editionLine("/books/OL3M", "A Title Without ISBN", nil, []string{"/authors/OL3A"}, []string{"/works/OL3W"}),
		workLine("/works/OL3W", "A Title Without ISBN", "A third work.", []string{"Fiction"}),
	}, ""), openlibrary.KindAll)
	dataset := &openlibrary.Dataset{
		Dir:      dir,
		Editions: file,
		Works:    file,
		Authors:  file,
		Combined: file,
	}

	result, err := StreamDataset(context.Background(), dataset, cache, StreamOptions{TempDir: tempDir})
	if err != nil {
		t.Fatal(err)
	}
	if result.Stats.Editions != 3 || result.Stats.Authors != 3 || result.Stats.Works != 0 {
		t.Fatalf("stats = %+v", result.Stats)
	}
	if len(result.Works) != 0 {
		t.Fatalf("works = %+v, want deferred", result.Works)
	}
	if !strings.Contains(strings.Join(result.Warnings, " "), "works metadata deferred") {
		t.Fatalf("warnings = %v", result.Warnings)
	}
	if result.Authors["/authors/OL1A"].Names[0] != "J. R. R. Tolkien" {
		t.Fatalf("authors = %+v", result.Authors)
	}
	findCandidate(t, result.Candidates[first], "/books/OL1M")
	findCandidate(t, result.Candidates[second], "/books/OL2M")
	findCandidate(t, result.Candidates[third], "/books/OL3M")

	ranked, err := Rank(result, cache, RankOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if got := ranked.Books[first].Status; got != StatusConfirmed {
		t.Fatalf("first status = %q, want confirmed", got)
	}
	if got := ranked.Books[second].Status; got != StatusProbable {
		t.Fatalf("second status = %q, want probable", got)
	}
	if got := ranked.Books[third].Status; got != StatusConfirmed {
		t.Fatalf("third status = %q, want confirmed", got)
	}

	entries, err := os.ReadDir(tempDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("temp dir is not clean: %v", entries)
	}
}

func TestStreamCombinedNoCandidatesCleansSidecar(t *testing.T) {
	queries := newTestQueries(t)
	addBook(t, queries, "No Match", nil, nil)
	cache, err := Build(context.Background(), queries, Options{})
	if err != nil {
		t.Fatal(err)
	}

	dir := t.TempDir()
	tempDir := t.TempDir()
	file := writeStreamDump(t, dir, "ol_dump_latest.txt.gz", editionLine("/books/OL1M", "Different", nil, nil, nil), openlibrary.KindAll)
	result, err := StreamDataset(context.Background(), &openlibrary.Dataset{
		Dir:      dir,
		Editions: file,
		Works:    file,
		Authors:  file,
		Combined: file,
	}, cache, StreamOptions{TempDir: tempDir})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Candidates) != 0 {
		t.Fatalf("candidates = %+v", result.Candidates)
	}
	entries, err := os.ReadDir(tempDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("temp dir is not clean: %v", entries)
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

func workLineWithAuthors(key, title string, authorKeys []string) string {
	authors := make([]map[string]any, 0, len(authorKeys))
	for _, authorKey := range authorKeys {
		authors = append(authors, map[string]any{
			"type":   map[string]string{"key": "/type/author_role"},
			"author": map[string]string{"key": authorKey},
		})
	}
	data, err := json.Marshal(map[string]any{
		"key":     key,
		"title":   title,
		"authors": authors,
	})
	if err != nil {
		panic(err)
	}
	return dumpLine("/type/work", key, string(data))
}

func TestStreamWorkAuthorsFillMissingEditionAuthors(t *testing.T) {
	queries := newTestQueries(t)
	bookID := addBook(t, queries, "The Hobbit", []string{"J. R. R. Tolkien"}, nil)
	cache, err := Build(context.Background(), queries, Options{})
	if err != nil {
		t.Fatal(err)
	}

	dir := t.TempDir()
	editions := writeStreamDump(t, dir, "editions.txt.gz",
		editionLine("/books/OL1M", "The Hobbit", nil, nil, []string{"/works/OL1W"}),
		openlibrary.KindEditions)
	works := writeStreamDump(t, dir, "works.txt.gz",
		workLineWithAuthors("/works/OL1W", "The Hobbit", []string{"/authors/OL1A"}),
		openlibrary.KindWorks)
	authors := writeStreamDump(t, dir, "authors.txt.gz",
		authorLine("/authors/OL1A", "J. R. R. Tolkien"),
		openlibrary.KindAuthors)
	dataset := &openlibrary.Dataset{Dir: dir, Editions: editions, Works: works, Authors: authors}

	result, err := StreamDataset(context.Background(), dataset, cache, StreamOptions{})
	if err != nil {
		t.Fatal(err)
	}
	candidates := result.Candidates[bookID]
	if len(candidates) != 1 {
		t.Fatalf("candidates = %d, want 1", len(candidates))
	}
	candidate := candidates[0]
	if len(candidate.AuthorKeys) != 1 || candidate.AuthorKeys[0] != "/authors/OL1A" {
		t.Fatalf("author keys = %v", candidate.AuthorKeys)
	}
	if candidate.AuthorScore == 0 {
		t.Fatalf("author score = %f, want a work-level author score", candidate.AuthorScore)
	}

	ranked, err := Rank(result, cache, RankOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if got := ranked.Books[bookID].Status; got != StatusConfirmed {
		t.Fatalf("status = %q, want confirmed", got)
	}
}

func TestStreamProgressReportsCurrentBook(t *testing.T) {
	cache, _, _, _, dataset := streamDataset(t)
	for _, workers := range []int{1, 4} {
		var events []StreamProgress
		_, err := StreamDataset(context.Background(), dataset, cache, StreamOptions{
			Workers: workers,
			Progress: func(progress StreamProgress) {
				events = append(events, progress)
			},
		})
		if err != nil {
			t.Fatal(err)
		}

		var editions, authors, works []StreamProgress
		for _, event := range events {
			switch event.Pass {
			case "editions":
				editions = append(editions, event)
			case "authors":
				authors = append(authors, event)
			case "works":
				works = append(works, event)
			}
		}
		if len(editions) == 0 {
			t.Fatal("no editions progress events")
		}
		final := editions[len(editions)-1]
		if final.Total != 3 || final.Done != 3 {
			t.Fatalf("workers %d editions progress = %+v, want total 3 done 3", workers, final)
		}
		if final.Current == "" || !strings.Contains(final.Current, ": ") {
			t.Fatalf("workers %d current book = %q, want author: title", workers, final.Current)
		}
		if len(authors) == 0 || authors[len(authors)-1].Total == 0 ||
			authors[len(authors)-1].Done != authors[len(authors)-1].Total {
			t.Fatalf("workers %d authors progress = %+v", workers, authors)
		}
		if len(works) == 0 || works[len(works)-1].Total == 0 ||
			works[len(works)-1].Done != works[len(works)-1].Total {
			t.Fatalf("workers %d works progress = %+v", workers, works)
		}
	}
}

func TestStreamDatasetParallelMatchesSingleWorker(t *testing.T) {
	cache, _, _, _, dataset := streamDataset(t)
	// Keep one book permanently pending so both passes scan the whole dump;
	// the early-exit boundary otherwise makes the candidate set depend on the
	// order in which editions happen to be processed.
	title := "An Unmatched Book"
	key := textmatch.TitleKey(title)
	tokens := textmatch.TokensFromKey(key)
	unmatchedID := int64(9999)
	cache.Books[unmatchedID] = &Book{
		ID:          unmatchedID,
		Index:       len(cache.Books),
		Title:       title,
		TitleKey:    key,
		TitleTokens: tokens,
		TitleHashes: textmatch.HashTokens(tokens),
	}

	single, err := StreamDataset(context.Background(), dataset, cache, StreamOptions{Workers: 1})
	if err != nil {
		t.Fatal(err)
	}
	parallel, err := StreamDataset(context.Background(), dataset, cache, StreamOptions{Workers: 4})
	if err != nil {
		t.Fatal(err)
	}
	singleRank, err := Rank(single, cache, RankOptions{})
	if err != nil {
		t.Fatal(err)
	}
	parallelRank, err := Rank(parallel, cache, RankOptions{})
	if err != nil {
		t.Fatal(err)
	}
	for bookID, want := range singleRank.Books {
		got := parallelRank.Books[bookID]
		if got.Status != want.Status || got.WorkKey != want.WorkKey {
			t.Fatalf("book %d parallel = %s/%s, want %s/%s",
				bookID, got.Status, got.WorkKey, want.Status, want.WorkKey)
		}
	}
}
