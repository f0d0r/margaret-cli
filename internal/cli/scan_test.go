package cli

import (
	"archive/zip"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/f0d0r/margaret-cli/internal/processor"
)

// withScanOutputs swaps the package-level scan output settings for the
// duration of a test and restores them afterwards. Tests in this package
// must not run in parallel: they share these globals.
func withScanOutputs(t *testing.T, dir string, report bool) (failures, duplicates, books string) {
	t.Helper()

	oldDB, oldFailures, oldDuplicates, oldBooks, oldReport, oldExt := dbPath, failuresOutPath, duplicatesOut, booksOut, reportEnabled, extStats
	t.Cleanup(func() {
		dbPath, failuresOutPath, duplicatesOut, booksOut, reportEnabled, extStats = oldDB, oldFailures, oldDuplicates, oldBooks, oldReport, oldExt
	})

	dbPath = ":memory:"
	failures = filepath.Join(dir, "failures.json")
	duplicates = filepath.Join(dir, "duplicates.json")
	books = filepath.Join(dir, "books.json")
	failuresOutPath, duplicatesOut, booksOut, reportEnabled = failures, duplicates, books, report
	extStats = false
	return failures, duplicates, books
}

func writeTestEpub(t *testing.T, path, title, author, body string) {
	t.Helper()

	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	w := zip.NewWriter(f)
	for name, content := range map[string]string{
		"mimetype":               "application/epub+zip",
		"META-INF/container.xml": `<?xml version="1.0"?><container xmlns="urn:oasis:names:tc:opendocument:xmlns:container" version="1.0"><rootfiles><rootfile full-path="OEBPS/content.opf" media-type="application/oebps-package+xml"/></rootfiles></container>`,
		"OEBPS/content.opf":      `<?xml version="1.0" encoding="UTF-8"?><package xmlns="http://www.idpf.org/2007/opf" version="3.0"><metadata xmlns:dc="http://purl.org/dc/elements/1.1/"><dc:title>` + title + `</dc:title><dc:creator>` + author + `</dc:creator><dc:language>en</dc:language></metadata><manifest><item id="c" href="c.xhtml" media-type="application/xhtml+xml"/></manifest><spine><itemref idref="c"/></spine></package>`,
		"OEBPS/c.xhtml":          `<html><body>` + body + `</body></html>`,
	} {
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

// scanFixture creates a directory with two byte-identical epubs under
// different names, so the scan finds one book plus one duplicate.
func scanFixture(t *testing.T) string {
	t.Helper()

	dir := t.TempDir()
	booksDir := filepath.Join(dir, "books")
	if err := os.Mkdir(booksDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeTestEpub(t, filepath.Join(booksDir, "a.epub"), "Test Book", "Author A", "same")
	if err := copyFile(filepath.Join(booksDir, "a.epub"), filepath.Join(booksDir, "b.epub")); err != nil {
		t.Fatal(err)
	}
	return booksDir
}

func copyFile(src, dst string) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, data, 0o644)
}

func exists(t *testing.T, path string) bool {
	t.Helper()
	_, err := os.Stat(path)
	if err == nil {
		return true
	}
	if os.IsNotExist(err) {
		return false
	}
	t.Fatal(err)
	return false
}

func TestRunScanDefaultSkipsBulkReports(t *testing.T) {
	dir := t.TempDir()
	failures, duplicates, books := withScanOutputs(t, dir, false)

	if err := runScan(context.Background(), scanFixture(t), processor.DefaultScanProcessorConfig()); err != nil {
		t.Fatal(err)
	}

	if !exists(t, failures) {
		t.Errorf("expected %s to be written on every run", failures)
	}
	if exists(t, duplicates) {
		t.Errorf("expected no %s without --report", duplicates)
	}
	if exists(t, books) {
		t.Errorf("expected no %s without --report", books)
	}
}

func TestRunScanReportWritesBulkReports(t *testing.T) {
	dir := t.TempDir()
	failures, duplicates, books := withScanOutputs(t, dir, true)

	if err := runScan(context.Background(), scanFixture(t), processor.DefaultScanProcessorConfig()); err != nil {
		t.Fatal(err)
	}

	for _, path := range []string{failures, duplicates, books} {
		if !exists(t, path) {
			t.Errorf("expected %s to be written with --report", path)
		}
	}
}

func TestFormatPercent(t *testing.T) {
	cases := []struct {
		count, total int64
		want         string
	}{
		{340, 3400, "10%"},
		{34, 3400, "1%"},
		{1, 200, "0.5%"},
		{1, 3, "33.3%"},
		{0, 0, "0%"},
		{5, 0, "0%"},
	}
	for _, c := range cases {
		if got := formatPercent(c.count, c.total); got != c.want {
			t.Errorf("formatPercent(%d, %d) = %q, want %q", c.count, c.total, got, c.want)
		}
	}
}

func TestRunScanExtStatsCollects(t *testing.T) {
	dir := t.TempDir()
	withScanOutputs(t, dir, false)

	booksDir := t.TempDir()
	writeTestEpub(t, filepath.Join(booksDir, "a.epub"), "Test Book", "Author A", "body-a")
	if err := os.WriteFile(filepath.Join(booksDir, "b.pdf"), []byte("pdf"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(booksDir, "c.pdf"), []byte("pdf"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(booksDir, "d.txt"), []byte("txt"), 0o644); err != nil {
		t.Fatal(err)
	}
	writeTestZip(t, filepath.Join(booksDir, "e.zip"), map[string]string{
		"inner.pdf": "pdf-inner",
		"inner.txt": "txt-inner",
	})

	cfg := processor.DefaultScanProcessorConfig()
	cfg.CollectExtStats = true
	out := captureStdout(t, func() {
		if err := runScan(context.Background(), booksDir, cfg); err != nil {
			t.Fatal(err)
		}
	})

	// 1 epub + 2 pdf + 1 txt on disk + 1 pdf + 1 txt in the zip = 6 files;
	// the zip itself is treated as a folder and excluded.
	for _, want := range []string{
		"Extension stats (6 files):",
		"Supported:",
		"  epub: 1 (16.7%)",
		"Unsupported:",
		"  pdf: 3 (50%)",
		"  txt: 2 (33.3%)",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("expected output to contain %q, got:\n%s", want, out)
		}
	}
	if strings.Contains(out, "zip:") {
		t.Errorf("archives must be excluded from ext stats, got:\n%s", out)
	}
	// Unsupported entries sort by count desc: pdf (3) before txt (2).
	if !strings.Contains(out, "pdf: 3") || !strings.Contains(out, "txt: 2") ||
		strings.Index(out, "pdf: 3") > strings.Index(out, "txt: 2") {
		t.Errorf("expected pdf before txt by count desc, got:\n%s", out)
	}
	// Ext stats come after the existing summary.
	if !strings.Contains(out, "Total") || !strings.Contains(out, "Extension stats") ||
		strings.Index(out, "Total") > strings.Index(out, "Extension stats") {
		t.Errorf("expected ext stats after the summary, got:\n%s", out)
	}
}

func TestRunScanWithoutExtStatsPrintsNothing(t *testing.T) {
	dir := t.TempDir()
	withScanOutputs(t, dir, false)

	booksDir := t.TempDir()
	writeTestEpub(t, filepath.Join(booksDir, "a.epub"), "Test Book", "Author A", "body-a")
	if err := os.WriteFile(filepath.Join(booksDir, "b.pdf"), []byte("pdf"), 0o644); err != nil {
		t.Fatal(err)
	}

	out := captureStdout(t, func() {
		if err := runScan(context.Background(), booksDir, processor.DefaultScanProcessorConfig()); err != nil {
			t.Fatal(err)
		}
	})
	if strings.Contains(out, "Extension stats") {
		t.Errorf("expected no ext stats without the flag, got:\n%s", out)
	}
}

func writeTestZip(t *testing.T, path string, members map[string]string) {
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
