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
	t.Cleanup(func() { _ = os.Chmod(sub, 0o755) })

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

func TestExtKey(t *testing.T) {
	cases := []struct {
		name      string
		ext       string
		isArchive bool
	}{
		{"a.epub", "epub", false},
		{"b.MOBI", "mobi", false},
		{"c.pdf", "pdf", false},
		{"d.EXE", "exe", false},
		{"e.zip", "zip", true},
		{"f.7z", "7z", true},
		{"g.tar.gz", "tgz", true},
		{"h.TGZ", "tgz", true},
		{"i.tar.bz2", "tbz2", true},
		{"j.tbz2", "tbz2", true},
		{"noext", "(noext)", false},
		{"a.zip!b.pdf", "pdf", false},
		{"a.zip!b.zip", "zip", true},
		{"/books/a.epub.gz", "gz", true},
		// Display paths of nested members are stripped to the innermost
		// name (processSingleStream records dropSuffix(displayPath)).
		{"outer.zip!data", "(noext)", false},
		{"outer.zip!inner.epub", "epub", false},
		{"outer.zip!data.gz", "gz", true},
		{"outer.zip!bundle/inner.zip!c.epub", "epub", false},
		{"outer.zip!bundle/inner.zip!README", "(noext)", false},
	}
	for _, c := range cases {
		ext, isArchive := ExtKey(c.name)
		if ext != c.ext || isArchive != c.isArchive {
			t.Errorf("ExtKey(%q) = (%q, %v), want (%q, %v)", c.name, ext, isArchive, c.ext, c.isArchive)
		}
	}
}

func TestIsSupportedExt(t *testing.T) {
	for _, ext := range []string{"epub", "mobi", "azw3", "azw", "prc"} {
		if !IsSupportedExt(ext) {
			t.Errorf("expected %q to be supported", ext)
		}
	}
	for _, ext := range []string{"pdf", "exe", "txt", "(noext)", "zip"} {
		if IsSupportedExt(ext) {
			t.Errorf("expected %q to be unsupported", ext)
		}
	}
}

func TestScanOnFileSeesAllFiles(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"a.epub", "b.pdf", "c.zip", "noext"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	var (
		mu    sync.Mutex
		files []string
	)
	_, err := New(Config{
		OnFile: func(name string) {
			mu.Lock()
			files = append(files, name)
			mu.Unlock()
		},
	}).Scan(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 4 {
		t.Fatalf("expected 4 OnFile calls, got %d: %v", len(files), files)
	}
}
