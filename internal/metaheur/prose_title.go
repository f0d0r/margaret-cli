package metaheur

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// IsProseTitle reports metadata titles that are really prose lines: body
// sentences, truncated paragraphs and blurb fragments. Only structural signals
// are used; language-specific word lists and capitalization assumptions are
// deliberately avoided because neither generalizes across languages. It is
// intentionally conservative and never rejects a line that ends like a
// complete sentence title; the filename fallback remains available for
// everything it rejects.
func IsProseTitle(s string) bool {
	s = Normalize(s)
	if s == "" {
		return false
	}
	if LooksLikeProse(s) {
		return true
	}
	if endsLikeSentence(s) {
		return false
	}

	words := strings.Fields(s)
	if len(words) >= 6 && endsWithClausePunctuation(s) {
		return true
	}
	// A long line with a lowercase continuation after a comma or semicolon is
	// a truncated clause rather than a title; colons usually introduce a real
	// subtitle and are left alone.
	return len(words) >= 10 && !strings.Contains(s, ":") &&
		strings.ContainsAny(s, ",;") && lowerClauseContinuation(s)
}

func endsLikeSentence(s string) bool {
	s = strings.TrimRight(strings.TrimSpace(s), `"'”’)]}`)
	if strings.HasSuffix(s, "...") || strings.HasSuffix(s, "…") {
		return true
	}
	r, _ := utf8.DecodeLastRuneInString(s)
	return r == '.' || r == '!' || r == '?'
}

func endsWithClausePunctuation(s string) bool {
	s = strings.TrimRight(strings.TrimSpace(s), `"'”’)]}`)
	r, _ := utf8.DecodeLastRuneInString(s)
	return r == ',' || r == ';' || r == ':'
}

// lowerClauseContinuation reports two consecutive lowercase words directly
// after a comma or semicolon. That shape is common in truncated prose and rare
// in titles.
func lowerClauseContinuation(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] != ',' && s[i] != ';' {
			continue
		}
		seen, lower := 0, 0
		for _, word := range strings.Fields(s[i+1:]) {
			word = trimWord(word)
			if word == "" {
				continue
			}
			r, _ := utf8.DecodeRuneInString(word)
			if !unicode.IsLetter(r) {
				continue
			}
			seen++
			if unicode.IsLower(r) {
				lower++
			}
			if seen == 2 {
				break
			}
		}
		if seen == 2 && lower == 2 {
			return true
		}
	}
	return false
}
