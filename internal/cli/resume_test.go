package cli

import (
	"context"
	"database/sql"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/f0d0r/margaret-tools/internal/database"
	"github.com/f0d0r/margaret-tools/internal/db"
	"github.com/f0d0r/margaret-tools/internal/processor"
)

// fileScanSetup prepares a file-backed scan: fixture books plus all
// package-level output settings pointed into dir. Everything touched is
// restored after the test; tests in this package must not run in parallel.
func fileScanSetup(t *testing.T, fresh, resume, term bool) (booksDir, dbFile, failures string) {
	t.Helper()

	oldDB, oldFailures, oldDuplicates, oldBooks := dbPath, failuresOutPath, duplicatesOut, booksOut
	oldReport, oldFresh, oldResume := reportEnabled, freshFlag, resumeFlag
	oldTerm := stdinIsTerminal
	t.Cleanup(func() {
		dbPath, failuresOutPath, duplicatesOut, booksOut = oldDB, oldFailures, oldDuplicates, oldBooks
		reportEnabled, freshFlag, resumeFlag = oldReport, oldFresh, oldResume
		stdinIsTerminal = oldTerm
	})

	dir := t.TempDir()
	booksDir = filepath.Join(dir, "books")
	if err := os.Mkdir(booksDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeTestEpub(t, filepath.Join(booksDir, "a.epub"), "Test Book", "Author A", "same")
	if err := copyFile(filepath.Join(booksDir, "a.epub"), filepath.Join(booksDir, "b.epub")); err != nil {
		t.Fatal(err)
	}

	dbFile = filepath.Join(dir, "test.db")
	failures = filepath.Join(dir, "failures.json")
	dbPath, failuresOutPath = dbFile, failures
	duplicatesOut = filepath.Join(dir, "duplicates.json")
	booksOut = filepath.Join(dir, "books.json")
	reportEnabled, freshFlag, resumeFlag = false, fresh, resume
	stdinIsTerminal = func() bool { return term }
	return booksDir, dbFile, failures
}

// captureStdout runs f with os.Stdout redirected to a pipe and returns what
// was written.
func captureStdout(t *testing.T, f func()) string {
	t.Helper()

	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w

	outCh := make(chan string, 1)
	go func() {
		data, _ := io.ReadAll(r)
		outCh <- string(data)
	}()

	f()

	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	os.Stdout = old
	out := <-outCh
	_ = r.Close()
	return out
}

func openTestDB(t *testing.T, dbFile string) *sql.DB {
	t.Helper()

	conn, err := database.Open(dbFile)
	if err != nil {
		t.Fatal(err)
	}
	return conn
}

func dbCounts(t *testing.T, dbFile string) (files, books, runs int) {
	t.Helper()

	conn := openTestDB(t, dbFile)
	defer func() { _ = conn.Close() }()
	q := db.New(conn)
	ctx := context.Background()

	n, err := q.CountBookFiles(ctx)
	if err != nil {
		t.Fatal(err)
	}
	b, err := q.CountBooks(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var r int
	if err := conn.QueryRow("SELECT count(*) FROM scan_runs").Scan(&r); err != nil {
		t.Fatal(err)
	}
	return int(n), int(b), r
}

func TestRunScanConflictingFlags(t *testing.T) {
	booksDir, _, _ := fileScanSetup(t, true, true, false)

	err := runScan(context.Background(), booksDir, processor.DefaultScanProcessorConfig())
	if err == nil || !strings.Contains(err.Error(), "mutually exclusive") {
		t.Fatalf("expected mutual-exclusion error, got %v", err)
	}
}

func TestRunScanNonInteractiveGuard(t *testing.T) {
	booksDir, _, _ := fileScanSetup(t, false, false, false)

	// First scan populates the database (empty DB needs no decision).
	if err := runScan(context.Background(), booksDir, processor.DefaultScanProcessorConfig()); err != nil {
		t.Fatal(err)
	}
	// Second scan without flags and without a terminal must fail closed.
	err := runScan(context.Background(), booksDir, processor.DefaultScanProcessorConfig())
	if err == nil || !strings.Contains(err.Error(), "--resume") {
		t.Fatalf("expected --resume hint error, got %v", err)
	}
}

func TestRunScanResumeSkipsUnchanged(t *testing.T) {
	booksDir, dbFile, _ := fileScanSetup(t, false, false, false)

	if err := runScan(context.Background(), booksDir, processor.DefaultScanProcessorConfig()); err != nil {
		t.Fatal(err)
	}
	filesBefore, booksBefore, _ := dbCounts(t, dbFile)

	freshFlag, resumeFlag = false, true
	out := captureStdout(t, func() {
		if err := runScan(context.Background(), booksDir, processor.DefaultScanProcessorConfig()); err != nil {
			t.Fatal(err)
		}
	})

	if !strings.Contains(out, "Skipped") {
		t.Errorf("expected Skipped line in summary, got:\n%s", out)
	}
	// Total counts every encountered unit, including skipped ones.
	if !strings.Contains(out, "Total      2 ebook(s)") {
		t.Errorf("expected Total 2 (both files skipped), got:\n%s", out)
	}
	filesAfter, booksAfter, runs := dbCounts(t, dbFile)
	if filesAfter != filesBefore || booksAfter != booksBefore {
		t.Errorf("resume changed row counts: files %d->%d, books %d->%d", filesBefore, filesAfter, booksBefore, booksAfter)
	}
	if runs != 2 {
		t.Errorf("expected 2 scan_runs rows, got %d", runs)
	}
}

func TestRunScanResumeSkipsChangedFile(t *testing.T) {
	booksDir, dbFile, _ := fileScanSetup(t, false, false, false)

	if err := runScan(context.Background(), booksDir, processor.DefaultScanProcessorConfig()); err != nil {
		t.Fatal(err)
	}

	// Change one file's content. Resume still skips the recorded path:
	// changed bytes need a fresh run to reprocess.
	writeTestEpub(t, filepath.Join(booksDir, "b.epub"), "Test Book Changed", "Author A", "different")

	freshFlag, resumeFlag = false, true
	out := captureStdout(t, func() {
		if err := runScan(context.Background(), booksDir, processor.DefaultScanProcessorConfig()); err != nil {
			t.Fatal(err)
		}
	})
	if !strings.Contains(out, "Skipped") {
		t.Errorf("expected Skipped line in summary, got:\n%s", out)
	}

	conn := openTestDB(t, dbFile)
	defer func() { _ = conn.Close() }()
	var files, dups int
	if err := conn.QueryRow("SELECT count(*) FROM book_files").Scan(&files); err != nil {
		t.Fatal(err)
	}
	if err := conn.QueryRow("SELECT count(*) FROM book_file_duplicates").Scan(&dups); err != nil {
		t.Fatal(err)
	}
	if files != 1 || dups != 1 {
		t.Errorf("expected recorded rows untouched (1 file, 1 dup), got %d files, %d dups", files, dups)
	}
}

func TestRunScanResumeRetriesFailedFiles(t *testing.T) {
	booksDir, dbFile, failuresPath := fileScanSetup(t, false, false, false)

	// Keep a single valid book next to the corrupt file so the resume
	// counters stay unambiguous (no byte-identical duplicates).
	if err := os.Remove(filepath.Join(booksDir, "b.epub")); err != nil {
		t.Fatal(err)
	}
	corruptPath := filepath.Join(booksDir, "corrupt.epub")
	if err := os.WriteFile(corruptPath, []byte("this is not an ebook"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := runScan(context.Background(), booksDir, processor.DefaultScanProcessorConfig()); err != nil {
		t.Fatal(err)
	}

	// The corrupt file leaves no row behind.
	conn := openTestDB(t, dbFile)
	var n int
	if err := conn.QueryRow("SELECT count(*) FROM book_files WHERE path = ?", corruptPath).Scan(&n); err != nil {
		_ = conn.Close()
		t.Fatal(err)
	}
	_ = conn.Close()
	if n != 0 {
		t.Fatalf("failed file %q must leave no row behind, found %d", corruptPath, n)
	}
	data, err := os.ReadFile(failuresPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "corrupt.epub") {
		t.Errorf("expected failures.json to list the corrupt file, got:\n%s", data)
	}

	// A resume run retries the failure (leaving failures.json a
	// current-run report) while skipping the recorded success.
	freshFlag, resumeFlag = false, true
	out := captureStdout(t, func() {
		if err := runScan(context.Background(), booksDir, processor.DefaultScanProcessorConfig()); err != nil {
			t.Fatal(err)
		}
	})
	if !strings.Contains(out, "Failed     1") {
		t.Errorf("expected the retried failure on resume, got:\n%s", out)
	}
	if !strings.Contains(out, "Skipped") {
		t.Errorf("expected a Skipped line in summary, got:\n%s", out)
	}
	data, err = os.ReadFile(failuresPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "corrupt.epub") {
		t.Errorf("expected failures.json to list the retried failure, got:\n%s", data)
	}
}

func TestPromptScanModeResumeDefault(t *testing.T) {
	_, dbFile, _ := fileScanSetup(t, false, false, true)
	booksDir := filepath.Join(filepath.Dir(filepath.Dir(dbFile)), "books")

	conn := openTestDB(t, dbFile)
	q := db.New(conn)
	ctx := context.Background()
	if _, err := q.CreateBookFile(ctx, db.CreateBookFileParams{Hash: "h", Path: filepath.Join(booksDir, "x.epub")}); err != nil {
		_ = conn.Close()
		t.Fatal(err)
	}
	_ = conn.Close()

	for _, tc := range []struct {
		name string
		in   string
		want scanMode
	}{
		{"empty defaults to resume", "\n", modeResume},
		{"explicit resume", "r\n", modeResume},
		{"explicit fresh", "f\n", modeFresh},
	} {
		t.Run(tc.name, func(t *testing.T) {
			stdinIsTerminal = func() bool { return true }
			oldStdin := os.Stdin
			r, w, err := os.Pipe()
			if err != nil {
				t.Fatal(err)
			}
			if _, err := w.WriteString(tc.in); err != nil {
				t.Fatal(err)
			}
			_ = w.Close()
			os.Stdin = r
			defer func() { os.Stdin = oldStdin }()

			conn := openTestDB(t, dbFile)
			defer func() { _ = conn.Close() }()
			got, err := promptScanMode(ctx, db.New(conn), booksDir)
			if err != nil {
				t.Fatal(err)
			}
			if got != tc.want {
				t.Errorf("got mode %v, want %v", got, tc.want)
			}
		})
	}
}

func TestPromptScanModeRejectsGarbage(t *testing.T) {
	_, dbFile, _ := fileScanSetup(t, false, false, true)

	stdinIsTerminal = func() bool { return true }
	oldStdin := os.Stdin
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.WriteString("maybe\n"); err != nil {
		t.Fatal(err)
	}
	_ = w.Close()
	os.Stdin = r
	defer func() { os.Stdin = oldStdin }()

	conn := openTestDB(t, dbFile)
	defer func() { _ = conn.Close() }()
	if _, err := q2(conn).GetLatestScanRun(context.Background()); err == nil {
		t.Fatal("expected no scan runs yet")
	}
	// Seed one file row so the prompt has data to show.
	if _, err := q2(conn).CreateBookFile(context.Background(), db.CreateBookFileParams{Hash: "h", Path: "x.epub"}); err != nil {
		t.Fatal(err)
	}
	if _, err := promptScanMode(context.Background(), q2(conn), t.TempDir()); err == nil {
		t.Fatal("expected error for unknown choice")
	}
}

func q2(conn *sql.DB) *db.Queries { return db.New(conn) }

func TestRunScanResumeDoesNotRefailDuplicates(t *testing.T) {
	booksDir, _, failures := fileScanSetup(t, false, false, false)

	if err := runScan(context.Background(), booksDir, processor.DefaultScanProcessorConfig()); err != nil {
		t.Fatal(err)
	}

	// The fixture holds two byte-identical files: one canonical row, one
	// duplicate entry. Resuming must not turn the duplicate into a
	// constraint-violation failure.
	freshFlag, resumeFlag = false, true
	if err := runScan(context.Background(), booksDir, processor.DefaultScanProcessorConfig()); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(failures)
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(data)) != "[]" {
		t.Errorf("expected no failures on resume, got: %s", data)
	}
}

func TestRunScanResumeDifferentRootGuard(t *testing.T) {
	booksDir, _, _ := fileScanSetup(t, false, false, false)

	if err := runScan(context.Background(), booksDir, processor.DefaultScanProcessorConfig()); err != nil {
		t.Fatal(err)
	}

	// Same database, different tree, no terminal: must refuse to mix.
	other := t.TempDir()
	freshFlag, resumeFlag = false, true
	err := runScan(context.Background(), other, processor.DefaultScanProcessorConfig())
	if err == nil || !strings.Contains(err.Error(), "mix") {
		t.Fatalf("expected different-tree refusal, got %v", err)
	}
}
