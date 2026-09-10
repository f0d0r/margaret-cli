package report

import (
	"path/filepath"
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/transform"
	"golang.org/x/text/unicode/norm"
)

// authorSeparator joins author names in GROUP_CONCAT aggregates. It matches
// the CHAR(31) separator used in the ListBooksWithFiles query.
const authorSeparator = "\x1f"

// placeholderAuthors holds folded author strings that carry no real
// information (parser fallbacks such as "Unknown", "Unknown" in other
// languages, device defaults such as "User"). Keys are stored folded
// (lower-cased, diacritics stripped) to match isPlaceholderAuthor.
var placeholderAuthors = map[string]struct{}{
	// Hungarian
	"ismeretlen":        {},
	"szerzo ismeretlen": {},
	"nevtelen":          {},
	// English
	"unknown":        {},
	"unknown author": {},
	"author unknown": {},
	"no author":      {},
	"untitled":       {},
	"anonymous":      {},
	"none":           {},
	"n/a":            {},
	"n.n.":           {},
	// French
	"inconnu":        {},
	"inconnue":       {},
	"auteur inconnu": {},
	"anonyme":        {},
	// German
	"unbekannt": {},
	"anonym":    {},
	// Spanish
	"desconocido": {},
	"desconocida": {},
	// Portuguese
	"desconhecido": {},
	"desconhecida": {},
	// Italian
	"sconosciuto":        {},
	"sconosciuta":        {},
	"autore sconosciuto": {},
	// Spanish, Portuguese ("anónimo"/"anônimo") and Italian ("anonimo")
	// anonymous, all folding to the same key.
	"anonimo": {},
	// Dutch
	"onbekend":         {},
	"onbekende auteur": {},
	// Romanian
	"necunoscut":       {},
	"autor necunoscut": {},
	"anonim":           {},
	// Czech
	"neznamy":       {},
	"neznama":       {},
	"neznamy autor": {},
	// Polish
	"nieznany":       {},
	"nieznana":       {},
	"autor nieznany": {},
	// Swedish
	"okand":            {},
	"okant":            {},
	"okand forfattare": {},
	// Turkish
	"bilinmiyor":       {},
	"bilinmeyen yazar": {},
	// Russian
	"неизвестный":       {},
	"неизвестный автор": {},
	"неизвестно":        {},
	// Arabic
	"مجهول":      {},
	"مؤلف مجهول": {},
	// Urdu
	"نامعلوم": {},
	// Hindi
	"अज्ञात": {},
	// Chinese ("佚名" is the classic anonymous marker)
	"未知":   {},
	"未知作者": {},
	"佚名":   {},
	// Japanese
	"不明":   {},
	"作者不明": {},
	// Korean
	"미상":    {},
	"작자 미상": {},
	// device/converter defaults and punctuation
	"user": {},
	"-":    {},
	"?":    {},
	"":     {},
}

// placeholderTitles holds folded title strings that carry no real
// information (converter defaults such as "Untitled", in several
// languages). Keys are stored folded to match isPlaceholderTitle. Any real
// book could theoretically be titled "Untitled", but that is rare enough to
// accept; the filename fallback still yields something informative.
var placeholderTitles = map[string]struct{}{
	"untitled":     {},
	"sans titre":   {},
	"ohne titel":   {},
	"unbenannt":    {},
	"sin titulo":   {},
	"senza titolo": {},
	"sem titulo":   {},
	"zonder titel": {},
	"naamloos":     {},
	"bez tytulu":   {},
	"bez nazvu":    {},
	"utan titel":   {},
	"basliksiz":    {},
	"adsiz":        {},
	"без названия": {},
	"بدون عنوان":   {},
	"无标题":          {},
	"無題":           {},
	"제목 없음":        {},
	"nevtelen":     {},
	"cim nelkul":   {},
	"":             {},
}

// isPlaceholderTitle reports whether s is a known placeholder title rather
// than a real title.
func isPlaceholderTitle(s string) bool {
	_, ok := placeholderTitles[foldName(s)]
	return ok
}

// ebookExtensions are stripped when deriving a fallback title from a file
// name. They cover the scanner's known formats plus common ebook types that
// may appear in display paths.
var ebookExtensions = map[string]struct{}{
	".epub": {},
	".mobi": {},
	".azw":  {},
	".azw3": {},
	".prc":  {},
	".pdf":  {},
	".fb2":  {},
	".djvu": {},
	".txt":  {},
}

// titleFromPath derives a last-resort book title from a file path (Calibre
// style): archive prefixes ("outer.zip!inner/c.epub") and directories are
// dropped, a known ebook extension is stripped, and underscores become
// spaces. Dots are intentionally kept (they may belong to the title, e.g.
// "J. Verne"). The "by Author" suffix, if any, is kept as part of the title:
// splitting it off is too risky at this stage.
func titleFromPath(path string) string {
	return normalizeMeta(cleanStem(path))
}

// cleanStem strips a display path down to a usable title stem: archive
// prefixes ("outer.zip!inner/c.epub") and directories are dropped, a known
// ebook extension is stripped, and underscores become spaces.
func cleanStem(path string) string {
	if i := strings.LastIndex(path, "!"); i >= 0 {
		path = path[i+1:]
	}
	name := filepath.Base(path)
	if ext := filepath.Ext(name); ext != "" {
		if _, ok := ebookExtensions[strings.ToLower(ext)]; ok {
			name = name[:len(name)-len(ext)]
		}
	}
	return strings.ReplaceAll(name, "_", " ")
}

// calibreSeparators are the "Title - Author" separators accepted by the
// Calibre-style filename parse: ASCII hyphen plus en/em dashes,
// always space-padded.
var calibreSeparators = []string{" - ", " – ", " — "}

// parseCalibreFilename tries to split a file stem into a Calibre-style
// "Title - Author" pair, split at the last separator. This is the strict
// variant: the author part only counts when it contains a comma ("Last,
// First" order is a strong name signal; a bare "X - Something" split would
// as often cut a subtitle). The title part must be non-empty and not a
// title placeholder. The author is returned as-is, without normalization.
func parseCalibreFilename(path string) (title, author string, ok bool) {
	return parseCalibre(path, true)
}

// parseCalibreAuthorOnly is the relaxed variant for author-only recovery
// next to a known-good title (which is never touched here): a comma-less
// "First Last" author part counts when it is exactly two raw words, each at
// least 2 runes ("James Clavell" yes, "A sivatag bolygója" no).
func parseCalibreAuthorOnly(path string) (author string, ok bool) {
	_, author, ok = parseCalibre(path, false)
	return author, ok
}

func parseCalibre(path string, strict bool) (title, author string, ok bool) {
	stem := normalizeMeta(cleanStem(path))
	sep := ""
	idx := -1
	for _, s := range calibreSeparators {
		if i := strings.LastIndex(stem, s); i > idx {
			idx, sep = i, s
		}
	}
	if idx < 0 {
		return "", "", false
	}
	title, author = normalizeMeta(stem[:idx]), normalizeMeta(stem[idx+len(sep):])
	if title == "" || isPlaceholderTitle(title) {
		return "", "", false
	}
	if strict {
		if !strings.Contains(author, ",") {
			return "", "", false
		}
		parts := strings.FieldsFunc(author, func(r rune) bool { return r == ',' || unicode.IsSpace(r) })
		if len(parts) < 2 || len(parts) > 4 {
			return "", "", false
		}
		for _, p := range parts {
			if runeLen(p) < 2 {
				return "", "", false
			}
		}
		return title, author, true
	}
	parts := strings.Fields(author)
	if len(parts) != 2 {
		return "", "", false
	}
	for _, p := range parts {
		if runeLen(p) < 2 {
			return "", "", false
		}
	}
	return title, author, true
}

// stemTok is one token of a filename stem: either a word or a separator run.
type stemTok struct {
	text string
	sep  bool
}

// isStemSep reports whether r separates words inside a filename stem.
func isStemSep(r rune) bool {
	switch r {
	case ' ', '\t', ',', '.', ';', ':', '!', '?', '(', ')', '[', ']', '"', '\'', '/', '_', '-', '–', '—':
		return true
	}
	return false
}

// tokenizeStem splits s into alternating word/separator tokens, preserving
// the original spelling for later reconstruction.
func tokenizeStem(s string) []stemTok {
	var toks []stemTok
	var cur strings.Builder
	curSep := false
	started := false
	flush := func() {
		if started {
			toks = append(toks, stemTok{cur.String(), curSep})
			cur.Reset()
			started = false
		}
	}
	for _, r := range s {
		sep := isStemSep(r)
		if !started {
			curSep, started = sep, true
		} else if sep != curSep {
			flush()
			curSep, started = sep, true
		}
		cur.WriteRune(r)
	}
	flush()
	return toks
}

// hasDash reports whether s contains a hyphen or dash.
func hasDash(s string) bool {
	return strings.ContainsAny(s, "-–—")
}

// wordFold returns the folded comparison form of a stem word token.
func wordFold(w string) string {
	return foldDiacritics(metaKey(w))
}

// tokenSetOf returns the folded set of words.
func tokenSetOf(words []string) map[string]struct{} {
	set := make(map[string]struct{}, len(words))
	for _, w := range words {
		set[w] = struct{}{}
	}
	return set
}

// setsEqual reports whether folded words equal the author token set.
func setsEqual(words []string, set map[string]struct{}) bool {
	if len(words) != len(set) {
		return false
	}
	for _, w := range words {
		if _, ok := set[w]; !ok {
			return false
		}
	}
	return true
}

// washAuthorAffix removes a credit affix from a filename-derived title stem:
// a leading "Author - " or a trailing " - Author" / " by Author", where
// Author matches one of the known authors' full token sets (order-free,
// whole-word, diacritic-folded). Guards: only credit positions at the stem
// edges with dash (or "by") adjacency are stripped, so genuine title words
// that happen to equal a common surname ("A fekete folt") survive; if the
// result is empty or a placeholder, the original stem is kept.
func washAuthorAffix(stem string, authorSets [][]string) string {
	if stem == "" || len(authorSets) == 0 {
		return stem
	}
	toks := tokenizeStem(stem)
	var words []int
	for i, t := range toks {
		if !t.sep {
			words = append(words, i)
		}
	}
	if len(words) == 0 {
		return stem
	}
	folded := make([]string, len(words))
	for i, wi := range words {
		folded[i] = wordFold(toks[wi].text)
	}

	dropFrom, dropTo := -1, -1
	for _, auth := range authorSets {
		set := tokenSetOf(auth)
		if len(set) == 0 {
			continue
		}
		k := len(set)
		// Leading "Author - rest": full token set first, dash separator after.
		// toks[words[k]-1] is the separator run between the k-th word and
		// the rest (word/sep tokens strictly alternate).
		if len(words) > k && setsEqual(folded[:k], set) && hasDash(toks[words[k]-1].text) {
			dropFrom, dropTo = 0, words[k]
			break
		}
	}
	if dropFrom < 0 {
		for _, auth := range authorSets {
			set := tokenSetOf(auth)
			if len(set) == 0 || len(words) < len(set) {
				continue
			}
			k := len(set)
			if !setsEqual(folded[len(words)-k:], set) {
				continue
			}
			// Trailing "rest - Author" or "rest by Author": dash separator
			// or a whole "by" word before the author window.
			start := words[len(words)-k]
			j := start - 1
			for j >= 0 && toks[j].sep {
				j--
			}
			if j < 0 {
				continue
			}
			if hasDash(toks[j+1].text) {
				dropFrom, dropTo = start, len(toks)
				break
			}
			if !toks[j].sep && wordFold(toks[j].text) == "by" {
				dropFrom, dropTo = j, len(toks)
				break
			}
		}
	}
	if dropFrom < 0 {
		return stem
	}
	var b strings.Builder
	for i, t := range toks {
		if i < dropFrom || i >= dropTo {
			b.WriteString(t.text)
		}
	}
	out := normalizeMeta(strings.Trim(b.String(), " \t-–—,.;:!?()[]\"'/_"))
	if out == "" || isPlaceholderTitle(out) {
		return stem
	}
	return out
}

// normalizeMeta trims a metadata string and collapses inner whitespace runs
// to single spaces.
func normalizeMeta(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// metaKey returns the comparison key for voting: normalized and lower-cased.
func metaKey(s string) string {
	return strings.ToLower(normalizeMeta(s))
}

// leftoverReplacer folds characters that have no Unicode decomposition and
// therefore survive NFD mark-stripping (e.g. Scandinavian ø, German ß).
// Input is expected to be lower-cased already.
var leftoverReplacer = strings.NewReplacer(
	"ø", "o",
	"ß", "ss",
	"æ", "ae", "œ", "oe",
	"ł", "l", "đ", "d", "þ", "th", "ı", "i",
)

// foldDiacritics strips diacritics to plain ASCII (e.g. "Márai" becomes
// "marai"). Technique from the official Go blog on text normalization
// (https://go.dev/blog/normalization): NFD decompose, drop nonspacing marks
// (Mn), NFC recompose, then fold the few characters without decomposition.
//
// Marks are dropped only after Latin base characters: Cyrillic й (и+breve)
// and Indic viramas are Mn marks too, and stripping them would corrupt
// those scripts. Non-Latin text therefore matches by exact folded form.
func foldDiacritics(s string) string {
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

// foldName returns the loose comparison key for author-name matching:
// normalized, lower-cased and diacritic-folded.
func foldName(s string) string {
	return foldDiacritics(metaKey(s))
}

// matchSeparators are turned into spaces for loose token matching, so that
// punctuation and name order never break author detection ("DEAVER,
// JEFFERY - ..." still matches "Jeffery Deaver").
var matchSeparators = strings.NewReplacer(
	",", " ", ".", " ", ";", " ", ":", " ", "!", " ", "?", " ",
	"(", " ", ")", " ", "[", " ", "]", " ", "\"", " ", "'", " ",
	"/", " ", "-", " ", "_", " ", "–", " ", "—", " ",
)

// nameParticles holds short tokens that carry no distinctive value in
// author matching (nobiliary particles, articles). Single- and two-letter
// tokens (initials such as "J.", articles such as "al") are dropped by
// length; this set covers the common three-letter ones.
var nameParticles = map[string]struct{}{
	"van": {}, "von": {}, "del": {}, "dos": {}, "das": {},
	"der": {}, "den": {}, "ter": {}, "ten": {},
}

// matchTokens folds s for token matching (lower-case, diacritics folded,
// separators spaced) and returns its distinctive tokens: lower-cased words
// longer than 2 runes, minus name particles, deduplicated.
func matchTokens(s string) []string {
	var out []string
	seen := make(map[string]struct{})
	for _, tok := range strings.Fields(matchSeparators.Replace(foldDiacritics(metaKey(s)))) {
		if runeLen(tok) <= 2 {
			continue
		}
		if _, ok := nameParticles[tok]; ok {
			continue
		}
		if _, ok := seen[tok]; !ok {
			seen[tok] = struct{}{}
			out = append(out, tok)
		}
	}
	return out
}

// titleMentionsAuthor reports whether every distinctive token of any author
// is present in the title as a whole word, in any order. Single-token
// authors (e.g. "Madonna", or a bare surname) match on that one word, which
// carries a small false-positive risk for common words — acceptable, since
// this only breaks ties, never disqualifies.
func titleMentionsAuthor(title string, authorTokenSets [][]string) bool {
	if len(authorTokenSets) == 0 {
		return false
	}
	titleSet := make(map[string]struct{})
	for _, tok := range matchTokens(title) {
		titleSet[tok] = struct{}{}
	}
	for _, tokens := range authorTokenSets {
		if len(tokens) == 0 {
			continue
		}
		all := true
		for _, tok := range tokens {
			if _, ok := titleSet[tok]; !ok {
				all = false
				break
			}
		}
		if all {
			return true
		}
	}
	return false
}

// isPlaceholderAuthor reports whether s is a known placeholder rather than a
// real author name. Comparison uses the folded form, so e.g. both "Névtelen"
// and "Nevtelen" match.
func isPlaceholderAuthor(s string) bool {
	_, ok := placeholderAuthors[foldName(s)]
	return ok
}

// isTitleEcho reports whether an author name is just the file's title
// copied into the author field (a known converter artifact, e.g. both
// title and author read "Patkánykirály"). Such claims carry no author
// information. Both sides must be non-empty, so empty titles never filter.
func isTitleEcho(name, title string) bool {
	name, title = normalizeMeta(name), normalizeMeta(title)
	return name != "" && title != "" && foldName(name) == foldName(title)
}

// splitAuthors splits a GROUP_CONCAT aggregate back into author names,
// dropping placeholders.
func splitAuthors(aggregate string) []string {
	if aggregate == "" {
		return nil
	}
	var out []string
	for _, name := range strings.Split(aggregate, authorSeparator) {
		if name = normalizeMeta(name); name != "" && !isPlaceholderAuthor(name) {
			out = append(out, name)
		}
	}
	return out
}

// hasSpace reports whether s contains a space, i.e. looks like a full name
// ("J. Verne") rather than a single token ("Verne", "Ismeretlen").
func hasSpace(s string) bool {
	return strings.ContainsRune(s, ' ')
}

// spacedCount counts names in the list that contain a space.
func spacedCount(names []string) int {
	n := 0
	for _, name := range names {
		if hasSpace(name) {
			n++
		}
	}
	return n
}

// runeLen returns the rune count of s.
func runeLen(s string) int {
	return utf8.RuneCountInString(s)
}

// pickTitle selects the best title from per-file candidates. Empty and
// placeholder titles ("Untitled" and its multilingual variants) are dropped
// first; of the rest the most common normalized title wins; ties are broken
// by fewer underscores (machine-generated titles tend to use them as
// separators while clean titles use spaces), then by not mentioning one of
// the authors (an "Author - Title" pattern is usually redundant metadata,
// not the real title), then longer string, then first-seen. authors holds
// the book's known author names and may be nil, in which case the author
// check is skipped.
func pickTitle(candidates []string, authors []string) string {
	type vote struct {
		title       string
		count       int
		first       int
		underscores int
		noisy       bool
	}
	var authorTokenSets [][]string
	for _, name := range authors {
		authorTokenSets = append(authorTokenSets, matchTokens(name))
	}
	votes := make(map[string]*vote)
	var order []string
	for i, c := range candidates {
		if c = normalizeMeta(c); c == "" || isPlaceholderTitle(c) {
			continue
		}
		key := metaKey(c)
		v, ok := votes[key]
		if !ok {
			v = &vote{first: i}
			votes[key] = v
			order = append(order, key)
		}
		v.count++
		// Keep the longest observed spelling of this title.
		if runeLen(c) > runeLen(v.title) {
			v.title = c
			v.underscores = strings.Count(c, "_")
			v.noisy = titleMentionsAuthor(c, authorTokenSets)
		}
	}
	best := ""
	bestCount, bestUnderscores, bestLen, bestFirst := 0, 0, -1, 0
	bestNoisy := false
	for _, key := range order {
		v := votes[key]
		if v.count > bestCount ||
			(v.count == bestCount && v.underscores < bestUnderscores) ||
			(v.count == bestCount && v.underscores == bestUnderscores && !v.noisy && bestNoisy) ||
			(v.count == bestCount && v.underscores == bestUnderscores && v.noisy == bestNoisy && runeLen(v.title) > bestLen) ||
			(v.count == bestCount && v.underscores == bestUnderscores && v.noisy == bestNoisy && runeLen(v.title) == bestLen && v.first < bestFirst) {
			best, bestCount, bestUnderscores, bestLen, bestFirst, bestNoisy = v.title, v.count, v.underscores, runeLen(v.title), v.first, v.noisy
		}
	}
	return best
}

// canonicalAuthorName returns the comparison form of an author name:
// "Last, First" becomes "First Last" (flipped around the first comma).
// Names without a comma (including Hungarian "Last First" order, which has
// no comma to mark it) are returned as-is. Only the voting key uses this;
// display keeps the first-seen original spelling.
func canonicalAuthorName(name string) string {
	if i := strings.Index(name, ","); i >= 0 {
		if flipped := normalizeMeta(name[i+1:] + " " + name[:i]); flipped != "" {
			return flipped
		}
	}
	return name
}

// pickAuthors selects the best author list from per-file candidates: the most
// common full list wins (order-insensitive, so "A, B" and "B, A" vote
// together, and "Last, First" spellings vote together with "First Last"
// ones; the first-seen order and spelling is kept for output). Ties are
// broken by more authors, then more characters, then more space-containing
// names, then first-seen.
func pickAuthors(candidates [][]string) []string {
	type vote struct {
		list  []string
		count int
		first int
	}
	votes := make(map[string]*vote)
	var order []string
	for i, c := range candidates {
		var kept []string
		for _, name := range c {
			if name = normalizeMeta(name); name != "" && !isPlaceholderAuthor(name) {
				kept = append(kept, name)
			}
		}
		if len(kept) == 0 {
			continue
		}
		sorted := append([]string(nil), kept...)
		for i, name := range sorted {
			sorted[i] = canonicalAuthorName(name)
		}
		sortStrings(sorted)
		key := strings.Join(sorted, authorSeparator)
		v, ok := votes[key]
		if !ok {
			v = &vote{list: kept, first: i}
			votes[key] = v
			order = append(order, key)
		}
		v.count++
	}
	var best []string
	bestCount, bestAuthors, bestRunes, bestSpaced, bestFirst := 0, -1, -1, -1, 0
	for _, key := range order {
		v := votes[key]
		runes := 0
		for _, name := range v.list {
			runes += runeLen(name)
		}
		spaced := spacedCount(v.list)
		if v.count > bestCount ||
			(v.count == bestCount && len(v.list) > bestAuthors) ||
			(v.count == bestCount && len(v.list) == bestAuthors && runes > bestRunes) ||
			(v.count == bestCount && len(v.list) == bestAuthors && runes == bestRunes && spaced > bestSpaced) ||
			(v.count == bestCount && len(v.list) == bestAuthors && runes == bestRunes && spaced == bestSpaced && v.first < bestFirst) {
			best, bestCount, bestAuthors, bestRunes, bestSpaced, bestFirst =
				v.list, v.count, len(v.list), runes, spaced, v.first
		}
	}
	if best == nil {
		return []string{}
	}
	return best
}

// sortStrings sorts s in place. A tiny insertion sort avoids pulling in the
// slices package for short author lists.
func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j] < s[j-1]; j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
}
