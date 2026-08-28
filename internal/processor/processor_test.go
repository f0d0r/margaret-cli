package processor

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/f0d0r/margaret-ebook-library/book"
	"github.com/f0d0r/margaret-tools/internal/database"
	"github.com/f0d0r/margaret-tools/internal/db"
	"github.com/f0d0r/margaret-tools/internal/parser"
)

// newTestProcessor builds a ScanProcessor backed by a temporary database that
// is removed when the test finishes.
func newTestProcessor(t *testing.T, cfg ScanProcessorConfig, root string) *ScanProcessor {
	t.Helper()
	conn, cleanup, err := database.OpenTemp()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(cleanup)
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

	conn, cleanup, err := database.OpenTemp()
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()

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
	)
	for _, q := range []struct {
		query string
		dest  *int
	}{
		{"SELECT count(*) FROM book_files", &bookFiles},
		{"SELECT count(*) FROM book_file_duplicates", &duplicates},
		{"SELECT count(*) FROM authors", &authors},
		{"SELECT count(*) FROM book_file_authors", &links},
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

	conn, cleanup, err := database.OpenTemp()
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()

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
