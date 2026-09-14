package cli

import (
	"archive/zip"
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/f0d0r/margaret-cli/internal/processor"
)

// withScanOutputs swaps the package-level scan output settings for the
// duration of a test and restores them afterwards. Tests in this package
// must not run in parallel: they share these globals.
func withScanOutputs(t *testing.T, dir string, report bool) (failures, duplicates, books string) {
	t.Helper()

	oldDB, oldFailures, oldDuplicates, oldBooks, oldReport := dbPath, failuresOutPath, duplicatesOut, booksOut, reportEnabled
	t.Cleanup(func() {
		dbPath, failuresOutPath, duplicatesOut, booksOut, reportEnabled = oldDB, oldFailures, oldDuplicates, oldBooks, oldReport
	})

	dbPath = ":memory:"
	failures = filepath.Join(dir, "failures.json")
	duplicates = filepath.Join(dir, "duplicates.json")
	books = filepath.Join(dir, "books.json")
	failuresOutPath, duplicatesOut, booksOut, reportEnabled = failures, duplicates, books, report
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
