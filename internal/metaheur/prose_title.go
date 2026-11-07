package metaheur

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// IsProseTitle reports metadata titles that are really prose lines: body
// sentences, truncated paragraphs and blurb fragments. It is intentionally
// conservative and never rejects a line that ends like a complete sentence
// title; the filename fallback remains available for everything it rejects.
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
	if len(words) >= 6 {
		if endsWithClausePunctuation(s) || startsLower(s) || endsWithProseWord(words) {
			return true
		}
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

func startsLower(s string) bool {
	for _, r := range s {
		if unicode.IsLetter(r) {
			return unicode.IsLower(r)
		}
	}
	return false
}

func endsWithProseWord(words []string) bool {
	last := strings.ToLower(trimWord(words[len(words)-1]))
	_, ok := proseEndingWords[last]
	return ok
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

// proseEndingWords are common function words that end a truncated sentence but
// rarely end a title. Matching uses lower case without diacritic folding so
// that e.g. Hungarian "tó" is not confused with English "to".
var proseEndingWords = map[string]struct{}{
	"a": {}, "az": {}, "egy": {},
	"és": {}, "de": {}, "vagy": {}, "hogy": {}, "mert": {}, "mivel": {},
	"amikor": {}, "ahol": {}, "ahová": {}, "aki": {}, "ami": {}, "amely": {},
	"amelyek": {}, "amit": {}, "akit": {}, "akik": {}, "mintha": {}, "mint": {},
	"ha": {}, "bár": {}, "még": {}, "már": {}, "csak": {}, "is": {}, "nem": {},
	"sem": {}, "volt": {}, "van": {}, "lesz": {}, "lehet": {}, "kell": {},
	"the": {}, "an": {}, "and": {}, "or": {}, "but": {}, "that": {}, "which": {},
	"who": {}, "whom": {}, "whose": {}, "when": {}, "where": {}, "while": {},
	"if": {}, "as": {}, "of": {}, "to": {}, "in": {}, "on": {}, "at": {},
	"by": {}, "for": {}, "with": {}, "from": {}, "into": {}, "over": {},
	"under": {}, "about": {}, "after": {}, "before": {}, "between": {},
	"through": {}, "during": {}, "than": {}, "then": {}, "so": {}, "because": {},
	"although": {}, "though": {}, "are": {}, "was": {}, "were": {},
	"be": {}, "been": {}, "being": {}, "have": {}, "has": {}, "had": {},
	"do": {}, "does": {}, "did": {}, "can": {}, "could": {}, "will": {},
	"would": {}, "shall": {}, "should": {}, "may": {}, "might": {}, "must": {},
}
