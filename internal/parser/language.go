package parser

import (
	"strings"

	"github.com/f0d0r/margaret-cli/internal/langid"
)

const (
	// minLanguageConfidence is the floor below which a detection is ignored.
	minLanguageConfidence = 0.5
	// overrideLanguageConfidence is the confidence needed to replace a
	// different declared language. Between the two thresholds the declared
	// metadata wins, so weak guesses never overwrite real data.
	overrideLanguageConfidence = 0.75
)

// ApplyDetectedLanguage fills or corrects a book's language list with a
// detected language. A declared language that agrees with the detection is
// kept untouched (including regional tags); an empty list is filled; a
// conflicting declaration is replaced only when the detection is confident.
func ApplyDetectedLanguage(languages []string, detected langid.Result, ok bool) []string {
	if !ok || detected.Confidence < minLanguageConfidence {
		return languages
	}
	if len(languages) == 0 {
		return []string{detected.Code}
	}
	if languageListMatches(languages, detected.Code) {
		return languages
	}
	if detected.Confidence >= overrideLanguageConfidence {
		return []string{detected.Code}
	}
	return languages
}

// languageListMatches reports whether any declared tag has the same base
// language as code ("en-US" matches "en").
func languageListMatches(languages []string, code string) bool {
	for _, tag := range languages {
		if baseLanguage(tag) == baseLanguage(code) {
			return true
		}
	}
	return false
}

func baseLanguage(tag string) string {
	tag = strings.ToLower(strings.TrimSpace(tag))
	if i := strings.IndexAny(tag, "-_"); i >= 0 {
		tag = tag[:i]
	}
	return tag
}
