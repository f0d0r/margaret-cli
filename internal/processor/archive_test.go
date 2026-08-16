package processor

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/f0d0r/margaret-ebook-library/pkg/model"
	"github.com/f0d0r/margaret-tools/internal/parser"
)

// stubParser always succeeds without inspecting the file, so tests that focus
// on archive handling (whose fixtures are not real ebooks) can run without a
// parser that validates content.
type stubParser struct{}

func (stubParser) Parse(_ model.Blob) (parser.Metadata, error) {
	return parser.Metadata{}, nil
}

func collectResults(t *testing.T, cfg ScanProcessorConfig, dir string) ([]Result, []Failure) {
	t.Helper()
	return collectResultsWithParser(t, cfg, dir, stubParser{})
}

func collectResultsWithParser(t *testing.T, cfg ScanProcessorConfig, dir string, p parser.Parser) ([]Result, []Failure) {
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
	proc := newTestProcessor(t, cfg, dir)
	proc.parsers = map[string]parser.Parser{"epub": p, "mobi": p}
	err := proc.Process(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return results, proc.Failures()
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

// stageFixture copies a binary fixture from testdata into dir and returns the
// path of the copy.
func stageFixture(t *testing.T, dir, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, b, 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestProcessZip(t *testing.T) {
	dir := t.TempDir()
	writeZip(t, filepath.Join(dir, "bundle.zip"), map[string]string{
		"readme.txt": "ignore me",
		"sub/a.epub": "a",
		"sub/b.mobi": "b",
	})

	results, failures := collectResults(t, DefaultScanProcessorConfig(), dir)
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

	results, failures := collectResults(t, DefaultScanProcessorConfig(), dir)
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

	cfg := DefaultScanProcessorConfig()
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
	defer func() {
		_ = f.Close()
	}()

	results, failures := collectResults(t, DefaultScanProcessorConfig(), dir)
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

	results, failures := collectResults(t, DefaultScanProcessorConfig(), dir)
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

	results, failures := collectResults(t, DefaultScanProcessorConfig(), dir)
	if len(failures) != 0 {
		t.Fatalf("unexpected failures: %v", failures)
	}
	got := pathsOf(results)
	if len(got) != 1 || got[0] != filepath.Join(dir, "book.epub") {
		t.Fatalf("expected gz ebook result, got %v", got)
	}
}

func TestProcessRar(t *testing.T) {
	dir := t.TempDir()
	path := stageFixture(t, dir, "bundle.rar")

	results, failures := collectResults(t, DefaultScanProcessorConfig(), dir)
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
	for i, w := range want {
		if got[i] != w {
			t.Errorf("expected path %q, got %q", w, got[i])
		}
	}
}

func TestProcess7z(t *testing.T) {
	dir := t.TempDir()
	path := stageFixture(t, dir, "bundle.7z")

	results, failures := collectResults(t, DefaultScanProcessorConfig(), dir)
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
	for i, w := range want {
		if got[i] != w {
			t.Errorf("expected path %q, got %q", w, got[i])
		}
	}
}

func TestNoTempFilesLeaked(t *testing.T) {
	before, err := os.ReadDir(os.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	leakedBefore := map[string]bool{}
	for _, e := range before {
		leakedBefore[e.Name()] = true
	}

	dir := t.TempDir()
	writeZip(t, filepath.Join(dir, "bundle.zip"), map[string]string{"a.epub": "a"})
	stageFixture(t, dir, "bundle.rar")
	stageFixture(t, dir, "bundle.7z")

	results, failures := collectResults(t, DefaultScanProcessorConfig(), dir)
	if len(failures) != 0 {
		t.Fatalf("unexpected failures: %v", failures)
	}
	if len(results) != 5 {
		t.Fatalf("expected 5 results, got %d: %v", len(results), pathsOf(results))
	}

	after, err := os.ReadDir(os.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range after {
		if (strings.HasPrefix(e.Name(), "margaret-archive-") || strings.HasPrefix(e.Name(), "margaret-ebook-")) && !leakedBefore[e.Name()] {
			t.Errorf("temporary file leaked into %s: %s", os.TempDir(), e.Name())
		}
	}
}

func TestProcessRarInside7z(t *testing.T) {
	dir := t.TempDir()
	path := stageFixture(t, dir, "outer.7z")

	results, failures := collectResults(t, DefaultScanProcessorConfig(), dir)
	if len(failures) != 0 {
		t.Fatalf("unexpected failures: %v", failures)
	}
	got := pathsOf(results)
	want := []string{path + "!inner.rar!c.epub"}
	if len(got) != len(want) {
		t.Fatalf("expected %d results, got %d: %v", len(want), len(got), got)
	}
	for i, w := range want {
		if got[i] != w {
			t.Errorf("expected path %q, got %q", w, got[i])
		}
	}
}

func TestProcessTarContainingRar(t *testing.T) {
	dir := t.TempDir()
	path := stageFixture(t, dir, "tar-outer-rar.tar")

	results, failures := collectResults(t, DefaultScanProcessorConfig(), dir)
	if len(failures) != 0 {
		t.Fatalf("unexpected failures: %v", failures)
	}
	got := pathsOf(results)
	want := []string{path + "!inner.rar!c.epub"}
	if len(got) != len(want) {
		t.Fatalf("expected %d results, got %d: %v", len(want), len(got), got)
	}
	for i, w := range want {
		if got[i] != w {
			t.Errorf("expected path %q, got %q", w, got[i])
		}
	}
}

func TestProcessPasswordProtectedRar(t *testing.T) {
	dir := t.TempDir()
	path := stageFixture(t, dir, "secret.rar")

	cfg := DefaultScanProcessorConfig()
	cfg.Password = "letmein"
	results, failures := collectResultsWithParser(t, cfg, dir, readingParser{})
	if len(failures) != 0 {
		t.Fatalf("unexpected failures: %v", failures)
	}
	got := pathsOf(results)
	if len(got) != 1 || got[0] != path+"!a.epub" {
		t.Fatalf("expected 1 result from password-protected rar, got %v", got)
	}
}

func TestProcessPasswordProtected7z(t *testing.T) {
	dir := t.TempDir()
	path := stageFixture(t, dir, "secret.7z")

	cfg := DefaultScanProcessorConfig()
	cfg.Password = "letmein"
	results, failures := collectResultsWithParser(t, cfg, dir, readingParser{})
	if len(failures) != 0 {
		t.Fatalf("unexpected failures: %v", failures)
	}
	got := pathsOf(results)
	if len(got) != 1 || got[0] != path+"!a.epub" {
		t.Fatalf("expected 1 result from password-protected 7z, got %v", got)
	}
}

func TestProcessEncryptedRarWithoutPassword(t *testing.T) {
	dir := t.TempDir()
	path := stageFixture(t, dir, "secret.rar")

	results, failures := collectResultsWithParser(t, DefaultScanProcessorConfig(), dir, readingParser{})
	if len(results) != 0 {
		t.Fatalf("expected no results, got %v", pathsOf(results))
	}
	if len(failures) != 1 {
		t.Fatalf("expected 1 failure, got %d: %v", len(failures), failures)
	}
	if failures[0].Path != path+"!a.epub" {
		t.Errorf("expected failure path %q, got %q", path+"!a.epub", failures[0].Path)
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
	cfg := DefaultScanProcessorConfig()
	cfg.OnResult = func(r Result) {
		mu.Lock()
		results = append(results, r)
		mu.Unlock()
	}
	proc := newTestProcessor(t, cfg, dir)
	proc.parsers = map[string]parser.Parser{"epub": readerParser{failContent: "will fail"}}
	err := proc.Process(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	failures := proc.Failures()
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

func (p readerParser) Parse(b model.Blob) (parser.Metadata, error) {
	content, err := readBlobAll(b)
	if err != nil {
		return parser.Metadata{}, err
	}
	if strings.Contains(string(content), p.failContent) {
		return parser.Metadata{}, errors.New("boom")
	}
	return parser.Metadata{}, nil
}

// readingParser reads the whole file, so decryption errors in encrypted
// archive members surface while the member is being spooled to disk.
type readingParser struct{}

func (readingParser) Parse(b model.Blob) (parser.Metadata, error) {
	_, err := readBlobAll(b)
	return parser.Metadata{}, err
}
