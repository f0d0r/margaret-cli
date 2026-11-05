package txt

import (
	"bytes"
	"io"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/f0d0r/margaret-cli/internal/langid"
	"github.com/f0d0r/margaret-ebook-library/book"
	"github.com/gogs/chardet"
	"golang.org/x/text/encoding"
	"golang.org/x/text/encoding/charmap"
	"golang.org/x/text/encoding/htmlindex"
	xunicode "golang.org/x/text/encoding/unicode"
	"golang.org/x/text/transform"
)

const (
	// sampleLimit is how many raw bytes encoding detection inspects.
	sampleLimit = 64 << 10
	// headLimit is how many decoded bytes are kept for metadata and ISBN
	// extraction.
	headLimit = 64 << 10
	// candidateLimit bounds how many detector candidates are decoded and
	// scored for a non-UTF-8 file.
	candidateLimit = 6
	// minTextQuality rejects decodings dominated by control characters,
	// replacement runes or unassigned code points.
	minTextQuality = -20
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
// UTF-8, then the statistical detector's candidates, each validated and
// identified by language. No language-specific scoring is applied here: any
// script is handled the same way.
func detectEncoding(b book.Blob, size int64) (detectedEncoding, error) {
	sample := make([]byte, min(size, sampleLimit))
	if _, err := io.ReadFull(io.NewSectionReader(b, 0, int64(len(sample))), sample); err != nil {
		return detectedEncoding{}, err
	}

	switch {
	case bytes.HasPrefix(sample, utf8BOM):
		return detectedEncoding{enc: xunicode.UTF8, bom: int64(len(utf8BOM))}, nil
	case bytes.HasPrefix(sample, utf16LEBOM):
		return detectedEncoding{enc: xunicode.UTF16(xunicode.LittleEndian, xunicode.IgnoreBOM), bom: int64(len(utf16LEBOM))}, nil
	case bytes.HasPrefix(sample, utf16BEBOM):
		return detectedEncoding{enc: xunicode.UTF16(xunicode.BigEndian, xunicode.IgnoreBOM), bom: int64(len(utf16BEBOM))}, nil
	}
	if utf8.Valid(sample) || validTruncatedUTF8(sample, size) {
		return detectedEncoding{enc: xunicode.UTF8}, nil
	}
	return detectedEncoding{enc: detectLegacy(sample)}, nil
}

// legacyCandidate is one detector candidate decoded and scored generically.
type legacyCandidate struct {
	enc        encoding.Encoding
	confidence int
	quality    int
	lang       langid.Result
	hasLang    bool
}

// detectLegacy decodes the detector's best candidates and chooses the one
// whose text is both well-formed and most plausibly a natural language. The
// detector's confidence only breaks ties, so a mislabelled legacy encoding can
// be corrected without hardcoding any language's character set.
//
// Candidates are kept in the detector's order. A candidate can be dropped
// before the language pass once the current best already scores a full
// confidence: no later candidate can beat it unless its cheaper text quality
// score is strictly higher, since a later candidate never has a higher
// detector confidence.
func detectLegacy(sample []byte) encoding.Encoding {
	results, err := chardet.NewTextDetector().DetectAll(sample)
	if err != nil {
		return charmap.Windows1252
	}
	seen := make(map[string]struct{}, len(results))
	var candidates []legacyCandidate
	var leader legacyCandidate
	hasLeader, accepted := false, 0
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
		accepted++
		if !dominated(candidate, leader, hasLeader) {
			candidate.scoreLanguage(text)
			candidates = append(candidates, candidate)
			if !hasLeader || betterCandidate(candidate, leader) {
				leader, hasLeader = candidate, true
			}
		}
		if accepted >= candidateLimit {
			break
		}
	}
	if len(candidates) == 0 {
		return charmap.Windows1252
	}
	return pickCandidate(candidates).enc
}

// dominated reports whether candidate cannot beat leader whatever its language
// turns out to be. A leader with full confidence is safe against a candidate of
// equal or lower text quality: the confidence tie band sends such a pair to the
// quality and detector-confidence ties, and both favour the earlier candidate.
func dominated(candidate, leader legacyCandidate, hasLeader bool) bool {
	return hasLeader && leader.hasLang && leader.lang.Confidence == 1 &&
		candidate.quality <= leader.quality
}

// decodeCandidate decodes sample with enc and rejects decodings that are not
// well-formed text. A sample cut in the middle of a multi-byte sequence is
// retried without its tail.
func decodeCandidate(sample []byte, enc encoding.Encoding, confidence int) (legacyCandidate, string, bool) {
	text, ok := decodeSample(sample, enc)
	if !ok {
		return legacyCandidate{}, "", false
	}
	quality := textQuality(text)
	if quality < minTextQuality {
		return legacyCandidate{}, "", false
	}
	return legacyCandidate{enc: enc, confidence: confidence, quality: quality}, text, true
}

// scoreLanguage fills in the language detection of a decoded candidate.
func (c *legacyCandidate) scoreLanguage(text string) {
	if result, ok := langid.Detect(text); ok {
		c.lang, c.hasLang = result, true
	}
}

// decodeSample decodes sample, tolerating a truncated trailing sequence.
func decodeSample(sample []byte, enc encoding.Encoding) (string, bool) {
	decoder := enc.NewDecoder()
	for trim := 0; trim <= utf8.UTFMax && trim <= len(sample); trim++ {
		raw := sample[:len(sample)-trim]
		out, _, err := transform.String(decoder, string(raw))
		if err == nil {
			return out, true
		}
	}
	return "", false
}

// pickCandidate prefers the decoding with the most confident language, then
// the cleanest text, then the detector's own ranking.
func pickCandidate(candidates []legacyCandidate) legacyCandidate {
	best := candidates[0]
	for _, candidate := range candidates[1:] {
		if betterCandidate(candidate, best) {
			best = candidate
		}
	}
	return best
}

func betterCandidate(a, b legacyCandidate) bool {
	if a.hasLang != b.hasLang {
		return a.hasLang
	}
	if a.hasLang {
		// Confidence values are probabilities; smaller differences are
		// noise and should not override the detector's ranking.
		if diff := a.lang.Confidence - b.lang.Confidence; diff > 0.05 {
			return true
		} else if diff < -0.05 {
			return false
		}
	}
	if a.quality != b.quality {
		return a.quality > b.quality
	}
	return a.confidence > b.confidence
}

// textQuality rates a decoding by Unicode category alone: all scripts are
// treated equally. Control characters, replacement runes and unassigned code
// points are penalized; letters, digits, punctuation and whitespace are not.
func textQuality(s string) int {
	total, letters, good, bad := 0, 0, 0, 0
	for _, r := range s {
		total++
		switch {
		case r == utf8.RuneError:
			bad += 5
		case unicode.IsLetter(r), unicode.IsDigit(r), unicode.IsSpace(r), unicode.IsPunct(r):
			good++
		case unicode.IsControl(r):
			bad += 5
		case unicode.Is(unicode.Co, r), unicode.Is(unicode.Cs, r), unicode.Is(unicode.Cn, r):
			bad += 5
		default:
			bad++
		}
		if unicode.IsLetter(r) {
			letters++
		}
	}
	if total == 0 || letters == 0 {
		return -1000
	}
	return good*100/total - bad*200/total
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

// encodingForLabel maps a detector label to an x/text encoding. Labels the
// WHATWG index does not know are rejected rather than guessed.
func encodingForLabel(label string) (encoding.Encoding, bool) {
	if enc, err := htmlindex.Get(label); err == nil {
		return enc, true
	}
	switch strings.ToLower(strings.TrimSpace(label)) {
	case "iso-8859-8-i":
		return charmap.ISO8859_8, true
	}
	return nil, false
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
