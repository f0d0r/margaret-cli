package cli

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/f0d0r/margaret-cli/internal/database"
	"github.com/f0d0r/margaret-cli/internal/processor"
	"github.com/f0d0r/margaret-cli/internal/report"
)

// withSearchFlags points the search globals at dir and restores everything
// after the test; tests in this package must not run in parallel.
func withSearchFlags(t *testing.T, dir string) string {
	t.Helper()
	oldAuthor, oldTitle := author, title
	oldLimit, oldJSON, oldJSONOut := limit, jsonFlag, jsonOut
	t.Cleanup(func() {
		author, title = oldAuthor, oldTitle
		limit, jsonFlag, jsonOut = oldLimit, oldJSON, oldJSONOut
	})
	author, title = false, false
	limit, jsonFlag = 0, false
	jsonOut = filepath.Join(dir, "search.json")
	return jsonOut
}

func TestRunSearchMissingDB(t *testing.T) {
	dir := t.TempDir()
	oldDB := dbPath
	t.Cleanup(func() { dbPath = oldDB })
	dbPath = filepath.Join(dir, "nope.db")

	if err := runSearch(context.Background(), "Test"); err == nil || !strings.Contains(err.Error(), "scan") {
		t.Fatalf("expected run-scan-first error, got %v", err)
	}
	if _, err := os.Stat(dbPath); !os.IsNotExist(err) {
		t.Fatalf("search must not create a missing database, stat err: %v", err)
	}
}

func TestRunSearchEmptyDB(t *testing.T) {
	dir := t.TempDir()
	oldDB := dbPath
	t.Cleanup(func() { dbPath = oldDB })
	dbPath = filepath.Join(dir, "empty.db")

	conn, err := database.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	_ = conn.Close()

	if err := runSearch(context.Background(), "Test"); err == nil || !strings.Contains(err.Error(), "scan") {
		t.Fatalf("expected run-scan-first error, got %v", err)
	}
}

func TestRunSearchFindsScannedBook(t *testing.T) {
	booksDir, _, _ := fileScanSetup(t, false, false, false)
	if err := runScan(context.Background(), booksDir, processor.DefaultScanProcessorConfig()); err != nil {
		t.Fatal(err)
	}
	withSearchFlags(t, t.TempDir())

	var runErr error
	out := captureStdout(t, func() {
		runErr = runSearch(context.Background(), "Test")
	})
	if runErr != nil {
		t.Fatal(runErr)
	}
	if !strings.Contains(out, "Test Book") || !strings.Contains(out, "Author A") {
		t.Fatalf("expected screen output with title and author, got %q", out)
	}
	// Duplicates must also be listed on screen
	if !strings.Contains(out, "a.epub") || !strings.Contains(out, "b.epub") {
		t.Fatalf("expected both canonical (a.epub) and duplicate (b.epub) files in output, got %q", out)
	}
}

func TestRunSearchFlagsAuthorAndTitle(t *testing.T) {
	booksDir, _, _ := fileScanSetup(t, false, false, false)
	writeTestEpub(t, filepath.Join(booksDir, "shining.epub"), "The Shining", "Stephen King", "c_shining")
	writeTestEpub(t, filepath.Join(booksDir, "solomon.epub"), "King Solomon", "Rider Haggard", "c_solomon")

	if err := runScan(context.Background(), booksDir, processor.DefaultScanProcessorConfig()); err != nil {
		t.Fatal(err)
	}
	withSearchFlags(t, t.TempDir())

	// 1. --author only: matches "The Shining" (author Stephen King), not "King Solomon"
	author = true
	title = false
	var out string
	out = captureStdout(t, func() {
		if err := runSearch(context.Background(), "King"); err != nil {
			t.Fatal(err)
		}
	})
	if !strings.Contains(out, "The Shining") {
		t.Fatalf("author-only: expected 'The Shining', got %q", out)
	}
	if strings.Contains(out, "King Solomon") {
		t.Fatalf("author-only: should NOT contain 'King Solomon', got %q", out)
	}

	// 2. --title only: matches "King Solomon" (title), not "The Shining"
	author = false
	title = true
	out = captureStdout(t, func() {
		if err := runSearch(context.Background(), "King"); err != nil {
			t.Fatal(err)
		}
	})
	if !strings.Contains(out, "King Solomon") {
		t.Fatalf("title-only: expected 'King Solomon', got %q", out)
	}
	if strings.Contains(out, "The Shining") {
		t.Fatalf("title-only: should NOT contain 'The Shining', got %q", out)
	}

	// 3. Default (neither or both): matches both
	author = false
	title = false
	out = captureStdout(t, func() {
		if err := runSearch(context.Background(), "King"); err != nil {
			t.Fatal(err)
		}
	})
	if !strings.Contains(out, "The Shining") || !strings.Contains(out, "King Solomon") {
		t.Fatalf("both: expected both books, got %q", out)
	}
}

func TestRunSearchFlagLimit(t *testing.T) {
	booksDir, _, _ := fileScanSetup(t, false, false, false)
	writeTestEpub(t, filepath.Join(booksDir, "dune1.epub"), "Dune One", "Frank Herbert", "c_d1")
	writeTestEpub(t, filepath.Join(booksDir, "dune2.epub"), "Dune Two", "Frank Herbert", "c_d2")
	writeTestEpub(t, filepath.Join(booksDir, "dune3.epub"), "Dune Three", "Frank Herbert", "c_d3")

	if err := runScan(context.Background(), booksDir, processor.DefaultScanProcessorConfig()); err != nil {
		t.Fatal(err)
	}
	jsonOut := withSearchFlags(t, t.TempDir())
	jsonFlag = true

	// limit = 1
	limit = 1
	if err := runSearch(context.Background(), "Dune"); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(jsonOut)
	if err != nil {
		t.Fatal(err)
	}
	var hits []report.BookReport
	if err := json.Unmarshal(data, &hits); err != nil {
		t.Fatal(err)
	}
	if len(hits) != 1 {
		t.Fatalf("expected 1 hit with limit=1, got %d", len(hits))
	}

	// limit = 2
	limit = 2
	if err := runSearch(context.Background(), "Dune"); err != nil {
		t.Fatal(err)
	}
	data, err = os.ReadFile(jsonOut)
	if err != nil {
		t.Fatal(err)
	}
	hits = nil
	if err := json.Unmarshal(data, &hits); err != nil {
		t.Fatal(err)
	}
	if len(hits) != 2 {
		t.Fatalf("expected 2 hits with limit=2, got %d", len(hits))
	}
}

func TestRunSearchJSONWritesSearchFile(t *testing.T) {
	booksDir, _, _ := fileScanSetup(t, false, false, false)
	if err := runScan(context.Background(), booksDir, processor.DefaultScanProcessorConfig()); err != nil {
		t.Fatal(err)
	}
	jsonOut := withSearchFlags(t, t.TempDir())
	jsonFlag = true

	if err := runSearch(context.Background(), "Test"); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(jsonOut)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "Test Book") {
		t.Fatalf("expected search.json with the hit, got %s", data)
	}
}

func TestRunSearchNoHitsWritesNothing(t *testing.T) {
	booksDir, _, _ := fileScanSetup(t, false, false, false)
	if err := runScan(context.Background(), booksDir, processor.DefaultScanProcessorConfig()); err != nil {
		t.Fatal(err)
	}
	jsonOut := withSearchFlags(t, t.TempDir())
	jsonFlag = true

	if err := runSearch(context.Background(), "zzzqqqnonexistent"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(jsonOut); !os.IsNotExist(err) {
		t.Fatalf("expected no search.json without hits, stat err: %v", err)
	}
}
