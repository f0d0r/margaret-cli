package cli

import (
	"archive/zip"
	"context"
	"database/sql"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/f0d0r/margaret-cli/internal/db"
	"github.com/f0d0r/margaret-cli/internal/processor"
)

// withScanOutputs swaps the package-level scan output settings for the
// duration of a test and restores them afterwards. Tests in this package
// must not run in parallel: they share these globals.
func withScanOutputs(t *testing.T, dir string, report bool) (failures, duplicates, books, skipped string) {
	t.Helper()

	oldDB, oldFailures, oldSkipped, oldDuplicates, oldBooks, oldReport, oldExt := dbPath, failuresOutPath, skippedOutPath, duplicatesOut, booksOut, reportEnabled, extStats
	oldMinDocSize := minDocSize
	t.Cleanup(func() {
		dbPath, failuresOutPath, skippedOutPath, duplicatesOut, booksOut, reportEnabled, extStats = oldDB, oldFailures, oldSkipped, oldDuplicates, oldBooks, oldReport, oldExt
		minDocSize = oldMinDocSize
	})

	dbPath = ":memory:"
	failures = filepath.Join(dir, "failures.json")
	skipped = filepath.Join(dir, "skipped.json")
	duplicates = filepath.Join(dir, "duplicates.json")
	books = filepath.Join(dir, "books.json")
	failuresOutPath, skippedOutPath, duplicatesOut, booksOut, reportEnabled = failures, skipped, duplicates, books, report
	extStats = false
	return failures, duplicates, books, skipped
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

func TestScanMinDocSizeFlag(t *testing.T) {
	flag := scanCmd.Flags().Lookup("min-doc-size")
	if flag == nil {
		t.Fatal("min-doc-size flag is not registered")
	}
	if want := strconv.FormatInt(processor.DefaultMinDocumentSize, 10); flag.DefValue != want {
		t.Errorf("min-doc-size default = %q, want %q", flag.DefValue, want)
	}
}

func TestScanPDFToolsDirFlag(t *testing.T) {
	if scanCmd.Flags().Lookup("pdf-tools-dir") == nil {
		t.Fatal("pdf-tools-dir flag is not registered")
	}
}

func TestConfigurePDFToolsWarnsWhenMissing(t *testing.T) {
	cfg := processor.DefaultScanProcessorConfig()
	out := captureStderr(t, func() {
		configurePDFTools(&cfg, t.TempDir())
	})
	if !strings.Contains(out, "warning: PDF support disabled") {
		t.Errorf("expected a PDF warning, got %q", out)
	}
	if cfg.PDFTools.Available() {
		t.Errorf("PDF tools = %+v, want unavailable", cfg.PDFTools)
	}
}

func TestConfigurePDFToolsDetects(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"pdftotext", "pdfinfo"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	cfg := processor.DefaultScanProcessorConfig()
	out := captureStderr(t, func() {
		configurePDFTools(&cfg, dir)
	})
	if out != "" {
		t.Errorf("unexpected warning: %q", out)
	}
	if !cfg.PDFTools.Available() {
		t.Errorf("PDF tools = %+v, want available", cfg.PDFTools)
	}
}

func captureStderr(t *testing.T, f func()) string {
	t.Helper()
	old := os.Stderr
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stderr = w
	outCh := make(chan string, 1)
	go func() {
		data, _ := io.ReadAll(r)
		outCh <- string(data)
	}()
	f()
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	os.Stderr = old
	out := <-outCh
	_ = r.Close()
	return out
}

func TestRunScanDefaultSkipsBulkReports(t *testing.T) {
	dir := t.TempDir()
	failures, duplicates, books, skipped := withScanOutputs(t, dir, false)

	if err := runScan(context.Background(), scanFixture(t), processor.DefaultScanProcessorConfig()); err != nil {
		t.Fatal(err)
	}

	if !exists(t, failures) {
		t.Errorf("expected %s to be written on every run", failures)
	}
	if !exists(t, skipped) {
		t.Errorf("expected %s to be written on every run", skipped)
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
	failures, duplicates, books, skipped := withScanOutputs(t, dir, true)

	if err := runScan(context.Background(), scanFixture(t), processor.DefaultScanProcessorConfig()); err != nil {
		t.Fatal(err)
	}

	for _, path := range []string{failures, skipped, duplicates, books} {
		if !exists(t, path) {
			t.Errorf("expected %s to be written with --report", path)
		}
	}
}

func TestRunScanWritesSkippedReport(t *testing.T) {
	dir := t.TempDir()
	_, _, _, skipped := withScanOutputs(t, dir, false)

	booksDir := t.TempDir()
	path := filepath.Join(booksDir, "small.txt")
	if err := os.WriteFile(path, []byte("Title: Tiny\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := runScan(context.Background(), booksDir, processor.DefaultScanProcessorConfig()); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(skipped)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"small.txt", "below minimum document size"} {
		if !strings.Contains(string(data), want) {
			t.Errorf("skipped report does not contain %q: %s", want, data)
		}
	}
}

// cancelingProcessor blocks until the scan context is canceled and reports one
// real failure, so the cancellation cleanup path can be exercised without a
// full filesystem scan.
type cancelingProcessor struct {
	started chan struct{}
}

func (p *cancelingProcessor) Process(ctx context.Context) error {
	close(p.started)
	<-ctx.Done()
	return ctx.Err()
}

func (p *cancelingProcessor) Stats() processor.Progress {
	return processor.Progress{Parsed: 1, Failed: 1}
}

func (p *cancelingProcessor) Failures() []processor.Failure {
	return []processor.Failure{{Path: "/books/broken.epub", Err: errors.New("parse failed")}}
}

func (p *cancelingProcessor) Skips() []processor.Skip {
	return nil
}

func TestRunScanCancelWritesFailuresAndFinishesRun(t *testing.T) {
	dir := t.TempDir()
	failures, duplicates, books, skipped := withScanOutputs(t, dir, true)
	dbPath = filepath.Join(dir, "test.db")

	oldNew := newScanProcessor
	started := make(chan struct{})
	newScanProcessor = func(*sql.DB, *db.Queries, processor.ScanProcessorConfig, string) processor.Processor {
		return &cancelingProcessor{started: started}
	}
	t.Cleanup(func() { newScanProcessor = oldNew })

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- runScan(ctx, filepath.Join(dir, "books"), processor.DefaultScanProcessorConfig())
	}()

	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("processor never started")
	}
	cancel()

	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("runScan error = %v, want context.Canceled", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("runScan did not return after cancellation")
	}

	if !exists(t, failures) {
		t.Fatalf("expected %s to be written on cancellation", failures)
	}
	data, err := os.ReadFile(failures)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "broken.epub") {
		t.Errorf("failures report does not list the recorded failure: %s", data)
	}
	if !exists(t, skipped) {
		t.Errorf("expected %s to be written on cancellation", skipped)
	}
	if exists(t, duplicates) || exists(t, books) {
		t.Error("bulk reports must not be written for a canceled scan")
	}

	conn := openTestDB(t, dbPath)
	defer func() { _ = conn.Close() }()
	var status string
	if err := conn.QueryRow("SELECT status FROM scan_runs ORDER BY id DESC LIMIT 1").Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "canceled" {
		t.Errorf("scan_runs status = %q, want canceled", status)
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
	// the zip itself is treated as a folder and excluded. All of these
	// formats are supported now; PDFs are skipped because the test config has
	// no Poppler tools, but they still count in the extension stats.
	for _, want := range []string{
		"Extension stats (6 files):",
		"Supported:",
		"  pdf: 3 (50%)",
		"  txt: 2 (33.3%)",
		"  epub: 1 (16.7%)",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("expected output to contain %q, got:\n%s", want, out)
		}
	}
	if strings.Contains(out, "zip:") {
		t.Errorf("archives must be excluded from ext stats, got:\n%s", out)
	}
	// Supported entries sort by count desc (pdf, txt, epub).
	if !strings.Contains(out, "pdf: 3") || !strings.Contains(out, "txt: 2") || !strings.Contains(out, "epub: 1") ||
		strings.Index(out, "pdf: 3") > strings.Index(out, "txt: 2") ||
		strings.Index(out, "txt: 2") > strings.Index(out, "epub: 1") {
		t.Errorf("expected pdf, txt, then epub order, got:\n%s", out)
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
