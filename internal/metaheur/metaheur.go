// Package metaheur scores metadata-like strings: personal names and title
// lines. The plain-text parser and the report fallbacks share it so that both
// apply the same plausibility rules.
package metaheur

import (
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/transform"
	"golang.org/x/text/unicode/norm"
)

// NameThreshold is the minimum ScoreName value accepted as a personal name.
const NameThreshold = 3

var (
	nnclValue      = regexp.MustCompile(`(?i)^nncl[\s\w./-]*$`)
	isbnLabel      = regexp.MustCompile(`(?i)^(isbn|issn)[\s:.-]`)
	urlValue       = regexp.MustCompile(`(?i)(https?://|www\.)`)
	emailValue     = regexp.MustCompile(`[\w.+-]+@[\w.-]+\.[A-Za-z]{2,}`)
	numeralHeading = regexp.MustCompile(`^(\d{1,2}|[IVXLCDM]+)\.?$`)
)

// Normalize trims s and collapses internal whitespace runs to single spaces.
func Normalize(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// Fold returns the lower-cased, diacritic-folded form used for keyword
// matching.
func Fold(s string) string {
	s = strings.ToLower(Normalize(s))
	decomposed, _, _ := transform.String(norm.NFD, s)
	var b strings.Builder
	for _, r := range decomposed {
		if unicode.Is(unicode.Mn, r) {
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

// IsJunkValue reports strings that carry no title or name information at all:
// separators, NNCL identifiers, ISBN labels, URLs and e-mail addresses.
func IsJunkValue(s string) bool {
	s = Normalize(s)
	if s == "" {
		return true
	}
	if nnclValue.MatchString(s) || isbnLabel.MatchString(s) {
		return true
	}
	if urlValue.MatchString(s) || emailValue.MatchString(s) || strings.Contains(s, "://") {
		return true
	}
	return !hasLetter(s) && !hasDigit(s)
}

// IsCredit reports publisher, translator, cover and digitization credit lines.
func IsCredit(s string) bool {
	s = Normalize(s)
	if s == "" {
		return false
	}
	f := Fold(s)
	for _, stem := range creditStems {
		if strings.Contains(f, stem) {
			return true
		}
	}
	words := wordSet(f)
	for _, phrase := range creditPhrases {
		if strings.Contains(f, phrase) {
			return true
		}
	}
	for w := range creditWords {
		if _, ok := words[w]; ok {
			return true
		}
	}
	// "A kiadó" can be a real title; only treat publisher markers as credit
	// when the line does not look like a short article-led title.
	fields := strings.Fields(f)
	if len(fields) > 0 && isArticle(fields[0]) && len(fields) <= 3 {
		return false
	}
	for _, stem := range publisherStems {
		if strings.Contains(f, stem) {
			return true
		}
	}
	for _, phrase := range publisherPhrases {
		if strings.Contains(f, phrase) {
			return true
		}
	}
	for w := range publisherWords {
		if _, ok := words[w]; ok {
			return true
		}
	}
	return false
}

// ScoreName rates how much s looks like a personal name. Values below
// NameThreshold are not names. Latin-script names require sensible
// capitalization; non-Latin names are accepted on structure alone so that
// international Calibre libraries keep working.
func ScoreName(s string) int {
	s = Normalize(s)
	if s == "" || runeLen(s) > 60 {
		return -10
	}
	if IsJunkValue(s) || IsCredit(s) {
		return -10
	}
	if strings.ContainsAny(s, ":;!?/\\@&") {
		return -10
	}
	folded := Fold(s)
	for _, stem := range organizationStems {
		if strings.Contains(folded, stem) {
			return -10
		}
	}
	words := strings.Fields(s)
	if len(words) == 0 || len(words) > 6 || !hasLetter(s) || hasDigit(s) {
		return -10
	}

	if !hasLatinLetter(s) {
		if len(words) >= 2 {
			return NameThreshold
		}
		return 1
	}

	score := 0
	switch len(words) {
	case 1:
		score = 0
	case 2:
		score = 3
	case 3:
		score = 2
	case 4:
		score = 1
	}

	if strings.Count(s, ",") > 1 {
		return -10
	}
	if i := strings.Index(s, ","); i >= 0 {
		left, right := strings.TrimSpace(s[:i]), strings.TrimSpace(s[i+1:])
		if left == "" || right == "" {
			return -10
		}
		lw, rw := len(strings.Fields(left)), len(strings.Fields(right))
		if lw < 1 || lw > 3 || rw < 1 || rw > 4 {
			return -10
		}
		if !nameCaseOK(left) || !nameCaseOK(right) {
			return -10
		}
		rwWords := strings.Fields(right)
		if len(rwWords) == 1 && isArticle(rwWords[0]) {
			return -10
		}
		score += 2
	}

	if isArticle(words[0]) {
		return -10
	}
	if len(words) == 1 {
		return score
	}

	for _, w := range words {
		w = trimWord(w)
		if w == "" {
			continue
		}
		if isParticle(w) {
			continue
		}
		if isInitial(w) {
			score++
			continue
		}
		if !startsUpper(w) {
			score -= 3
			continue
		}
		score++
	}
	return score
}

// IsName reports whether s reaches the personal-name threshold.
func IsName(s string) bool {
	return ScoreName(s) >= NameThreshold
}

// ScoreTitle rates how much s looks like a title-page title line. It rejects
// prose, chapter headings, credits and pure personal names; a title that only
// repeats a name is too ambiguous to use.
func ScoreTitle(s string) int {
	s = Normalize(s)
	if s == "" || runeLen(s) > 200 {
		return -10
	}
	if IsJunkValue(s) || IsCredit(s) || isChapterHeading(s) {
		return -10
	}
	words := strings.Fields(s)
	if len(words) == 0 || len(words) > 12 {
		return -10
	}
	if len(words) == 1 && isArticle(words[0]) {
		return -10
	}
	if strings.HasSuffix(s, ".") && len(words) >= 5 {
		return -10
	}
	if LooksLikeProse(s) {
		return -10
	}

	score := 0
	switch {
	case len(words) == 1:
		score = 1
	case len(words) <= 10:
		score = 2
	default:
		score = 1
	}
	if isArticle(words[0]) {
		score += 2
	}
	if strings.ContainsAny(s, "?!") {
		score++
	}
	if strings.Contains(s, ":") {
		score++
	}
	if hasLowerWord(words) {
		score++
	}
	if hasOpeningQuote(s) {
		score -= 2
	}
	if ScoreName(s) >= NameThreshold {
		score -= 3
	}
	return score
}

// IsTitle reports whether s is usable as a title next to an author line.
func IsTitle(s string) bool {
	return ScoreTitle(s) >= 1
}

// LooksLikeProse reports a long, sentence-final line that is likely body text
// rather than a title-page line.
func LooksLikeProse(s string) bool {
	s = Normalize(s)
	words := strings.Fields(s)
	if len(words) < 8 || runeLen(s) < 60 {
		return false
	}
	return strings.HasSuffix(s, ".") || strings.HasSuffix(s, "!") || strings.HasSuffix(s, "?")
}

// IsChapterHeading reports chapter, TOC, dedication and similar headings.
func IsChapterHeading(s string) bool {
	return isChapterHeading(s)
}

// isChapterHeading reports chapter, TOC, dedication and similar headings.
func isChapterHeading(s string) bool {
	f := Fold(s)
	if chapterHeading.MatchString(f) || numeralHeading.MatchString(strings.TrimSpace(s)) {
		return true
	}
	words := strings.Fields(f)
	for i := 0; i < len(words) && i < 2; i++ {
		w := trimWord(words[i])
		if _, ok := chapterWords[w]; ok {
			return true
		}
	}
	return false
}

func isArticle(w string) bool {
	_, ok := articles[Fold(trimWord(w))]
	return ok
}

func isParticle(w string) bool {
	_, ok := particles[Fold(trimWord(w))]
	return ok
}

func isInitial(w string) bool {
	w = strings.Trim(trimWord(w), ".")
	if w == "" {
		return false
	}
	n := 0
	for _, r := range w {
		if !unicode.IsLetter(r) {
			return false
		}
		n++
		if n > 3 {
			return false
		}
	}
	return n >= 1 && unicode.IsUpper(firstRune(w))
}

func nameCaseOK(s string) bool {
	for _, w := range strings.Fields(s) {
		w = trimWord(w)
		if w == "" || isParticle(w) || isInitial(w) {
			continue
		}
		if !startsUpper(w) {
			return false
		}
	}
	return true
}

func hasLowerWord(words []string) bool {
	for _, w := range words {
		w = trimWord(w)
		if w == "" {
			continue
		}
		r, _ := utf8.DecodeRuneInString(w)
		if unicode.IsLower(r) && !isParticle(w) {
			return true
		}
	}
	return false
}

func startsUpper(s string) bool {
	r, _ := utf8.DecodeRuneInString(strings.TrimLeft(s, `"'([`))
	return unicode.IsUpper(r)
}

func firstRune(s string) rune {
	r, _ := utf8.DecodeRuneInString(s)
	return r
}

func trimWord(w string) string {
	return strings.Trim(w, `"'()[]{}.,;:!?`)
}

func hasLetter(s string) bool {
	for _, r := range s {
		if unicode.IsLetter(r) {
			return true
		}
	}
	return false
}

func hasDigit(s string) bool {
	for _, r := range s {
		if unicode.IsDigit(r) {
			return true
		}
	}
	return false
}

func hasLatinLetter(s string) bool {
	for _, r := range s {
		if unicode.Is(unicode.Latin, r) {
			return true
		}
	}
	return false
}

func hasOpeningQuote(s string) bool {
	s = strings.TrimSpace(s)
	return strings.HasPrefix(s, `"`) || strings.HasPrefix(s, "„") || strings.HasPrefix(s, "“")
}

func wordSet(s string) map[string]struct{} {
	out := make(map[string]struct{})
	for _, w := range strings.FieldsFunc(s, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	}) {
		out[w] = struct{}{}
	}
	return out
}

func runeLen(s string) int {
	return utf8.RuneCountInString(s)
}
