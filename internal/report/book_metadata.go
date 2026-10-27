package report

import (
	"strings"
	"time"

	"github.com/f0d0r/margaret-cli/internal/identifier"
	"github.com/f0d0r/margaret-ebook-library/tools"
	"golang.org/x/text/language"
)

type fileMetadata struct {
	description     string
	publisher       string
	publicationDate string
	series          string
	seriesIndex     string
}

type bookMetadata struct {
	description  string
	series       string
	seriesIndex  string
	subjects     []string
	languages    []string
	identifiers  []BookIdentifier
	publications []BookPublication
}

func mergeBookMetadata(files []fileMetadata, subjects, languages []string, identifiers []BookIdentifier) bookMetadata {
	descriptions := make([]string, 0, len(files))
	series := make([]string, 0, len(files))
	for _, f := range files {
		if v := plainText(f.description); v != "" {
			descriptions = append(descriptions, v)
		}
		if v := normalizeMeta(f.series); v != "" {
			series = append(series, v)
		}
	}
	chosenSeries := pickCommonValue(series, nil)
	return bookMetadata{
		description:  pickCommonValue(descriptions, preferLonger),
		series:       chosenSeries,
		seriesIndex:  pickSeriesIndex(files, chosenSeries),
		subjects:     mergeSubjects(subjects),
		languages:    mergeLanguages(languages),
		identifiers:  mergeIdentifiers(identifiers),
		publications: mergePublications(files),
	}
}

func pickCommonValue(candidates []string, prefer func(a, b string) bool) string {
	type vote struct {
		value string
		count int
	}
	votes := make(map[string]*vote, len(candidates))
	order := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		candidate = normalizeMeta(candidate)
		if candidate == "" {
			continue
		}
		key := foldName(candidate)
		v, ok := votes[key]
		if !ok {
			v = &vote{value: candidate}
			votes[key] = v
			order = append(order, key)
		}
		v.count++
	}
	var best *vote
	for _, key := range order {
		v := votes[key]
		if best == nil || v.count > best.count ||
			(v.count == best.count && prefer != nil && prefer(v.value, best.value)) {
			best = v
		}
	}
	if best == nil {
		return ""
	}
	return best.value
}

func preferLonger(a, b string) bool {
	return runeLen(a) > runeLen(b)
}

func pickSeriesIndex(files []fileMetadata, series string) string {
	if series == "" {
		return ""
	}
	key := foldName(series)
	candidates := make([]string, 0, len(files))
	for _, f := range files {
		if foldName(normalizeMeta(f.series)) != key {
			continue
		}
		if index := normalizeMeta(f.seriesIndex); index != "" {
			candidates = append(candidates, index)
		}
	}
	return pickCommonValue(candidates, nil)
}

func mergePublications(files []fileMetadata) []BookPublication {
	type group struct {
		publisher string
		dates     []string
		seen      map[string]struct{}
	}
	groups := make(map[string]*group, len(files))
	var order []string
	for _, f := range files {
		publisher := normalizeMeta(f.publisher)
		date := normalizePublicationDate(f.publicationDate)
		if publisher == "" && date == "" {
			continue
		}
		key := foldName(publisher)
		g, ok := groups[key]
		if !ok {
			g = &group{publisher: publisher, seen: make(map[string]struct{})}
			groups[key] = g
			order = append(order, key)
		}
		if date == "" {
			continue
		}
		if _, ok := g.seen[date]; ok {
			continue
		}
		g.seen[date] = struct{}{}
		g.dates = append(g.dates, date)
	}
	out := make([]BookPublication, 0, len(order))
	for _, key := range order {
		g := groups[key]
		if len(g.dates) == 0 {
			if g.publisher != "" {
				out = append(out, BookPublication{Publisher: g.publisher})
			}
			continue
		}
		for _, date := range g.dates {
			out = append(out, BookPublication{
				Publisher:       g.publisher,
				PublicationDate: date,
			})
		}
	}
	return out
}

func mergeSubjects(values []string) []string {
	out := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		value = normalizeMeta(value)
		if value == "" {
			continue
		}
		key := foldName(value)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, value)
	}
	return out
}

func mergeLanguages(values []string) []string {
	out := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		tag, ok := normalizeLanguage(value)
		if !ok {
			continue
		}
		if _, ok := seen[tag]; ok {
			continue
		}
		seen[tag] = struct{}{}
		out = append(out, tag)
	}
	return out
}

func normalizeLanguage(value string) (string, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", false
	}
	tag, err := language.Parse(strings.ReplaceAll(value, "_", "-"))
	if err != nil {
		return "", false
	}
	value = tag.String()
	base := value
	if i := strings.IndexByte(base, '-'); i >= 0 {
		base = base[:i]
	}
	switch base {
	case "und", "mul", "zxx", "mis":
		return "", false
	}
	return value, true
}

func mergeIdentifiers(values []BookIdentifier) []BookIdentifier {
	out := make([]BookIdentifier, 0, len(values))
	index := make(map[string]int, len(values))
	for _, item := range values {
		if !identifier.IsISBNScheme(item.Scheme) {
			continue
		}
		value, ok := tools.CanonicalISBN13(item.Value)
		if !ok {
			continue
		}
		if idx, exists := index[value]; exists {
			out[idx].Source = identifier.MergeSources(out[idx].Source, item.Source)
			continue
		}
		index[value] = len(out)
		out = append(out, BookIdentifier{
			Scheme: "ISBN",
			Value:  value,
			Source: identifier.MergeSources(item.Source, item.Source),
		})
	}
	return out
}

func normalizePublicationDate(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	for _, layout := range []string{"2006-01-02", "2006-01", "2006"} {
		if t, err := time.Parse(layout, value); err == nil {
			if !validPublicationYear(t.Year()) {
				return ""
			}
			return t.Format(layout)
		}
	}
	for _, layout := range []string{time.RFC3339, "2006-01-02T15:04:05", "2006-01-02 15:04:05"} {
		if t, err := time.Parse(layout, value); err == nil {
			if !validPublicationYear(t.Year()) {
				return ""
			}
			return t.Format("2006-01-02")
		}
	}
	return ""
}

func validPublicationYear(year int) bool {
	return year >= 1000 && year <= time.Now().Year()+1
}
