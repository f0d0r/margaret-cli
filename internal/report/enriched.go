package report

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/f0d0r/margaret-cli/internal/db"
	"github.com/f0d0r/margaret-cli/internal/identifier"
)

// MatchInfo is the match metadata embedded in enriched and review reports.
type MatchInfo struct {
	Status         string  `json:"status"`
	Confidence     float64 `json:"confidence"`
	ContentScore   float64 `json:"content_score"`
	Method         string  `json:"method"`
	EditionKey     string  `json:"edition_key"`
	WorkKey        string  `json:"work_key"`
	ISBN           string  `json:"isbn"`
	ISBNSource     string  `json:"isbn_source"`
	ReviewReason   string  `json:"review_reason"`
	ScoreGap       float64 `json:"score_gap"`
	CandidateCount int64   `json:"candidate_count"`
	WorkCount      int64   `json:"work_count"`
	MatcherVersion string  `json:"matcher_version"`
	DumpVersion    string  `json:"dump_version"`
}

// EnrichmentInfo records where the enriched values came from.
type EnrichmentInfo struct {
	Source         string `json:"source"`
	EditionKey     string `json:"edition_key"`
	WorkKey        string `json:"work_key"`
	MatcherVersion string `json:"matcher_version"`
	DumpVersion    string `json:"dump_version"`
	EnrichedAt     int64  `json:"enriched_at"`
}

// AuthorInfo is one enriched OpenLibrary author record.
type AuthorInfo struct {
	Key            string   `json:"key"`
	Name           string   `json:"name"`
	AlternateNames []string `json:"alternate_names,omitempty"`
	BirthDate      string   `json:"birth_date,omitempty"`
	DeathDate      string   `json:"death_date,omitempty"`
	Bio            string   `json:"bio,omitempty"`
}

// EnrichedBook is one confirmed book with its OpenLibrary metadata.
type EnrichedBook struct {
	BookReport
	ScannedTitle     string          `json:"scanned_title"`
	ScannedAuthors   []string        `json:"scanned_authors"`
	Subtitle         string          `json:"subtitle,omitempty"`
	FirstPublishDate string          `json:"first_publish_date,omitempty"`
	AuthorDetails    []AuthorInfo    `json:"author_details,omitempty"`
	Match            MatchInfo       `json:"match"`
	Enrichment       *EnrichmentInfo `json:"enrichment,omitempty"`
}

type enrichmentBundle struct {
	info         *db.BookEnrichment
	subjects     []string
	languages    []string
	publications []db.BookEnrichmentPublication
	identifiers  []db.BookEnrichmentIdentifier
	authors      []db.BookEnrichmentAuthor
}

// WriteEnrichedBooks writes confirmed books with their match and enriched
// metadata. When no book is confirmed nothing is written.
func WriteEnrichedBooks(ctx context.Context, q *db.Queries, path string) error {
	matches, err := q.ListConfirmedBookMatches(ctx)
	if err != nil {
		return fmt.Errorf("list confirmed matches: %w", err)
	}
	if len(matches) == 0 {
		return nil
	}
	bundles, err := loadEnrichments(ctx, q)
	if err != nil {
		return err
	}

	books := make([]EnrichedBook, 0, len(matches))
	for _, match := range matches {
		book, ok, err := getBookReport(ctx, q, match.BookID)
		if err != nil {
			return fmt.Errorf("read confirmed book %d: %w", match.BookID, err)
		}
		if !ok {
			continue
		}
		enriched := mergeEnrichment(book, bundles[match.BookID])
		enriched.Match = matchInfo(match)
		books = append(books, enriched)
	}
	if len(books) == 0 {
		return nil
	}
	data, err := json.MarshalIndent(books, "", "  ")
	if err != nil {
		return err
	}
	if err := writeFile(path, data); err != nil {
		return fmt.Errorf("write enriched books report: %w", err)
	}
	return nil
}

func loadEnrichments(ctx context.Context, q *db.Queries) (map[int64]*enrichmentBundle, error) {
	parents, err := q.ListBookEnrichments(ctx)
	if err != nil {
		return nil, fmt.Errorf("list enrichments: %w", err)
	}
	if len(parents) == 0 {
		return map[int64]*enrichmentBundle{}, nil
	}
	bundles := make(map[int64]*enrichmentBundle, len(parents))
	for _, parent := range parents {
		parent := parent
		bundles[parent.BookID] = &enrichmentBundle{info: &parent}
	}
	subjects, err := q.ListBookEnrichmentSubjects(ctx)
	if err != nil {
		return nil, fmt.Errorf("list enrichment subjects: %w", err)
	}
	for _, subject := range subjects {
		if bundle := bundles[subject.BookID]; bundle != nil {
			bundle.subjects = append(bundle.subjects, subject.Name)
		}
	}
	languages, err := q.ListBookEnrichmentLanguages(ctx)
	if err != nil {
		return nil, fmt.Errorf("list enrichment languages: %w", err)
	}
	for _, language := range languages {
		if bundle := bundles[language.BookID]; bundle != nil {
			bundle.languages = append(bundle.languages, language.Name)
		}
	}
	publications, err := q.ListBookEnrichmentPublications(ctx)
	if err != nil {
		return nil, fmt.Errorf("list enrichment publications: %w", err)
	}
	for _, publication := range publications {
		if bundle := bundles[publication.BookID]; bundle != nil {
			bundle.publications = append(bundle.publications, publication)
		}
	}
	identifiers, err := q.ListBookEnrichmentIdentifiers(ctx)
	if err != nil {
		return nil, fmt.Errorf("list enrichment identifiers: %w", err)
	}
	for _, item := range identifiers {
		if bundle := bundles[item.BookID]; bundle != nil {
			bundle.identifiers = append(bundle.identifiers, item)
		}
	}
	authors, err := q.ListBookEnrichmentAuthors(ctx)
	if err != nil {
		return nil, fmt.Errorf("list enrichment authors: %w", err)
	}
	for _, author := range authors {
		if bundle := bundles[author.BookID]; bundle != nil {
			bundle.authors = append(bundle.authors, author)
		}
	}
	return bundles, nil
}

func mergeEnrichment(book BookReport, bundle *enrichmentBundle) EnrichedBook {
	enriched := EnrichedBook{
		BookReport:     book,
		ScannedTitle:   book.Title,
		ScannedAuthors: book.Authors,
	}
	if bundle == nil || bundle.info == nil {
		return enriched
	}
	info := bundle.info
	if info.Title != "" {
		enriched.Title = info.Title
	}
	if info.Description != "" {
		enriched.Description = info.Description
	}
	if info.Series != "" {
		enriched.Series = info.Series
	}
	if info.SeriesIndex != "" {
		enriched.SeriesIndex = info.SeriesIndex
	}
	enriched.Subtitle = info.Subtitle
	enriched.FirstPublishDate = info.FirstPublishDate
	if len(bundle.subjects) > 0 {
		enriched.Subjects = bundle.subjects
	}
	if len(bundle.languages) > 0 {
		enriched.Languages = bundle.languages
	}
	if len(bundle.publications) > 0 {
		enriched.Publications = make([]BookPublication, 0, len(bundle.publications))
		for _, publication := range bundle.publications {
			enriched.Publications = append(enriched.Publications, BookPublication{
				Publisher:       publication.Publisher,
				PublicationDate: publication.PublicationDate,
			})
		}
	}
	if len(bundle.identifiers) > 0 {
		enriched.Identifiers = mergeEnrichedIdentifiers(book.Identifiers, bundle.identifiers)
	}
	if len(bundle.authors) > 0 {
		names := make([]string, 0, len(bundle.authors))
		details := make([]AuthorInfo, 0, len(bundle.authors))
		for _, author := range bundle.authors {
			if author.Name != "" {
				names = append(names, author.Name)
			}
			details = append(details, AuthorInfo{
				Key:            author.AuthorKey,
				Name:           author.Name,
				AlternateNames: decodeJSONStrings(author.AlternateNames),
				BirthDate:      author.BirthDate,
				DeathDate:      author.DeathDate,
				Bio:            author.Bio,
			})
		}
		if len(names) > 0 {
			enriched.Authors = names
		}
		enriched.AuthorDetails = details
	}
	enriched.Enrichment = &EnrichmentInfo{
		Source:         info.Source,
		EditionKey:     info.EditionKey,
		WorkKey:        info.WorkKey,
		MatcherVersion: info.MatcherVersion,
		DumpVersion:    info.DumpVersion,
		EnrichedAt:     info.EnrichedAt,
	}
	return enriched
}

func mergeEnrichedIdentifiers(raw []BookIdentifier, enriched []db.BookEnrichmentIdentifier) []BookIdentifier {
	out := make([]BookIdentifier, 0, len(raw)+len(enriched))
	seen := make(map[string]struct{}, len(raw)+len(enriched))
	add := func(item BookIdentifier) {
		key := strings.ToUpper(item.Scheme) + "\x1f" + item.Value
		if _, ok := seen[key]; ok {
			return
		}
		seen[key] = struct{}{}
		out = append(out, item)
	}
	for _, item := range raw {
		add(item)
	}
	for _, item := range enriched {
		add(BookIdentifier{
			Scheme: item.Scheme,
			Value:  item.Value,
			Source: identifier.SourceOpenLibrary,
		})
	}
	return out
}

func matchInfo(match db.BookMatch) MatchInfo {
	return MatchInfo{
		Status:         match.Status,
		Confidence:     match.Confidence,
		ContentScore:   match.ContentScore,
		Method:         match.Method,
		EditionKey:     match.EditionKey,
		WorkKey:        match.WorkKey,
		ISBN:           match.Isbn,
		ISBNSource:     match.IsbnSource,
		ReviewReason:   match.ReviewReason,
		ScoreGap:       match.ScoreGap,
		CandidateCount: match.CandidateCount,
		WorkCount:      match.WorkCount,
		MatcherVersion: match.MatcherVersion,
		DumpVersion:    match.DumpVersion,
	}
}
