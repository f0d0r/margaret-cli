package txt

import (
	"bytes"
	"io"
	"strings"
	"unicode/utf8"

	"github.com/f0d0r/margaret-ebook-library/book"
	"github.com/gogs/chardet"
	"golang.org/x/text/encoding"
	"golang.org/x/text/encoding/charmap"
	"golang.org/x/text/encoding/htmlindex"
	"golang.org/x/text/encoding/unicode"
	"golang.org/x/text/transform"
)

const (
	// sampleLimit is how many raw bytes encoding detection inspects.
	sampleLimit = 64 << 10
	// headLimit is how many decoded bytes are kept for metadata and ISBN
	// extraction.
	headLimit = 64 << 10
)

var (
	utf8BOM    = []byte{0xEF, 0xBB, 0xBF}
	utf16LEBOM = []byte{0xFF, 0xFE}
	utf16BEBOM = []byte{0xFE, 0xFF}
)

// detectedEncoding is the encoding of a plain-text file together with the
// length of the byte order mark the decoder must skip.
type detectedEncoding struct {
	enc encoding.Encoding
	bom int64
}

// detectEncoding picks the text encoding of a file: a BOM wins, then valid
// UTF-8, then statistical detection, then the windows-1252 fallback Calibre
// also uses.
func detectEncoding(b book.Blob, size int64) (detectedEncoding, error) {
	sample := make([]byte, min(size, sampleLimit))
	if _, err := io.ReadFull(io.NewSectionReader(b, 0, int64(len(sample))), sample); err != nil {
		return detectedEncoding{}, err
	}

	switch {
	case bytes.HasPrefix(sample, utf8BOM):
		return detectedEncoding{enc: unicode.UTF8, bom: int64(len(utf8BOM))}, nil
	case bytes.HasPrefix(sample, utf16LEBOM):
		return detectedEncoding{enc: unicode.UTF16(unicode.LittleEndian, unicode.IgnoreBOM), bom: int64(len(utf16LEBOM))}, nil
	case bytes.HasPrefix(sample, utf16BEBOM):
		return detectedEncoding{enc: unicode.UTF16(unicode.BigEndian, unicode.IgnoreBOM), bom: int64(len(utf16BEBOM))}, nil
	}
	if utf8.Valid(sample) || validTruncatedUTF8(sample, size) {
		return detectedEncoding{enc: unicode.UTF8}, nil
	}

	label := ""
	if result, err := chardet.NewTextDetector().DetectBest(sample); err == nil && result != nil {
		label = result.Charset
	}
	return detectedEncoding{enc: resolveSingleByte(sample, label)}, nil
}

// resolveSingleByte refines a single-byte detection with Hungarian-specific
// scoring. chardet often labels Hungarian ISO-8859-2 or windows-1250 text as
// windows-1252, which turns ő/ű into õ/û. The detector's own choice is kept
// unless another candidate scores clearly better.
func resolveSingleByte(sample []byte, label string) encoding.Encoding {
	detected := encodingForLabel(label)
	if !evaluateSingleByte(label) {
		return detected
	}
	best, bestScore := detected, scoreDecoded(sample, detected)
	for _, cand := range singleByteCandidates {
		if strings.EqualFold(cand.name, label) {
			continue
		}
		if score := scoreDecoded(sample, cand.enc); score > bestScore+2 {
			best, bestScore = cand.enc, score
		}
	}
	return best
}

// evaluateSingleByte reports whether label denotes a single-byte encoding
// whose ambiguity Hungarian scoring can resolve.
func evaluateSingleByte(label string) bool {
	switch strings.ToLower(strings.TrimSpace(label)) {
	case "", "utf-8", "utf8", "ascii", "us-ascii",
		"windows-1250", "cp1250", "windows-1252", "cp1252",
		"iso-8859-1", "iso8859-1", "latin1",
		"iso-8859-2", "iso8859-2", "latin2":
		return true
	}
	return false
}

// singleByteCandidates are the encodings Hungarian legacy texts use.
var singleByteCandidates = []struct {
	name string
	enc  encoding.Encoding
}{
	{"iso-8859-2", charmap.ISO8859_2},
	{"windows-1250", charmap.Windows1250},
	{"windows-1252", charmap.Windows1252},
}

// hungarianStopwords carry most of the signal when a sample has few accented
// characters.
var hungarianStopwords = map[string]struct{}{
	"a": {}, "az": {}, "és": {}, "hogy": {}, "nem": {}, "van": {},
	"egy": {}, "még": {}, "csak": {}, "mint": {}, "vagy": {}, "már": {},
	"el": {}, "meg": {}, "fel": {}, "le": {}, "be": {}, "ki": {},
}

// scoreDecoded rates how plausibly sample decodes as Hungarian text.
func scoreDecoded(sample []byte, enc encoding.Encoding) int {
	s, _, err := transform.String(enc.NewDecoder(), string(sample))
	if err != nil {
		return -1000
	}
	score := 0
	for _, r := range s {
		switch r {
		case 'ő', 'ű', 'Ő', 'Ű':
			score += 2
		case 'á', 'é', 'í', 'ó', 'ö', 'ú', 'ü', 'Á', 'É', 'Í', 'Ó', 'Ö', 'Ú', 'Ü':
			score++
		case 'õ', 'û', 'Õ', 'Û':
			score -= 2
		case utf8.RuneError:
			score -= 10
		}
		if r < 0x20 && r != '\n' && r != '\r' && r != '\t' && r != '\f' {
			score -= 10
		}
	}
	for _, w := range strings.Fields(strings.ToLower(s)) {
		w = strings.Trim(w, "….,;:!?\"'()[]{}—-–")
		if _, ok := hungarianStopwords[w]; ok {
			score++
		}
	}
	return score
}

// validTruncatedUTF8 reports whether sample is a UTF-8 prefix cut at the
// sample limit, possibly in the middle of a rune.
func validTruncatedUTF8(sample []byte, size int64) bool {
	if int64(len(sample)) >= size {
		return false
	}
	for i := 1; i < utf8.UTFMax && i < len(sample); i++ {
		if utf8.Valid(sample[:len(sample)-i]) {
			return true
		}
	}
	return false
}

// encodingForLabel maps a detector label to an x/text encoding. Unknown
// labels fall back to windows-1252 so decoding always makes progress.
func encodingForLabel(label string) encoding.Encoding {
	switch strings.ToLower(strings.TrimSpace(label)) {
	case "utf-8", "utf8", "ascii", "us-ascii":
		return unicode.UTF8
	case "utf-16le", "utf-16-le":
		return unicode.UTF16(unicode.LittleEndian, unicode.IgnoreBOM)
	case "utf-16be", "utf-16-be":
		return unicode.UTF16(unicode.BigEndian, unicode.IgnoreBOM)
	case "utf-16":
		return unicode.UTF16(unicode.BigEndian, unicode.UseBOM)
	}
	if enc, err := htmlindex.Get(label); err == nil {
		return enc
	}
	return charmap.Windows1252
}

// decodedReader returns the whole file decoded to UTF-8, starting after the
// BOM.
func decodedReader(b book.Blob, size int64, d detectedEncoding) io.Reader {
	section := io.NewSectionReader(b, d.bom, size-d.bom)
	return transform.NewReader(section, d.enc.NewDecoder())
}

// readHead returns at most headLimit bytes of decoded text. The extra runes
// read beyond the limit let truncation back off to a rune boundary.
func readHead(b book.Blob, size int64, d detectedEncoding) (string, error) {
	raw, err := io.ReadAll(io.LimitReader(decodedReader(b, size, d), headLimit+utf8.UTFMax))
	if err != nil {
		return "", err
	}
	return string(truncateUTF8(raw, headLimit)), nil
}

// truncateUTF8 cuts b to at most n bytes without splitting a rune.
func truncateUTF8(b []byte, n int) []byte {
	if len(b) <= n {
		return b
	}
	b = b[:n]
	for len(b) > 0 && !utf8.Valid(b) {
		b = b[:len(b)-1]
	}
	return b
}

// looksBinary reports whether the decoded text head is dominated by control
// characters, which indicates binary content that happened to decode.
func looksBinary(head string) bool {
	const window = 4096
	var total, suspicious int
	for _, r := range head {
		if total == window {
			break
		}
		total++
		switch {
		case r == 0, r == utf8.RuneError, r == 0x7F:
			suspicious++
		case r < 0x20 && r != '\t' && r != '\n' && r != '\r' && r != '\f':
			suspicious++
		}
	}
	return total > 0 && suspicious*100 > total
}
