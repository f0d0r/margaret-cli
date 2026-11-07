package pdf

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"slices"
	"testing"

	"github.com/f0d0r/margaret-cli/internal/identifier"
	"github.com/f0d0r/margaret-cli/internal/parser"
	"github.com/f0d0r/margaret-ebook-library/book"
)

func sha256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func TestHashingReaderHashesRemainder(t *testing.T) {
	data := []byte("0123456789abcdef")
	sum := sha256.New()
	// Simulate pdftotext reading only the first seven bytes of the blob.
	reader := &hashingReader{reader: bytes.NewReader(data[:7]), sum: sum}
	if _, err := io.Copy(io.Discard, reader); err != nil {
		t.Fatal(err)
	}
	if err := finishHash(book.NewBytesBlob(data), int64(len(data)), sum, reader.offset.Load()); err != nil {
		t.Fatal(err)
	}
	if got, want := hex.EncodeToString(sum.Sum(nil)), sha256Hex(data); got != want {
		t.Errorf("hash = %q, want %q", got, want)
	}
}

func TestParsePDF(t *testing.T) {
	tools, err := DetectTools("")
	if err != nil {
		t.Skipf("poppler is not installed: %v", err)
	}
	data, err := os.ReadFile("testdata/sample.pdf")
	if err != nil {
		t.Fatal(err)
	}

	md, err := PDFParser{MinBookSize: -1, Tools: tools}.Parse(context.Background(), book.NewBytesBlob(data))
	if err != nil {
		t.Fatal(err)
	}
	if md.Title != "The PDF Test Book" {
		t.Errorf("title = %q, want %q", md.Title, "The PDF Test Book")
	}
	if !slices.Equal(md.Authors, []string{"Jane Doe"}) {
		t.Errorf("authors = %v, want [Jane Doe]", md.Authors)
	}
	if md.Description != "A short PDF test description" {
		t.Errorf("description = %q, want %q", md.Description, "A short PDF test description")
	}
	if !slices.Contains(md.Subjects, "Fiction") || !slices.Contains(md.Subjects, "Testing") {
		t.Errorf("subjects = %v, want Fiction and Testing", md.Subjects)
	}
	wantID := identifier.Identifier{Scheme: "ISBN", Value: "9783161484100", Source: identifier.SourceMetadata}
	if !slices.Contains(md.Identifiers, wantID) {
		t.Errorf("identifiers = %v, want %v", md.Identifiers, wantID)
	}
	if md.Hash != sha256Hex(data) {
		t.Errorf("hash = %q, want %q", md.Hash, sha256Hex(data))
	}
	if len(md.MinHash) != parser.MinHashHashes {
		t.Errorf("minhash has %d hashes, want %d", len(md.MinHash), parser.MinHashHashes)
	}
	if len(md.Languages) == 0 || md.Languages[0] != "en" {
		t.Errorf("languages = %v, want [en]", md.Languages)
	}
}

func TestParsePDFTextThreshold(t *testing.T) {
	tools, err := DetectTools("")
	if err != nil {
		t.Skipf("poppler is not installed: %v", err)
	}
	for _, name := range []string{"no_text.pdf", "short_text.pdf"} {
		t.Run(name, func(t *testing.T) {
			data, err := os.ReadFile("testdata/" + name)
			if err != nil {
				t.Fatal(err)
			}
			_, err = PDFParser{MinBookSize: 0, Tools: tools}.Parse(context.Background(), book.NewBytesBlob(data))
			if !errors.Is(err, parser.ErrNotABook) {
				t.Fatalf("error = %v, want ErrNotABook", err)
			}
		})
	}
}

func TestParsePDFNotPDF(t *testing.T) {
	_, err := PDFParser{MinBookSize: -1}.Parse(context.Background(), book.NewBytesBlob([]byte("not a pdf")))
	if !errors.Is(err, ErrNotPDF) {
		t.Fatalf("error = %v, want ErrNotPDF", err)
	}
}

func TestParsePDFMissingTools(t *testing.T) {
	_, err := PDFParser{MinBookSize: -1}.Parse(context.Background(), book.NewBytesBlob([]byte("%PDF-1.4\n")))
	if !errors.Is(err, ErrToolsUnavailable) {
		t.Fatalf("error = %v, want ErrToolsUnavailable", err)
	}
}

func TestParsePDFNilBlob(t *testing.T) {
	if _, err := (PDFParser{}).Parse(context.Background(), nil); err == nil {
		t.Fatal("expected an error for a nil blob")
	}
}

func TestParsePDFCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := PDFParser{MinBookSize: -1}.Parse(ctx, book.NewBytesBlob([]byte("%PDF-1.4\n")))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context.Canceled", err)
	}
}

func TestHasPDFHeader(t *testing.T) {
	junk := []byte("junk\n%PDF-1.7\n")
	if ok, err := hasPDFHeader(book.NewBytesBlob(junk), int64(len(junk))); err != nil || !ok {
		t.Errorf("hasPDFHeader = %v, %v, want true", ok, err)
	}
	plain := []byte("not a pdf")
	if ok, err := hasPDFHeader(book.NewBytesBlob(plain), int64(len(plain))); err != nil || ok {
		t.Errorf("hasPDFHeader = %v, %v, want false", ok, err)
	}
}

func TestDetectTools(t *testing.T) {
	dir := t.TempDir()
	if _, err := DetectTools(dir); err == nil {
		t.Fatal("expected an error for an empty tools directory")
	}
	for _, name := range []string{"pdftotext", "pdfinfo"} {
		if err := os.WriteFile(dir+"/"+name, []byte("x"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	tools, err := DetectTools(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !tools.Available() {
		t.Errorf("tools = %+v, want available", tools)
	}
}

var _ parser.Parser = PDFParser{}
