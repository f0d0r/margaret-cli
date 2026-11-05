package rtf

import (
	"io"
	"strings"
	"testing"
)

// TestDecodeLongTextRun pins text runs that cross the decoder's buffer
// boundary: plain runs must be emitted byte-for-byte, without reordering
// relative to high bytes or escapes.
func TestDecodeLongTextRun(t *testing.T) {
	text := strings.Repeat("The quick brown fox jumps over the lazy dog. ", 2000)
	rtf := `{\rtf1\ansi ` + text + `}`
	if got := decodeRTF(t, rtf); got != text {
		t.Errorf("decoded %d bytes, want %d", len(got), len(text))
	}
}

// TestDecodeSkippedDestinationRuns pins that a large skipped destination is
// dropped but the markup after it is still decoded.
func TestDecodeSkippedDestinationRuns(t *testing.T) {
	payload := strings.Repeat("89504e47", 100000)
	rtf := `{\rtf1\ansi Before{\pict\pngblip ` + payload + `}After}`
	if got := decodeRTF(t, rtf); got != "BeforeAfter" {
		t.Errorf("decoded text = %q, want %q", got, "BeforeAfter")
	}
}

// TestDecodeInfoHighBytes pins that \info fields keep capturing text even
// though the enclosing group is skipped, including bytes that go through the
// code-page decoder.
func TestDecodeInfoHighBytes(t *testing.T) {
	rtf := `{\rtf1\ansi{\info{\title Caf\'e9}{\author Jane}}Body}`
	d := newTextDecoder(strings.NewReader(rtf))
	decoded, err := io.ReadAll(d)
	if err != nil {
		t.Fatal(err)
	}
	if got := string(decoded); got != "Body" {
		t.Errorf("decoded text = %q, want %q", got, "Body")
	}
	if got := d.info["title"]; got == nil || got.String() != "Café" {
		t.Errorf("info title = %v, want %q", got, "Café")
	}
}

func BenchmarkDecodeText(b *testing.B) {
	text := strings.Repeat("The quick brown fox jumps over the lazy dog. ", 20000)
	data := `{\rtf1\ansi ` + text + `}`
	b.SetBytes(int64(len(data)))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := io.Copy(io.Discard, newTextDecoder(strings.NewReader(data))); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkDecodePict(b *testing.B) {
	payload := strings.Repeat("89504e47", 100000)
	data := `{\rtf1\ansi Before{\pict\pngblip ` + payload + `}After}`
	b.SetBytes(int64(len(data)))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := io.Copy(io.Discard, newTextDecoder(strings.NewReader(data))); err != nil {
			b.Fatal(err)
		}
	}
}
