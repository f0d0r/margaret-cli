package processor

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/f0d0r/margaret-tools/internal/parser"
)

func collectResults(t *testing.T, cfg Config, dir string) ([]Result, []Failure) {
	t.Helper()
	var (
		mu      sync.Mutex
		results []Result
	)
	cfg.OnResult = func(r Result) {
		mu.Lock()
		results = append(results, r)
		mu.Unlock()
	}
	failures, err := New(parser.Dummy{}, cfg).Process(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	return results, failures
}

func pathsOf(results []Result) []string {
	paths := make([]string, 0, len(results))
	for _, r := range results {
		paths = append(paths, r.File.Path)
	}
	sort.Strings(paths)
	return paths
}

func writeZip(t *testing.T, path string, members map[string]string) {
	t.Helper()
	if err := os.WriteFile(path, zipBytes(t, members), 0o644); err != nil {
		t.Fatal(err)
	}
}

func zipBytes(t *testing.T, members map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, content := range members {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestProcessZip(t *testing.T) {
	dir := t.TempDir()
	writeZip(t, filepath.Join(dir, "bundle.zip"), map[string]string{
		"readme.txt": "ignore me",
		"sub/a.epub": "a",
		"sub/b.mobi": "b",
	})

	results, failures := collectResults(t, DefaultConfig(), dir)
	if len(failures) != 0 {
		t.Fatalf("unexpected failures: %v", failures)
	}
	got := pathsOf(results)
	want := []string{
		filepath.Join(dir, "bundle.zip") + "!sub/a.epub",
		filepath.Join(dir, "bundle.zip") + "!sub/b.mobi",
	}
	if len(got) != len(want) {
		t.Fatalf("expected %d results, got %d: %v", len(want), len(got), got)
	}
	for i, w := range want {
		if got[i] != w {
			t.Errorf("expected path %q, got %q", w, got[i])
		}
	}
}

func TestProcessNestedZip(t *testing.T) {
	dir := t.TempDir()
	inner := zipBytes(t, map[string]string{"c.epub": "c"})
	writeZip(t, filepath.Join(dir, "outer.zip"), map[string]string{
		"a.epub":           "a",
		"bundle/inner.zip": string(inner),
	})

	results, failures := collectResults(t, DefaultConfig(), dir)
	if len(failures) != 0 {
		t.Fatalf("unexpected failures: %v", failures)
	}
	got := pathsOf(results)
	want := []string{
		filepath.Join(dir, "outer.zip") + "!a.epub",
		filepath.Join(dir, "outer.zip") + "!bundle/inner.zip!c.epub",
	}
	if len(got) != len(want) {
		t.Fatalf("expected %d results, got %d: %v", len(want), len(got), got)
	}
	for i, w := range want {
		if got[i] != w {
			t.Errorf("expected path %q, got %q", w, got[i])
		}
	}
}

func TestProcessArchiveDepthLimit(t *testing.T) {
	dir := t.TempDir()
	// a.zip -> b.zip -> c.zip -> d.epub (3 nesting levels)
	c := zipBytes(t, map[string]string{"d.epub": "d"})
	b := zipBytes(t, map[string]string{"c.zip": string(c)})
	writeZip(t, filepath.Join(dir, "a.zip"), map[string]string{"b.zip": string(b)})

	cfg := DefaultConfig()
	cfg.MaxDepth = 2
	results, failures := collectResults(t, cfg, dir)
	if len(results) != 0 {
		t.Fatalf("expected no results, got %v", pathsOf(results))
	}
	if len(failures) != 1 {
		t.Fatalf("expected 1 depth failure, got %d: %v", len(failures), failures)
	}
	if !strings.Contains(failures[0].Err.Error(), "nesting depth") {
		t.Errorf("expected depth error, got %v", failures[0].Err)
	}
	if !strings.HasSuffix(failures[0].Path, "a.zip!b.zip!c.zip") {
		t.Errorf("expected failure path to be the too-deep archive, got %q", failures[0].Path)
	}
}

func TestProcessTar(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bundle.tar")

	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	tw := tar.NewWriter(f)
	for _, m := range []struct{ name, content string }{
		{"readme.txt", "ignore"},
		{"a.epub", "a"},
		{"sub/b.mobi", "b"},
	} {
		if err := tw.WriteHeader(&tar.Header{Name: m.name, Mode: 0o644, Size: int64(len(m.content))}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(m.content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	defer func () {
		_ = f.Close()
	}()

	results, failures := collectResults(t, DefaultConfig(), dir)
	if len(failures) != 0 {
		t.Fatalf("unexpected failures: %v", failures)
	}
	got := pathsOf(results)
	want := []string{
		path + "!a.epub",
		path + "!sub/b.mobi",
	}
	if len(got) != len(want) {
		t.Fatalf("expected %d results, got %d: %v", len(want), len(got), got)
	}
}

func TestProcessTgz(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bundle.tgz")

	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)
	content := "x"
	if err := tw.WriteHeader(&tar.Header{Name: "a.epub", Mode: 0o644, Size: int64(len(content))}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write([]byte(content)); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = f.Close()
	}()

	results, failures := collectResults(t, DefaultConfig(), dir)
	if len(failures) != 0 {
		t.Fatalf("unexpected failures: %v", failures)
	}
	got := pathsOf(results)
	if len(got) != 1 || got[0] != path+"!a.epub" {
		t.Fatalf("expected 1 result from tgz, got %v", got)
	}
}

func TestProcessGzSingleFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "book.epub.gz")

	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	gz := gzip.NewWriter(f)
	if _, err := gz.Write([]byte("epub content")); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = f.Close()
	}()

	results, failures := collectResults(t, DefaultConfig(), dir)
	if len(failures) != 0 {
		t.Fatalf("unexpected failures: %v", failures)
	}
	got := pathsOf(results)
	if len(got) != 1 || got[0] != filepath.Join(dir, "book.epub") {
		t.Fatalf("expected gz ebook result, got %v", got)
	}
}

func TestProcessFailuresReported(t *testing.T) {
	dir := t.TempDir()
	writeZip(t, filepath.Join(dir, "bad.zip"), map[string]string{
		"ok.epub":  "fine",
		"bad.epub": "will fail",
	})

	var mu sync.Mutex
	results := []Result{}
	cfg := DefaultConfig()
	cfg.OnResult = func(r Result) {
		mu.Lock()
		results = append(results, r)
		mu.Unlock()
	}
	failures, err := New(readerParser{failContent: "will fail"}, cfg).Process(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if len(failures) != 1 {
		t.Fatalf("expected 1 failure, got %d: %v", len(failures), failures)
	}
	if !strings.HasSuffix(failures[0].Path, "bad.zip!bad.epub") {
		t.Errorf("expected failure path to be the archive member, got %q", failures[0].Path)
	}
}

type readerParser struct {
	failContent string
}

func (p readerParser) Parse(_ context.Context, r io.Reader, _ string) (parser.Metadata, error) {
	var buf bytes.Buffer
	_, err := buf.ReadFrom(r)
	if err != nil {
		return parser.Metadata{}, err
	}
	if strings.Contains(buf.String(), p.failContent) {
		return parser.Metadata{}, errors.New("boom")
	}
	return parser.Metadata{}, nil
}
