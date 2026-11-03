package txt

import (
	"regexp"
	"strings"
	"unicode"

	"github.com/f0d0r/margaret-cli/internal/metaheur"
	"github.com/f0d0r/margaret-cli/internal/parser"
)

const (
	// metadataLines bounds how many leading lines are inspected for headers.
	metadataLines = 100
	// markerSearchLines bounds how far the Gutenberg start marker is sought.
	markerSearchLines = 200
)

var (
	labelLine       = regexp.MustCompile(`^([\p{L}][\p{L}\p{N} _-]{0,31}):[ \t]*(.+?)[ \t]*$`)
	upperLabel      = regexp.MustCompile(`^[A-Z]{2,6}[ \t]*:(\s|$)`)
	gutenbergStart  = regexp.MustCompile(`(?i)^\*{3}\s*start of (?:the|this) project gutenberg ebook\b(.*?)\*{0,3}\s*$`)
	authorSplit     = regexp.MustCompile(`(?i),?\s+(?:and|with|és|es)\s+`)
	newlineReplacer = strings.NewReplacer("\r\n", "\n", "\r", "\n")
	nnclHeader      = regexp.MustCompile(`(?i)^nncl[\s\w./-]*$`)
)

var titleKeys = map[string]struct{}{
	"title": {}, "cím": {}, "cim": {}, "könyv címe": {}, "konyv cime": {},
	// MEK (Magyar Elektronikus Könyvtár) header keys used by the Hungarian
	// plain-text corpus.
	"ti": {},
}

var authorKeys = map[string]struct{}{
	"author": {}, "authors": {}, "by": {}, "creator": {},
	"szerző": {}, "szerzo": {}, "szerzők": {}, "szerzok": {},
	"író": {}, "iro": {},
	// MEK header key for the author.
	"au": {},
}

// extractMetadata derives a title and authors from the text head. Labeled
// headers win over the Gutenberg marker, which wins over Calibre's title-page
// pattern. Field by field, the first source that yields a value wins.
func extractMetadata(head string) parser.Metadata {
	head = strings.TrimPrefix(head, "\ufeff")
	lines := strings.Split(newlineReplacer.Replace(head), "\n")
	lines = stripNNCLHeader(lines)

	var md parser.Metadata
	for _, region := range headerRegions(lines) {
		title, authors := parseLabels(region)
		if md.Title == "" {
			md.Title = title
		}
		if len(md.Authors) == 0 {
			md.Authors = authors
		}
	}
	if md.Title == "" {
		md.Title = gutenbergTitle(lines)
	}
	if title, author, ok := calibreTitleAuthor(lines); ok {
		if md.Title == "" {
			md.Title = title
		}
		if len(md.Authors) == 0 {
			md.Authors = splitAuthors(author)
		}
	}

	if !hasYAMLFrontMatter(lines) && startMarkerIndex(lines) < 0 &&
		(md.Title == "" || len(md.Authors) == 0) {
		if title, authors, ok := hungarianTitlePage(lines); ok {
			md = mergeHungarianMetadata(md, title, authors)
		}
	}

	md.Title = cleanValue(md.Title)
	if isPlaceholder(md.Title) || metaheur.IsJunkValue(md.Title) {
		md.Title = ""
	}
	md.Authors = filterMetadataAuthors(md.Authors, md.Title)
	return md
}

// stripNNCLHeader removes the NNCL distribution header from the first few
// lines, where it would otherwise be mistaken for a title or an author.
func stripNNCLHeader(lines []string) []string {
	seen := 0
	for i, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		seen++
		if seen > 5 {
			break
		}
		if nnclHeader.MatchString(line) {
			out := make([]string, 0, len(lines)-1)
			out = append(out, lines[:i]...)
			return append(out, lines[i+1:]...)
		}
	}
	return lines
}

// hasYAMLFrontMatter reports a complete YAML block at the top of the file.
func hasYAMLFrontMatter(lines []string) bool {
	if len(lines) == 0 || strings.TrimSpace(lines[0]) != "---" {
		return false
	}
	for i := 1; i < len(lines) && i <= metadataLines; i++ {
		if strings.TrimSpace(lines[i]) == "---" {
			return true
		}
	}
	return false
}

// mergeHungarianMetadata fills only empty fields. When the other field is
// already known, the Hungarian pair must agree with it, so a title from one
// source is never combined with an author from another book.
func mergeHungarianMetadata(md parser.Metadata, title string, authors []string) parser.Metadata {
	if md.Title == "" && title != "" {
		if len(md.Authors) == 0 || len(authors) == 0 || authorsAgree(md.Authors, authors) {
			md.Title = title
		}
	}
	if len(md.Authors) == 0 && len(authors) > 0 {
		if md.Title == "" || title == "" || titlesAgree(md.Title, title) {
			md.Authors = authors
		}
	}
	return md
}

func titlesAgree(a, b string) bool {
	fa, fb := metaheur.Fold(a), metaheur.Fold(b)
	if fa == "" || fb == "" {
		return false
	}
	if fa == fb {
		return true
	}
	if len(strings.Fields(fa)) < 2 || len(strings.Fields(fb)) < 2 {
		return false
	}
	return phraseContains(fa, fb) || phraseContains(fb, fa)
}

func phraseContains(haystack, needle string) bool {
	return strings.Contains(" "+haystack+" ", " "+needle+" ")
}

func authorsAgree(a, b []string) bool {
	for _, x := range a {
		for _, y := range b {
			if metaheur.Fold(x) == metaheur.Fold(y) {
				return true
			}
		}
	}
	return false
}

func filterMetadataAuthors(authors []string, title string) []string {
	out := make([]string, 0, len(authors))
	seen := make(map[string]struct{}, len(authors))
	for _, name := range authors {
		name = cleanValue(name)
		if name == "" || isPlaceholder(name) || metaheur.IsCredit(name) ||
			metaheur.IsJunkValue(name) || !containsLetter(name) {
			continue
		}
		if title != "" && metaheur.Fold(name) == metaheur.Fold(title) {
			continue
		}
		key := metaheur.Fold(name)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, name)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func containsLetter(s string) bool {
	return strings.IndexFunc(s, unicode.IsLetter) >= 0
}

// headerRegions returns the candidate header blocks in priority order: YAML
// front matter, the Gutenberg preamble before the start marker, and a strict
// leading block of label lines.
func headerRegions(lines []string) [][]string {
	var regions [][]string
	if len(lines) > 0 && strings.TrimSpace(lines[0]) == "---" {
		for i := 1; i < len(lines) && i <= metadataLines; i++ {
			if strings.TrimSpace(lines[i]) == "---" {
				regions = append(regions, lines[1:i])
				break
			}
		}
	}
	if idx := startMarkerIndex(lines); idx > 0 {
		regions = append(regions, lines[:idx])
	}
	return append(regions, leadingBlock(lines))
}

// leadingBlock returns the leading lines that consist only of blank lines and
// "Key: value" lines, so body text is never scanned for metadata.
func leadingBlock(lines []string) []string {
	end := 0
	for end < len(lines) && end < metadataLines {
		line := strings.TrimSpace(lines[end])
		if line == "" || labelLine.MatchString(line) || gutenbergStart.MatchString(line) {
			end++
			continue
		}
		break
	}
	return lines[:end]
}

// parseLabels extracts the first title and the first author list from the
// known label keys in lines. Hungarian keys are accepted alongside the
// English ones.
func parseLabels(lines []string) (title string, authors []string) {
	for _, line := range lines {
		key, value, ok := parseLabelLine(line)
		if !ok {
			continue
		}
		switch {
		case isTitleKey(key):
			if title == "" {
				value = cleanValue(value)
				if !isPlaceholder(value) && !metaheur.IsJunkValue(value) {
					title = value
				}
			}
		case isAuthorKey(key):
			if len(authors) == 0 {
				authors = filterMetadataAuthors(splitAuthors(value), "")
			}
		}
	}
	return title, authors
}

func isTitleKey(key string) bool {
	_, ok := titleKeys[strings.ToLower(strings.TrimSpace(key))]
	return ok
}

func isAuthorKey(key string) bool {
	_, ok := authorKeys[strings.ToLower(strings.TrimSpace(key))]
	return ok
}

// isKnownLabelLine reports a line that is a structured metadata label: a
// title or author key, or an all-caps header prefix such as the MEK "DT:" or
// "SO:" lines. Such lines must never become title-page candidates, otherwise
// the bare prefix ("TI") is read as a title and its value as an author.
func isKnownLabelLine(line string) bool {
	line = strings.TrimSpace(line)
	if upperLabel.MatchString(line) {
		return true
	}
	key, _, ok := parseLabelLine(line)
	return ok && (isTitleKey(key) || isAuthorKey(key))
}

// parseLabelLine splits a short "Key: value" line. Only the known keys are
// used for metadata, so prose that happens to contain a colon is harmless.
func parseLabelLine(line string) (key, value string, ok bool) {
	m := labelLine.FindStringSubmatch(strings.TrimSpace(line))
	if m == nil {
		return "", "", false
	}
	return strings.TrimSpace(m[1]), m[2], true
}

// startMarkerIndex returns the line index of the Project Gutenberg start
// marker, or -1.
func startMarkerIndex(lines []string) int {
	for i := 0; i < len(lines) && i < markerSearchLines; i++ {
		if gutenbergStart.MatchString(strings.TrimSpace(lines[i])) {
			return i
		}
	}
	return -1
}

// gutenbergTitle derives a title from the start marker itself, for texts
// without a Title label.
func gutenbergTitle(lines []string) string {
	idx := startMarkerIndex(lines)
	if idx < 0 {
		return ""
	}
	m := gutenbergStart.FindStringSubmatch(strings.TrimSpace(lines[idx]))
	if len(m) < 2 {
		return ""
	}
	return cleanValue(strings.TrimLeft(strings.TrimSpace(m[1]), " ,;:-"))
}

// calibreTitleAuthor implements Calibre's title-page pattern: a title line,
// two blank lines and an author line within the first four lines and the
// first 1024 bytes. The author line must look like a real name, so an
// arbitrary fourth line (a credit, an NNCL identifier or a body sentence) no
// longer turns into an author.
func calibreTitleAuthor(lines []string) (title, author string, ok bool) {
	if len(lines) < 4 {
		return "", "", false
	}
	block := strings.Join(lines[:4], "\n")
	if len(block) > 1024 {
		return "", "", false
	}
	if strings.TrimSpace(lines[0]) == "" || !blank(lines[1]) || !blank(lines[2]) || strings.TrimSpace(lines[3]) == "" {
		return "", "", false
	}
	title = cleanValue(lines[0])
	if title == "" || isPlaceholder(title) || metaheur.IsJunkValue(title) {
		return "", "", false
	}
	author = cleanValue(lines[3])
	if author == "" || isPlaceholder(author) || metaheur.IsCredit(author) || metaheur.IsJunkValue(author) {
		return "", "", false
	}
	names := splitAuthors(author)
	if len(names) == 0 {
		return "", "", false
	}
	for _, name := range names {
		if !metaheur.IsName(name) {
			return "", "", false
		}
	}
	if metaheur.Fold(author) == metaheur.Fold(title) {
		return "", "", false
	}
	return title, author, true
}

// splitAuthors splits an author list on "&", ";" and the "and"/"with"
// separators Calibre recognizes. Commas inside a name ("Last, First") are
// preserved; a bracketed YAML list is split on commas as well.
func splitAuthors(raw string) []string {
	raw = strings.TrimSpace(raw)
	if strings.HasPrefix(raw, "[") && strings.HasSuffix(raw, "]") {
		raw = strings.ReplaceAll(strings.TrimSuffix(strings.TrimPrefix(raw, "["), "]"), ",", ";")
	}
	raw = authorSplit.ReplaceAllString(raw, ";")
	parts := strings.FieldsFunc(raw, func(r rune) bool { return r == '&' || r == ';' })
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		name := cleanValue(part)
		if name == "" || isPlaceholder(name) {
			continue
		}
		out = append(out, name)
	}
	return out
}

// cleanValue trims a metadata value, removes one pair of surrounding quotes
// and collapses internal whitespace.
func cleanValue(s string) string {
	s = strings.TrimSpace(s)
	for _, quote := range [][2]string{{`"`, `"`}, {`'`, `'`}, {"\u201c", "\u201d"}, {"\u2018", "\u2019"}} {
		if len(s) >= len(quote[0])+len(quote[1]) && strings.HasPrefix(s, quote[0]) && strings.HasSuffix(s, quote[1]) {
			s = strings.TrimSpace(s[len(quote[0]) : len(s)-len(quote[1])])
		}
	}
	return strings.Join(strings.Fields(s), " ")
}

// isPlaceholder reports values that carry no real title or author
// information, such as Calibre's "Unknown" fallback.
func isPlaceholder(s string) bool {
	_, ok := placeholderValues[strings.ToLower(strings.TrimSpace(s))]
	return ok
}

var placeholderValues = map[string]struct{}{
	"unknown":        {},
	"unknown author": {},
	"author unknown": {},
	"anonymous":      {},
	"n/a":            {},
	"none":           {},
	"null":           {},
	"ismeretlen":     {},
	"nevtelen":       {},
	"névtelen":       {},
	"untitled":       {},
	"cim nelkul":     {},
	"cím nélkül":     {},
}

// blank reports whether a line contains only whitespace.
func blank(line string) bool {
	return strings.TrimSpace(line) == ""
}
