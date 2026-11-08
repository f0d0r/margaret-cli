package openlibrary

import (
	"bytes"
	"compress/gzip"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"sync/atomic"
	"testing"
	"time"
)

func gzipDumpBytes(t *testing.T, body string) []byte {
	t.Helper()
	var buffer bytes.Buffer
	writer := gzip.NewWriter(&buffer)
	if _, err := writer.Write([]byte(body)); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

func TestEnsureDownloadsAll(t *testing.T) {
	body := "/type/edition\t/books/OL1M\t1\t2024-01-01\t{\"title\":\"Test\"}\n"
	data := gzipDumpBytes(t, body)
	modTime := time.Date(2024, 1, 31, 0, 0, 0, 0, time.UTC)

	var requests atomic.Int64
	serveDump := func(writer http.ResponseWriter, request *http.Request) {
		requests.Add(1)
		http.ServeContent(writer, request, "dump.txt.gz", modTime, bytes.NewReader(data))
	}
	mux := http.NewServeMux()
	for _, family := range []string{"editions", "works", "authors"} {
		family := family
		mux.HandleFunc("/"+family+"-latest", func(writer http.ResponseWriter, request *http.Request) {
			http.Redirect(writer, request, "/ol_dump_"+family+"_2024-01-31.txt.gz", http.StatusFound)
		})
		mux.HandleFunc("/ol_dump_"+family+"_2024-01-31.txt.gz", serveDump)
	}
	server := httptest.NewServer(mux)
	defer server.Close()

	dir := t.TempDir()
	downloader := &Downloader{
		Dir:    dir,
		Client: server.Client(),
		URLs: map[Kind]string{
			KindEditions: server.URL + "/editions-latest",
			KindWorks:    server.URL + "/works-latest",
			KindAuthors:  server.URL + "/authors-latest",
		},
	}
	results, err := downloader.Ensure(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 3 {
		t.Fatalf("results = %d, want 3", len(results))
	}
	for _, result := range results {
		if result.Skipped {
			t.Fatalf("%s was unexpectedly skipped", result.Kind)
		}
		if got, want := filepath.Base(result.Path), "ol_dump_"+string(result.Kind)+"_2024-01-31.txt.gz"; got != want {
			t.Fatalf("%s path = %q, want %q", result.Kind, got, want)
		}
		if err := validateDump(result.Path, true); err != nil {
			t.Fatalf("%s validation: %v", result.Kind, err)
		}
	}

	before := requests.Load()
	results, err = downloader.Ensure(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, result := range results {
		if !result.Skipped {
			t.Fatalf("%s was downloaded again", result.Kind)
		}
	}
	if got := requests.Load(); got != before {
		t.Fatalf("second Ensure made %d requests, want 0", got-before)
	}

	downloader.Refresh = true
	results, err = downloader.Ensure(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, result := range results {
		if !result.Skipped {
			t.Fatalf("%s was downloaded again during refresh", result.Kind)
		}
	}
	if got := requests.Load(); got <= before {
		t.Fatalf("refresh made no requests")
	}
}

func TestEnsureResumesPartial(t *testing.T) {
	body := "/type/edition\t/books/OL1M\t1\t2024-01-01\t{\"title\":\"Test\"}\n"
	data := gzipDumpBytes(t, body)
	modTime := time.Date(2024, 1, 31, 0, 0, 0, 0, time.UTC)

	ranges := make(chan string, 4)
	mux := http.NewServeMux()
	mux.HandleFunc("/editions-latest", func(writer http.ResponseWriter, request *http.Request) {
		http.Redirect(writer, request, "/ol_dump_editions_2024-01-31.txt.gz", http.StatusFound)
	})
	mux.HandleFunc("/ol_dump_editions_2024-01-31.txt.gz", func(writer http.ResponseWriter, request *http.Request) {
		if value := request.Header.Get("Range"); value != "" {
			ranges <- value
		}
		http.ServeContent(writer, request, "dump.txt.gz", modTime, bytes.NewReader(data))
	})
	server := httptest.NewServer(mux)
	defer server.Close()

	dir := t.TempDir()
	target := filepath.Join(dir, "ol_dump_editions_2024-01-31.txt.gz")
	half := len(data) / 2
	if err := os.WriteFile(target+".part", data[:half], 0o644); err != nil {
		t.Fatal(err)
	}

	downloader := &Downloader{
		Dir:    dir,
		Client: server.Client(),
		URLs: map[Kind]string{
			KindEditions: server.URL + "/editions-latest",
		},
	}
	results, err := downloader.Ensure(context.Background(), []Kind{KindEditions})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].Skipped {
		t.Fatalf("results = %+v", results)
	}

	select {
	case value := <-ranges:
		if want := "bytes=" + strconv.Itoa(half) + "-"; value != want {
			t.Fatalf("Range = %q, want %q", value, want)
		}
	case <-time.After(time.Second):
		t.Fatal("no Range request was sent")
	}

	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, data) {
		t.Fatal("resumed download does not match the full dump")
	}
}

func TestEnsureRejectsInvalidDump(t *testing.T) {
	data := []byte("this is not a gzip dump")
	modTime := time.Date(2024, 1, 31, 0, 0, 0, 0, time.UTC)

	mux := http.NewServeMux()
	mux.HandleFunc("/editions-latest", func(writer http.ResponseWriter, request *http.Request) {
		http.Redirect(writer, request, "/ol_dump_editions_2024-01-31.txt.gz", http.StatusFound)
	})
	mux.HandleFunc("/ol_dump_editions_2024-01-31.txt.gz", func(writer http.ResponseWriter, request *http.Request) {
		http.ServeContent(writer, request, "dump.txt.gz", modTime, bytes.NewReader(data))
	})
	server := httptest.NewServer(mux)
	defer server.Close()

	dir := t.TempDir()
	downloader := &Downloader{
		Dir:    dir,
		Client: server.Client(),
		URLs: map[Kind]string{
			KindEditions: server.URL + "/editions-latest",
		},
	}
	if _, err := downloader.Ensure(context.Background(), []Kind{KindEditions}); err == nil {
		t.Fatal("expected a validation error")
	}
	target := filepath.Join(dir, "ol_dump_editions_2024-01-31.txt.gz")
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Fatalf("invalid dump was left at the final path: %v", err)
	}
	if _, err := os.Stat(target + ".part"); !os.IsNotExist(err) {
		t.Fatalf("invalid part file was not removed: %v", err)
	}
}

func TestEnsureProgress(t *testing.T) {
	data := gzipDumpBytes(t, "/type/edition\t/books/OL1M\t1\t2024-01-01\t{}\n")
	modTime := time.Date(2024, 1, 31, 0, 0, 0, 0, time.UTC)

	mux := http.NewServeMux()
	mux.HandleFunc("/editions-latest", func(writer http.ResponseWriter, request *http.Request) {
		http.Redirect(writer, request, "/ol_dump_editions_2024-01-31.txt.gz", http.StatusFound)
	})
	mux.HandleFunc("/ol_dump_editions_2024-01-31.txt.gz", func(writer http.ResponseWriter, request *http.Request) {
		http.ServeContent(writer, request, "dump.txt.gz", modTime, bytes.NewReader(data))
	})
	server := httptest.NewServer(mux)
	defer server.Close()

	var events int
	downloader := &Downloader{
		Dir:    t.TempDir(),
		Client: server.Client(),
		URLs: map[Kind]string{
			KindEditions: server.URL + "/editions-latest",
		},
		Progress: func(Kind, int64, int64) { events++ },
	}
	if _, err := downloader.Ensure(context.Background(), []Kind{KindEditions}); err != nil {
		t.Fatal(err)
	}
	if events == 0 {
		t.Fatal("expected progress events")
	}
}

func TestTargetName(t *testing.T) {
	if got, want := targetName(KindEditions, "https://example.com/ol_dump_editions_2024-01-31.txt.gz"), "ol_dump_editions_2024-01-31.txt.gz"; got != want {
		t.Fatalf("targetName = %q, want %q", got, want)
	}
	if got, want := targetName(KindWorks, "https://example.com/download"), "ol_dump_works_latest.txt.gz"; got != want {
		t.Fatalf("targetName = %q, want %q", got, want)
	}
	if got, want := targetName(KindWorks, "https://example.com/ol_dump_editions_2024-01-31.txt.gz"), "ol_dump_works_latest.txt.gz"; got != want {
		t.Fatalf("targetName = %q, want %q", got, want)
	}
}
