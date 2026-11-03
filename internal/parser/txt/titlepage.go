package txt

import (
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/f0d0r/margaret-cli/internal/metaheur"
)

const (
	// titlePageCandidateLimit bounds how many leading lines the title-page
	// fallback considers. Title pages are short; body text must never be
	// scanned.
	titlePageCandidateLimit = 6
	// pairScoreThreshold is the minimum combined name and title score.
	pairScoreThreshold = 6
)

// titlePagePair extracts a title/author pair from the title page of a plain
// text book. It deliberately returns nothing when the two lines are equally
// plausible in both orders: a missing author is better than a swapped one.
// Standalone titles are not extracted; the report falls back to the file name
// for those.
//
// The layouts handled here (author and title in either order, one-line
// "Author: Title" / "Author - Title" pairs, names or titles wrapped over two
// lines) are common in plain-text books in many languages. Nothing in this
// file is tied to a particular language; plausibility comes from the
// structural scoring in the metaheur package.
func titlePagePair(lines []string) (title string, authors []string, ok bool) {
	candidates := titlePageCandidates(lines)
	if len(candidates) == 0 {
		return "", nil, false
	}

	type pick struct {
		title   string
		authors []string
		score   int
	}
	best := pick{score: -1}
	second := -1
	consider := func(title string, authors []string, score int) {
		if score <= 0 || metaheur.IsJunkValue(title) || metaheur.IsCredit(title) {
			return
		}
		switch {
		case score > best.score:
			second = best.score
			best = pick{title: title, authors: authors, score: score}
		case score > second:
			second = score
		}
	}

	// One-line "Author: Title" and "Author - Title" forms.
	for i := 0; i < len(candidates) && i < 2; i++ {
		for _, parts := range splitPairLine(candidates[i]) {
			if authors, title, score, ok := orientPair(parts[0], parts[1]); ok {
				consider(title, authors, score)
			}
		}
	}

	// Two-line author/title forms in either order.
	if len(candidates) >= 2 {
		if authors, title, score, ok := orientPair(candidates[0], candidates[1]); ok {
			consider(title, authors, score)
		}
	}

	// A name split over two lines, or a title wrapped over two lines.
	if len(candidates) >= 3 {
		joinedAuthor := strings.Join(candidates[:2], " ")
		if metaheur.IsName(joinedAuthor) && metaheur.IsTitle(candidates[2]) {
			consider(candidates[2], []string{joinedAuthor},
				metaheur.ScoreName(joinedAuthor)+metaheur.ScoreTitle(candidates[2]))
		}
		joinedTitle := strings.Join(candidates[1:3], " ")
		if metaheur.IsName(candidates[0]) && metaheur.IsTitle(joinedTitle) {
			consider(joinedTitle, []string{candidates[0]},
				metaheur.ScoreName(candidates[0])+metaheur.ScoreTitle(joinedTitle))
		}
	}

	if best.score < pairScoreThreshold {
		return "", nil, false
	}
	// A near-tie means both orientations are equally plausible.
	if second >= 0 && best.score-second < 2 {
		return "", nil, false
	}
	return best.title, best.authors, true
}

// titlePageCandidates returns the leading title-page lines, stopping before
// body text. Leading noise (copyright, scanner credits) is skipped; noise
// after the first candidate ends the title page.
func titlePageCandidates(lines []string) []string {
	var out []string
	for i := 0; i < len(lines) && i < metadataLines; i++ {
		line := strings.TrimSpace(lines[i])
		if line == "" {
			continue
		}
		if isKnownLabelLine(line) || isTitlePageNoise(line) {
			if len(out) > 0 {
				break
			}
			continue
		}
		if isSentenceLine(line) || metaheur.LooksLikeProse(line) {
			break
		}
		out = append(out, line)
		if len(out) >= titlePageCandidateLimit {
			break
		}
	}
	return out
}

func isTitlePageNoise(line string) bool {
	return metaheur.IsJunkValue(line) || metaheur.IsCredit(line) ||
		isPageNumber(line) || metaheur.IsChapterHeading(line)
}

// isSentenceLine reports a body sentence ending with a period. Author lines
// ending in initials or a comma do not contain a lowercase word and survive.
func isSentenceLine(line string) bool {
	if !strings.HasSuffix(line, ".") {
		return false
	}
	words := strings.Fields(line)
	if len(words) < 2 {
		return false
	}
	for _, w := range words {
		w = strings.Trim(w, `"'()[]{}.,;:!?`)
		r, _ := utf8.DecodeRuneInString(w)
		if unicode.IsLower(r) {
			return true
		}
	}
	return false
}

// splitPairLine splits a one-line author/title pair at ":", " - ", " – " or
// " — ". The first colon is used; up to three dashes are tried so a subtitle
// containing a dash does not hide the real split.
func splitPairLine(line string) [][2]string {
	var out [][2]string
	for _, sep := range []string{": ", " - ", " – ", " — "} {
		limit := 3
		if sep == ": " {
			limit = 1
		}
		idx := 0
		for n := 0; n < limit; n++ {
			i := strings.Index(line[idx:], sep)
			if i < 0 {
				break
			}
			i += idx
			left, right := strings.TrimSpace(line[:i]), strings.TrimSpace(line[i+len(sep):])
			if left != "" && right != "" {
				out = append(out, [2]string{left, right})
			}
			idx = i + len(sep)
		}
	}
	return out
}

// orientPair assigns one side to the author and the other to the title. Both
// orders are scored; a tie yields no result.
func orientPair(a, b string) (authors []string, title string, score int, ok bool) {
	aName, bName := metaheur.IsName(a), metaheur.IsName(b)
	aTitle, bTitle := metaheur.IsTitle(a), metaheur.IsTitle(b)
	aScore := metaheur.ScoreName(a) + metaheur.ScoreTitle(b)
	bScore := metaheur.ScoreName(b) + metaheur.ScoreTitle(a)
	aFits, bFits := aName && bTitle, bName && aTitle
	switch {
	case aFits && !bFits:
		return []string{a}, b, aScore, true
	case bFits && !aFits:
		return []string{b}, a, bScore, true
	case aFits && bFits:
		if aScore >= bScore+2 {
			return []string{a}, b, aScore, true
		}
		if bScore >= aScore+2 {
			return []string{b}, a, bScore, true
		}
	}
	return nil, "", 0, false
}
