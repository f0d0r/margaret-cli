package openlibrary

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
)

func TestReadRow(t *testing.T) {
	path := filepath.Join(t.TempDir(), "dump.txt")
	body := "/type/edition\t/books/OL1M\t1\t2024-01-01\t{\"title\":\"Test\"}\r\n" +
		"\n" +
		"/type/author\t/authors/OL1A\t1\t2024-01-01\t{\"name\":\"Author\"}\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	reader, err := OpenDump(&File{Path: path})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = reader.Close()
	}()
	scanner := NewDumpScanner(reader)

	row, err := ReadRow(scanner)
	if err != nil {
		t.Fatal(err)
	}
	if row.Type != "/type/edition" || row.Key != "/books/OL1M" || string(row.Data) != `{"title":"Test"}` {
		t.Fatalf("row = %+v", row)
	}

	row, err = ReadRow(scanner)
	if err != nil {
		t.Fatal(err)
	}
	if row.Type != "/type/author" || row.Key != "/authors/OL1A" {
		t.Fatalf("row = %+v", row)
	}

	if _, err := ReadRow(scanner); err == nil {
		t.Fatal("expected EOF")
	}
}

func TestReadRowMalformed(t *testing.T) {
	path := filepath.Join(t.TempDir(), "dump.txt")
	if err := os.WriteFile(path, []byte("only-two\tcolumns\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	reader, err := OpenDump(&File{Path: path})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = reader.Close()
	}()

	if _, err := ReadRow(NewDumpScanner(reader)); !errors.Is(err, ErrMalformedRow) {
		t.Fatalf("error = %v, want ErrMalformedRow", err)
	}
}

func TestOpenDumpGzip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "dump.txt.gz")
	writeGzipDump(t, path)

	reader, err := OpenDump(&File{Path: path, Compressed: true})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = reader.Close()
	}()
	row, err := ReadRow(NewDumpScanner(reader))
	if err != nil {
		t.Fatal(err)
	}
	if row.Type != "/type/edition" {
		t.Fatalf("row type = %q", row.Type)
	}
}

func TestOpenDumpWithCounter(t *testing.T) {
	body := "/type/edition\t/books/OL1M\t1\t2024-01-01\t{\"title\":\"Test\"}\n"
	dir := t.TempDir()
	gzPath := filepath.Join(dir, "dump.txt.gz")
	if err := os.WriteFile(gzPath, gzipDumpBytes(t, body), 0o644); err != nil {
		t.Fatal(err)
	}
	plainPath := filepath.Join(dir, "dump.txt")
	if err := os.WriteFile(plainPath, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		name       string
		path       string
		compressed bool
	}{
		{name: "gzip", path: gzPath, compressed: true},
		{name: "plain", path: plainPath},
	} {
		t.Run(tc.name, func(t *testing.T) {
			file := &File{Path: tc.path, Compressed: tc.compressed}
			reader, counter, err := OpenDumpWithCounter(file)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := io.ReadAll(reader); err != nil {
				t.Fatal(err)
			}
			if err := reader.Close(); err != nil {
				t.Fatal(err)
			}
			info, err := os.Stat(tc.path)
			if err != nil {
				t.Fatal(err)
			}
			if counter.Load() != info.Size() {
				t.Fatalf("counter = %d, want %d", counter.Load(), info.Size())
			}
			if got := DumpSize(file); got != info.Size() {
				t.Fatalf("DumpSize = %d, want %d", got, info.Size())
			}
		})
	}
}
