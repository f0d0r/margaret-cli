package rtf

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"slices"
	"strings"
	"testing"

	"github.com/f0d0r/margaret-cli/internal/identifier"
	"github.com/f0d0r/margaret-cli/internal/parser"
	"github.com/f0d0r/margaret-ebook-library/book"
	"github.com/f0d0r/margaret-ebook-library/tools"
)

func parseRTF(t *testing.T, data string, minSize int64) (parser.Metadata, error) {
	t.Helper()
	return RtfParser{MinBookSize: minSize}.Parse(context.Background(), book.NewBytesBlob([]byte(data)))
}

func decodeRTF(t *testing.T, data string) string {
	t.Helper()
	decoded, err := io.ReadAll(newTextDecoder(strings.NewReader(data)))
	if err != nil {
		t.Fatal(err)
	}
	return string(decoded)
}

func sha256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func TestDecodeRTFText(t *testing.T) {
	cases := []struct {
		name string
		rtf  string
		want string
	}{
		{
			name: "paragraphs",
			rtf:  `{\rtf1\ansi Hello\par World}`,
			want: "Hello\nWorld",
		},
		{
			name: "formatting groups",
			rtf:  `{\rtf1 This is {\b bold} text.}`,
			want: "This is bold text.",
		},
		{
			name: "escaped braces",
			rtf:  `{\rtf1 \{\}}`,
			want: "{}",
		},
		{
			name: "escaped backslash",
			rtf:  `{\rtf1 \\}`,
			want: "\\",
		},
		{
			name: "code page",
			rtf:  `{\rtf1\ansi\ansicpg1250 \'e1\'e9}`,
			want: "áé",
		},
		{
			name: "utf-8 bom",
			rtf:  "\ufeff{\\rtf1\\ansi Body}",
			want: "Body",
		},
		{
			name: "unicode fallback skipped",
			rtf:  `{\rtf1 \u233?}`,
			want: "é",
		},
		{
			name: "unicode without fallback",
			rtf:  `{\rtf1\uc0 \u233?}`,
			want: "é?",
		},
		{
			name: "multi character fallback",
			rtf:  `{\rtf1\uc2 \u233\'e9?X}`,
			want: "éX",
		},
		{
			name: "skipped destinations",
			rtf:  `{\rtf1{\fonttbl{\f0 Arial;}}{\colortbl;\red0\green0\blue0;}{\*\generator Foo;}{\info{\title Secret}}Body}`,
			want: "Body",
		},
		{
			name: "field result",
			rtf:  `{\rtf1{\field{\*\fldinst{HYPERLINK "http://example.org"}}{\fldrslt{link}}}}`,
			want: "link",
		},
		{
			name: "binary data",
			rtf:  `{\rtf1 A\bin3 xyz\par B}`,
			want: "A\nB",
		},
		{
			name: "hidden text",
			rtf:  `{\rtf1 visible{\v hidden} text}`,
			want: "visible text",
		},
		{
			name: "line and tab",
			rtf:  `{\rtf1 A\line B\tab C}`,
			want: "A\nB\tC",
		},
		{
			name: "unicode alternative group",
			rtf:  `{\rtf1{\upr{\'e1}{\*\ud{\u225}}}X}`,
			want: "áX",
		},
		{
			name: "control symbols",
			rtf:  `{\rtf1 A\emdash B\endash C\bullet D\lquote E\rquote F\ldblquote G\rdblquote H}`,
			want: "A—B–C•D‘E’F“G”H",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := decodeRTF(t, c.rtf); got != c.want {
				t.Errorf("decoded text = %q, want %q", got, c.want)
			}
		})
	}
}

func TestParseInfoMetadata(t *testing.T) {
	body := strings.Repeat("The quick brown fox jumps over the lazy dog. ", 40)
	data := `{\rtf1\ansi{\info{\title The Great Novel}{\author Jane Doe, John Smith}` +
		`{\subject A short description}{\category Fiction, Adventure}{\manager Acme Press}` +
		`{\keywords Epic}}{\info{\title Second Title}}` + body + `}`
	md, err := parseRTF(t, data, -1)
	if err != nil {
		t.Fatal(err)
	}
	if md.Title != "The Great Novel" {
		t.Errorf("title = %q, want %q", md.Title, "The Great Novel")
	}
	if want := []string{"Jane Doe", "John Smith"}; strings.Join(md.Authors, "|") != strings.Join(want, "|") {
		t.Errorf("authors = %v, want %v", md.Authors, want)
	}
	if md.Description != "A short description" {
		t.Errorf("description = %q, want %q", md.Description, "A short description")
	}
	if want := []string{"Fiction", "Adventure", "Epic"}; strings.Join(md.Subjects, "|") != strings.Join(want, "|") {
		t.Errorf("subjects = %v, want %v", md.Subjects, want)
	}
	if md.Publisher != "Acme Press" {
		t.Errorf("publisher = %q, want %q", md.Publisher, "Acme Press")
	}
}

func TestParseInfoEncodedTitle(t *testing.T) {
	data := `{\rtf1\ansi\ansicpg1250{\info{\title \'c1rv\'edzt\'fbr\'f5 t\'fck\'f6rf\'far\'f3g\'e9p}{\author Kov\'e1cs B\'e9la}}` +
		strings.Repeat("The quick brown fox jumps over the lazy dog. ", 40)
	md, err := parseRTF(t, data, -1)
	if err != nil {
		t.Fatal(err)
	}
	if md.Title != "Árvíztűrő tükörfúrógép" {
		t.Errorf("title = %q, want %q", md.Title, "Árvíztűrő tükörfúrógép")
	}
	if want := []string{"Kovács Béla"}; strings.Join(md.Authors, "|") != strings.Join(want, "|") {
		t.Errorf("authors = %v, want %v", md.Authors, want)
	}
}

func TestParseInfoRawHighByteTitle(t *testing.T) {
	data := "{\\rtf1\\ansi{\\info{\\title Caf\xe9}{\\author Jane Doe}} " +
		strings.Repeat("The quick brown fox jumps over the lazy dog. ", 40) + "}"
	md, err := parseRTF(t, data, -1)
	if err != nil {
		t.Fatal(err)
	}
	if md.Title != "Café" {
		t.Errorf("title = %q, want %q", md.Title, "Café")
	}
}

func TestParseRTFStripsBOM(t *testing.T) {
	data := "\ufeff{\\rtf1\\ansi Moby Dick\\par\\par\\par Herman Melville\\par Call me Ishmael.}"
	md, err := parseRTF(t, data, -1)
	if err != nil {
		t.Fatal(err)
	}
	if md.Title != "Moby Dick" {
		t.Errorf("title = %q, want %q", md.Title, "Moby Dick")
	}
	if want := []string{"Herman Melville"}; strings.Join(md.Authors, "|") != strings.Join(want, "|") {
		t.Errorf("authors = %v, want %v", md.Authors, want)
	}
}

func TestParseTitlePageFallback(t *testing.T) {
	data := `{\rtf1\ansi Moby Dick\par\par\par Herman Melville\par Call me Ishmael.}`
	md, err := parseRTF(t, data, -1)
	if err != nil {
		t.Fatal(err)
	}
	if md.Title != "Moby Dick" {
		t.Errorf("title = %q, want %q", md.Title, "Moby Dick")
	}
	if want := []string{"Herman Melville"}; strings.Join(md.Authors, "|") != strings.Join(want, "|") {
		t.Errorf("authors = %v, want %v", md.Authors, want)
	}
}

func TestParseRTFMetadataPipeline(t *testing.T) {
	body := "Title: The Great Novel\nAuthor: Jane Doe\n\nISBN 978-3-16-148410-0\n\n" +
		strings.Repeat("The quick brown fox jumps over the lazy dog. ", 40)
	data := `{\rtf1\ansi ` + strings.ReplaceAll(body, "\n", `\par `) + `}`
	md, err := parseRTF(t, data, -1)
	if err != nil {
		t.Fatal(err)
	}
	if md.Title != "The Great Novel" {
		t.Errorf("title = %q, want %q", md.Title, "The Great Novel")
	}
	if want := []string{"Jane Doe"}; strings.Join(md.Authors, "|") != strings.Join(want, "|") {
		t.Errorf("authors = %v, want %v", md.Authors, want)
	}
	wantID := identifier.Identifier{Scheme: "ISBN", Value: "9783161484100", Source: identifier.SourceText}
	if len(md.Identifiers) != 1 || md.Identifiers[0] != wantID {
		t.Errorf("identifiers = %v, want %v", md.Identifiers, []identifier.Identifier{wantID})
	}
	if md.Hash != sha256Hex([]byte(data)) {
		t.Errorf("hash = %q, want %q", md.Hash, sha256Hex([]byte(data)))
	}
	expected := strings.ReplaceAll(body, "\n", "\n")
	fp, err := tools.FingerprintText(expected, tools.WithMinHash(tools.MinHashConfig{NumHashes: 128, ShingleSize: 5}))
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(md.MinHash, fp.MinHash) {
		t.Errorf("minhash differs from the decoded plain text")
	}
	if len(md.Languages) != 1 || md.Languages[0] != "en" {
		t.Errorf("languages = %v, want [en]", md.Languages)
	}
}

func TestParseRTFMinSize(t *testing.T) {
	tiny := `{\rtf1\ansi Title: Tiny\par Author: Jane Doe\par ` +
		strings.Repeat("A reggel hűvös volt. ", 20) + `}`
	if _, err := parseRTF(t, tiny, 0); !errors.Is(err, parser.ErrNotABook) {
		t.Errorf("default parse error = %v, want ErrNotABook", err)
	}
	if _, err := parseRTF(t, tiny, -1); err != nil {
		t.Errorf("disabled size filter returned error: %v", err)
	}
	if got := (RtfParser{}).MinSize(); got != parser.DefaultMinDocumentSize {
		t.Errorf("default MinSize() = %d, want %d", got, parser.DefaultMinDocumentSize)
	}
}

func TestParseNotRTF(t *testing.T) {
	if _, err := parseRTF(t, "Title: The Great Novel\n\nBody.\n", -1); !errors.Is(err, ErrNotRTF) {
		t.Fatalf("error = %v, want ErrNotRTF", err)
	}
}

func TestParseNoText(t *testing.T) {
	data := `{\rtf1{\fonttbl{\f0 Arial;}}{\pict\pngblip 89504e47}}`
	if _, err := parseRTF(t, data, -1); !errors.Is(err, parser.ErrNotABook) {
		t.Fatalf("error = %v, want ErrNotABook", err)
	}
}

func TestParseRTFCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := (RtfParser{MinBookSize: -1}).Parse(ctx, book.NewBytesBlob([]byte(`{\rtf1 Body}`)))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context.Canceled", err)
	}
}

func TestParseRTFNilBlob(t *testing.T) {
	if _, err := (RtfParser{}).Parse(context.Background(), nil); err == nil {
		t.Fatal("expected an error for a nil blob")
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

func TestParseRTFReadsEachByteOnce(t *testing.T) {
	body := "The quick brown fox jumps over the lazy dog. "
	cases := []struct {
		name string
		rtf  string
	}{
		{
			name: "small",
			rtf:  `{\rtf1\ansi ` + strings.Repeat(body, 100) + `}`,
		},
		{
			name: "large",
			rtf:  `{\rtf1\ansi ` + strings.Repeat(body, 5000) + `}`,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			data := []byte(c.rtf)
			blob := &countingBlob{blob: book.NewBytesBlob(data)}
			if _, err := (RtfParser{MinBookSize: -1}).Parse(context.Background(), blob); err != nil {
				t.Fatal(err)
			}
			want := int64(min(len(data), signatureWindow) + len(data))
			if blob.read != want {
				t.Errorf("read %d bytes, want %d", blob.read, want)
			}
		})
	}
}

func TestParseRTFJunkStopsEarly(t *testing.T) {
	body := "<!doctype html><html><body>" +
		strings.Repeat("lorem ipsum dolor sit amet ", 20000) + "</body></html>"
	data := []byte(`{\rtf1\ansi ` + body + `}`)
	blob := &countingBlob{blob: book.NewBytesBlob(data)}
	if _, err := (RtfParser{MinBookSize: -1}).Parse(context.Background(), blob); !errors.Is(err, parser.ErrNotABook) {
		t.Fatalf("error = %v, want ErrNotABook", err)
	}
	if blob.read >= int64(len(data)) {
		t.Errorf("read %d bytes of %d, want an early stop", blob.read, len(data))
	}
}
