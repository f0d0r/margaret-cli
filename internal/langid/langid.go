// Package langid detects the natural language of a text sample. It wraps
// lingua's low-accuracy mode: the models stay around 100 MB in memory and
// remain accurate for the book-length samples the scanner works with.
//
// The detector is language-agnostic on purpose. It is the single place where
// the codebase decides what language a text is written in; callers must not
// add their own language-specific heuristics on top.
package langid

import (
	"html"
	"regexp"
	"strings"
	"sync"
	"unicode"
	"unicode/utf8"

	"github.com/pemistahl/lingua-go"
)

const (
	// MinLetters is the minimum number of letters a sample needs before a
	// detection is attempted.
	MinLetters = 40
	// MaxRunes bounds the sample size. Longer input does not improve
	// accuracy and only costs time.
	MaxRunes = 2000
)

// Result is a detected language: an ISO 639-1 code and the detector's
// confidence between 0 and 1.
type Result struct {
	Code       string
	Confidence float64
}

var (
	detectorOnce sync.Once
	detector     lingua.LanguageDetector

	scriptStyleBlock = regexp.MustCompile(`(?is)<(script|style)\b[^>]*>.*?</(script|style)\s*>`)
	htmlTag          = regexp.MustCompile(`(?s)<[^>]*>`)
	cssBlock         = regexp.MustCompile(`(?s)\{[^}]*\}`)
)

func languageDetector() lingua.LanguageDetector {
	detectorOnce.Do(func() {
		detector = lingua.NewLanguageDetectorBuilder().
			FromAllLanguages().
			WithLowAccuracyMode().
			Build()
	})
	return detector
}

// Detect returns the most likely language of text. ok is false when the
// sample is too short or the detector cannot name a language.
func Detect(text string) (Result, bool) {
	text = sample(text)
	if countLetters(text) < MinLetters {
		return Result{}, false
	}
	values := languageDetector().ComputeLanguageConfidenceValues(text)
	if len(values) == 0 {
		return Result{}, false
	}
	top := values[0]
	code := strings.ToLower(top.Language().IsoCode639_1().String())
	if code == "" || code == "und" || top.Value() <= 0 {
		return Result{}, false
	}
	return Result{Code: code, Confidence: top.Value()}, true
}

// DetectProse detects the language of the prose lines in text, dropping
// code-like lines such as embedded CSS, scripts or ASCII tables. The filter is
// structural and language-independent; it only decides what does not look like
// natural text.
func DetectProse(text string) (Result, bool) {
	return Detect(proseSample(text))
}

// proseSample keeps the lines that look like natural text, up to MaxRunes.
func proseSample(text string) string {
	text = scriptStyleBlock.ReplaceAllString(text, "\n")
	text = htmlTag.ReplaceAllString(text, "\n")
	text = cssBlock.ReplaceAllString(text, "\n")
	text = html.UnescapeString(text)

	var b strings.Builder
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || looksLikeCode(line) || looksLikeMarkupNoise(line) {
			continue
		}
		b.WriteString(line)
		b.WriteByte('\n')
	}
	return b.String()
}

// looksLikeMarkupNoise reports markup or style leftovers that survived tag and
// block removal. Natural prose rarely contains these markers at line level.
func looksLikeMarkupNoise(line string) bool {
	// Minified HTML often puts a whole chapter on one line, so a long line is
	// prose even if it contains a stray marker.
	if utf8.RuneCountInString(line) >= 80 {
		return false
	}
	if strings.ContainsAny(line, "@;{}<>") {
		return true
	}
	if strings.Contains(line, ":") && len(strings.Fields(line)) < 4 {
		return true
	}
	return false
}

// looksLikeCode reports a line with a high share of programming or markup
// punctuation, which natural prose almost never has.
func looksLikeCode(line string) bool {
	if len(line) < 8 {
		return false
	}
	special := 0
	for _, r := range line {
		switch r {
		case '{', '}', '[', ']', '(', ')', ';', '=', '<', '>', '@', '#', '$',
			'%', '^', '&', '*', '_', '|', '~', '\\', '`':
			special++
		}
	}
	return special*10 > utf8.RuneCountInString(line)
}

// sample keeps at most MaxRunes runes from the beginning of text.
func sample(text string) string {
	if utf8.RuneCountInString(text) <= MaxRunes {
		return text
	}
	var b strings.Builder
	b.Grow(MaxRunes * 2)
	n := 0
	for _, r := range text {
		b.WriteRune(r)
		n++
		if n >= MaxRunes {
			break
		}
	}
	return b.String()
}

func countLetters(s string) int {
	n := 0
	for _, r := range s {
		if unicode.IsLetter(r) {
			n++
		}
	}
	return n
}
