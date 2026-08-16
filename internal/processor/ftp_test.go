package processor

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/f0d0r/margaret-tools/internal/ftptest"
	"github.com/f0d0r/margaret-tools/internal/parser"
)

func TestProcessFTP(t *testing.T) {
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

	srv, err := ftptest.NewServer(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()

	url := "ftp://" + srv.Addr()

	var (
		mu      sync.Mutex
		results []Result
	)
	proc := newTestProcessor(t, ScanProcessorConfig{
		ScanWorkers:  2,
		ParseWorkers: 2,
		OnResult: func(r Result) {
			mu.Lock()
			results = append(results, r)
			mu.Unlock()
		},
	}, url)

	proc.parsers = map[string]parser.Parser{
		"epub": fakeParser{failSubstr: "bad/"},
		"mobi": fakeParser{failSubstr: "bad/"},
	}

	err = proc.Process(context.Background())
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
	for _, r := range results {
		if !strings.HasPrefix(r.File.Path, url) {
			t.Errorf("expected result path %q to start with %q", r.File.Path, url)
		}
	}
}

func TestProcessFTPMissingRoot(t *testing.T) {
	proc := newTestProcessor(t, ScanProcessorConfig{}, "ftp://127.0.0.1:1/nope")
	err := proc.Process(context.Background())
	if err == nil {
		t.Fatal("expected fatal error for an unreachable FTP root")
	}
	failures := proc.Failures()
	if len(failures) != 0 {
		t.Fatalf("expected no failures, got %v", failures)
	}
}
