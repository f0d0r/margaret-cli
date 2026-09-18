package processor

import (
	"archive/zip"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/f0d0r/margaret-cli/internal/database"
	"github.com/f0d0r/margaret-cli/internal/db"
	"github.com/f0d0r/margaret-cli/internal/parser"
	"github.com/f0d0r/margaret-ebook-library/book"
)

// newTestProcessor builds a ScanProcessor backed by an in-memory database.
func newTestProcessor(t *testing.T, cfg ScanProcessorConfig, root string) *ScanProcessor {
	t.Helper()
	conn, err := database.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return NewScanProcessor(conn, db.New(conn), cfg, root)
}

// readBlobAll reads the full contents of a Blob into memory.
func readBlobAll(b book.Blob) ([]byte, error) {
	size, err := b.Size()
	if err != nil {
		return nil, err
	}
	data := make([]byte, size)
	if _, err := b.ReadAt(data, 0); err != nil && err != io.EOF {
		return nil, err
	}
	return data, nil
}

type fakeParser struct {
	failSubstr string
}

func (f fakeParser) Parse(b book.Blob) (parser.Metadata, error) {
	content, err := readBlobAll(b)
	if err != nil {
		return parser.Metadata{}, err
	}
	if strings.Contains(string(content), f.failSubstr) {
		return parser.Metadata{}, errors.New("boom")
	}
	return parser.Metadata{Authors: []string{"Author"}, Title: "Title"}, nil
}

func TestProcess(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"a.epub", "b.mobi", "bad/c.epub"} {
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		content := []byte("x")
		if strings.Contains(name, "bad/") {
			content = []byte("bad/boom")
		}
		if err := os.WriteFile(path, content, 0o644); err != nil {
			t.Fatal(err)
		}
	}

	var (
		mu        sync.Mutex
		results   []Result
		maxParsed int64
		maxFailed int64
	)
	proc := newTestProcessor(t, ScanProcessorConfig{
		ScanWorkers:  2,
		ParseWorkers: 2,
		OnResult: func(r Result) {
			mu.Lock()
			results = append(results, r)
			mu.Unlock()
		},
		OnProgress: func(p Progress) {
			mu.Lock()
			if p.Parsed > maxParsed {
				maxParsed = p.Parsed
			}
			if p.Failed > maxFailed {
				maxFailed = p.Failed
			}
			mu.Unlock()
		},
	}, dir)

	proc.parsers = map[string]parser.Parser{
		"epub": fakeParser{failSubstr: "bad/"},
		"mobi": fakeParser{failSubstr: "bad/"},
	}

	err := proc.Process(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	failures := proc.Failures()

	if len(failures) != 1 {
		t.Fatalf("expected 1 failure, got %d: %v", len(failures), failures)
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}
	if maxParsed != 2 {
		t.Errorf("expected progress parsed=2, got %d", maxParsed)
	}
	if maxFailed != 1 {
		t.Errorf("expected progress failed=1, got %d", maxFailed)
	}

	st := proc.Stats()
	if st.Scan.Found != 3 {
		t.Errorf("expected stats found=3, got %d", st.Scan.Found)
	}
	if st.Parsed != 2 {
		t.Errorf("expected stats parsed=2, got %d", st.Parsed)
	}
	if st.Failed != 1 {
		t.Errorf("expected stats failed=1, got %d", st.Failed)
	}
}

func TestProcessMissingRoot(t *testing.T) {
	proc := newTestProcessor(t, ScanProcessorConfig{}, filepath.Join(t.TempDir(), "nope"))
	err := proc.Process(context.Background())
	if err == nil {
		t.Fatal("expected fatal error for missing root")
	}
	failures := proc.Failures()
	if len(failures) != 0 {
		t.Fatalf("expected no failures, got %v", failures)
	}
}

func TestProcessCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	proc := newTestProcessor(t, ScanProcessorConfig{}, t.TempDir())
	err := proc.Process(ctx)
	if err != nil {
		t.Fatal(err)
	}
	failures := proc.Failures()
	if len(failures) != 0 {
		t.Fatalf("expected no failures, got %v", failures)
	}
}

// hashOrFailParser returns a content-derived hash like hashParser but fails
// on contents containing failSubstr, so tests can mix successful and failed
// files while keeping deterministic hashes.
type hashOrFailParser struct {
	failSubstr string
}

func (p hashOrFailParser) Parse(b book.Blob) (parser.Metadata, error) {
	content, err := readBlobAll(b)
	if err != nil {
		return parser.Metadata{}, err
	}
	if strings.Contains(string(content), p.failSubstr) {
		return parser.Metadata{}, errors.New("boom")
	}
	return parser.Metadata{
		Authors: []string{"Author"},
		Title:   "Title " + string(content),
		Hash:    "hash-" + string(content),
	}, nil
}

// TestFailedFilesRetriedOnResume pins the optimistic resume rule: failures
// leave no row behind, so a resume run retries them instead of skipping.
// Only successes are recorded and skipped; the failed file never appears in
// book grouping, and fixing it is picked up by the next resume without a
// fresh run.
func TestFailedFilesRetriedOnResume(t *testing.T) {
	dir := t.TempDir()
	goodPath := filepath.Join(dir, "good.epub")
	badPath := filepath.Join(dir, "bad.epub")
	if err := os.WriteFile(goodPath, []byte("good"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(badPath, []byte("bad"), 0o644); err != nil {
		t.Fatal(err)
	}

	conn, err := database.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	q := db.New(conn)
	ctx := context.Background()

	newProc := func(resume bool) *ScanProcessor {
		cfg := DefaultScanProcessorConfig()
		cfg.Resume = resume
		proc := NewScanProcessor(conn, q, cfg, dir)
		proc.parsers = map[string]parser.Parser{"epub": hashOrFailParser{failSubstr: "bad"}}
		return proc
	}
	counts := func() (files, books int) {
		t.Helper()
		if err := conn.QueryRow("SELECT count(*) FROM book_files").Scan(&files); err != nil {
			t.Fatal(err)
		}
		if err := conn.QueryRow("SELECT count(*) FROM books").Scan(&books); err != nil {
			t.Fatal(err)
		}
		return files, books
	}
	hasRow := func(path string) bool {
		t.Helper()
		var n int
		if err := conn.QueryRow("SELECT count(*) FROM book_files WHERE path = ?", path).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n > 0
	}

	// First run records only the success; the failure leaves no row.
	proc1 := newProc(false)
	if err := proc1.Process(ctx); err != nil {
		t.Fatal(err)
	}
	if got := proc1.Stats(); got.Parsed != 1 || got.Failed != 1 {
		t.Fatalf("first run: expected 1 parsed 1 failed, got %+v", got)
	}
	if files, books := counts(); files != 1 || books != 1 {
		t.Fatalf("after first run: files=%d books=%d, want 1/1", files, books)
	}
	if hasRow(badPath) {
		t.Fatalf("failed file %q must leave no row behind", badPath)
	}
	// The failed file carries no links, so grouping never sees it.
	rows, err := q.ListBooksWithFiles(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range rows {
		if r.FilePath == badPath {
			t.Fatalf("failed file %q must not appear in book grouping", badPath)
		}
	}

	// A resume run skips the recorded success and retries the failure.
	proc2 := newProc(true)
	if err := proc2.Process(ctx); err != nil {
		t.Fatal(err)
	}
	if got := proc2.Stats(); got.Parsed != 0 || got.Failed != 1 || got.Skipped != 1 {
		t.Fatalf("resume run: expected 0 parsed 1 failed 1 skipped, got %+v", got)
	}
	if failures := proc2.Failures(); len(failures) != 1 || failures[0].Path != badPath {
		t.Fatalf("expected the retried failure for %q, got %+v", badPath, failures)
	}
	if files, books := counts(); files != 1 || books != 1 {
		t.Fatalf("after resume: files=%d books=%d, want 1/1", files, books)
	}

	// Fixing the bad file is picked up by the next resume without a fresh
	// run; rewriting the good file is ignored (recorded paths always win).
	if err := os.WriteFile(badPath, []byte("fixed"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(goodPath, []byte("bad now"), 0o644); err != nil {
		t.Fatal(err)
	}
	proc3 := newProc(true)
	if err := proc3.Process(ctx); err != nil {
		t.Fatal(err)
	}
	if got := proc3.Stats(); got.Parsed != 1 || got.Failed != 0 || got.Skipped != 1 {
		t.Fatalf("fixed run: expected 1 parsed 0 failed 1 skipped, got %+v", got)
	}
	if files, books := counts(); files != 2 || books != 2 {
		t.Fatalf("after fixed run: files=%d books=%d, want 2/2", files, books)
	}
}

// TestFailedFilesLeaveNoRows asserts that identical failing files record
// nothing: both fail on every run and no book grouping is built from them.
func TestFailedFilesLeaveNoRows(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"a.epub", "b.epub"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("bad"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	conn, err := database.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()

	newProc := func(resume bool) *ScanProcessor {
		cfg := DefaultScanProcessorConfig()
		cfg.Resume = resume
		proc := NewScanProcessor(conn, db.New(conn), cfg, dir)
		proc.parsers = map[string]parser.Parser{"epub": hashOrFailParser{failSubstr: "bad"}}
		return proc
	}

	for run, resume := range []bool{false, true} {
		proc := newProc(resume)
		if err := proc.Process(context.Background()); err != nil {
			t.Fatal(err)
		}
		if got := proc.Stats(); got.Failed != 2 || got.Parsed != 0 {
			t.Fatalf("run %d: expected 0 parsed 2 failed, got %+v", run, got)
		}
	}
	var n int
	if err := conn.QueryRow("SELECT count(*) FROM book_files").Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("expected no book_files rows from failed files, got %d", n)
	}
	var books int
	if err := conn.QueryRow("SELECT count(*) FROM books").Scan(&books); err != nil {
		t.Fatal(err)
	}
	if books != 0 {
		t.Fatalf("expected no books from failed files, got %d", books)
	}
}

// hashParser derives a deterministic hash from the content length so tests can
// create files that share a hash.
type hashParser struct{}

func (hashParser) Parse(b book.Blob) (parser.Metadata, error) {
	content, err := readBlobAll(b)
	if err != nil {
		return parser.Metadata{}, err
	}
	return parser.Metadata{
		Authors: []string{"Author"},
		Title:   "Title",
		Hash:    fmt.Sprintf("hash-%d", len(content)),
	}, nil
}

// TestResumeSkipsDuplicates pins the duplicate resume path: a recorded
// duplicate is skipped by path existence instead of being reparsed (which
// previously also failed on reinserting the duplicate row with a UNIQUE
// constraint violation, flipping Succeeded to Failed).
func TestResumeSkipsDuplicates(t *testing.T) {
	dir := t.TempDir()
	// a and b share a hash (same content length); c is unique.
	for name, content := range map[string]string{
		"a.epub": "one",
		"b.epub": "one",
		"c.epub": "twelve",
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	conn, err := database.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	q := db.New(conn)
	ctx := context.Background()

	newProc := func(resume bool) *ScanProcessor {
		cfg := DefaultScanProcessorConfig()
		cfg.Resume = resume
		proc := NewScanProcessor(conn, q, cfg, dir)
		proc.parsers = map[string]parser.Parser{"epub": hashParser{}}
		return proc
	}

	proc1 := newProc(false)
	if err := proc1.Process(ctx); err != nil {
		t.Fatal(err)
	}
	if got := proc1.Stats(); got.Parsed != 3 || got.Failed != 0 {
		t.Fatalf("first run: expected 3 parsed 0 failed, got %+v", got)
	}
	var dups int
	if err := conn.QueryRow("SELECT count(*) FROM book_file_duplicates").Scan(&dups); err != nil {
		t.Fatal(err)
	}
	if dups != 1 {
		t.Fatalf("expected 1 duplicate row, got %d", dups)
	}
	// The duplicate row carries the file stat used for resume skipping.
	// Either of the identical files can lose the hash race and land here.
	var dupPath string
	if err := conn.QueryRow("SELECT path FROM book_file_duplicates").Scan(&dupPath); err != nil {
		t.Fatal(err)
	}
	t.Logf("duplicate path: %q", dupPath)

	proc2 := newProc(true)
	if err := proc2.Process(ctx); err != nil {
		t.Fatal(err)
	}
	if got := proc2.Stats(); got.Parsed != 0 || got.Failed != 0 || got.Skipped != 3 {
		t.Fatalf("resume run: expected 0 parsed 0 failed 3 skipped, got %+v", got)
	}
	var files, books int
	if err := conn.QueryRow("SELECT count(*) FROM book_files").Scan(&files); err != nil {
		t.Fatal(err)
	}
	if err := conn.QueryRow("SELECT count(*) FROM books").Scan(&books); err != nil {
		t.Fatal(err)
	}
	if err := conn.QueryRow("SELECT count(*) FROM book_file_duplicates").Scan(&dups); err != nil {
		t.Fatal(err)
	}
	if files != 2 || dups != 1 || books != 2 {
		t.Fatalf("after resume: files=%d dups=%d books=%d, want 2/1/2", files, dups, books)
	}
}

func TestProcessStoresInDatabase(t *testing.T) {
	dir := t.TempDir()
	// a and b share a hash (same content length); c is unique.
	for name, content := range map[string]string{
		"a.epub": "one",
		"b.epub": "one",
		"c.epub": "twelve",
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	conn, err := database.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = conn.Close()
	}()

	proc := NewScanProcessor(conn, db.New(conn), DefaultScanProcessorConfig(), dir)
	proc.parsers = map[string]parser.Parser{"epub": hashParser{}}

	if err := proc.Process(context.Background()); err != nil {
		t.Fatal(err)
	}
	if failures := proc.Failures(); len(failures) != 0 {
		t.Fatalf("unexpected failures: %v", failures)
	}

	var (
		bookFiles  int
		duplicates int
		authors    int
		links      int
		books      int
		bookLinks  int
	)
	for _, q := range []struct {
		query string
		dest  *int
	}{
		{"SELECT count(*) FROM book_files", &bookFiles},
		{"SELECT count(*) FROM book_file_duplicates", &duplicates},
		{"SELECT count(*) FROM authors", &authors},
		{"SELECT count(*) FROM book_file_authors", &links},
		{"SELECT count(*) FROM books", &books},
		{"SELECT count(*) FROM book_book_files", &bookLinks},
	} {
		if err := conn.QueryRow(q.query).Scan(q.dest); err != nil {
			t.Fatal(err)
		}
	}

	if bookFiles != 2 {
		t.Errorf("expected 2 book_files, got %d", bookFiles)
	}
	if duplicates != 1 {
		t.Errorf("expected 1 duplicate, got %d", duplicates)
	}
	if authors != 1 {
		t.Errorf("expected 1 author, got %d", authors)
	}
	if links != 2 {
		t.Errorf("expected 2 author links, got %d", links)
	}
	// hashParser returns no MinHash, so LSH cannot match: each file gets its
	// own book, and the exact duplicate gets none.
	if books != 2 {
		t.Errorf("expected 2 books, got %d", books)
	}
	if bookLinks != 2 {
		t.Errorf("expected 2 book links, got %d", bookLinks)
	}
}

func TestProcessSkipsEmptyHash(t *testing.T) {
	dir := t.TempDir()
	for name, content := range map[string]string{
		"a.epub": "one",
		"b.epub": "two",
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	conn, err := database.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = conn.Close()
	}()

	proc := NewScanProcessor(conn, db.New(conn), DefaultScanProcessorConfig(), dir)
	proc.parsers = map[string]parser.Parser{"epub": stubParser{}}

	if err := proc.Process(context.Background()); err != nil {
		t.Fatal(err)
	}
	if failures := proc.Failures(); len(failures) != 0 {
		t.Fatalf("unexpected failures: %v", failures)
	}

	var n int
	if err := conn.QueryRow("SELECT count(*) FROM book_files").Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("expected no book_files for empty hashes, got %d", n)
	}
}

type lshMockParser struct {
	sigs map[string][]uint64
}

func (m lshMockParser) Parse(b book.Blob) (parser.Metadata, error) {
	content, err := readBlobAll(b)
	if err != nil {
		return parser.Metadata{}, err
	}
	name := string(content)
	sig, ok := m.sigs[name]
	if !ok {
		return parser.Metadata{}, fmt.Errorf("unknown mock book: %s", name)
	}
	return parser.Metadata{
		Authors: []string{"Test Author"},
		Title:   "Book " + name,
		Hash:    "hash-" + name,
		MinHash: sig,
	}, nil
}

func TestProcessMinHashLSHGrouping(t *testing.T) {
	dir := t.TempDir()

	// Base signature
	sigA := make([]uint64, 128)
	for i := range sigA {
		sigA[i] = uint64(i*1000 + 7)
	}

	// Near duplicate: 96% similar (only 5 entries differ)
	sigB := make([]uint64, 128)
	copy(sigB, sigA)
	for i := range 5 {
		sigB[i] = ^uint64(i)
	}

	// Completely different book: all entries differ
	sigC := make([]uint64, 128)
	for i := range sigC {
		sigC[i] = uint64(999999 + i)
	}

	for filename, content := range map[string]string{
		"book1_v1.epub": "book1_v1",
		"book1_v2.mobi": "book1_v2",
		"book2.epub":    "book2",
	} {
		if err := os.WriteFile(filepath.Join(dir, filename), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	conn, err := database.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = conn.Close()
	}()

	cfg := DefaultScanProcessorConfig()
	cfg.ParseWorkers = 1 // deterministic single-worker for sequential matching
	proc := NewScanProcessor(conn, db.New(conn), cfg, dir)
	mock := lshMockParser{
		sigs: map[string][]uint64{
			"book1_v1": sigA,
			"book1_v2": sigB,
			"book2":    sigC,
		},
	}
	proc.parsers = map[string]parser.Parser{"epub": mock, "mobi": mock}

	if err := proc.Process(context.Background()); err != nil {
		t.Fatal(err)
	}
	if failures := proc.Failures(); len(failures) != 0 {
		t.Fatalf("unexpected failures: %v", failures)
	}

	var bookFilesCount, booksCount int
	if err := conn.QueryRow("SELECT count(*) FROM book_files").Scan(&bookFilesCount); err != nil {
		t.Fatal(err)
	}
	if err := conn.QueryRow("SELECT count(*) FROM books").Scan(&booksCount); err != nil {
		t.Fatal(err)
	}

	if bookFilesCount != 3 {
		t.Fatalf("expected 3 book_files, got %d", bookFilesCount)
	}
	// book1_v1 and book1_v2 should be grouped under 1 book, book2 has 1 book -> total 2 books
	if booksCount != 2 {
		t.Fatalf("expected 2 books, got %d", booksCount)
	}

	// Verify that book1_v1 and book1_v2 belong to the same book
	var book1ID, book2ID int64
	err = conn.QueryRow(`
		SELECT bbf.book_id
		FROM book_book_files bbf
		JOIN book_files bf ON bf.id = bbf.book_file_id
		WHERE bf.hash = 'hash-book1_v1'
	`).Scan(&book1ID)
	if err != nil {
		t.Fatalf("query book1_v1 book_id: %v", err)
	}

	err = conn.QueryRow(`
		SELECT bbf.book_id
		FROM book_book_files bbf
		JOIN book_files bf ON bf.id = bbf.book_file_id
		WHERE bf.hash = 'hash-book1_v2'
	`).Scan(&book2ID)
	if err != nil {
		t.Fatalf("query book1_v2 book_id: %v", err)
	}

	if book1ID != book2ID {
		t.Errorf("expected book1_v1 and book1_v2 to share the same book_id, got %d vs %d", book1ID, book2ID)
	}

	var otherBookID int64
	err = conn.QueryRow(`
		SELECT bbf.book_id
		FROM book_book_files bbf
		JOIN book_files bf ON bf.id = bbf.book_file_id
		WHERE bf.hash = 'hash-book2'
	`).Scan(&otherBookID)
	if err != nil {
		t.Fatalf("query book2 book_id: %v", err)
	}

	if otherBookID == book1ID {
		t.Errorf("expected book2 to have a different book_id than book1, got %d", otherBookID)
	}
}

// TestProcessMinHashLSHThresholdBoundary pins the 75% Jaccard grouping
// threshold from both sides. All three signatures share intact LSH bands with
// the base, so every pair reaches the Jaccard verification — the grouping
// decision itself is what differs:
//   - sigAbove: 102/128 = 79.7% similar -> must group with the base.
//   - sigBelow: 89/128 = 69.5% similar -> must NOT group (own book).
//
// sigBelow is also <75% away from sigAbove (63/128 = 49.2%), so the outcome
// is independent of processing order.
func TestProcessMinHashLSHThresholdBoundary(t *testing.T) {
	dir := t.TempDir()

	sigBase := make([]uint64, 128)
	for i := range sigBase {
		sigBase[i] = uint64(i*1000 + 7)
	}

	// 26 diffs confined to bands 0..5; bands 6..24 stay intact.
	sigAbove := make([]uint64, 128)
	copy(sigAbove, sigBase)
	for i := range 26 {
		sigAbove[i] = ^uint64(i)
	}

	// 39 diffs confined to bands 5..12; bands 0..4 and 13..24 stay intact.
	sigBelow := make([]uint64, 128)
	copy(sigBelow, sigBase)
	for i := 26; i < 65; i++ {
		sigBelow[i] = ^uint64(i)
	}

	for filename, content := range map[string]string{
		"base.epub":  "base",
		"above.mobi": "above",
		"below.epub": "below",
	} {
		if err := os.WriteFile(filepath.Join(dir, filename), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	conn, err := database.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = conn.Close()
	}()

	cfg := DefaultScanProcessorConfig()
	cfg.ParseWorkers = 1 // deterministic single-worker for sequential matching
	proc := NewScanProcessor(conn, db.New(conn), cfg, dir)
	mock := lshMockParser{
		sigs: map[string][]uint64{
			"base":  sigBase,
			"above": sigAbove,
			"below": sigBelow,
		},
	}
	proc.parsers = map[string]parser.Parser{"epub": mock, "mobi": mock}

	if err := proc.Process(context.Background()); err != nil {
		t.Fatal(err)
	}
	if failures := proc.Failures(); len(failures) != 0 {
		t.Fatalf("unexpected failures: %v", failures)
	}

	bookIDOf := func(hash string) int64 {
		t.Helper()
		var id int64
		err := conn.QueryRow(`
			SELECT bbf.book_id
			FROM book_book_files bbf
			JOIN book_files bf ON bf.id = bbf.book_file_id
			WHERE bf.hash = ?
		`, hash).Scan(&id)
		if err != nil {
			t.Fatalf("query book_id for %s: %v", hash, err)
		}
		return id
	}

	baseID := bookIDOf("hash-base")
	aboveID := bookIDOf("hash-above")
	belowID := bookIDOf("hash-below")

	if aboveID != baseID {
		t.Errorf("expected above-threshold file to share base's book_id, got %d vs %d", aboveID, baseID)
	}
	if belowID == baseID {
		t.Errorf("expected below-threshold file to have its own book_id, got %d", belowID)
	}

	var booksCount int
	if err := conn.QueryRow("SELECT count(*) FROM books").Scan(&booksCount); err != nil {
		t.Fatal(err)
	}
	if booksCount != 2 {
		t.Errorf("expected 2 books, got %d", booksCount)
	}
}

func writeZipWithMembers(t *testing.T, path string, members map[string]string) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	w := zip.NewWriter(f)
	for name, content := range members {
		fw, err := w.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := fw.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
}

// TestExtStatsCountsFilesystemAndArchiveMembers pins the --ext-stats rule:
// supported + unsupported files are counted on the filesystem and inside
// archives, while supported archives are treated as folders and excluded.
func TestExtStatsCountsFilesystemAndArchiveMembers(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.epub"), []byte("epub-a"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "b.pdf"), []byte("pdf-b"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "noext"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	writeZipWithMembers(t, filepath.Join(dir, "c.zip"), map[string]string{
		"d.epub": "epub-d",
		"e.pdf":  "pdf-e",
		"f.txt":  "txt-f",
	})

	cfg := DefaultScanProcessorConfig()
	cfg.CollectExtStats = true
	proc := newTestProcessor(t, cfg, dir)
	proc.parsers = map[string]parser.Parser{
		"epub": hashOrFailParser{failSubstr: "\x00-never-matches"},
	}
	if err := proc.Process(context.Background()); err != nil {
		t.Fatal(err)
	}
	got := proc.Stats().ExtCounts
	want := map[string]int64{
		"epub":    2,
		"pdf":     2,
		"txt":     1,
		"(noext)": 1,
	}
	if len(got) != len(want) {
		t.Fatalf("expected ext counts %v, got %v", want, got)
	}
	for ext, n := range want {
		if got[ext] != n {
			t.Errorf("expected %s=%d, got %d (all: %v)", ext, n, got[ext], got)
		}
	}
	if _, ok := got["zip"]; ok {
		t.Errorf("archives must be excluded, got zip in %v", got)
	}
}

// TestExtStatsDisabledReturnsNil ensures no counting overhead or output data
// when collection is off.
func TestExtStatsDisabledReturnsNil(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.epub"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	proc := newTestProcessor(t, DefaultScanProcessorConfig(), dir)
	proc.parsers = map[string]parser.Parser{"epub": hashParser{}}
	if err := proc.Process(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := proc.Stats().ExtCounts; got != nil {
		t.Fatalf("expected nil ExtCounts when disabled, got %v", got)
	}
}

// TestExtStatsIncludesResumeSkipped pins that resume-skipped files are still
// counted: the second (resume) run must report the same per-extension counts
// as the first run, even though supported files are skipped without reading.
func TestExtStatsIncludesResumeSkipped(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.epub"), []byte("epub-a"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "b.pdf"), []byte("pdf-b"), 0o644); err != nil {
		t.Fatal(err)
	}
	writeZipWithMembers(t, filepath.Join(dir, "c.zip"), map[string]string{
		"d.epub": "epub-d",
		"e.pdf":  "pdf-e",
	})

	conn, err := database.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	q := db.New(conn)
	ctx := context.Background()

	newProc := func(resume bool) *ScanProcessor {
		cfg := DefaultScanProcessorConfig()
		cfg.Resume = resume
		cfg.CollectExtStats = true
		proc := NewScanProcessor(conn, q, cfg, dir)
		proc.parsers = map[string]parser.Parser{
			"epub": hashOrFailParser{failSubstr: "\x00-never-matches"},
		}
		return proc
	}

	want := map[string]int64{"epub": 2, "pdf": 2}

	proc1 := newProc(false)
	if err := proc1.Process(ctx); err != nil {
		t.Fatal(err)
	}
	if got := proc1.Stats().ExtCounts; !equalExtCounts(got, want) {
		t.Fatalf("first run: expected ext counts %v, got %v", want, got)
	}

	proc2 := newProc(true)
	if err := proc2.Process(ctx); err != nil {
		t.Fatal(err)
	}
	stats2 := proc2.Stats()
	if stats2.Skipped != 2 {
		t.Fatalf("resume run: expected 2 skipped (a.epub + c.zip!d.epub), got %+v", stats2)
	}
	if !equalExtCounts(stats2.ExtCounts, want) {
		t.Fatalf("resume run: expected ext counts %v (skipped files included), got %v", want, stats2.ExtCounts)
	}
}

func equalExtCounts(got, want map[string]int64) bool {
	if len(got) != len(want) {
		return false
	}
	for k, v := range want {
		if got[k] != v {
			return false
		}
	}
	return true
}
