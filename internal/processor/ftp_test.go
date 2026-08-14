package processor

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/f0d0r/margaret-tools/internal/ftptest"
	"github.com/f0d0r/margaret-tools/internal/source"
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
	factory, err := source.FactoryForURL(url, source.FTPOptions{})
	if err != nil {
		t.Fatal(err)
	}

	var (
		mu      sync.Mutex
		results []Result
	)
	proc := New(fakeParser{failSubstr: "bad/"}, Config{
		ScanWorkers:   2,
		ParseWorkers:  2,
		SourceFactory: factory,
		OnResult: func(r Result) {
			mu.Lock()
			results = append(results, r)
			mu.Unlock()
		},
	})

	failures, err := proc.Process(context.Background(), url)
	if err != nil {
		t.Fatal(err)
	}

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
	factory, err := source.FactoryForURL("ftp://127.0.0.1:1/nope", source.FTPOptions{})
	if err != nil {
		t.Fatal(err)
	}
	proc := New(fakeParser{}, Config{SourceFactory: factory})
	failures, err := proc.Process(context.Background(), "ftp://127.0.0.1:1/nope")
	if err == nil {
		t.Fatal("expected fatal error for an unreachable FTP root")
	}
	if len(failures) != 0 {
		t.Fatalf("expected no failures, got %v", failures)
	}
}
