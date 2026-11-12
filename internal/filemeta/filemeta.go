// Package filemeta derives title and author candidates from file paths.
// The rules are shared by the scan-time claim extraction and the book-level
// filename fallback.
package filemeta

import (
	"path/filepath"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/f0d0r/margaret-cli/internal/metaheur"
	"github.com/f0d0r/margaret-cli/internal/textmatch"
	"golang.org/x/text/encoding"
	"golang.org/x/text/encoding/charmap"
)

// placeholderTitles holds folded title strings that carry no real
// information (converter defaults such as "Untitled", in several languages).
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

// IsPlaceholderTitle reports whether s is a known placeholder title rather
// than a real title.
func IsPlaceholderTitle(s string) bool {
	_, ok := placeholderTitles[fold(s)]
	return ok
}

// ebookExtensions are stripped when deriving a fallback title from a file
// name. They cover the scanner's known formats plus common ebook types that
// may appear in display paths.
var ebookExtensions = map[string]struct{}{
	".epub": {}, ".mobi": {}, ".azw": {}, ".azw3": {}, ".prc": {},
	".pdf": {}, ".fb2": {}, ".djvu": {}, ".txt": {}, ".text": {}, ".rtf": {},
}

// TitleFromPath derives a last-resort title from a file path: archive
// prefixes and directories are dropped, a known ebook extension is stripped,
// and underscores become spaces.
func TitleFromPath(path string) string {
	return normalize(CleanStem(path))
}

// CleanStem strips a display path down to a usable title stem: archive
// prefixes ("outer.zip!inner/c.epub") and directories are dropped, a known
// ebook extension is stripped, and underscores become spaces.
func CleanStem(path string) string {
	if i := archiveBoundary(path); i >= 0 {
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

// archiveExtensions are stripped from path components before they are
// considered as author or "Author - Title" candidates.
var archiveExtensions = map[string]struct{}{
	".zip": {}, ".rar": {}, ".7z": {}, ".tar": {}, ".gz": {}, ".bz2": {},
	".tgz": {}, ".tbz2": {},
}

// archiveBoundary returns the index of the last "!" that separates an archive
// from its member, or -1 when the path has no archive separator.
func archiveBoundary(path string) int {
	for i := strings.LastIndex(path, "!"); i >= 0; i = strings.LastIndex(path[:i], "!") {
		if isArchiveBoundary(path, i) {
			return i
		}
	}
	return -1
}

// isArchiveBoundary reports whether the "!" at path[i] follows an archive
// name.
func isArchiveBoundary(path string, i int) bool {
	_, ok := archiveExtensions[strings.ToLower(filepath.Ext(path[:i]))]
	return ok
}

// archiveSegments splits a display path at its archive boundaries.
func archiveSegments(path string) []string {
	var cuts []int
	for i := strings.LastIndex(path, "!"); i >= 0; i = strings.LastIndex(path[:i], "!") {
		if isArchiveBoundary(path, i) {
			cuts = append(cuts, i)
		}
	}
	if len(cuts) == 0 {
		return []string{path}
	}
	segments := make([]string, 0, len(cuts)+1)
	start := 0
	for i := len(cuts) - 1; i >= 0; i-- {
		segments = append(segments, path[start:cuts[i]])
		start = cuts[i] + 1
	}
	return append(segments, path[start:])
}

// calibreSeparators are the "Title - Author" separators accepted by the
// Calibre-style filename parse: ASCII hyphen plus en/em dashes, always
// space-padded.
var calibreSeparators = []string{" - ", " – ", " — "}

// ParseCalibreFilename tries to split a file stem into a Calibre-style
// "Title - Author" pair, split at the last separator. This is the strict
// variant: the author part only counts when it contains a comma.
func ParseCalibreFilename(path string) (title, author string, ok bool) {
	return parseCalibre(path, true)
}

// ParseCalibreAuthorOnly is the relaxed variant for author-only recovery next
// to a known-good title: a comma-less "First Last" author part counts when it
// is exactly two raw words, each at least 2 runes.
func ParseCalibreAuthorOnly(path string) (author string, ok bool) {
	_, author, ok = parseCalibre(path, false)
	return author, ok
}

func parseCalibre(path string, strict bool) (title, author string, ok bool) {
	stem := normalize(CleanStem(path))
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
	title, author = normalize(stem[:idx]), normalize(stem[idx+len(sep):])
	if title == "" || IsPlaceholderTitle(title) {
		return "", "", false
	}
	if strict {
		if calibreAuthorShape(author) && metaheur.IsName(author) {
			return normalizeTitleArticle(title), author, true
		}
		if calibreAuthorShape(title) && metaheur.IsName(title) && metaheur.IsTitle(author) {
			return normalizeTitleArticle(author), title, true
		}
		return "", "", false
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
	if !metaheur.IsName(author) {
		return "", "", false
	}
	return normalizeTitleArticle(title), author, true
}

// calibreAuthorShape reports whether s has the "Last, First" shape Calibre
// uses: one comma and two to four name-sized tokens.
func calibreAuthorShape(s string) bool {
	if !strings.Contains(s, ",") {
		return false
	}
	parts := strings.FieldsFunc(s, func(r rune) bool { return r == ',' || unicode.IsSpace(r) })
	if len(parts) < 2 || len(parts) > 4 {
		return false
	}
	for _, p := range parts {
		if runeLen(p) < 2 {
			return false
		}
	}
	return true
}

// ParseNaturalFilename splits the natural-order "Author - Title" and
// "Title - Author" spellings common in sideloaded libraries. Both sides are
// validated as a name and a title; a tie yields no result.
func ParseNaturalFilename(path string) (title, author string, ok bool) {
	stem := normalize(CleanStem(path))
	for _, pair := range splitFilenamePairs(stem) {
		left, right := pair[0], pair[1]
		if IsPlaceholderTitle(left) || IsPlaceholderTitle(right) {
			continue
		}
		leftName, rightName := metaheur.IsName(left), metaheur.IsName(right)
		leftTitle, rightTitle := metaheur.IsTitle(left), metaheur.IsTitle(right)
		authorLeft, authorRight := leftName && rightTitle, rightName && leftTitle
		switch {
		case authorLeft && !authorRight && !containsGenericPathWord(left):
			return normalizeTitleArticle(right), left, true
		case authorRight && !authorLeft && !containsGenericPathWord(right):
			return normalizeTitleArticle(left), right, true
		}
	}
	return "", "", false
}

// splitFilenamePairs returns the first and last dash-separated splits of a
// file stem, deduplicated.
func splitFilenamePairs(stem string) [][2]string {
	var out [][2]string
	seen := make(map[[2]string]struct{})
	add := func(left, right string) {
		left, right = normalize(left), normalize(right)
		if left == "" || right == "" {
			return
		}
		pair := [2]string{left, right}
		if _, ok := seen[pair]; ok {
			return
		}
		seen[pair] = struct{}{}
		out = append(out, pair)
	}
	for _, sep := range calibreSeparators {
		if i := strings.Index(stem, sep); i >= 0 {
			add(stem[:i], stem[i+len(sep):])
		}
		if i := strings.LastIndex(stem, sep); i >= 0 {
			add(stem[:i], stem[i+len(sep):])
		}
	}
	return out
}

// normalizeTitleArticle moves a trailing ", The" (or another article in the
// same position) to the front.
func normalizeTitleArticle(title string) string {
	title = normalize(title)
	for _, article := range []string{"The", "An", "A", "Az", "Egy"} {
		suffix := ", " + article
		if len(title) > len(suffix) && strings.HasSuffix(strings.ToLower(title), strings.ToLower(suffix)) {
			rest := normalize(title[:len(title)-len(suffix)])
			if rest != "" {
				return article + " " + rest
			}
		}
	}
	return title
}

// genericPathWords are folder words that say nothing about the author.
var genericPathWords = map[string]struct{}{
	"author": {}, "authors": {}, "writer": {}, "writers": {},
	"book": {}, "books": {}, "ebook": {}, "ebooks": {}, "library": {},
	"series": {}, "collection": {}, "collections": {},
	"fiction": {}, "nonfiction": {}, "novel": {}, "novels": {},
	"story": {}, "stories": {}, "poetry": {}, "drama": {},
	"history": {}, "biography": {}, "children": {}, "young": {}, "adult": {},
	"comics": {}, "magazine": {}, "magazines": {}, "misc": {}, "other": {},
	"various": {}, "download": {}, "downloads": {}, "incoming": {},
	"temp": {}, "tmp": {}, "archive": {}, "archives": {}, "backup": {},
	"old": {}, "new": {}, "test": {}, "tests": {}, "source": {}, "sources": {},
	"docs": {}, "documents": {}, "data": {}, "files": {}, "resources": {},
	"media": {}, "video": {}, "music": {}, "scanned": {}, "digital": {},
	"unknown": {}, "publisher": {}, "publishers": {}, "press": {},
	"star": {}, "wars": {}, "trek": {}, "harry": {}, "potter": {},
	"pokemon": {}, "disney": {}, "marvel": {}, "anime": {}, "manga": {},
	"chronicles": {}, "saga": {}, "trilogy": {}, "tetralogy": {}, "cycle": {},
	"omnibus": {}, "anthology": {}, "box": {}, "boxed": {}, "set": {},
	"complete": {}, "collected": {}, "selected": {}, "works": {},
	"regeny": {}, "regenyek": {}, "irodalom": {}, "konyv": {}, "konyvek": {},
	"sorozat": {}, "novella": {}, "novellak": {}, "vers": {}, "versek": {},
	"tortenelem": {}, "eletrajz": {}, "gyerek": {}, "gyerekek": {},
	"ifjusagi": {}, "krimi": {}, "scifi": {}, "fantasy": {}, "magazin": {},
	"ujsag": {}, "cikk": {}, "cikkek": {}, "tanulmany": {}, "tanulmanyok": {},
	"gyujtemeny": {}, "gyujtemenyek": {}, "egyeb": {}, "vegyes": {},
	"angol": {}, "nemet": {}, "francia": {}, "olasz": {}, "orosz": {},
	"spanyol": {}, "magyar": {}, "nyelv": {}, "nyelvu": {}, "forditas": {},
	"nyers": {}, "forras": {}, "letoltes": {}, "letoltott": {},
	"archivum": {}, "mentes": {}, "uj": {}, "regi": {}, "teszt": {},
	"kezirat": {}, "kiadas": {}, "kiado": {},
}

// containsGenericPathWord reports whether s contains a folder word that says
// nothing about the author.
func containsGenericPathWord(s string) bool {
	folded := fold(s)
	for _, word := range strings.FieldsFunc(folded, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	}) {
		if _, ok := genericPathWords[word]; ok {
			return true
		}
	}
	return false
}

// ArchiveNames returns the archive segments of a display path, excluding the
// final member name.
func ArchiveNames(path string) []string {
	segments := archiveSegments(path)
	if len(segments) <= 1 {
		return nil
	}
	return segments[:len(segments)-1]
}

// ParseArchiveFilename parses an archive name such as "Blatty, William Peter
// - Az ördögűző.zip". It is tolerant: a title containing replacement runes is
// still returned so the author part can survive claim junk filtering.
func ParseArchiveFilename(segment string) (title, author string, ok bool) {
	name := filepath.Base(segment)
	if ext := strings.ToLower(filepath.Ext(name)); ext != "" {
		if _, archive := archiveExtensions[ext]; archive {
			name = name[:len(name)-len(ext)]
		}
	}
	stem := normalize(name)
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
	left, right := normalize(stem[:idx]), normalize(stem[idx+len(sep):])
	switch {
	case calibreAuthorShape(left) && metaheur.IsName(left):
		return right, left, true
	case calibreAuthorShape(right) && metaheur.IsName(right):
		return left, right, true
	case metaheur.IsName(left) && metaheur.IsTitle(right):
		return right, left, true
	case metaheur.IsName(right) && metaheur.IsTitle(left):
		return left, right, true
	default:
		return "", "", false
	}
}

var legacyNameEncodings = []encoding.Encoding{
	charmap.CodePage852,
	charmap.CodePage437,
	charmap.CodePage850,
	charmap.Windows1250,
	charmap.Windows1252,
	charmap.ISO8859_2,
}

// RepairName decodes a path component that was stored in a legacy code page.
// Valid UTF-8 is left untouched; mixed-encoding paths are repaired one
// component at a time.
func RepairName(s string) string {
	if utf8.ValidString(s) {
		return s
	}
	best := s
	bestScore := -1 << 30
	for _, enc := range legacyNameEncodings {
		decoded, err := enc.NewDecoder().String(s)
		if err != nil || !utf8.ValidString(decoded) {
			continue
		}
		if score := legacyNameScore(decoded); score > bestScore {
			best, bestScore = decoded, score
		}
	}
	return best
}

// RepairDisplayPath repairs each '/', '\\' and '!' separated component.
func RepairDisplayPath(path string) string {
	var b strings.Builder
	start := 0
	for i := 0; i < len(path); i++ {
		if path[i] != '/' && path[i] != '\\' && path[i] != '!' {
			continue
		}
		b.WriteString(RepairName(path[start:i]))
		b.WriteByte(path[i])
		start = i + 1
	}
	b.WriteString(RepairName(path[start:]))
	return b.String()
}

func legacyNameScore(s string) int {
	score := 0
	for _, r := range s {
		switch {
		case r == utf8.RuneError:
			score -= 50
		case unicode.IsControl(r):
			score -= 50
		case strings.ContainsRune("áéíóöőúüűÁÉÍÓÖŐÚÜŰ", r):
			score += 4
		case unicode.IsLetter(r):
			score += 2
		case unicode.IsDigit(r):
			score += 1
		}
	}
	return score
}

// ParentAuthor returns the nearest parent directory that looks like a
// personal name. Archive member names are ignored; archive names are handled
// separately by ParseArchiveFilename.
func ParentAuthor(path string) (string, bool) {
	if i := strings.IndexByte(path, '!'); i >= 0 {
		path = path[:i]
	}
	dir := filepath.Dir(path)
	for {
		name := RepairName(filepath.Base(dir))
		if name == "" || strings.HasPrefix(name, ".") || containsGenericPathWord(name) {
			break
		}
		if metaheur.IsName(name) && !metaheur.IsCredit(name) && !metaheur.IsJunkValue(name) {
			return name, true
		}
		parent := filepath.Dir(dir)
		if parent == dir || parent == "." {
			break
		}
		dir = parent
	}
	return "", false
}

// SourceGroup returns the conservative origin key used for cross-file
// independence: the archive prefix plus the containing directory.
func SourceGroup(path string) string {
	if i := archiveBoundary(path); i >= 0 {
		inner := path[i+1:]
		if dir := filepath.Dir(inner); dir != "." && dir != "" {
			return path[:i+1] + dir
		}
		return path[:i+1]
	}
	return filepath.Dir(path)
}

func normalize(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

func fold(s string) string {
	return textmatch.TitleKey(s)
}

func runeLen(s string) int {
	return utf8.RuneCountInString(s)
}
