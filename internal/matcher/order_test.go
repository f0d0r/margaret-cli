package matcher

import (
	"context"
	"fmt"
	"reflect"
	"sort"
	"testing"

	"github.com/f0d0r/margaret-cli/internal/openlibrary"
	"github.com/f0d0r/margaret-cli/internal/textmatch"
)

func referenceRareTokenCandidatesByHash(c *Cache, tokens []uint64, maxPosting int) []int64 {
	type tokenCount struct {
		token uint64
		count int
	}
	var stats []tokenCount
	for _, token := range tokens {
		count := len(c.ByTokenHash[token])
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
	if len(stats) > 2 {
		stats = stats[:2]
	}
	seen := make(map[int64]struct{})
	var out []int64
	for _, stat := range stats {
		for _, id := range c.ByTokenHash[stat.token] {
			if _, ok := seen[id]; ok {
				continue
			}
			seen[id] = struct{}{}
			out = append(out, id)
		}
	}
	return out
}

func TestRareTokenCandidatesByHashMatchesReference(t *testing.T) {
	queries := newTestQueries(t)
	titles := []string{
		"Alpha Beta Gamma",
		"Alpha Beta Delta",
		"Alpha Epsilon Zeta",
		"Beta Gamma Delta",
		"Gamma Delta Epsilon",
		"Zeta Eta Theta",
	}
	for _, title := range titles {
		addBook(t, queries, title, nil, nil)
	}
	cache, err := Build(context.Background(), queries, Options{})
	if err != nil {
		t.Fatal(err)
	}
	inputs := [][]string{
		{"alpha", "beta"},
		{"gamma", "delta", "epsilon"},
		{"zeta", "theta", "missing-token"},
		{"alpha", "alpha"},
		{"missing-token"},
		nil,
	}
	for _, tokens := range inputs {
		var hashes []uint64
		if tokens != nil {
			hashes = textmatch.HashTokens(tokens)
		}
		for _, maxPosting := range []int{0, 1, 2, 10} {
			want := referenceRareTokenCandidatesByHash(cache, hashes, maxPosting)
			got := cache.RareTokenCandidatesByHash(hashes, maxPosting)
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("tokens %v maxPosting %d: got %v, want %v", tokens, maxPosting, got, want)
			}
		}
	}
}

func TestStreamFastOrderMatchesLegacyOrder(t *testing.T) {
	newDataset := func(t *testing.T) (*Cache, *openlibrary.Dataset) {
		t.Helper()
		cache, _, _, _, dataset := streamDataset(t)
		return cache, dataset
	}
	cache, dataset := newDataset(t)
	forceLegacyScanOrder = false
	fast, err := StreamDataset(context.Background(), dataset, cache, StreamOptions{})
	if err != nil {
		t.Fatal(err)
	}
	forceLegacyScanOrder = true
	defer func() { forceLegacyScanOrder = false }()
	legacy, err := StreamDataset(context.Background(), dataset, cache, StreamOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(fast.Candidates, legacy.Candidates) {
		t.Fatalf("candidates differ:\nfast: %+v\nlegacy: %+v", fast.Candidates, legacy.Candidates)
	}
	if !reflect.DeepEqual(fast.Authors, legacy.Authors) {
		t.Fatalf("authors differ:\nfast: %+v\nlegacy: %+v", fast.Authors, legacy.Authors)
	}
	if !reflect.DeepEqual(fast.Works, legacy.Works) {
		t.Fatalf("works differ:\nfast: %+v\nlegacy: %+v", fast.Works, legacy.Works)
	}
	if !reflect.DeepEqual(fast.Warnings, legacy.Warnings) {
		t.Fatalf("warnings differ: %v vs %v", fast.Warnings, legacy.Warnings)
	}
}

func TestExceedsWorksCap(t *testing.T) {
	set := newCandidateSet()
	for i := range 5 {
		set.add(Candidate{BookID: 1, EditionKey: fmt.Sprintf("/books/E%dM", i)}, 20)
	}
	if set.exceedsWorksCap(5) {
		t.Fatal("5 candidates must not exceed a cap of 5")
	}
	set.add(Candidate{BookID: 1, EditionKey: "/books/E5M"}, 20)
	if !set.exceedsWorksCap(5) {
		t.Fatal("6 candidates must exceed a cap of 5")
	}
}

func TestStreamLegacyCapFallback(t *testing.T) {
	queries := newTestQueries(t)
	bookID := addBook(t, queries, "Common Title", []string{"Jane Doe"}, nil)
	cache, err := Build(context.Background(), queries, Options{})
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	var editions string
	var works string
	var authors string
	for i := range 7 {
		editions += editionLine(
			fmt.Sprintf("/books/OL%dM", i),
			fmt.Sprintf("Common Title Edition %d", i),
			nil,
			[]string{"/authors/OL1A"},
			[]string{fmt.Sprintf("/works/OL%dW", i)},
		)
		works += workLine(
			fmt.Sprintf("/works/OL%dW", i),
			fmt.Sprintf("Common Title Edition %d", i),
			"description",
			[]string{"Fiction"},
		)
	}
	authors = authorLine("/authors/OL1A", "Jane Doe")
	dataset := &openlibrary.Dataset{
		Dir:      dir,
		Editions: writeStreamDump(t, dir, "editions.txt.gz", editions, openlibrary.KindEditions),
		Works:    writeStreamDump(t, dir, "works.txt.gz", works, openlibrary.KindWorks),
		Authors:  writeStreamDump(t, dir, "authors.txt.gz", authors, openlibrary.KindAuthors),
	}
	result, err := StreamDataset(context.Background(), dataset, cache, StreamOptions{})
	if err != nil {
		t.Fatal(err)
	}
	candidates := result.Candidates[bookID]
	if len(candidates) != 7 {
		t.Fatalf("candidates = %d, want 7", len(candidates))
	}
	for _, candidate := range candidates {
		if candidate.AuthorScore != 1 {
			t.Fatalf("candidate %+v has no work-level author score", candidate)
		}
	}
}

func TestStreamCombinedParallelMatchesSingleWorker(t *testing.T) {
	cache, _, _, _ := streamCache(t)
	dir := t.TempDir()
	file := writeStreamDump(t, dir, "ol_dump_latest.txt.gz", editionLine("/books/OL1M", "The Hobbit", nil, []string{"/authors/OL1A"}, []string{"/works/OL1W"})+
		authorLine("/authors/OL1A", "J. R. R. Tolkien")+
		workLine("/works/OL1W", "The Hobbit", "A hobbit goes there and back again.", []string{"Fantasy"}),
		openlibrary.KindAll)
	dataset := &openlibrary.Dataset{
		Dir:      dir,
		Editions: file,
		Works:    file,
		Authors:  file,
		Combined: file,
	}
	single, err := StreamDataset(context.Background(), dataset, cache, StreamOptions{Workers: 1})
	if err != nil {
		t.Fatal(err)
	}
	parallel, err := StreamDataset(context.Background(), dataset, cache, StreamOptions{Workers: 4})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(single.Authors, parallel.Authors) {
		t.Fatalf("authors differ:\nsingle: %+v\nparallel: %+v", single.Authors, parallel.Authors)
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
