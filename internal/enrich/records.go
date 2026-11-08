package enrich

import (
	"encoding/json"
	"strings"
	"time"

	"github.com/f0d0r/margaret-ebook-library/tools"
)

type linkedKey struct {
	Key string `json:"key"`
}

type editionRecord struct {
	Key         string          `json:"key"`
	Title       string          `json:"title"`
	Subtitle    string          `json:"subtitle"`
	Publishers  []string        `json:"publishers"`
	PublishDate string          `json:"publish_date"`
	Series      []string        `json:"series"`
	Languages   []linkedKey     `json:"languages"`
	ISBN10      []string        `json:"isbn_10"`
	ISBN13      []string        `json:"isbn_13"`
	OCLC        []string        `json:"oclc_numbers"`
	LCCN        []string        `json:"lccn"`
	Dewey       []string        `json:"dewey_decimal_class"`
	LC          []string        `json:"lc_classifications"`
	OCAID       string          `json:"ocaid"`
	Authors     []linkedKey     `json:"authors"`
	Works       []linkedKey     `json:"works"`
	Subjects    []string        `json:"subjects"`
	Description json.RawMessage `json:"description"`
}

type workRecord struct {
	Key              string          `json:"key"`
	Title            string          `json:"title"`
	Description      json.RawMessage `json:"description"`
	Subjects         []string        `json:"subjects"`
	FirstPublishDate string          `json:"first_publish_date"`
	Authors          []linkedKey     `json:"authors"`
}

type authorRecord struct {
	Key            string          `json:"key"`
	Name           string          `json:"name"`
	PersonalName   string          `json:"personal_name"`
	AlternateNames []string        `json:"alternate_names"`
	BirthDate      string          `json:"birth_date"`
	DeathDate      string          `json:"death_date"`
	Bio            json.RawMessage `json:"bio"`
}

type publication struct {
	Publisher       string
	PublicationDate string
}

type identifierValue struct {
	Scheme string
	Value  string
	Source string
}

type authorInfo struct {
	Key            string
	Name           string
	AlternateNames []string
	BirthDate      string
	DeathDate      string
	Bio            string
}

type bookEnrichment struct {
	BookID           int64
	EditionKey       string
	WorkKey          string
	Source           string
	Title            string
	Subtitle         string
	Description      string
	Series           string
	SeriesIndex      string
	FirstPublishDate string
	Subjects         []string
	Languages        []string
	Publications     []publication
	Identifiers      []identifierValue
	Authors          []authorInfo
	MatcherVersion   string
	DumpVersion      string
	EnrichedAt       int64
}

func buildEnrichment(bookID int64, edition *editionRecord, work *workRecord, authors map[string]*authorRecord, matcherVersion, dumpVersion string, now int64) *bookEnrichment {
	workKey := ""
	if len(edition.Works) > 0 {
		workKey = edition.Works[0].Key
	}
	enrichment := &bookEnrichment{
		BookID:         bookID,
		EditionKey:     edition.Key,
		WorkKey:        workKey,
		Source:         "OPENLIBRARY",
		Title:          edition.Title,
		Subtitle:       edition.Subtitle,
		SeriesIndex:    "",
		Subjects:       uniqueStrings(subjectsFor(edition, work)),
		MatcherVersion: matcherVersion,
		DumpVersion:    dumpVersion,
		EnrichedAt:     now,
	}
	if work != nil {
		enrichment.WorkKey = work.Key
		enrichment.FirstPublishDate = work.FirstPublishDate
		enrichment.Description = textValue(work.Description)
		if enrichment.Title == "" {
			enrichment.Title = work.Title
		}
	}
	if enrichment.Description == "" {
		enrichment.Description = textValue(edition.Description)
	}
	for _, series := range edition.Series {
		if series = strings.TrimSpace(series); series != "" {
			enrichment.Series = series
			break
		}
	}
	enrichment.Languages = mapLanguages(edition.Languages)
	enrichment.Publications = publicationsFor(edition)
	enrichment.Identifiers = identifiersFor(edition)
	enrichment.Authors = authorsFor(edition, work, authors)
	return enrichment
}

func subjectsFor(edition *editionRecord, work *workRecord) []string {
	subjects := append([]string(nil), edition.Subjects...)
	if work != nil {
		subjects = append(subjects, work.Subjects...)
	}
	return subjects
}

func publicationsFor(edition *editionRecord) []publication {
	date := normalizeDate(edition.PublishDate)
	publishers := uniqueStrings(edition.Publishers)
	if len(publishers) == 0 {
		if date == "" {
			return nil
		}
		publishers = []string{""}
	}
	out := make([]publication, 0, len(publishers))
	seen := make(map[string]struct{}, len(publishers))
	for _, publisher := range publishers {
		key := publisher + "\x1f" + date
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, publication{Publisher: publisher, PublicationDate: date})
	}
	return out
}

func identifiersFor(edition *editionRecord) []identifierValue {
	var out []identifierValue
	add := func(scheme, value string) {
		value = strings.TrimSpace(value)
		if value == "" {
			return
		}
		out = append(out, identifierValue{Scheme: scheme, Value: value, Source: "OPENLIBRARY"})
	}
	for _, value := range edition.ISBN10 {
		if isbn, ok := tools.ValidateBookISBN(value); ok {
			if isbn13, ok := isbn.ToISBN13(); ok {
				add("ISBN", isbn13.String())
			}
		}
	}
	for _, value := range edition.ISBN13 {
		if isbn, ok := tools.ValidateBookISBN(value); ok {
			if isbn13, ok := isbn.ToISBN13(); ok {
				add("ISBN", isbn13.String())
			}
		}
	}
	for _, value := range edition.OCLC {
		add("OCLC", value)
	}
	for _, value := range edition.LCCN {
		add("LCCN", value)
	}
	for _, value := range edition.Dewey {
		add("DEWEY", value)
	}
	for _, value := range edition.LC {
		add("LC", value)
	}
	add("OCAID", edition.OCAID)
	return dedupeIdentifiers(out)
}

func authorsFor(edition *editionRecord, work *workRecord, authors map[string]*authorRecord) []authorInfo {
	keys := make([]string, 0, len(edition.Authors))
	for _, author := range edition.Authors {
		if author.Key != "" {
			keys = append(keys, author.Key)
		}
	}
	if len(keys) == 0 && work != nil {
		for _, author := range work.Authors {
			if author.Key != "" {
				keys = append(keys, author.Key)
			}
		}
	}
	out := make([]authorInfo, 0, len(keys))
	seen := make(map[string]struct{}, len(keys))
	for _, key := range keys {
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		record := authors[key]
		if record == nil {
			continue
		}
		name := record.Name
		if name == "" {
			name = record.PersonalName
		}
		out = append(out, authorInfo{
			Key:            key,
			Name:           name,
			AlternateNames: uniqueStrings(record.AlternateNames),
			BirthDate:      strings.TrimSpace(record.BirthDate),
			DeathDate:      strings.TrimSpace(record.DeathDate),
			Bio:            textValue(record.Bio),
		})
	}
	return out
}

func dedupeIdentifiers(values []identifierValue) []identifierValue {
	out := make([]identifierValue, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		key := value.Scheme + "\x1f" + value.Value
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, value)
	}
	return out
}

func textValue(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		return text
	}
	var object struct {
		Value string `json:"value"`
	}
	if err := json.Unmarshal(raw, &object); err == nil {
		return object.Value
	}
	return ""
}

func normalizeDate(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	for _, layout := range []string{"2006-01-02", "2006-01", "2006"} {
		if parsed, err := time.Parse(layout, value); err == nil {
			if parsed.Year() < 1000 || parsed.Year() > time.Now().Year()+1 {
				return ""
			}
			return parsed.Format(layout)
		}
	}
	for _, layout := range []string{time.RFC3339, "2006-01-02T15:04:05", "2006-01-02 15:04:05"} {
		if parsed, err := time.Parse(layout, value); err == nil {
			if parsed.Year() < 1000 || parsed.Year() > time.Now().Year()+1 {
				return ""
			}
			return parsed.Format("2006-01-02")
		}
	}
	return ""
}

func uniqueStrings(values []string) []string {
	out := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		key := strings.ToLower(value)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, value)
	}
	return out
}

func mapLanguages(languages []linkedKey) []string {
	out := make([]string, 0, len(languages))
	seen := make(map[string]struct{}, len(languages))
	for _, language := range languages {
		tag := languageTag(language.Key)
		if tag == "" {
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

func languageTag(key string) string {
	code := strings.TrimPrefix(key, "/languages/")
	if tag, ok := marcLanguages[strings.ToLower(code)]; ok {
		return tag
	}
	return ""
}

var marcLanguages = map[string]string{
	"afr": "af", "alb": "sq", "amh": "am", "ara": "ar", "arm": "hy",
	"aze": "az", "ben": "bn", "bos": "bs", "bul": "bg", "cat": "ca",
	"chi": "zh", "cze": "cs", "dan": "da", "dut": "nl", "eng": "en",
	"est": "et", "eus": "eu", "fil": "tl", "fin": "fi", "fre": "fr",
	"geo": "ka", "ger": "de", "gle": "ga", "glg": "gl", "gre": "el",
	"guj": "gu", "heb": "he", "hin": "hi", "hrv": "hr", "hun": "hu",
	"ice": "is", "ind": "id", "ita": "it", "jpn": "ja", "kan": "kn",
	"kaz": "kk", "khm": "km", "kor": "ko", "lao": "lo", "lat": "la",
	"lav": "lv", "lit": "lt", "mal": "ml", "mar": "mr", "may": "ms",
	"mkd": "mk", "mlt": "mt", "mya": "my", "nep": "ne", "nor": "no",
	"pan": "pa", "per": "fa", "pol": "pl", "por": "pt", "rum": "ro",
	"rus": "ru", "san": "sa", "sin": "si", "slo": "sk", "slv": "sl",
	"spa": "es", "srp": "sr", "swa": "sw", "swe": "sv", "tam": "ta",
	"tel": "te", "tha": "th", "tur": "tr", "ukr": "uk", "urd": "ur",
	"uzb": "uz", "vie": "vi", "wel": "cy", "yid": "yi", "zul": "zu",
}
