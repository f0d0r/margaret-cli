package matcher

import (
	"bufio"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
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

// StreamDataset runs the editions, authors and works passes over a dataset and
// returns the collected candidates.
func StreamDataset(ctx context.Context, dataset *openlibrary.Dataset, cache *Cache, opts StreamOptions) (*StreamResult, error) {
	if dataset == nil || !dataset.Usable() {
		return nil, errors.New("no usable OpenLibrary dataset")
	}
	opts = opts.withDefaults()
	if dataset.EditionsFromCombined() {
		return streamCombinedDataset(ctx, dataset, cache, opts)
	}

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

func streamCombinedDataset(ctx context.Context, dataset *openlibrary.Dataset, cache *Cache, opts StreamOptions) (*StreamResult, error) {
	result := &StreamResult{
		Candidates: map[int64][]Candidate{},
		Authors:    map[string]AuthorRecord{},
		Works:      map[string]WorkRecord{},
	}
	if cache == nil || len(cache.Books) == 0 {
		return result, nil
	}

	stats := &result.Stats
	candidates, sidecarPath, err := streamCombinedExtraction(ctx, dataset.Editions, cache, opts, stats)
	if err != nil {
		return nil, err
	}
	if sidecarPath != "" {
		defer func() {
			_ = os.Remove(sidecarPath)
		}()
	}
	if candidates.total() == 0 {
		return result, nil
	}

	authorKeys := candidates.authorKeys()
	if len(authorKeys) > 0 {
		authors, err := streamAuthorSidecar(ctx, sidecarPath, authorKeys, opts)
		if err != nil {
			return nil, err
		}
		result.Authors = authors
		applyAuthorScores(candidates, authors, cache)
	}
	candidates.sortAll()

	if dataset.Works != nil && dataset.Combined != nil && dataset.Works.Path != dataset.Combined.Path {
		workKeys := candidates.workKeys(opts.MaxWorksCandidatesPerBook)
		if len(workKeys) > 0 {
			works, err := streamWorks(ctx, dataset.Works, workKeys, opts, stats)
			if err != nil {
				return nil, err
			}
			result.Works = works
		}
	} else {
		result.Warnings = append(result.Warnings, "works metadata deferred for combined dump")
	}

	result.Candidates = candidates.values()
	result.Stats.Candidates = candidates.total()
	result.Stats.BooksWithCandidates = candidates.bookCount()
	return result, nil
}

type editionProcessor struct {
	cache      *Cache
	opts       StreamOptions
	stats      *StreamStats
	candidates *candidateSet
	remaining  map[int64]struct{}
}

func newEditionProcessor(cache *Cache, opts StreamOptions, stats *StreamStats) *editionProcessor {
	processor := &editionProcessor{
		cache:      cache,
		opts:       opts,
		stats:      stats,
		candidates: newCandidateSet(),
		remaining:  make(map[int64]struct{}, len(cache.Books)),
	}
	for id, book := range cache.Books {
		if book.TitleKey != "" || len(book.ISBNs) > 0 {
			processor.remaining[id] = struct{}{}
		}
	}
	return processor
}

func (p *editionProcessor) process(row openlibrary.Row) {
	p.stats.Editions++

	var record editionRecord
	if err := json.Unmarshal(row.Data, &record); err != nil {
		p.stats.Malformed++
		return
	}
	key := record.Key
	if key == "" {
		key = row.Key
	}
	if key == "" {
		p.stats.Malformed++
		return
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
		for _, bookID := range p.cache.ByISBN[isbn13] {
			if _, ok := p.remaining[bookID]; !ok {
				continue
			}
			book := p.cache.Books[bookID]
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
			p.candidates.add(Candidate{
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
			}, p.opts.MaxCandidatesPerBook)
			p.stats.ISBNMatches++
			if priority <= 1 && (len(book.TitleTokens) == 0 || titleScore >= p.opts.ISBNTitleConfirm) {
				delete(p.remaining, bookID)
			}
		}
	}
	for _, raw := range record.ISBN13 {
		handleISBN(raw)
	}
	for _, raw := range record.ISBN10 {
		handleISBN(raw)
	}

	if len(p.remaining) > 0 && titleKey != "" {
		matchedExact := false
		for _, bookID := range p.cache.ByTitle[titleKey] {
			if _, ok := p.remaining[bookID]; !ok {
				continue
			}
			matchedExact = true
			p.candidates.add(Candidate{
				BookID:       bookID,
				EditionKey:   key,
				WorkKey:      workKey,
				EditionTitle: title,
				AuthorKeys:   authorKeys,
				TitleScore:   1,
				Methods:      []string{"title_exact"},
			}, p.opts.MaxCandidatesPerBook)
			p.stats.TitleExactMatches++
		}
		if !matchedExact && len(titleTokens) > 0 {
			for _, bookID := range p.cache.RareTokenCandidates(titleTokens, p.opts.MaxPosting) {
				if _, ok := p.remaining[bookID]; !ok {
					continue
				}
				book := p.cache.Books[bookID]
				if book == nil {
					continue
				}
				titleScore := textmatch.Jaccard(titleTokens, book.TitleTokens)
				if titleScore < p.opts.MinTitleScore {
					continue
				}
				p.candidates.add(Candidate{
					BookID:       bookID,
					EditionKey:   key,
					WorkKey:      workKey,
					EditionTitle: title,
					AuthorKeys:   authorKeys,
					TitleScore:   titleScore,
					Methods:      []string{"title_fuzzy"},
				}, p.opts.MaxCandidatesPerBook)
				p.stats.TitleFuzzyMatches++
			}
		}
	}
}

func streamEditions(ctx context.Context, file *openlibrary.File, cache *Cache, opts StreamOptions, stats *StreamStats) (*candidateSet, error) {
	processor := newEditionProcessor(cache, opts, stats)
	if len(processor.remaining) == 0 {
		return processor.candidates, nil
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
		processor.process(row)
		if len(processor.remaining) == 0 {
			break
		}
		if opts.Progress != nil && time.Since(lastProgress) >= progressInterval {
			opts.Progress(StreamProgress{
				Pass:       "editions",
				Lines:      stats.Lines,
				Records:    stats.Editions,
				Candidates: processor.candidates.total(),
				Pending:    len(processor.remaining),
			})
			lastProgress = time.Now()
		}
	}
	if opts.Progress != nil {
		opts.Progress(StreamProgress{
			Pass:       "editions",
			Lines:      stats.Lines,
			Records:    stats.Editions,
			Candidates: processor.candidates.total(),
			Pending:    len(processor.remaining),
		})
	}
	return processor.candidates, nil
}

func streamCombinedExtraction(ctx context.Context, file *openlibrary.File, cache *Cache, opts StreamOptions, stats *StreamStats) (*candidateSet, string, error) {
	processor := newEditionProcessor(cache, opts, stats)
	if len(processor.remaining) == 0 {
		return processor.candidates, "", nil
	}

	sidecar, err := newAuthorSidecar(opts.TempDir)
	if err != nil {
		return nil, "", err
	}
	success := false
	defer func() {
		if success {
			return
		}
		_ = sidecar.close()
		_ = os.Remove(sidecar.path)
	}()

	reader, err := openlibrary.OpenDump(file)
	if err != nil {
		return nil, "", fmt.Errorf("open combined dump: %w", err)
	}
	defer func() {
		_ = reader.Close()
	}()
	scanner := openlibrary.NewDumpScanner(reader)
	lastProgress := time.Now()

	for {
		if err := ctx.Err(); err != nil {
			return nil, "", err
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
			return nil, "", err
		}
		stats.Lines++

		switch row.Type {
		case "/type/edition":
			if len(processor.remaining) > 0 {
				processor.process(row)
			}
		case "/type/author":
			var record authorRecord
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
			if err := sidecar.write(key, authorNames(record)); err != nil {
				return nil, "", fmt.Errorf("write author sidecar: %w", err)
			}
			stats.Authors++
		}

		if len(processor.remaining) == 0 && processor.candidates.total() == 0 {
			break
		}
		if opts.Progress != nil && time.Since(lastProgress) >= progressInterval {
			opts.Progress(StreamProgress{
				Pass:       "combined",
				Lines:      stats.Lines,
				Records:    stats.Editions,
				Candidates: processor.candidates.total(),
				Pending:    len(processor.remaining),
			})
			lastProgress = time.Now()
		}
	}
	if opts.Progress != nil {
		opts.Progress(StreamProgress{
			Pass:       "combined",
			Lines:      stats.Lines,
			Records:    stats.Editions,
			Candidates: processor.candidates.total(),
			Pending:    len(processor.remaining),
		})
	}
	if err := sidecar.close(); err != nil {
		return nil, "", fmt.Errorf("close author sidecar: %w", err)
	}
	success = true
	return processor.candidates, sidecar.path, nil
}

type authorSidecar struct {
	path   string
	file   *os.File
	gzip   *gzip.Writer
	buffer *bufio.Writer
	closed bool
}

func newAuthorSidecar(dir string) (*authorSidecar, error) {
	file, err := os.CreateTemp(dir, "margaret-authors-*.tsv.gz")
	if err != nil {
		return nil, fmt.Errorf("create author sidecar: %w", err)
	}
	writer := gzip.NewWriter(file)
	return &authorSidecar{
		path:   file.Name(),
		file:   file,
		gzip:   writer,
		buffer: bufio.NewWriter(writer),
	}, nil
}

func (s *authorSidecar) write(key string, names []string) error {
	if s.closed {
		return errors.New("author sidecar is closed")
	}
	var line strings.Builder
	line.WriteString(sanitizeSidecar(key))
	line.WriteByte('\t')
	for i, name := range names {
		if i > 0 {
			line.WriteByte(0x1f)
		}
		line.WriteString(sanitizeSidecar(name))
	}
	line.WriteByte('\n')
	_, err := s.buffer.WriteString(line.String())
	return err
}

func (s *authorSidecar) close() error {
	if s.closed {
		return nil
	}
	s.closed = true
	var err error
	if flushErr := s.buffer.Flush(); flushErr != nil {
		err = flushErr
	}
	if gzipErr := s.gzip.Close(); err == nil {
		err = gzipErr
	}
	if fileErr := s.file.Close(); err == nil {
		err = fileErr
	}
	return err
}

func streamAuthorSidecar(ctx context.Context, path string, keys map[string]struct{}, opts StreamOptions) (map[string]AuthorRecord, error) {
	found := make(map[string]AuthorRecord, len(keys))
	if path == "" || len(keys) == 0 {
		return found, nil
	}

	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open author sidecar: %w", err)
	}
	defer func() {
		_ = file.Close()
	}()
	reader, err := gzip.NewReader(file)
	if err != nil {
		return nil, fmt.Errorf("open gzip author sidecar: %w", err)
	}
	defer func() {
		_ = reader.Close()
	}()

	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 64*1024), 1<<20)
	lastProgress := time.Now()
	var lines int64

	for scanner.Scan() {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		lines++
		key, names, ok := strings.Cut(scanner.Text(), "\t")
		if !ok {
			continue
		}
		if _, needed := keys[key]; !needed {
			continue
		}
		found[key] = AuthorRecord{Key: key, Names: splitSidecarNames(names)}
		if len(found) == len(keys) {
			break
		}
		if opts.Progress != nil && time.Since(lastProgress) >= progressInterval {
			opts.Progress(StreamProgress{
				Pass:    "authors",
				Lines:   lines,
				Records: int64(len(found)),
			})
			lastProgress = time.Now()
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read author sidecar: %w", err)
	}
	return found, nil
}

func sanitizeSidecar(value string) string {
	return strings.Map(func(r rune) rune {
		switch r {
		case '\t', '\n', '\r', 0x1f:
			return ' '
		default:
			return r
		}
	}, value)
}

func splitSidecarNames(value string) []string {
	if value == "" {
		return nil
	}
	return strings.Split(value, string(rune(0x1f)))
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
