package txt

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/f0d0r/margaret-ebook-library/book"
	"golang.org/x/text/encoding"
	"golang.org/x/text/encoding/charmap"
	"golang.org/x/text/encoding/unicode"
	"golang.org/x/text/transform"
)

const encodedText = "Title: Árvíztűrő tükörfúrógép\n" +
	"Author: Kovács Béla\n\n" +
	"A magyar nyelvű szöveg hosszabb annak érdekében, hogy a statisztikai " +
	"kódolásfelismerő biztosabban felismerje az ISO-8859-2 vagy a windows-1250 " +
	"kódolást. Árvíztűrő tükörfúrógép, őúóüűöáéí.\n"

func encodeWith(t *testing.T, enc encoding.Encoding, text string) []byte {
	t.Helper()
	b, err := enc.NewEncoder().Bytes([]byte(text))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestDetectionRoundTrip(t *testing.T) {
	plain := []byte(encodedText)
	cases := []struct {
		name string
		data []byte
	}{
		{"utf-8", plain},
		{"utf-8 bom", append([]byte{0xEF, 0xBB, 0xBF}, plain...)},
		{"utf-16le bom", encodeWith(t, unicode.UTF16(unicode.LittleEndian, unicode.UseBOM), encodedText)},
		{"utf-16be bom", encodeWith(t, unicode.UTF16(unicode.BigEndian, unicode.UseBOM), encodedText)},
		{"iso-8859-2", encodeWith(t, charmap.ISO8859_2, encodedText)},
		{"windows-1250", encodeWith(t, charmap.Windows1250, encodedText)},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			blob := book.NewBytesBlob(c.data)
			detected, err := detectEncoding(blob, int64(len(c.data)))
			if err != nil {
				t.Fatal(err)
			}
			head, err := readHead(blob, int64(len(c.data)), detected)
			if err != nil {
				t.Fatal(err)
			}
			if head != encodedText {
				t.Errorf("decoded text differs:\n got %q\nwant %q", head, encodedText)
			}
		})
	}
}

func TestResolveSingleByteHungarian(t *testing.T) {
	cases := []struct {
		name string
		enc  encoding.Encoding
	}{
		{"iso-8859-2", charmap.ISO8859_2},
		{"windows-1250", charmap.Windows1250},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			data := encodeWith(t, c.enc, encodedText)
			// Simulate chardet's common windows-1252 misdetection.
			chosen := resolveSingleByte(data, "windows-1252")
			decoded, _, err := transform.String(chosen.NewDecoder(), string(data))
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(decoded, "ő") || !strings.Contains(decoded, "ű") {
				t.Errorf("decoded text lost Hungarian double accents: %q", decoded)
			}
		})
	}
}

func TestResolveSingleByteKeepsWesternText(t *testing.T) {
	const text = "The quick brown fox jumps over the lazy dog. This is plain English text."
	data := encodeWith(t, charmap.Windows1252, text)
	chosen := resolveSingleByte(data, "windows-1252")
	decoded, _, err := transform.String(chosen.NewDecoder(), string(data))
	if err != nil {
		t.Fatal(err)
	}
	if decoded != text {
		t.Errorf("decoded = %q, want %q", decoded, text)
	}
}

func TestReadHeadStopsAtRuneBoundary(t *testing.T) {
	text := strings.Repeat("世", headLimit)
	data := []byte(text)
	blob := book.NewBytesBlob(data)
	detected, err := detectEncoding(blob, int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	head, err := readHead(blob, int64(len(data)), detected)
	if err != nil {
		t.Fatal(err)
	}
	if len(head) > headLimit {
		t.Errorf("head has %d bytes, want at most %d", len(head), headLimit)
	}
	if !utf8.ValidString(head) || strings.ContainsRune(head, utf8.RuneError) {
		t.Errorf("head is not valid UTF-8 at the truncation boundary")
	}
}

func TestLooksBinary(t *testing.T) {
	if looksBinary("Title: The Great Novel\n\nBody text.\n") {
		t.Error("plain text reported as binary")
	}
	if !looksBinary(strings.Repeat("\x00", 100)) {
		t.Error("NUL bytes not reported as binary")
	}
	if !looksBinary(strings.Repeat("\x01a", 100)) {
		t.Error("control characters not reported as binary")
	}
}
