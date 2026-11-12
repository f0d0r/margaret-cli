// Package textmatch normalizes titles and author names for matching.
package textmatch

import (
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/transform"
	"golang.org/x/text/unicode/norm"
)

var leftoverReplacer = strings.NewReplacer(
	"ø", "o",
	"ß", "ss",
	"æ", "ae", "œ", "oe",
	"ł", "l", "đ", "d", "þ", "th", "ı", "i",
)

var nameParticles = map[string]struct{}{
	"van": {}, "von": {}, "del": {}, "dos": {}, "das": {},
	"der": {}, "den": {}, "ter": {}, "ten": {},
}

// TitleKey returns the exact-match key for a title: folded, lower-cased and
// stripped of punctuation and repeated whitespace.
func TitleKey(s string) string {
	if isASCII(s) {
		return titleKeyASCII(s)
	}
	return strings.Join(strings.Fields(normalizeSeparators(fold(s))), " ")
}

// TitleTokens returns the deduplicated, sorted tokens of a title. Stopwords
// are intentionally kept; document frequency handles common words during
// candidate selection.
func TitleTokens(s string) []string {
	return TokensFromKey(TitleKey(s))
}

// TokensFromKey returns the deduplicated, sorted tokens of an already
// normalized title key. It avoids running the Unicode normalization twice.
func TokensFromKey(key string) []string {
	return uniqueSorted(strings.Fields(key))
}

// AuthorTokens returns the distinctive tokens of a person's name: folded,
// lower-cased, punctuation separated, without particles or tokens of two runes
// or fewer.
func AuthorTokens(s string) []string {
	if isASCII(s) {
		return authorTokensASCII(s)
	}
	fields := strings.Fields(normalizeSeparators(fold(s)))
	out := make([]string, 0, len(fields))
	for _, token := range fields {
		if utf8.RuneCountInString(token) <= 2 {
			continue
		}
		if _, ok := nameParticles[token]; ok {
			continue
		}
		out = append(out, token)
	}
	return uniqueSorted(out)
}

func isASCII(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] >= utf8.RuneSelf {
			return false
		}
	}
	return true
}

func titleKeyASCII(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	space := true
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= 'A' && c <= 'Z' {
			c += 'a' - 'A'
		}
		if (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') {
			b.WriteByte(c)
			space = false
			continue
		}
		if !space {
			b.WriteByte(' ')
			space = true
		}
	}
	return strings.TrimRight(b.String(), " ")
}

func authorTokensASCII(s string) []string {
	fields := strings.Fields(titleKeyASCII(s))
	out := make([]string, 0, len(fields))
	for _, token := range fields {
		if len(token) <= 2 {
			continue
		}
		if _, ok := nameParticles[token]; ok {
			continue
		}
		out = append(out, token)
	}
	return uniqueSorted(out)
}

var contentStopwords = map[string]struct{}{
	"a": {}, "an": {}, "and": {}, "are": {}, "as": {}, "at": {}, "be": {},
	"but": {}, "by": {}, "for": {}, "from": {}, "has": {}, "he": {}, "her": {},
	"his": {}, "if": {}, "in": {}, "into": {}, "is": {}, "it": {}, "its": {},
	"no": {}, "not": {}, "of": {}, "on": {}, "or": {}, "over": {}, "so": {},
	"than": {}, "that": {}, "the": {}, "then": {}, "these": {}, "this": {},
	"those": {}, "to": {}, "was": {}, "were": {}, "will": {}, "with": {},
	"de": {}, "la": {}, "le": {}, "les": {}, "el": {}, "los": {}, "las": {},
	"un": {}, "una": {}, "und": {}, "der": {}, "die": {}, "das": {}, "des": {},
	"dem": {}, "den": {}, "et": {}, "en": {}, "du": {}, "au": {}, "aux": {},
	"sur": {},
}

// ContentTokens returns the distinctive tokens of a text for content
// confirmation: folded, lower-cased, punctuation separated and stripped of
// common stopwords.
func ContentTokens(s string) []string {
	fields := strings.Fields(TitleKey(s))
	out := make([]string, 0, len(fields))
	for _, token := range fields {
		if _, ok := contentStopwords[token]; ok {
			continue
		}
		out = append(out, token)
	}
	return uniqueSorted(out)
}

// Jaccard returns the Jaccard similarity of two sorted, deduplicated token
// sets. It returns 0 when either set is empty.
func Jaccard(a, b []string) float64 {
	if len(a) == 0 || len(b) == 0 {
		return 0
	}
	i, j, intersection := 0, 0, 0
	for i < len(a) && j < len(b) {
		switch {
		case a[i] == b[j]:
			intersection++
			i++
			j++
		case a[i] < b[j]:
			i++
		default:
			j++
		}
	}
	union := len(a) + len(b) - intersection
	if union == 0 {
		return 0
	}
	return float64(intersection) / float64(union)
}

const (
	fnvOffset64 = 14695981039346656037
	fnvPrime64  = 1099511628211
)

// HashTokens maps tokens to sorted, deduplicated 64-bit hashes. Candidate
// matching uses hashes to avoid repeated string comparisons.
func HashTokens(tokens []string) []uint64 {
	if len(tokens) == 0 {
		return nil
	}
	out := make([]uint64, len(tokens))
	for i, token := range tokens {
		sum := uint64(fnvOffset64)
		for j := 0; j < len(token); j++ {
			sum ^= uint64(token[j])
			sum *= fnvPrime64
		}
		out[i] = sum
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	n := 1
	for i := 1; i < len(out); i++ {
		if out[i] != out[n-1] {
			out[n] = out[i]
			n++
		}
	}
	return out[:n]
}

// JaccardHashed is Jaccard over sorted token hashes.
func JaccardHashed(a, b []uint64) float64 {
	if len(a) == 0 || len(b) == 0 {
		return 0
	}
	i, j, intersection := 0, 0, 0
	for i < len(a) && j < len(b) {
		switch {
		case a[i] == b[j]:
			intersection++
			i++
			j++
		case a[i] < b[j]:
			i++
		default:
			j++
		}
	}
	union := len(a) + len(b) - intersection
	if union == 0 {
		return 0
	}
	return float64(intersection) / float64(union)
}

func normalizeSeparators(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsNumber(r) || unicode.IsMark(r) {
			b.WriteRune(r)
			continue
		}
		b.WriteRune(' ')
	}
	return b.String()
}

func fold(s string) string {
	s = strings.ToLower(strings.Join(strings.Fields(s), " "))
	decomposed, _, _ := transform.String(norm.NFD, s)
	var b strings.Builder
	prevLatin := false
	for _, r := range decomposed {
		if unicode.Is(unicode.Mn, r) && prevLatin {
			continue
		}
		b.WriteRune(r)
		prevLatin = unicode.Is(unicode.Latin, r)
	}
	recomposed, _, _ := transform.String(norm.NFC, b.String())
	return leftoverReplacer.Replace(recomposed)
}

func uniqueSorted(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	sort.Strings(values)
	out := values[:1]
	for _, value := range values[1:] {
		if value != out[len(out)-1] {
			out = append(out, value)
		}
	}
	return out
}
