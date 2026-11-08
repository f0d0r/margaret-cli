package openlibrary

import (
	"errors"
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
