package matcher

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/f0d0r/margaret-cli/internal/identifier"
	"github.com/f0d0r/margaret-cli/internal/openlibrary"
	"github.com/f0d0r/margaret-cli/internal/textmatch"
	"github.com/f0d0r/margaret-ebook-library/tools"
)

const progressInterval = 250 * time.Millisecond

type linkedKey struct {
	Key string `json:"key"`
}

type editionRecord struct {
	Key      string      `json:"key"`
	Title    string      `json:"title"`
	Subtitle string      `json:"subtitle"`
	ISBN10   []string    `json:"isbn_10"`
	ISBN13   []string    `json:"isbn_13"`
	Authors  []linkedKey `json:"authors"`
	Works    []linkedKey `json:"works"`
}

type authorRecord struct {
	Key            string   `json:"key"`
	Name           string   `json:"name"`
	PersonalName   string   `json:"personal_name"`
	AlternateNames []string `json:"alternate_names"`
}

type workRecord struct {
	Key              string          `json:"key"`
	Title            string          `json:"title"`
	Description      json.RawMessage `json:"description"`
	Subjects         []string        `json:"subjects"`
	FirstPublishDate string          `json:"first_publish_date"`
}

// StreamDataset runs the editions, authors and works passes over a family-file
// dataset and returns the collected candidates.
func StreamDataset(ctx context.Context, dataset *openlibrary.Dataset, cache *Cache, opts StreamOptions) (*StreamResult, error) {
	if dataset == nil || !dataset.Usable() {
		return nil, errors.New("no usable OpenLibrary dataset")
	}
	if dataset.UsesCombined() {
		return nil, errors.New("combined OpenLibrary dumps are not supported yet; download editions, works and authors separately")
	}
	opts = opts.withDefaults()
	result := &StreamResult{
		Candidates: map[int64][]Candidate{},
		Authors:    map[string]AuthorRecord{},
		Works:      map[string]WorkRecord{},
	}
	if cache == nil || len(cache.Books) == 0 {
		return result, nil
	}

	stats := &result.Stats
	candidates, err := streamEditions(ctx, dataset.Editions, cache, opts, stats)
	if err != nil {
		return nil, err
	}
	if candidates.total() == 0 {
		return result, nil
	}

	if dataset.Authors == nil {
		result.Warnings = append(result.Warnings, "authors dump missing; author scores are unavailable")
	} else {
		authorKeys := candidates.authorKeys()
		if len(authorKeys) > 0 {
			authors, err := streamAuthors(ctx, dataset.Authors, authorKeys, opts, stats)
			if err != nil {
				return nil, err
			}
			result.Authors = authors
			applyAuthorScores(candidates, authors, cache)
		}
	}
	candidates.sortAll()

	if dataset.Works == nil {
		result.Warnings = append(result.Warnings, "works dump missing; work metadata is unavailable")
	} else {
		workKeys := candidates.workKeys(opts.MaxWorksCandidatesPerBook)
		if len(workKeys) > 0 {
			works, err := streamWorks(ctx, dataset.Works, workKeys, opts, stats)
			if err != nil {
				return nil, err
			}
			result.Works = works
		}
	}

	result.Candidates = candidates.values()
	result.Stats.Candidates = candidates.total()
	result.Stats.BooksWithCandidates = candidates.bookCount()
	return result, nil
}

func streamEditions(ctx context.Context, file *openlibrary.File, cache *Cache, opts StreamOptions, stats *StreamStats) (*candidateSet, error) {
	candidates := newCandidateSet()
	remaining := make(map[int64]struct{}, len(cache.Books))
	for id, book := range cache.Books {
		if book.TitleKey != "" || len(book.ISBNs) > 0 {
			remaining[id] = struct{}{}
		}
	}
	if len(remaining) == 0 {
		return candidates, nil
	}

	reader, err := openlibrary.OpenDump(file)
	if err != nil {
		return nil, fmt.Errorf("open editions dump: %w", err)
	}
	defer func() {
		_ = reader.Close()
	}()
	scanner := openlibrary.NewDumpScanner(reader)
	lastProgress := time.Now()

	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		row, err := openlibrary.ReadRow(scanner)
		if errors.Is(err, io.EOF) {
			break
		}
		if errors.Is(err, openlibrary.ErrMalformedRow) {
			stats.Malformed++
			continue
		}
		if err != nil {
			return nil, err
		}
		stats.Lines++
		if row.Type != "/type/edition" {
			continue
		}
		stats.Editions++

		var record editionRecord
		if err := json.Unmarshal(row.Data, &record); err != nil {
			stats.Malformed++
			continue
		}
		key := record.Key
		if key == "" {
			key = row.Key
		}
		if key == "" {
			stats.Malformed++
			continue
		}
		title := record.Title
		if title == "" {
			title = record.Subtitle
		}
		titleKey := ""
		var titleTokens []string
		if title != "" {
			titleKey = textmatch.TitleKey(title)
			titleTokens = textmatch.TitleTokens(title)
		}
		workKey := ""
		if len(record.Works) > 0 {
			workKey = record.Works[0].Key
		}
		authorKeys := uniqueKeys(record.Authors)

		handleISBN := func(raw string) {
			isbn13, ok := normalizeISBN(raw)
			if !ok {
				return
			}
			for _, bookID := range cache.ByISBN[isbn13] {
				if _, ok := remaining[bookID]; !ok {
					continue
				}
				book := cache.Books[bookID]
				if book == nil {
					continue
				}
				source, priority, ok := findBookISBN(book, isbn13)
				if !ok {
					continue
				}
				titleScore := 0.0
				if len(titleTokens) > 0 && len(book.TitleTokens) > 0 {
					titleScore = textmatch.Jaccard(titleTokens, book.TitleTokens)
				}
				candidates.add(Candidate{
					BookID:       bookID,
					EditionKey:   key,
					WorkKey:      workKey,
					EditionTitle: title,
					AuthorKeys:   authorKeys,
					ISBN:         isbn13,
					ISBNSource:   source,
					ISBNPriority: priority,
					TitleScore:   titleScore,
					Methods:      []string{"isbn"},
				}, opts.MaxCandidatesPerBook)
				stats.ISBNMatches++
				if priority <= 1 && (len(book.TitleTokens) == 0 || titleScore >= opts.ISBNTitleConfirm) {
					delete(remaining, bookID)
				}
			}
		}
		for _, raw := range record.ISBN13 {
			handleISBN(raw)
		}
		for _, raw := range record.ISBN10 {
			handleISBN(raw)
		}

		if len(remaining) > 0 && titleKey != "" {
			matchedExact := false
			for _, bookID := range cache.ByTitle[titleKey] {
				if _, ok := remaining[bookID]; !ok {
					continue
				}
				matchedExact = true
				candidates.add(Candidate{
					BookID:       bookID,
					EditionKey:   key,
					WorkKey:      workKey,
					EditionTitle: title,
					AuthorKeys:   authorKeys,
					TitleScore:   1,
					Methods:      []string{"title_exact"},
				}, opts.MaxCandidatesPerBook)
				stats.TitleExactMatches++
			}
			if !matchedExact && len(titleTokens) > 0 {
				for _, bookID := range cache.RareTokenCandidates(titleTokens, opts.MaxPosting) {
					if _, ok := remaining[bookID]; !ok {
						continue
					}
					book := cache.Books[bookID]
					if book == nil {
						continue
					}
					titleScore := textmatch.Jaccard(titleTokens, book.TitleTokens)
					if titleScore < opts.MinTitleScore {
						continue
					}
					candidates.add(Candidate{
						BookID:       bookID,
						EditionKey:   key,
						WorkKey:      workKey,
						EditionTitle: title,
						AuthorKeys:   authorKeys,
						TitleScore:   titleScore,
						Methods:      []string{"title_fuzzy"},
					}, opts.MaxCandidatesPerBook)
					stats.TitleFuzzyMatches++
				}
			}
		}

		if len(remaining) == 0 {
			break
		}
		if opts.Progress != nil && time.Since(lastProgress) >= progressInterval {
			opts.Progress(StreamProgress{
				Pass:       "editions",
				Lines:      stats.Lines,
				Records:    stats.Editions,
				Candidates: candidates.total(),
				Pending:    len(remaining),
			})
			lastProgress = time.Now()
		}
	}
	if opts.Progress != nil {
		opts.Progress(StreamProgress{
			Pass:       "editions",
			Lines:      stats.Lines,
			Records:    stats.Editions,
			Candidates: candidates.total(),
			Pending:    len(remaining),
		})
	}
	return candidates, nil
}

func streamAuthors(ctx context.Context, file *openlibrary.File, keys map[string]struct{}, opts StreamOptions, stats *StreamStats) (map[string]AuthorRecord, error) {
	found := make(map[string]AuthorRecord, len(keys))
	if len(keys) == 0 {
		return found, nil
	}
	reader, err := openlibrary.OpenDump(file)
	if err != nil {
		return nil, fmt.Errorf("open authors dump: %w", err)
	}
	defer func() {
		_ = reader.Close()
	}()
	scanner := openlibrary.NewDumpScanner(reader)
	lastProgress := time.Now()

	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		row, err := openlibrary.ReadRow(scanner)
		if errors.Is(err, io.EOF) {
			break
		}
		if errors.Is(err, openlibrary.ErrMalformedRow) {
			stats.Malformed++
			continue
		}
		if err != nil {
			return nil, err
		}
		stats.Lines++
		if row.Type != "/type/author" {
			continue
		}
		stats.Authors++

		var record authorRecord
		if err := json.Unmarshal(row.Data, &record); err != nil {
			stats.Malformed++
			continue
		}
		key := record.Key
		if key == "" {
			key = row.Key
		}
		if _, ok := keys[key]; !ok {
			continue
		}
		found[key] = AuthorRecord{Key: key, Names: authorNames(record)}
		if len(found) == len(keys) {
			break
		}
		if opts.Progress != nil && time.Since(lastProgress) >= progressInterval {
			opts.Progress(StreamProgress{
				Pass:    "authors",
				Lines:   stats.Lines,
				Records: stats.Authors,
			})
			lastProgress = time.Now()
		}
	}
	return found, nil
}

func streamWorks(ctx context.Context, file *openlibrary.File, keys map[string]struct{}, opts StreamOptions, stats *StreamStats) (map[string]WorkRecord, error) {
	found := make(map[string]WorkRecord, len(keys))
	if len(keys) == 0 {
		return found, nil
	}
	reader, err := openlibrary.OpenDump(file)
	if err != nil {
		return nil, fmt.Errorf("open works dump: %w", err)
	}
	defer func() {
		_ = reader.Close()
	}()
	scanner := openlibrary.NewDumpScanner(reader)
	lastProgress := time.Now()

	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		row, err := openlibrary.ReadRow(scanner)
		if errors.Is(err, io.EOF) {
			break
		}
		if errors.Is(err, openlibrary.ErrMalformedRow) {
			stats.Malformed++
			continue
		}
		if err != nil {
			return nil, err
		}
		stats.Lines++
		if row.Type != "/type/work" {
			continue
		}
		stats.Works++

		var record workRecord
		if err := json.Unmarshal(row.Data, &record); err != nil {
			stats.Malformed++
			continue
		}
		key := record.Key
		if key == "" {
			key = row.Key
		}
		if _, ok := keys[key]; !ok {
			continue
		}
		found[key] = WorkRecord{
			Key:              key,
			Title:            record.Title,
			Description:      descriptionText(record.Description),
			Subjects:         record.Subjects,
			FirstPublishDate: record.FirstPublishDate,
		}
		if len(found) == len(keys) {
			break
		}
		if opts.Progress != nil && time.Since(lastProgress) >= progressInterval {
			opts.Progress(StreamProgress{
				Pass:    "works",
				Lines:   stats.Lines,
				Records: stats.Works,
			})
			lastProgress = time.Now()
		}
	}
	return found, nil
}

func applyAuthorScores(candidates *candidateSet, authors map[string]AuthorRecord, cache *Cache) {
	for bookID, list := range candidates.byBook {
		book := cache.Books[bookID]
		if book == nil {
			continue
		}
		for _, candidate := range list {
			var names []string
			best := 0.0
			for _, key := range candidate.AuthorKeys {
				record, ok := authors[key]
				if !ok {
					continue
				}
				for _, name := range record.Names {
					names = append(names, name)
					tokens := textmatch.AuthorTokens(name)
					if len(tokens) == 0 {
						continue
					}
					for _, bookTokens := range book.AuthorTokens {
						if score := textmatch.Jaccard(tokens, bookTokens); score > best {
							best = score
						}
					}
				}
			}
			candidate.AuthorNames = uniqueStrings(names)
			candidate.AuthorScore = best
			candidate.Score = candidate.computeScore()
		}
	}
}

func normalizeISBN(raw string) (string, bool) {
	isbn, ok := tools.ValidateBookISBN(raw)
	if !ok {
		return "", false
	}
	isbn13, ok := isbn.ToISBN13()
	if !ok {
		return "", false
	}
	return isbn13.String(), true
}

func findBookISBN(book *Book, value string) (identifier.Source, int, bool) {
	for _, isbn := range book.ISBNs {
		if isbn.Value == value {
			return isbn.Source, isbn.Priority, true
		}
	}
	return "", 0, false
}

func uniqueKeys(keys []linkedKey) []string {
	out := make([]string, 0, len(keys))
	seen := make(map[string]struct{}, len(keys))
	for _, item := range keys {
		if item.Key == "" {
			continue
		}
		if _, ok := seen[item.Key]; ok {
			continue
		}
		seen[item.Key] = struct{}{}
		out = append(out, item.Key)
	}
	return out
}

func authorNames(record authorRecord) []string {
	names := make([]string, 0, 1+len(record.AlternateNames))
	if record.Name != "" {
		names = append(names, record.Name)
	}
	if record.PersonalName != "" {
		names = append(names, record.PersonalName)
	}
	names = append(names, record.AlternateNames...)
	return uniqueStrings(names)
}

func uniqueStrings(values []string) []string {
	out := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}

func descriptionText(raw json.RawMessage) string {
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
