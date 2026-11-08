package cli

import (
	"bytes"
	"compress/gzip"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/f0d0r/margaret-cli/internal/database"
	"github.com/f0d0r/margaret-cli/internal/db"
	"github.com/f0d0r/margaret-cli/internal/openlibrary"
)

func writeCLIDump(t *testing.T, dir, name string) {
	t.Helper()
	file, err := os.Create(filepath.Join(dir, name))
	if err != nil {
		t.Fatal(err)
	}
	writer := gzip.NewWriter(file)
	if _, err := writer.Write([]byte("/type/edition\t/books/OL1M\t1\t2024-01-01\t{\"title\":\"Test\"}\n")); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
}

func createScannedDB(t *testing.T, path string) {
	t.Helper()
	conn, err := database.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = conn.Close()
	}()

	ctx := context.Background()
	queries := db.New(conn)
	fileID, err := queries.CreateBookFile(ctx, db.CreateBookFileParams{
		Hash:  "hash-one",
		Path:  "books/one.epub",
		Title: "One",
	})
	if err != nil {
		t.Fatal(err)
	}
	bookID, err := queries.CreateBook(ctx, "One")
	if err != nil {
		t.Fatal(err)
	}
	if err := queries.CreateBookBookFile(ctx, db.CreateBookBookFileParams{
		BookID:     bookID,
		BookFileID: fileID,
	}); err != nil {
		t.Fatal(err)
	}
	if err := queries.CreateBookIdentifier(ctx, db.CreateBookIdentifierParams{
		BookID: bookID,
		Scheme: "ISBN",
		Value:  "9780306406157",
		Source: "METADATA",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := queries.CreateBook(ctx, "Two"); err != nil {
		t.Fatal(err)
	}
}

func withEnrichFlags(t *testing.T, databasePath, dumpDir string, download, status bool) {
	t.Helper()
	oldDB, oldDir, oldDownload, oldStatus := dbPath, enrichOpenLibraryDir, enrichDownloadFlag, enrichStatusFlag
	dbPath, enrichOpenLibraryDir, enrichDownloadFlag, enrichStatusFlag = databasePath, dumpDir, download, status
	t.Cleanup(func() {
		dbPath, enrichOpenLibraryDir, enrichDownloadFlag, enrichStatusFlag = oldDB, oldDir, oldDownload, oldStatus
	})
}

func TestEnrichFlags(t *testing.T) {
	for _, name := range []string{"db", "openlibrary-dir", "download", "status"} {
		if enrichCmd.Flags().Lookup(name) == nil {
			t.Fatalf("enrich flag %q is not registered", name)
		}
	}
	if flag := enrichCmd.Flags().Lookup("download"); flag.DefValue != "false" {
		t.Fatalf("download default = %q, want false", flag.DefValue)
	}
	if flag := enrichCmd.Flags().Lookup("status"); flag.DefValue != "false" {
		t.Fatalf("status default = %q, want false", flag.DefValue)
	}
}

func TestRunEnrichStatus(t *testing.T) {
	dumpDir := t.TempDir()
	for _, family := range []string{"editions", "works", "authors"} {
		writeCLIDump(t, dumpDir, "ol_dump_"+family+"_2024-01-31.txt.gz")
	}
	dbFile := filepath.Join(t.TempDir(), "margaret.db")
	createScannedDB(t, dbFile)
	withEnrichFlags(t, dbFile, dumpDir, false, true)

	var err error
	output := captureStdout(t, func() {
		err = runEnrichStatus(context.Background())
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"Scan database: " + dbFile,
		"books: 2",
		"files: 1",
		"books with ISBN: 1 (50%)",
		"distinct ISBNs: 1",
		"books without ISBN: 1",
		"matching cache: 2 books (2 with title, 1 with ISBN, 0 with authors)",
		"OpenLibrary dump search:",
		"[ok]",
		"Selected: " + dumpDir,
		"ol_dump_editions_2024-01-31.txt.gz",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("status output does not contain %q:\n%s", want, output)
		}
	}
}

func TestRunEnrichStatusCombined(t *testing.T) {
	dumpDir := t.TempDir()
	writeCLIDump(t, dumpDir, "ol_dump_latest.txt.gz")
	dbFile := filepath.Join(t.TempDir(), "missing.db")
	withEnrichFlags(t, dbFile, dumpDir, false, true)

	output := captureStdout(t, func() {
		if err := runEnrichStatus(context.Background()); err != nil {
			t.Fatal(err)
		}
	})
	if !strings.Contains(output, "mode: combined dump (works metadata deferred)") {
		t.Fatalf("status output does not mention combined mode:\n%s", output)
	}
}

func TestRunEnrichStatusMissingDatabaseDoesNotCreateIt(t *testing.T) {
	dbFile := filepath.Join(t.TempDir(), "missing.db")
	withEnrichFlags(t, dbFile, t.TempDir(), false, true)

	output := captureStdout(t, func() {
		if err := runEnrichStatus(context.Background()); err != nil {
			t.Fatal(err)
		}
	})
	if !strings.Contains(output, "(missing)") {
		t.Fatalf("status output does not mention the missing database:\n%s", output)
	}
	if _, err := os.Stat(dbFile); !os.IsNotExist(err) {
		t.Fatalf("status created the missing database: %v", err)
	}
}

func TestRunEnrichStatusAndDownloadMutuallyExclusive(t *testing.T) {
	withEnrichFlags(t, "unused.db", "", true, true)
	err := runEnrich(context.Background())
	if err == nil || !strings.Contains(err.Error(), "mutually exclusive") {
		t.Fatalf("error = %v, want mutually exclusive", err)
	}
}

func TestRunEnrichNoDumpNonInteractive(t *testing.T) {
	dbFile := filepath.Join(t.TempDir(), "margaret.db")
	createScannedDB(t, dbFile)
	withEnrichFlags(t, dbFile, "", false, false)

	t.Chdir(t.TempDir())
	t.Setenv(openlibrary.EnvDir, "")
	t.Setenv("XDG_DATA_HOME", t.TempDir())

	oldTerminal := stdinIsTerminal
	stdinIsTerminal = func() bool { return false }
	t.Cleanup(func() { stdinIsTerminal = oldTerminal })

	err := runEnrich(context.Background())
	if err == nil {
		t.Fatal("expected an error without a dump")
	}
	if !strings.Contains(err.Error(), "no OpenLibrary dump found") {
		t.Fatalf("error = %v", err)
	}
	if !strings.Contains(err.Error(), "--openlibrary-dir") {
		t.Fatalf("error does not suggest --openlibrary-dir: %v", err)
	}
}

func TestRunEnrichInteractiveDecline(t *testing.T) {
	dbFile := filepath.Join(t.TempDir(), "margaret.db")
	createScannedDB(t, dbFile)
	withEnrichFlags(t, dbFile, "", false, false)

	t.Chdir(t.TempDir())
	t.Setenv(openlibrary.EnvDir, "")
	t.Setenv("XDG_DATA_HOME", t.TempDir())

	oldTerminal := stdinIsTerminal
	stdinIsTerminal = func() bool { return true }
	t.Cleanup(func() { stdinIsTerminal = oldTerminal })

	oldStdin := os.Stdin
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdin = reader
	t.Cleanup(func() {
		os.Stdin = oldStdin
		_ = reader.Close()
	})
	if _, err := writer.WriteString("n\n"); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}

	output := captureStdout(t, func() {
		if err := runEnrich(context.Background()); err != nil {
			t.Fatal(err)
		}
	})
	if !strings.Contains(output, "No changes made.") {
		t.Fatalf("output does not mention declining:\n%s", output)
	}
}

func TestRunEnrichDownload(t *testing.T) {
	server, urls := newDumpServer(t)
	defer server.Close()

	dumpDir := t.TempDir()
	withEnrichFlags(t, "unused.db", dumpDir, true, false)

	oldDownloader := newOpenLibraryDownloader
	newOpenLibraryDownloader = func(dir string) *openlibrary.Downloader {
		return &openlibrary.Downloader{Dir: dir, URLs: urls, Client: server.Client()}
	}
	t.Cleanup(func() { newOpenLibraryDownloader = oldDownloader })

	var runErr error
	var stderr string
	output := captureStdout(t, func() {
		stderr = captureStderr(t, func() {
			runErr = runEnrich(context.Background())
		})
	})
	if runErr != nil {
		t.Fatalf("runEnrich: %v\nstderr: %s", runErr, stderr)
	}
	if !strings.Contains(output, "Downloading OpenLibrary dumps to") {
		t.Fatalf("output does not mention downloading:\n%s", output)
	}
	if !strings.Contains(output, "Metadata matching is not implemented yet.") {
		t.Fatalf("output does not mention the missing matching pass:\n%s", output)
	}
	for _, family := range []string{"editions", "works", "authors"} {
		path := filepath.Join(dumpDir, "ol_dump_"+family+"_2024-01-31.txt.gz")
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("%s dump: %v", family, err)
		}
	}
}

func TestRunEnrichDownloadSkipsExisting(t *testing.T) {
	server, urls := newDumpServer(t)
	defer server.Close()

	dumpDir := t.TempDir()
	for _, family := range []string{"editions", "works", "authors"} {
		writeCLIDump(t, dumpDir, "ol_dump_"+family+"_2024-01-31.txt.gz")
	}
	withEnrichFlags(t, "unused.db", dumpDir, true, false)

	oldDownloader := newOpenLibraryDownloader
	newOpenLibraryDownloader = func(dir string) *openlibrary.Downloader {
		return &openlibrary.Downloader{Dir: dir, URLs: urls, Client: server.Client()}
	}
	t.Cleanup(func() { newOpenLibraryDownloader = oldDownloader })

	var runErr error
	output := captureStdout(t, func() {
		_ = captureStderr(t, func() {
			runErr = runEnrich(context.Background())
		})
	})
	if runErr != nil {
		t.Fatal(runErr)
	}
	if got := strings.Count(output, "already present"); got != 3 {
		t.Fatalf("already present count = %d, want 3:\n%s", got, output)
	}
}

func TestPromptDownload(t *testing.T) {
	dumpDir := t.TempDir()
	candidates := openlibrary.Candidates(dumpDir)

	output := captureStdout(t, func() {
		ok, err := promptDownload(context.Background(), strings.NewReader("y\n"), candidates, dumpDir, openlibrary.DefaultKinds)
		if err != nil {
			t.Fatal(err)
		}
		if !ok {
			t.Fatal("expected yes")
		}
	})
	if !strings.Contains(output, "Download the latest OpenLibrary dumps") {
		t.Fatalf("prompt output:\n%s", output)
	}

	output = captureStdout(t, func() {
		ok, err := promptDownload(context.Background(), strings.NewReader("\n"), candidates, dumpDir, openlibrary.DefaultKinds)
		if err != nil {
			t.Fatal(err)
		}
		if ok {
			t.Fatal("expected no by default")
		}
	})
	if !strings.Contains(output, "[y/N]") {
		t.Fatalf("prompt output:\n%s", output)
	}
}

func newDumpServer(t *testing.T) (*httptest.Server, map[openlibrary.Kind]string) {
	t.Helper()
	var buffer bytes.Buffer
	writer := gzip.NewWriter(&buffer)
	if _, err := writer.Write([]byte("/type/edition\t/books/OL1M\t1\t2024-01-01\t{}\n")); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	data := buffer.Bytes()
	modified := time.Date(2024, 1, 31, 0, 0, 0, 0, time.UTC)

	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		http.ServeContent(response, request, request.URL.Path, modified, bytes.NewReader(data))
	}))
	urls := make(map[openlibrary.Kind]string, len(openlibrary.DefaultKinds))
	for _, kind := range openlibrary.DefaultKinds {
		urls[kind] = server.URL + "/ol_dump_" + string(kind) + "_2024-01-31.txt.gz"
	}
	return server, urls
}
