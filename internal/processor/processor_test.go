package processor

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/f0d0r/margaret-tools/internal/parser"
)

type fakeParser struct {
	failSubstr string
}

func (f fakeParser) Parse(_ context.Context, r io.Reader, _ string) (parser.Metadata, error) {
	content, _ := io.ReadAll(r)
	if strings.Contains(string(content), f.failSubstr) {
		return parser.Metadata{}, errors.New("boom")
	}
	return parser.Metadata{Author: "Author", Title: "Title"}, nil
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
	proc := New(fakeParser{failSubstr: "bad/"}, Config{
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
	})

	failures, err := proc.Process(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}

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
	proc := New(fakeParser{}, Config{})
	failures, err := proc.Process(context.Background(), filepath.Join(t.TempDir(), "nope"))
	if err == nil {
		t.Fatal("expected fatal error for missing root")
	}
	if len(failures) != 0 {
		t.Fatalf("expected no failures, got %v", failures)
	}
}

func TestProcessCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	proc := New(fakeParser{}, Config{})
	failures, err := proc.Process(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if len(failures) != 0 {
		t.Fatalf("expected no failures, got %v", failures)
	}
}
