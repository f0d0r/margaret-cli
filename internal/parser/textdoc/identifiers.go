package textdoc

import (
	"regexp"
	"strings"

	"github.com/f0d0r/margaret-cli/internal/identifier"
	"github.com/f0d0r/margaret-ebook-library/tools"
)

var (
	isbnLineLabel  = regexp.MustCompile(`(?i)^\s*(?:e)?isbn(?:\s*[-\x{2010}-\x{2015}]?\s*(?:10|13))?\s*`)
	isbnBinding    = `(?:pbk|paperback|hardcover|hardback|hbk|ebook|e-book|electronic|pdf|epub|mobi|print|cloth|audio|cd|dvd)`
	isbnBindingAt  = regexp.MustCompile(`(?i)^\s*[\(\[]\s*` + isbnBinding + `\s*\.?\s*[\)\]]\s*:?\s*`)
	isbnBindingEnd = regexp.MustCompile(`(?i)\s*[\(\[]\s*` + isbnBinding + `\s*\.?\s*[\)\]]\s*[.;]?\s*$`)
	isbnLineChars  = regexp.MustCompile(`(?i)[\p{Nd}x\s\-\x{2010}-\x{2015}.,;:()\[\]]+`)
)

// TextIdentifiers returns at most one book-valid ISBN-13 found on a standalone
// line in head. An ISBN embedded in prose, and especially one in a citation of
// another book, is never trusted.
func TextIdentifiers(head string) []identifier.Identifier {
	for _, isbn := range standaloneISBNs(head) {
		isbn13, ok := isbn.ToISBN13()
		if !ok {
			continue
		}
		return []identifier.Identifier{{
			Scheme: "ISBN",
			Value:  isbn13.String(),
			Source: identifier.SourceText,
		}}
	}
	return nil
}

// ContainsISBN reports whether head mentions value in any form, including
// prose. It is used to reject a non-head fallback that is actually present in
// the scanned text.
func ContainsISBN(head, value string) bool {
	for _, isbn := range tools.FindISBNs(head) {
		isbn13, ok := isbn.ToISBN13()
		if ok && isbn13.String() == value {
			return true
		}
	}
	return false
}

func standaloneISBNs(head string) []tools.ISBN {
	var out []tools.ISBN
	seen := make(map[string]struct{})
	for _, line := range strings.Split(newlineReplacer.Replace(head), "\n") {
		isbn, ok := standaloneISBN(line)
		if !ok {
			continue
		}
		isbn13, ok := isbn.ToISBN13()
		if !ok {
			continue
		}
		if _, duplicate := seen[isbn13.String()]; duplicate {
			continue
		}
		seen[isbn13.String()] = struct{}{}
		out = append(out, isbn)
	}
	return out
}

func standaloneISBN(line string) (tools.ISBN, bool) {
	line = strings.TrimSpace(line)
	loc := isbnLineLabel.FindStringIndex(line)
	if loc == nil {
		return tools.ISBN{}, false
	}
	rest := strings.TrimSpace(strings.TrimLeft(strings.TrimSpace(line[loc[1]:]), ":"))
	if rest == "" {
		return tools.ISBN{}, false
	}
	rest = isbnBindingAt.ReplaceAllString(rest, "")
	rest = strings.TrimSpace(rest)
	found := tools.FindISBNs(rest)
	if len(found) != 1 {
		return tools.ISBN{}, false
	}
	rest = isbnBindingEnd.ReplaceAllString(rest, "")
	if leftover := isbnLineChars.ReplaceAllString(rest, ""); strings.TrimSpace(leftover) != "" {
		return tools.ISBN{}, false
	}
	return found[0], true
}
