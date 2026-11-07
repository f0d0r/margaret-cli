package metaheur

import (
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

// IsArtifactTitle reports metadata titles that are certainly not book titles:
// file paths, source file stems, converter labels and similar machine
// artifacts. It is deliberately conservative; plausible titles are left to the
// normal scoring heuristics.
func IsArtifactTitle(s string) bool {
	s = Normalize(s)
	if s == "" {
		return false
	}
	if hasBrokenRune(s) {
		return true
	}
	if looksLikePath(s) {
		return true
	}
	if strings.HasPrefix(s, ",") || strings.HasPrefix(s, ";") || strings.HasPrefix(s, "|") {
		return true
	}
	if hasSourceExtension(s) {
		return true
	}
	if looksLikeFileStem(s) {
		return true
	}

	folded := Fold(s)
	if _, ok := artifactLabels[folded]; ok {
		return true
	}
	if artifactLabelNumber.MatchString(folded) || artifactNumberLabel.MatchString(folded) {
		return true
	}
	if artifactConverterPrefix.MatchString(s) {
		return true
	}

	// A single letter without digits says nothing; a title such as "1984" or
	// "M*A*S*H" still carries enough content.
	return letterCount(s) < 2 && !hasDigit(s)
}

var (
	artifactDrivePath       = regexp.MustCompile(`^[A-Za-z]:[\\/]`)
	artifactExtension       = regexp.MustCompile(`(?i)\.(pdf|epub|mobi|azw3?|prc|fb2|djvu|lit|lrf|pdb|chm|txt|text|rtf|html?|xhtml|xml|docx?|odt|indd|indb|qxd|vp|book|fm|frm|tex|dvi|ps|eps|prn|utf-?8|ascii|key|zip|rar|7z|tar|gz|bz2|jpg|jpeg|png|gif|tiff?|bmp)(?:\.\d+)?$`)
	artifactLabelNumber     = regexp.MustCompile(`^(chapter|ch|part|volume|vol|book|kotet|fejezet|resz)\s*[0-9ivxlcdm]+\.?$`)
	artifactNumberLabel     = regexp.MustCompile(`^[0-9ivxlcdm]+\.?\s*(chapter|part|volume|vol|book|kotet|fejezet|resz)$`)
	artifactConverterPrefix = regexp.MustCompile(`(?i)^\(?microsoft\s+(word|powerpoint|excel|publisher)\b`)
)

var artifactLabels = map[string]struct{}{
	"title":             {},
	"copyright":         {},
	"copyright page":    {},
	"toc":               {},
	"table of contents": {},
	"contents":          {},
	"index":             {},
	"glossary":          {},
	"cover":             {},
	"front matter":      {},
	"back matter":       {},
	"preface":           {},
	"foreword":          {},
	"dedication":        {},
	"impresszum":        {},
	"tartalom":          {},
	"tartalomjegyzek":   {},
	"borito":            {},
	"fedel":             {},
}

// hasBrokenRune reports control characters and replacement runes left behind
// by failed encodings.
func hasBrokenRune(s string) bool {
	for _, r := range s {
		if r == '\uFFFD' {
			return true
		}
		if unicode.IsControl(r) && r != '\t' && r != '\n' && r != '\r' {
			return true
		}
	}
	return false
}

// looksLikePath reports paths and path-like separators, while leaving
// fractions ("3/4") and common slash abbreviations ("TCP/IP Illustrated")
// alone.
func looksLikePath(s string) bool {
	if strings.HasPrefix(s, "/") || strings.Contains(s, `\`) ||
		artifactDrivePath.MatchString(s) || strings.Contains(s, "://") ||
		strings.Contains(s, " / ") {
		return true
	}
	slashes := strings.Count(s, "/")
	if slashes == 0 {
		return false
	}
	if slashes >= 2 {
		return true
	}
	i := strings.IndexByte(s, '/')
	left, right := s[:i], s[i+1:]
	if left == "" || right == "" {
		return true
	}
	lr, _ := utf8.DecodeLastRuneInString(left)
	rr, _ := utf8.DecodeRuneInString(right)
	if unicode.IsDigit(lr) && unicode.IsDigit(rr) {
		return false
	}
	// A single slash between letters is a common title abbreviation
	// ("TCP/IP Illustrated"); without spaces it is a path stem.
	if unicode.IsLetter(lr) && unicode.IsLetter(rr) && strings.Contains(s, " ") {
		return false
	}
	return true
}

// hasSourceExtension reports a trailing source-file extension, optionally
// followed by a numeric suffix (as in "aspcTOC.fm.21592").
func hasSourceExtension(s string) bool {
	s = strings.TrimRight(s, " \t.,;:!?)]}\"'")
	return artifactExtension.MatchString(s)
}

// looksLikeFileStem reports space-less file names: underscore-separated names
// and dotted names with at least three informative segments.
func looksLikeFileStem(s string) bool {
	if strings.ContainsAny(s, " \t") {
		return false
	}
	if strings.Contains(s, "_") && runeLen(s) >= 8 {
		return true
	}
	if runeLen(s) < 12 {
		return false
	}
	segments := strings.FieldsFunc(s, func(r rune) bool { return r == '.' })
	informative := 0
	for _, segment := range segments {
		if runeLen(segment) >= 2 && hasLetter(segment) {
			informative++
		}
	}
	return informative >= 3
}

func letterCount(s string) int {
	n := 0
	for _, r := range s {
		if unicode.IsLetter(r) {
			n++
		}
	}
	return n
}
