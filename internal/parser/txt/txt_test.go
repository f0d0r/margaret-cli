package txt

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/f0d0r/margaret-cli/internal/identifier"
	"github.com/f0d0r/margaret-cli/internal/parser"
	"github.com/f0d0r/margaret-ebook-library/book"
	"github.com/f0d0r/margaret-ebook-library/tools"
	"golang.org/x/text/encoding/charmap"
	"golang.org/x/text/encoding/unicode"
)

func parseText(t *testing.T, ctx context.Context, data []byte) (parser.Metadata, error) {
	t.Helper()
	// Metadata tests use tiny synthetic texts, so the size filter is disabled
	// here; TestTxtParserMinSize covers the filter itself.
	return TxtParser{MinBookSize: -1}.Parse(ctx, book.NewBytesBlob(data))
}

func sha256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func minHashOf(t *testing.T, text string) []uint64 {
	t.Helper()
	fp, err := tools.FingerprintText(text, tools.WithMinHash(tools.MinHashConfig{NumHashes: 128, ShingleSize: 5}))
	if err != nil {
		t.Fatal(err)
	}
	return fp.MinHash
}

func TestParseLabeledHeader(t *testing.T) {
	text := "Title: The Great Novel\nAuthor: Jane Doe & John Smith\n\n" +
		"ISBN 978-3-16-148410-0\n\nThe body begins here.\n"
	data := []byte(text)

	md, err := parseText(t, context.Background(), data)
	if err != nil {
		t.Fatal(err)
	}
	if md.Title != "The Great Novel" {
		t.Errorf("title = %q, want %q", md.Title, "The Great Novel")
	}
	if want := []string{"Jane Doe", "John Smith"}; !slices.Equal(md.Authors, want) {
		t.Errorf("authors = %v, want %v", md.Authors, want)
	}
	wantIDs := []identifier.Identifier{{Scheme: "ISBN", Value: "9783161484100", Source: identifier.SourceText}}
	if !slices.Equal(md.Identifiers, wantIDs) {
		t.Errorf("identifiers = %v, want %v", md.Identifiers, wantIDs)
	}
	if md.Hash != sha256Hex(data) {
		t.Errorf("hash = %q, want %q", md.Hash, sha256Hex(data))
	}
	if want := minHashOf(t, text); !slices.Equal(md.MinHash, want) {
		t.Errorf("minhash differs from FingerprintText")
	}
}

func TestParseCalibreTitlePage(t *testing.T) {
	md, err := parseText(t, context.Background(), []byte("Moby Dick\n\n\nHerman Melville\nCall me Ishmael.\n"))
	if err != nil {
		t.Fatal(err)
	}
	if md.Title != "Moby Dick" {
		t.Errorf("title = %q, want %q", md.Title, "Moby Dick")
	}
	if want := []string{"Herman Melville"}; !slices.Equal(md.Authors, want) {
		t.Errorf("authors = %v, want %v", md.Authors, want)
	}
}

func TestParseGutenbergHeader(t *testing.T) {
	text := "The Project Gutenberg eBook of Moby Dick\n\n" +
		"This eBook is for the use of anyone anywhere.\n\n" +
		"Title: Moby Dick\n\nAuthor: Herman Melville\n\n" +
		"*** START OF THE PROJECT GUTENBERG EBOOK MOBY DICK ***\n\nCHAPTER 1.\n"
	md, err := parseText(t, context.Background(), []byte(text))
	if err != nil {
		t.Fatal(err)
	}
	if md.Title != "Moby Dick" {
		t.Errorf("title = %q, want %q", md.Title, "Moby Dick")
	}
	if want := []string{"Herman Melville"}; !slices.Equal(md.Authors, want) {
		t.Errorf("authors = %v, want %v", md.Authors, want)
	}
}

func TestParseYAMLFrontMatter(t *testing.T) {
	text := "---\ntitle: \"The Great Novel\"\nauthor: [Jane Doe, John Smith]\n---\n\nThe body.\n"
	md, err := parseText(t, context.Background(), []byte(text))
	if err != nil {
		t.Fatal(err)
	}
	if md.Title != "The Great Novel" {
		t.Errorf("title = %q, want %q", md.Title, "The Great Novel")
	}
	if want := []string{"Jane Doe", "John Smith"}; !slices.Equal(md.Authors, want) {
		t.Errorf("authors = %v, want %v", md.Authors, want)
	}
}

func TestParseEncodedText(t *testing.T) {
	cases := []struct {
		name string
		data []byte
	}{
		{"utf-16le", encodeWith(t, unicode.UTF16(unicode.LittleEndian, unicode.UseBOM), encodedText)},
		{"iso-8859-2", encodeWith(t, charmap.ISO8859_2, encodedText)},
		{"windows-1250", encodeWith(t, charmap.Windows1250, encodedText)},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			md, err := parseText(t, context.Background(), c.data)
			if err != nil {
				t.Fatal(err)
			}
			if md.Title != "Árvíztűrő tükörfúrógép" {
				t.Errorf("title = %q, want %q", md.Title, "Árvíztűrő tükörfúrógép")
			}
			if want := []string{"Kovács Béla"}; !slices.Equal(md.Authors, want) {
				t.Errorf("authors = %v, want %v", md.Authors, want)
			}
			if md.Hash != sha256Hex(c.data) {
				t.Errorf("hash = %q, want %q", md.Hash, sha256Hex(c.data))
			}
			if want := minHashOf(t, encodedText); !slices.Equal(md.MinHash, want) {
				t.Errorf("minhash differs from FingerprintText of the decoded text")
			}
		})
	}
}

func TestParseISBNOutsideHead(t *testing.T) {
	filler := strings.Repeat("filler text. ", 6000)
	text := "Title: The Great Novel\n\n" + filler + "\nISBN 978-3-16-148410-0\n"
	if len(text) <= headLimit {
		t.Fatalf("test text is %d bytes, it must exceed the %d-byte head", len(text), headLimit)
	}
	md, err := parseText(t, context.Background(), []byte(text))
	if err != nil {
		t.Fatal(err)
	}
	if len(md.Identifiers) != 0 {
		t.Errorf("identifiers = %v, want none outside the head window", md.Identifiers)
	}
}

func TestParseRejectsEmptyText(t *testing.T) {
	cases := map[string][]byte{
		"empty":           {},
		"whitespace only": []byte(" \n\t\r\n"),
		"utf-8 bom only":  {0xEF, 0xBB, 0xBF},
	}
	for name, data := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := parseText(t, context.Background(), data); !errors.Is(err, ErrEmptyText) {
				t.Fatalf("error = %v, want ErrEmptyText", err)
			}
		})
	}
}

func TestParseRejectsBinary(t *testing.T) {
	data := strings.Repeat("\x00a", 512)
	if _, err := parseText(t, context.Background(), []byte(data)); !errors.Is(err, ErrBinaryText) {
		t.Fatalf("error = %v, want ErrBinaryText", err)
	}
}

func TestTxtParserMinSize(t *testing.T) {
	tiny := []byte("Title: Tiny\nAuthor: Jane Doe\n\n" + strings.Repeat("A reggel hűvös volt. ", 20))

	if got := (TxtParser{}).MinSize(); got != parser.DefaultMinDocumentSize {
		t.Errorf("default MinSize() = %d, want %d", got, parser.DefaultMinDocumentSize)
	}
	if _, err := (TxtParser{}).Parse(context.Background(), book.NewBytesBlob(tiny)); !errors.Is(err, parser.ErrNotABook) {
		t.Errorf("default parse error = %v, want ErrNotABook", err)
	}
	if _, err := (TxtParser{MinBookSize: 1 << 20}).Parse(context.Background(), book.NewBytesBlob(tiny)); !errors.Is(err, parser.ErrNotABook) {
		t.Errorf("large threshold parse error = %v, want ErrNotABook", err)
	}
	if _, err := (TxtParser{MinBookSize: -1}).Parse(context.Background(), book.NewBytesBlob(tiny)); err != nil {
		t.Errorf("disabled size filter returned error: %v", err)
	}
	if _, err := (TxtParser{}).Parse(context.Background(), book.NewBytesBlob([]byte(strings.Repeat("A reggel hűvös volt. ", 800)))); errors.Is(err, parser.ErrNotABook) {
		t.Errorf("file at the default threshold was rejected as too small")
	}
}

func TestParseCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := parseText(t, ctx, []byte("Title: The Great Novel\n")); !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context.Canceled", err)
	}
}

func TestParseNilBlob(t *testing.T) {
	if _, err := (TxtParser{}).Parse(context.Background(), nil); err == nil {
		t.Fatal("expected an error for a nil blob")
	}
}

func TestParseDetectsLanguage(t *testing.T) {
	cases := []struct {
		name string
		text string
		want string
	}{
		{"hungarian", encodedText, "hu"},
		{"english", "The quick brown fox jumps over the lazy dog. This plain text " +
			"book contains enough English words for the language detector to work.\n", "en"},
		{"arabic", "اللغة العربية هي واحدة من أكثر اللغات انتشارا في العالم، ويتحدث بها " +
			"ملايين الناس في الوطن العربي. هذا النص مكتوب لاختبار التعرف على اللغة.\n", "ar"},
		{"chinese", "中文是世界上使用人数最多的语言之一，主要在中国和世界各地的华人社区中使用。" +
			"这段文字只是为了测试语言识别系统而写的，包含常见的汉语词汇和句式。\n", "zh"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			md, err := parseText(t, context.Background(), []byte(c.text))
			if err != nil {
				t.Fatal(err)
			}
			if want := []string{c.want}; !slices.Equal(md.Languages, want) {
				t.Errorf("languages = %v, want %v", md.Languages, want)
			}
		})
	}
}

// countingBlob counts the bytes returned by ReadAt so tests can assert how many
// times the parser traverses the file.
type countingBlob struct {
	blob book.Blob
	read int64
}

func (b *countingBlob) Size() (int64, error) { return b.blob.Size() }

func (b *countingBlob) ReadAt(p []byte, off int64) (int, error) {
	n, err := b.blob.ReadAt(p, off)
	b.read += int64(n)
	return n, err
}

func TestParseTXTReadsEachByteOnce(t *testing.T) {
	body := "The quick brown fox jumps over the lazy dog. "
	cases := []struct {
		name string
		data []byte
	}{
		{"small", []byte(strings.Repeat(body, 400))},
		{"large", []byte(strings.Repeat(body, 4000))},
		{"utf16 bom", encodeWith(t, unicode.UTF16(unicode.LittleEndian, unicode.UseBOM), strings.Repeat(body, 4000))},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			blob := &countingBlob{blob: book.NewBytesBlob(c.data)}
			if _, err := (TxtParser{MinBookSize: -1}).Parse(context.Background(), blob); err != nil {
				t.Fatal(err)
			}
			want := int64(min(len(c.data), sampleLimit) + len(c.data))
			if blob.read != want {
				t.Errorf("read %d bytes, want %d", blob.read, want)
			}
		})
	}
}

func TestParseTXTJunkStopsEarly(t *testing.T) {
	body := "<!doctype html><html><body>" +
		strings.Repeat("lorem ipsum dolor sit amet ", 20000) + "</body></html>"
	data := []byte(body)
	blob := &countingBlob{blob: book.NewBytesBlob(data)}
	if _, err := (TxtParser{MinBookSize: -1}).Parse(context.Background(), blob); !errors.Is(err, parser.ErrNotABook) {
		t.Fatalf("error = %v, want ErrNotABook", err)
	}
	if blob.read >= int64(len(data)) {
		t.Errorf("read %d bytes of %d, want an early stop", blob.read, len(data))
	}
}
