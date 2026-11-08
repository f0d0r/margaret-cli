package processor

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/f0d0r/margaret-cli/internal/identifier"
	"github.com/f0d0r/margaret-cli/internal/parser"
)

// TestProcessTxt pins the end-to-end handling of plain-text books: the txt
// parser extracts the label header, the ISBN and the hash/MinHash, and the
// ".text" alias maps to the same format.
func TestProcessSkipsJunkTxt(t *testing.T) {
	dir := t.TempDir()
	body := strings.Repeat("A reggel még hűvös volt, a folyó felett köd ült. ", 350)
	files := map[string]string{
		"small.txt":  "Title: Tiny Metadata\nAuthor: Jane Doe\n\nBody text.\n",
		"html.txt":   "<!DOCTYPE html>\n<html><body>" + strings.Repeat("<p>not a book</p>", 1500) + "</body></html>\n",
		"normal.txt": "Móricz Zsigmond\n\nAz arany ember\n\n" + body,
		"01.txt":     "Title: Chapter One\nAuthor: Jane Doe\n\n" + body,
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	var (
		mu      sync.Mutex
		results []Result
	)
	cfg := DefaultScanProcessorConfig()
	cfg.OnResult = func(r Result) {
		mu.Lock()
		results = append(results, r)
		mu.Unlock()
	}
	proc := newTestProcessor(t, cfg, dir)
	if err := proc.Process(context.Background()); err != nil {
		t.Fatal(err)
	}
	if failures := proc.Failures(); len(failures) != 0 {
		t.Fatalf("junk must not be reported as failures: %v", failures)
	}
	want := []string{filepath.Join(dir, "01.txt"), filepath.Join(dir, "normal.txt")}
	got := make([]string, 0, len(results))
	for _, r := range results {
		got = append(got, r.File.Path)
	}
	slices.Sort(got)
	if !slices.Equal(got, want) {
		t.Fatalf("results = %v, want %v", got, want)
	}
	if got := proc.Stats().Skipped; got != 2 {
		t.Errorf("skipped = %d, want 2", got)
	}
	skips := proc.Skips()
	if len(skips) != 2 {
		t.Fatalf("skips = %+v, want 2 entries", skips)
	}
	reasons := make(map[string]string, len(skips))
	for _, s := range skips {
		reasons[s.Path] = s.Reason
	}
	if reason := reasons[filepath.Join(dir, "small.txt")]; !strings.Contains(reason, "below minimum document size") {
		t.Errorf("small.txt skip reason = %q", reason)
	}
	if reason := reasons[filepath.Join(dir, "html.txt")]; reason != "not a book" {
		t.Errorf("html.txt skip reason = %q, want %q", reason, "not a book")
	}

	var rows int
	if err := proc.db.QueryRow("SELECT count(*) FROM book_files").Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if rows != 2 {
		t.Errorf("book_files rows = %d, want 2", rows)
	}
}

func TestProcessEmptyTextIsSkip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "empty.txt")
	if err := os.WriteFile(path, []byte("   \n\t\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg := DefaultScanProcessorConfig()
	cfg.MinDocSize = -1 // reach the parser-level empty-text check
	proc := newTestProcessor(t, cfg, dir)
	if err := proc.Process(context.Background()); err != nil {
		t.Fatal(err)
	}
	if failures := proc.Failures(); len(failures) != 0 {
		t.Fatalf("empty text must not be a failure: %v", failures)
	}
	stats := proc.Stats()
	if stats.Parsed != 0 || stats.Skipped != 1 || stats.AlreadyProcessed != 0 {
		t.Fatalf("stats = %+v, want parsed=0 skipped=1", stats)
	}
	skips := proc.Skips()
	if len(skips) != 1 || skips[0].Path != path {
		t.Fatalf("skips = %+v, want the empty file", skips)
	}
	if !strings.Contains(skips[0].Reason, "empty or whitespace-only text") {
		t.Errorf("skip reason = %q, want an empty-text reason", skips[0].Reason)
	}
}

func TestProcessTxtSizeThresholdOverride(t *testing.T) {
	dir := t.TempDir()
	content := "Title: A Short Book\nAuthor: Jane Doe\n\n" + strings.Repeat("A reggel hűvös volt. ", 40)
	for name, body := range map[string]string{"small.txt": content} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	run := func(minSize int64) (parsed, skipped int64) {
		cfg := DefaultScanProcessorConfig()
		cfg.MinDocSize = minSize
		proc := newTestProcessor(t, cfg, dir)
		if err := proc.Process(context.Background()); err != nil {
			t.Fatal(err)
		}
		s := proc.Stats()
		return s.Parsed, s.Skipped
	}

	if parsed, skipped := run(1 << 20); parsed != 0 || skipped != 1 {
		t.Errorf("large threshold: parsed=%d skipped=%d, want 0, 1", parsed, skipped)
	}
	if parsed, skipped := run(-1); parsed != 1 || skipped != 0 {
		t.Errorf("disabled threshold: parsed=%d skipped=%d, want 1, 0", parsed, skipped)
	}
}

func TestProcessTxt(t *testing.T) {
	dir := t.TempDir()
	files := map[string]string{
		"a.txt": "Title: The Great Novel\nAuthor: Jane Doe & John Smith\n\n" +
			"ISBN 978-3-16-148410-0\n\nThe body.\n",
		"b.text": "Moby Dick\n\n\nHerman Melville\nCall me Ishmael.\n",
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	var (
		mu      sync.Mutex
		results []Result
	)
	cfg := DefaultScanProcessorConfig()
	cfg.MinDocSize = -1 // the fixture is smaller than the default TXT size limit
	cfg.OnResult = func(r Result) {
		mu.Lock()
		results = append(results, r)
		mu.Unlock()
	}
	proc := newTestProcessor(t, cfg, dir)
	if err := proc.Process(context.Background()); err != nil {
		t.Fatal(err)
	}
	if failures := proc.Failures(); len(failures) != 0 {
		t.Fatalf("unexpected failures: %v", failures)
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d: %v", len(results), pathsOf(results))
	}

	byPath := make(map[string]Result, len(results))
	for _, r := range results {
		if r.File.Format != "txt" {
			t.Errorf("%s: format = %q, want %q", r.File.Path, r.File.Format, "txt")
		}
		if len(r.Metadata.MinHash) != parser.MinHashHashes {
			t.Errorf("%s: minhash has %d hashes, want %d", r.File.Path, len(r.Metadata.MinHash), parser.MinHashHashes)
		}
		if r.Metadata.Hash == "" {
			t.Errorf("%s: empty hash", r.File.Path)
		}
		byPath[r.File.Path] = r
	}

	a := byPath[filepath.Join(dir, "a.txt")]
	if a.Metadata.Title != "The Great Novel" {
		t.Errorf("a.txt title = %q, want %q", a.Metadata.Title, "The Great Novel")
	}
	wantAuthors := []string{"Jane Doe", "John Smith"}
	if !slices.Equal(a.Metadata.Authors, wantAuthors) {
		t.Errorf("a.txt authors = %v, want %v", a.Metadata.Authors, wantAuthors)
	}
	wantID := identifier.Identifier{Scheme: "ISBN", Value: "9783161484100", Source: identifier.SourceText}
	if len(a.Metadata.Identifiers) != 1 || a.Metadata.Identifiers[0] != wantID {
		t.Errorf("a.txt identifiers = %v, want %v", a.Metadata.Identifiers, []identifier.Identifier{wantID})
	}

	b := byPath[filepath.Join(dir, "b.text")]
	if b.Metadata.Title != "Moby Dick" {
		t.Errorf("b.text title = %q, want %q", b.Metadata.Title, "Moby Dick")
	}
	if want := []string{"Herman Melville"}; !slices.Equal(b.Metadata.Authors, want) {
		t.Errorf("b.text authors = %v, want %v", b.Metadata.Authors, want)
	}

	var rows int
	if err := proc.db.QueryRow("SELECT count(*) FROM book_files").Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if rows != 2 {
		t.Errorf("book_files rows = %d, want 2", rows)
	}
}
