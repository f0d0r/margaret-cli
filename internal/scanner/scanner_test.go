package scanner

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func TestScan(t *testing.T) {
	dir := t.TempDir()

	files := map[string]string{
		"a.epub":     "epub",
		"b.mobi":     "mobi",
		"c.PDF":      "pdf",
		"sub/d.azw3": "azw3",
	}
	excluded := []string{"readme.txt", "sub/image.png"}

	for name := range files {
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range excluded {
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	var (
		mu      sync.Mutex
		results []Result
	)
	_, err := New(Config{
		OnResult: func(r Result) {
			mu.Lock()
			results = append(results, r)
			mu.Unlock()
		},
	}).Scan(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}

	if len(results) != len(files) {
		t.Fatalf("expected %d results, got %d: %v", len(files), len(results), results)
	}

	got := map[string]string{}
	for _, r := range results {
		rel, err := filepath.Rel(dir, r.Path)
		if err != nil {
			t.Fatal(err)
		}
		got[filepath.ToSlash(rel)] = r.Format
	}

	for name, format := range files {
		if got[name] != format {
			t.Errorf("expected %s (%s), got %v", name, format, got[name])
		}
	}
}

func TestScanMissingRoot(t *testing.T) {
	if _, err := New(DefaultConfig()).Scan(context.Background(), filepath.Join(t.TempDir(), "nope")); err == nil {
		t.Fatal("expected error for missing root")
	}
}

func TestScanParallel(t *testing.T) {
	dir := t.TempDir()
	expected := 0
	for i := range 20 {
		sub := filepath.Join(dir, fmt.Sprintf("d%02d", i))
		if err := os.MkdirAll(sub, 0o755); err != nil {
			t.Fatal(err)
		}
		for j := range 5 {
			name := filepath.Join(sub, fmt.Sprintf("b%02d.epub", j))
			if err := os.WriteFile(name, []byte("x"), 0o644); err != nil {
				t.Fatal(err)
			}
			expected++
		}
	}

	var (
		mu       sync.Mutex
		results  []Result
		maxFound int64
		maxDirs  int64
	)
	_, err := New(Config{
		Workers: 4,
		OnResult: func(r Result) {
			mu.Lock()
			results = append(results, r)
			mu.Unlock()
		},
		OnReport: func(p Progress) {
			mu.Lock()
			if p.Found > maxFound {
				maxFound = p.Found
			}
			if p.Dirs > maxDirs {
				maxDirs = p.Dirs
			}
			mu.Unlock()
		},
	}).Scan(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}

	if len(results) != expected {
		t.Fatalf("expected %d results, got %d", expected, len(results))
	}
	if maxFound != int64(expected) {
		t.Errorf("expected progress found=%d, got %d", expected, maxFound)
	}
	if maxDirs != 21 {
		t.Errorf("expected progress dirs=21, got %d", maxDirs)
	}
}

func TestScanReportsUnreadableDir(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root, permissions are bypassed")
	}
	dir := t.TempDir()
	sub := filepath.Join(dir, "locked")
	if err := os.MkdirAll(filepath.Join(sub, "nested"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sub, "nested", "a.epub"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(sub, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(sub, 0o755) })

	errCount, err := New(DefaultConfig()).Scan(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	if errCount == 0 {
		t.Fatal("expected at least one error")
	}
}

func TestFormatOf(t *testing.T) {
	cases := []struct {
		name   string
		format string
		ok     bool
	}{
		{"a.epub", "epub", true},
		{"b.PDF", "pdf", true},
		{"c.tar.gz", "tgz", true},
		{"d.TGZ", "tgz", true},
		{"e.tar.bz2", "tbz2", true},
		{"f.tbz2", "tbz2", true},
		{"g.zip", "zip", true},
		{"h.7z", "7z", true},
		{"i.rar", "rar", true},
		{"j.txt", "", false},
		{"noext", "", false},
	}
	for _, c := range cases {
		format, ok := FormatOf(c.name)
		if ok != c.ok || (ok && format != c.format) {
			t.Errorf("FormatOf(%q) = (%q, %v), want (%q, %v)", c.name, format, ok, c.format, c.ok)
		}
	}
}
