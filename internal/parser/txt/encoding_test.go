package txt

import (
	"io"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/f0d0r/margaret-cli/internal/parser/textdoc"
	"github.com/f0d0r/margaret-ebook-library/book"
	"github.com/gogs/chardet"
	"golang.org/x/text/encoding"
	"golang.org/x/text/encoding/charmap"
	"golang.org/x/text/encoding/japanese"
	"golang.org/x/text/encoding/korean"
	"golang.org/x/text/encoding/simplifiedchinese"
	"golang.org/x/text/encoding/traditionalchinese"
	"golang.org/x/text/encoding/unicode"
	"golang.org/x/text/transform"
)

// baseSamples are plain-language paragraphs used to build long enough samples
// for the statistical detector.
var baseSamples = map[string]string{
	"hu": "A magyar nyelv az uráli nyelvcsalád tagja, és a honfoglalás óta " +
		"több mint ezer éve beszélik a Kárpát-medencében. A magyar irodalom " +
		"gazdag hagyományokkal rendelkezik, a népmeséktől a modern regényekig.",
	"en": "The quick brown fox jumps over the lazy dog. Language detection " +
		"should identify this passage as English, because it contains many " +
		"common English words and a familiar sentence structure.",
	"ru": "Русский язык относится к восточнославянской группе языков и " +
		"используется миллионами людей. Этот текст содержит типичные русские " +
		"слова и выражения.",
	"ar": "اللغة العربية هي واحدة من أكثر اللغات انتشارا في العالم، ويتحدث بها " +
		"ملايين الناس في الوطن العربي. هذا النص مكتوب لاختبار التعرف على اللغة.",
	"el": "Η ελληνική γλώσσα είναι μία από τις αρχαιότερες γλώσσες του " +
		"κόσμου και μιλιέται ακόμη και σήμερα. Αυτό το κείμενο γράφτηκε για " +
		"να δοκιμαστεί η αναγνώριση γλώσσας.",
	"he": "השפה העברית היא אחת השפות העתיקות בעולם והיא מדוברת גם היום. " +
		"הטקסט הזה נכתב רק כדי לבדוק את מערכת זיהוי השפה.",
	"tr": "Türkçe, Ural-Altay dil ailesine ait olduğu düşünülen bir dildir " +
		"ve milyonlarca insan tarafından konuşulur. Bu metin dil algılamayı " +
		"denemek için yazılmıştır.",
	"zh": "中文是世界上使用人数最多的语言之一，主要在中国和世界各地的华人社区中使用。" +
		"这段文字只是为了测试语言识别系统而写的，包含常见的汉语词汇和句式。",
	"zh-tw": "中文是世界上使用人數最多的語言之一，主要在中國以及世界各地的華人社會中使用。" +
		"這段文字只是為了測試語言辨識系統而寫的，包含常見的漢語詞彙和句式。",
	"ja": "日本語は日本で広く使われている言語であり、多くの人々によって話されています。" +
		"この文章は言語検出システムの動作を確認するためだけに書かれました。",
	"ko": "한국어는 한국에서 사용되는 언어이며 많은 사람들이 사용합니다. " +
		"이 글은 언어 감지 시스템의 작동을 확인하기 위해서만 작성되었습니다.",
	"te": "తెలుగు దక్షిణ భారతదేశంలో మాట్లాడే ఒక ప్రధాన భాష. ఈ పాఠం భాష " +
		"గుర్తింపు వ్యవస్థను పరీక్షించడానికి మాత్రమే రాయబడింది.",
}

// encodedText is a small sample book head used by the parser tests.
const encodedText = "Title: Árvíztűrő tükörfúrógép\n" +
	"Author: Kovács Béla\n\n" +
	"A magyar nyelvű szöveg hosszabb annak érdekében, hogy a statisztikai " +
	"kódolásfelismerő biztosabban felismerje az ISO-8859-2 vagy a windows-1250 " +
	"kódolást. Árvíztűrő tükörfúrógép, őúóüűöáéí.\n"

func longSample(lang string) string {
	return strings.Repeat(baseSamples[lang]+" ", 8)
}

func encodeWith(t *testing.T, enc encoding.Encoding, text string) []byte {
	t.Helper()
	b, err := enc.NewEncoder().Bytes([]byte(text))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// TestDetectUTF8 covers scripts that reach the parser as plain UTF-8. None of
// them needs a legacy code page, so the fast path must keep them intact.
func TestDetectUTF8(t *testing.T) {
	for _, lang := range []string{"hu", "en", "ru", "ar", "el", "he", "tr", "zh", "ja", "ko", "te"} {
		t.Run(lang, func(t *testing.T) {
			text := longSample(lang)
			assertRoundTrip(t, []byte(text), text)
		})
	}
}

func TestDetectUTF16(t *testing.T) {
	text := longSample("ar")
	assertRoundTrip(t, encodeWith(t, unicode.UTF16(unicode.LittleEndian, unicode.UseBOM), text), text)
	assertRoundTrip(t, encodeWith(t, unicode.UTF16(unicode.BigEndian, unicode.UseBOM), text), text)
}

// TestDetectLegacySingleByte covers the one-byte code pages the detector can
// emit. Every script is checked through the same generic pipeline.
func TestDetectLegacySingleByte(t *testing.T) {
	cases := []struct {
		name string
		lang string
		enc  encoding.Encoding
	}{
		{"iso-8859-2", "hu", charmap.ISO8859_2},
		{"windows-1250", "hu", charmap.Windows1250},
		{"english windows-1252", "en", charmap.Windows1252},
		{"russian windows-1251", "ru", charmap.Windows1251},
		{"russian iso-8859-5", "ru", charmap.ISO8859_5},
		{"arabic windows-1256", "ar", charmap.Windows1256},
		{"arabic iso-8859-6", "ar", charmap.ISO8859_6},
		{"greek iso-8859-7", "el", charmap.ISO8859_7},
		{"hebrew windows-1255", "he", charmap.Windows1255},
		{"turkish windows-1254", "tr", charmap.Windows1254},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			text := longSample(tc.lang)
			assertRoundTrip(t, encodeWith(t, tc.enc, text), text)
		})
	}
}

// TestDetectLegacyMultiByte covers the legacy CJK code pages.
func TestDetectLegacyMultiByte(t *testing.T) {
	cases := []struct {
		name string
		lang string
		enc  encoding.Encoding
	}{
		{"chinese gb18030", "zh", simplifiedchinese.GB18030},
		{"chinese big5", "zh-tw", traditionalchinese.Big5},
		{"japanese shift_jis", "ja", japanese.ShiftJIS},
		{"japanese euc-jp", "ja", japanese.EUCJP},
		{"korean euc-kr", "ko", korean.EUCKR},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			text := longSample(tc.lang)
			assertRoundTrip(t, encodeWith(t, tc.enc, text), text)
		})
	}
}

// referenceDetectLegacy is the exhaustive candidate scoring. The parser's fast
// path must always agree with it.
func referenceDetectLegacy(sample []byte) encoding.Encoding {
	results, err := chardet.NewTextDetector().DetectAll(sample)
	if err != nil {
		return charmap.Windows1252
	}
	seen := make(map[string]struct{}, len(results))
	var candidates []legacyCandidate
	for _, result := range results {
		label := strings.TrimSpace(result.Charset)
		key := strings.ToLower(label)
		if _, dup := seen[key]; dup || key == "" {
			continue
		}
		seen[key] = struct{}{}
		enc, ok := encodingForLabel(label)
		if !ok {
			continue
		}
		candidate, text, ok := decodeCandidate(sample, enc, result.Confidence)
		if !ok {
			continue
		}
		candidate.scoreLanguage(text)
		candidates = append(candidates, candidate)
		if len(candidates) >= candidateLimit {
			break
		}
	}
	if len(candidates) == 0 {
		return charmap.Windows1252
	}
	return pickCandidate(candidates).enc
}

// TestDetectLegacyMatchesFullScoring pins the skipped language passes: the fast
// path must choose the same encoding as exhaustive scoring for every sample.
func TestDetectLegacyMatchesFullScoring(t *testing.T) {
	encodings := map[string]encoding.Encoding{
		"iso-8859-1":   charmap.ISO8859_1,
		"iso-8859-2":   charmap.ISO8859_2,
		"iso-8859-5":   charmap.ISO8859_5,
		"iso-8859-6":   charmap.ISO8859_6,
		"iso-8859-7":   charmap.ISO8859_7,
		"iso-8859-15":  charmap.ISO8859_15,
		"windows-1250": charmap.Windows1250,
		"windows-1251": charmap.Windows1251,
		"windows-1252": charmap.Windows1252,
		"windows-1254": charmap.Windows1254,
		"windows-1255": charmap.Windows1255,
		"windows-1256": charmap.Windows1256,
		"koi8-r":       charmap.KOI8R,
	}
	langs := []string{"hu", "en", "ru", "ar", "el", "he", "tr"}
	for name, enc := range encodings {
		for _, lang := range langs {
			t.Run(name+"/"+lang, func(t *testing.T) {
				sample, err := enc.NewEncoder().Bytes([]byte(longSample(lang)))
				if err != nil {
					t.Skipf("%s cannot encode %s", name, lang)
				}
				want := referenceDetectLegacy(sample)
				if got := detectLegacy(sample); got != want {
					t.Errorf("detectLegacy = %v, want %v", got, want)
				}
			})
		}
	}
	// Truncated input exercises the fallback tail trimming of both paths.
	multibyte := encodeWith(t, japanese.ShiftJIS, longSample("ja"))
	truncated := multibyte[:len(multibyte)-1]
	if got, want := detectLegacy(truncated), referenceDetectLegacy(truncated); got != want {
		t.Errorf("truncated: detectLegacy = %v, want %v", got, want)
	}
}

func BenchmarkDetectLegacy(b *testing.B) {
	text := strings.Repeat(baseSamples["hu"]+" ", 40)
	data, err := charmap.ISO8859_2.NewEncoder().Bytes([]byte(text))
	if err != nil {
		b.Fatal(err)
	}
	b.SetBytes(int64(len(data)))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if enc := detectLegacy(data); enc == nil {
			b.Fatal("no encoding")
		}
	}
}

// BenchmarkDetectLegacyFull scores every candidate, the cost the fast path
// avoids.
func BenchmarkDetectLegacyFull(b *testing.B) {
	text := strings.Repeat(baseSamples["hu"]+" ", 40)
	data, err := charmap.ISO8859_2.NewEncoder().Bytes([]byte(text))
	if err != nil {
		b.Fatal(err)
	}
	b.SetBytes(int64(len(data)))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if enc := referenceDetectLegacy(data); enc == nil {
			b.Fatal("no encoding")
		}
	}
}

func assertRoundTrip(t *testing.T, data []byte, want string) {
	t.Helper()
	blob := book.NewBytesBlob(data)
	detected, err := detectEncoding(blob, int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	head := decodeHead(t, blob, int64(len(data)), detected)
	if !utf8.ValidString(head) {
		t.Fatalf("decoded head is not valid UTF-8")
	}
	if head != want {
		t.Errorf("decoded text differs:\n got %q\nwant %q", head, want)
	}
}

// decodeHead decodes at most headLimit bytes through the same gate the parser
// uses, so the decoding and rune-boundary behavior are covered together.
func decodeHead(t *testing.T, b book.Blob, size int64, d detectedEncoding) string {
	t.Helper()
	gate := textdoc.NewHeadGate(headLimit, size, nil)
	section := io.NewSectionReader(b, d.bom, size-d.bom)
	if _, err := io.Copy(gate, transform.NewReader(section, d.enc.NewDecoder())); err != nil {
		t.Fatal(err)
	}
	return gate.Head()
}

func TestDecodeHeadStopsAtRuneBoundary(t *testing.T) {
	text := strings.Repeat("世", headLimit)
	data := []byte(text)
	blob := book.NewBytesBlob(data)
	detected, err := detectEncoding(blob, int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	head := decodeHead(t, blob, int64(len(data)), detected)
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
