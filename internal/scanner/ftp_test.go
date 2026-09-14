package scanner

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/f0d0r/margaret-cli/internal/ftptest"
	"github.com/f0d0r/margaret-cli/internal/source"
)

func TestScanFTP(t *testing.T) {
	root := t.TempDir()
	files := map[string]string{
		"a.epub":     "epub",
		"b.mobi":     "mobi",
		"sub/d.azw3": "azw3",
	}
	excluded := []string{"readme.txt", "sub/image.png"}

	for name := range files {
		path := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range excluded {
		path := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	srv, err := ftptest.NewServer(root)
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()

	url := "ftp://" + srv.Addr()

	var (
		mu      sync.Mutex
		results []Result
	)
	_, err = New(Config{
		Workers:    2,
		FTPOptions: source.FTPOptions{},
		OnResult: func(r Result) {
			mu.Lock()
			results = append(results, r)
			mu.Unlock()
		},
	}).Scan(context.Background(), url)
	if err != nil {
		t.Fatal(err)
	}

	if len(results) != len(files) {
		t.Fatalf("expected %d results, got %d: %v", len(files), len(results), results)
	}

	got := map[string]string{}
	for _, r := range results {
		rel := strings.TrimPrefix(r.Path, url)
		rel = strings.TrimPrefix(rel, "/")
		if r.Size <= 0 {
			t.Errorf("expected positive size for %s, got %d", r.Path, r.Size)
		}
		got[rel] = r.Format
	}
	for name, format := range files {
		if got[name] != format {
			t.Errorf("expected %s (%s), got %v", name, format, got)
		}
	}
}
