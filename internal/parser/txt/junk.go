package txt

import (
	"strings"
	"unicode"

	"github.com/f0d0r/margaret-cli/internal/metaheur"
)

// isJunkContent reports whole-file junk that reached the parser despite a
// harmless name: HTML sources, HTTP caches, blurbs, credit pages, tables of
// contents and files with almost no text. File size alone is handled by
// TxtParser.MinSize before this check.
func isJunkContent(head string, size int64) bool {
	h := strings.TrimSpace(strings.TrimPrefix(head, "\ufeff"))
	if h == "" {
		return false
	}
	lower := strings.ToLower(h)
	if looksLikeHTML(lower) || looksLikeHTTPCache(h) {
		return true
	}
	if size >= 0 && size < 4<<10 {
		if tooFewLetters(h) || looksLikeCreditsOnly(h) || looksLikeBlurbOnly(h) {
			return true
		}
	}
	return size >= 0 && size < 20<<10 && looksLikeTOCOnly(h)
}

func looksLikeHTML(lower string) bool {
	for _, prefix := range []string{"<!doctype", "<html", "<head", "<body"} {
		if strings.HasPrefix(lower, prefix) {
			return true
		}
	}
	sample := lower
	if len(sample) > 4096 {
		sample = sample[:4096]
	}
	tags := strings.Count(sample, "<") + strings.Count(sample, ">")
	return tags > 40 && tags*100 > len(sample)
}

func looksLikeHTTPCache(head string) bool {
	sample := head
	if len(sample) > 8192 {
		sample = sample[:8192]
	}
	lower := strings.ToLower(sample)
	if strings.Contains(lower, "hts-cache") {
		return true
	}
	headers := 0
	for _, marker := range []string{
		"http/1.", "content-type:", "content-length:", "last-modified:", "accept-ranges:",
	} {
		if strings.Contains(lower, marker) {
			headers++
		}
	}
	if headers >= 2 {
		return true
	}
	urls := strings.Count(lower, "http://") + strings.Count(lower, "https://")
	words := len(strings.Fields(sample))
	return urls >= 5 && words > 0 && urls*20 > words
}

func looksLikeCreditsOnly(head string) bool {
	lines := nonEmptyLines(head)
	if len(lines) == 0 {
		return false
	}
	credit, letters := 0, 0
	for _, line := range lines {
		letters += countLetters(line)
		if metaheur.IsCredit(line) || metaheur.IsJunkValue(line) || isPageNumber(line) {
			credit++
		}
	}
	return credit*100 >= len(lines)*70 && letters < 1000
}

func looksLikeBlurbOnly(head string) bool {
	lines := nonEmptyLines(head)
	if len(lines) == 0 {
		return false
	}
	return strings.HasPrefix(metaheur.Fold(lines[0]), "fulszoveg") && len(lines) <= 12
}

func looksLikeTOCOnly(head string) bool {
	lines := nonEmptyLines(head)
	if len(lines) < 5 {
		return false
	}
	toc := 0
	for _, line := range lines {
		folded := metaheur.Fold(line)
		switch {
		case strings.HasPrefix(folded, "tartalom"):
			toc++
		case strings.HasSuffix(line, ".") || strings.Contains(line, "..."):
			toc++
		case isPageNumber(lastField(line)):
			toc++
		}
	}
	return toc*100 >= len(lines)*60
}

func nonEmptyLines(s string) []string {
	var out []string
	for _, line := range strings.Split(newlineReplacer.Replace(s), "\n") {
		if line = strings.TrimSpace(line); line != "" {
			out = append(out, line)
		}
	}
	return out
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

func tooFewLetters(s string) bool {
	return countLetters(s) < 32
}

func isPageNumber(s string) bool {
	s = strings.TrimSpace(s)
	if s == "" {
		return false
	}
	n := 0
	for _, r := range s {
		if !unicode.IsDigit(r) {
			return false
		}
		n++
	}
	return n >= 1 && n <= 4
}

func lastField(s string) string {
	fields := strings.Fields(s)
	if len(fields) == 0 {
		return ""
	}
	return fields[len(fields)-1]
}
