package enrich

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/f0d0r/margaret-cli/internal/db"
	"github.com/f0d0r/margaret-cli/internal/openlibrary"
	"github.com/f0d0r/margaret-cli/internal/tx"
)

// Options configures the enrichment pass.
type Options struct {
	MatcherVersion string
	DumpVersion    string
	Force          bool
	Now            time.Time
	BatchSize      int
}

// Result summarizes an enrichment run.
type Result struct {
	Targets         int
	Enriched        int
	MissingEditions int
	MissingWorks    int
	MissingAuthors  int
}

// Enrich applies OpenLibrary metadata to confirmed books. Raw scan metadata
// is left untouched; enriched values go to the book_enrichment tables.
func Enrich(ctx context.Context, dataset *openlibrary.Dataset, conn *sql.DB, q *db.Queries, opts Options) (*Result, error) {
	if dataset == nil || !dataset.Usable() {
		return nil, errors.New("no usable OpenLibrary dataset")
	}
	if conn == nil {
		return nil, errors.New("nil database")
	}
	if q == nil {
		return nil, errors.New("nil queries")
	}
	if opts.Now.IsZero() {
		opts.Now = time.Now()
	}
	if opts.BatchSize <= 0 {
		opts.BatchSize = 500
	}
	if err := cleanupNonConfirmed(ctx, conn, q); err != nil {
		return nil, err
	}
	force := int64(0)
	if opts.Force {
		force = 1
	}

	targets, err := q.ListBookEnrichmentTargets(ctx, db.ListBookEnrichmentTargetsParams{
		Force:          force,
		MatcherVersion: opts.MatcherVersion,
		DumpVersion:    opts.DumpVersion,
	})
	if err != nil {
		return nil, fmt.Errorf("list enrichment targets: %w", err)
	}
	result := &Result{Targets: len(targets)}
	if len(targets) == 0 {
		return result, nil
	}

	editionKeys, workKeys, authorKeys := targetKeys(targets)
	var editions map[string]*editionRecord
	var works map[string]*workRecord
	var authors map[string]*authorRecord

	if dataset.EditionsFromCombined() {
		editions, works, authors, err = scanCombined(ctx, dataset.Editions, editionKeys, workKeys, authorKeys)
		if err != nil {
			return nil, err
		}
		extra := authorKeysFromEditions(editions, authorKeys)
		for key := range authorKeysFromWorks(works, authorKeys) {
			extra[key] = struct{}{}
		}
		if len(extra) > 0 {
			more, err := scanAuthors(ctx, dataset.Editions, extra)
			if err != nil {
				return nil, err
			}
			for key, record := range more {
				authors[key] = record
			}
		}
	} else {
		editions, err = scanEditions(ctx, dataset.Editions, editionKeys)
		if err != nil {
			return nil, err
		}
		if dataset.Works != nil {
			works, err = scanWorks(ctx, dataset.Works, workKeys)
			if err != nil {
				return nil, err
			}
		}
		allAuthors := copySet(authorKeys)
		for key := range authorKeysFromEditions(editions, allAuthors) {
			allAuthors[key] = struct{}{}
		}
		for key := range authorKeysFromWorks(works, allAuthors) {
			allAuthors[key] = struct{}{}
		}
		if dataset.Authors != nil && len(allAuthors) > 0 {
			authors, err = scanAuthors(ctx, dataset.Authors, allAuthors)
			if err != nil {
				return nil, err
			}
		}
		authorKeys = allAuthors
	}
	for key := range authorKeys {
		if _, ok := authors[key]; !ok {
			result.MissingAuthors++
		}
	}
	if editions == nil {
		editions = map[string]*editionRecord{}
	}
	if works == nil {
		works = map[string]*workRecord{}
	}

	enrichments := make([]*bookEnrichment, 0, len(targets))
	for _, target := range targets {
		edition := editions[target.EditionKey]
		if edition == nil {
			result.MissingEditions++
			continue
		}
		work := works[target.WorkKey]
		if work == nil {
			result.MissingWorks++
		}
		enrichment := buildEnrichment(
			target.BookID, edition, work, authors,
			opts.MatcherVersion, opts.DumpVersion, opts.Now.Unix(),
		)
		if enrichment.WorkKey == "" {
			enrichment.WorkKey = target.WorkKey
		}
		enrichments = append(enrichments, enrichment)
	}
	if err := persist(ctx, conn, q, enrichments, opts.BatchSize); err != nil {
		return nil, err
	}
	result.Enriched = len(enrichments)
	return result, nil
}

func cleanupNonConfirmed(ctx context.Context, conn *sql.DB, q *db.Queries) error {
	err := tx.WithTx(ctx, conn, q, func(txCtx context.Context) error {
		txq := tx.QueryFrom(txCtx, q)
		if err := txq.DeleteNonConfirmedEnrichmentSubjects(txCtx); err != nil {
			return err
		}
		if err := txq.DeleteNonConfirmedEnrichmentLanguages(txCtx); err != nil {
			return err
		}
		if err := txq.DeleteNonConfirmedEnrichmentPublications(txCtx); err != nil {
			return err
		}
		if err := txq.DeleteNonConfirmedEnrichmentIdentifiers(txCtx); err != nil {
			return err
		}
		if err := txq.DeleteNonConfirmedEnrichmentAuthors(txCtx); err != nil {
			return err
		}
		return txq.DeleteNonConfirmedEnrichment(txCtx)
	})
	if err != nil {
		return fmt.Errorf("cleanup non-confirmed enrichment: %w", err)
	}
	return nil
}

func targetKeys(targets []db.ListBookEnrichmentTargetsRow) (map[string]struct{}, map[string]struct{}, map[string]struct{}) {
	editionKeys := make(map[string]struct{})
	workKeys := make(map[string]struct{})
	authorKeys := make(map[string]struct{})
	for _, target := range targets {
		if target.EditionKey != "" {
			editionKeys[target.EditionKey] = struct{}{}
		}
		if target.WorkKey != "" {
			workKeys[target.WorkKey] = struct{}{}
		}
		for _, key := range decodeStringList(target.AuthorKeys) {
			authorKeys[key] = struct{}{}
		}
	}
	return editionKeys, workKeys, authorKeys
}

func scanEditions(ctx context.Context, file *openlibrary.File, keys map[string]struct{}) (map[string]*editionRecord, error) {
	out := make(map[string]*editionRecord, len(keys))
	if len(keys) == 0 {
		return out, nil
	}
	err := scanFile(ctx, file, func(row openlibrary.Row) bool {
		if row.Type != "/type/edition" {
			return false
		}
		var record editionRecord
		if json.Unmarshal(row.Data, &record) != nil {
			return false
		}
		key := record.Key
		if key == "" {
			key = row.Key
		}
		if _, ok := keys[key]; !ok {
			return false
		}
		record.Key = key
		out[key] = &record
		return len(out) == len(keys)
	})
	if err != nil {
		return nil, fmt.Errorf("scan editions: %w", err)
	}
	return out, nil
}

func scanWorks(ctx context.Context, file *openlibrary.File, keys map[string]struct{}) (map[string]*workRecord, error) {
	out := make(map[string]*workRecord, len(keys))
	if len(keys) == 0 {
		return out, nil
	}
	err := scanFile(ctx, file, func(row openlibrary.Row) bool {
		if row.Type != "/type/work" {
			return false
		}
		var record workRecord
		if json.Unmarshal(row.Data, &record) != nil {
			return false
		}
		key := record.Key
		if key == "" {
			key = row.Key
		}
		if _, ok := keys[key]; !ok {
			return false
		}
		record.Key = key
		out[key] = &record
		return len(out) == len(keys)
	})
	if err != nil {
		return nil, fmt.Errorf("scan works: %w", err)
	}
	return out, nil
}

func scanAuthors(ctx context.Context, file *openlibrary.File, keys map[string]struct{}) (map[string]*authorRecord, error) {
	out := make(map[string]*authorRecord, len(keys))
	if len(keys) == 0 {
		return out, nil
	}
	err := scanFile(ctx, file, func(row openlibrary.Row) bool {
		if row.Type != "/type/author" {
			return false
		}
		var record authorRecord
		if json.Unmarshal(row.Data, &record) != nil {
			return false
		}
		key := record.Key
		if key == "" {
			key = row.Key
		}
		if _, ok := keys[key]; !ok {
			return false
		}
		record.Key = key
		out[key] = &record
		return len(out) == len(keys)
	})
	if err != nil {
		return nil, fmt.Errorf("scan authors: %w", err)
	}
	return out, nil
}

func scanCombined(ctx context.Context, file *openlibrary.File, editionKeys, workKeys, authorKeys map[string]struct{}) (map[string]*editionRecord, map[string]*workRecord, map[string]*authorRecord, error) {
	editions := make(map[string]*editionRecord, len(editionKeys))
	works := make(map[string]*workRecord, len(workKeys))
	authors := make(map[string]*authorRecord, len(authorKeys))
	err := scanFile(ctx, file, func(row openlibrary.Row) bool {
		switch row.Type {
		case "/type/edition":
			var record editionRecord
			if json.Unmarshal(row.Data, &record) != nil {
				return false
			}
			key := record.Key
			if key == "" {
				key = row.Key
			}
			if _, ok := editionKeys[key]; ok {
				record.Key = key
				editions[key] = &record
			}
		case "/type/work":
			var record workRecord
			if json.Unmarshal(row.Data, &record) != nil {
				return false
			}
			key := record.Key
			if key == "" {
				key = row.Key
			}
			if _, ok := workKeys[key]; ok {
				record.Key = key
				works[key] = &record
			}
		case "/type/author":
			var record authorRecord
			if json.Unmarshal(row.Data, &record) != nil {
				return false
			}
			key := record.Key
			if key == "" {
				key = row.Key
			}
			if _, ok := authorKeys[key]; ok {
				record.Key = key
				authors[key] = &record
			}
		}
		return len(editions) == len(editionKeys) &&
			len(works) == len(workKeys) &&
			len(authors) == len(authorKeys)
	})
	if err != nil {
		return nil, nil, nil, fmt.Errorf("scan combined dump: %w", err)
	}
	return editions, works, authors, nil
}

func scanFile(ctx context.Context, file *openlibrary.File, handle func(openlibrary.Row) bool) error {
	reader, err := openlibrary.OpenDump(file)
	if err != nil {
		return err
	}
	defer func() {
		_ = reader.Close()
	}()
	scanner := openlibrary.NewDumpScanner(reader)
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		row, err := openlibrary.ReadRow(scanner)
		if errors.Is(err, io.EOF) {
			return nil
		}
		if errors.Is(err, openlibrary.ErrMalformedRow) {
			continue
		}
		if err != nil {
			return err
		}
		if handle(row) {
			return nil
		}
	}
}

func authorKeysFromEditions(editions map[string]*editionRecord, known map[string]struct{}) map[string]struct{} {
	extra := make(map[string]struct{})
	for _, edition := range editions {
		for _, author := range edition.Authors {
			key := author.authorKey()
			if key == "" {
				continue
			}
			if _, ok := known[key]; !ok {
				extra[key] = struct{}{}
			}
		}
	}
	return extra
}

func authorKeysFromWorks(works map[string]*workRecord, known map[string]struct{}) map[string]struct{} {
	extra := make(map[string]struct{})
	for _, work := range works {
		for _, author := range work.Authors {
			key := author.authorKey()
			if key == "" {
				continue
			}
			if _, ok := known[key]; !ok {
				extra[key] = struct{}{}
			}
		}
	}
	return extra
}

func copySet(values map[string]struct{}) map[string]struct{} {
	out := make(map[string]struct{}, len(values))
	for value := range values {
		out[value] = struct{}{}
	}
	return out
}

func decodeStringList(raw string) []string {
	if raw == "" {
		return nil
	}
	var values []string
	if err := json.Unmarshal([]byte(raw), &values); err != nil {
		return nil
	}
	return values
}

func persist(ctx context.Context, conn *sql.DB, q *db.Queries, enrichments []*bookEnrichment, batchSize int) error {
	for start := 0; start < len(enrichments); start += batchSize {
		end := start + batchSize
		if end > len(enrichments) {
			end = len(enrichments)
		}
		batch := enrichments[start:end]
		err := tx.WithTx(ctx, conn, q, func(txCtx context.Context) error {
			txq := tx.QueryFrom(txCtx, q)
			for _, enrichment := range batch {
				if err := writeEnrichment(txCtx, txq, enrichment); err != nil {
					return err
				}
			}
			return nil
		})
		if err != nil {
			return fmt.Errorf("persist enrichment: %w", err)
		}
	}
	return nil
}

func writeEnrichment(ctx context.Context, q *db.Queries, enrichment *bookEnrichment) error {
	bookID := enrichment.BookID
	if err := q.DeleteBookEnrichmentSubjectsByBookID(ctx, bookID); err != nil {
		return fmt.Errorf("delete enrichment subjects %d: %w", bookID, err)
	}
	if err := q.DeleteBookEnrichmentLanguagesByBookID(ctx, bookID); err != nil {
		return fmt.Errorf("delete enrichment languages %d: %w", bookID, err)
	}
	if err := q.DeleteBookEnrichmentPublicationsByBookID(ctx, bookID); err != nil {
		return fmt.Errorf("delete enrichment publications %d: %w", bookID, err)
	}
	if err := q.DeleteBookEnrichmentIdentifiersByBookID(ctx, bookID); err != nil {
		return fmt.Errorf("delete enrichment identifiers %d: %w", bookID, err)
	}
	if err := q.DeleteBookEnrichmentAuthorsByBookID(ctx, bookID); err != nil {
		return fmt.Errorf("delete enrichment authors %d: %w", bookID, err)
	}
	if err := q.DeleteBookEnrichmentByBookID(ctx, bookID); err != nil {
		return fmt.Errorf("delete enrichment %d: %w", bookID, err)
	}
	if err := q.UpsertBookEnrichment(ctx, db.UpsertBookEnrichmentParams{
		BookID:           bookID,
		EditionKey:       enrichment.EditionKey,
		WorkKey:          enrichment.WorkKey,
		Source:           enrichment.Source,
		Title:            enrichment.Title,
		Subtitle:         enrichment.Subtitle,
		Description:      enrichment.Description,
		Series:           enrichment.Series,
		SeriesIndex:      enrichment.SeriesIndex,
		FirstPublishDate: enrichment.FirstPublishDate,
		MatcherVersion:   enrichment.MatcherVersion,
		DumpVersion:      enrichment.DumpVersion,
		EnrichedAt:       enrichment.EnrichedAt,
	}); err != nil {
		return fmt.Errorf("upsert enrichment %d: %w", bookID, err)
	}
	for i, subject := range enrichment.Subjects {
		if err := q.CreateBookEnrichmentSubject(ctx, db.CreateBookEnrichmentSubjectParams{
			BookID:  bookID,
			Name:    subject,
			Ordinal: int64(i),
		}); err != nil {
			return fmt.Errorf("create enrichment subject %d: %w", bookID, err)
		}
	}
	for i, language := range enrichment.Languages {
		if err := q.CreateBookEnrichmentLanguage(ctx, db.CreateBookEnrichmentLanguageParams{
			BookID:  bookID,
			Name:    language,
			Ordinal: int64(i),
		}); err != nil {
			return fmt.Errorf("create enrichment language %d: %w", bookID, err)
		}
	}
	for i, publication := range enrichment.Publications {
		if err := q.CreateBookEnrichmentPublication(ctx, db.CreateBookEnrichmentPublicationParams{
			BookID:          bookID,
			Publisher:       publication.Publisher,
			PublicationDate: publication.PublicationDate,
			Ordinal:         int64(i),
		}); err != nil {
			return fmt.Errorf("create enrichment publication %d: %w", bookID, err)
		}
	}
	for i, identifier := range enrichment.Identifiers {
		if err := q.CreateBookEnrichmentIdentifier(ctx, db.CreateBookEnrichmentIdentifierParams{
			BookID:  bookID,
			Scheme:  identifier.Scheme,
			Value:   identifier.Value,
			Source:  identifier.Source,
			Ordinal: int64(i),
		}); err != nil {
			return fmt.Errorf("create enrichment identifier %d: %w", bookID, err)
		}
	}
	for i, author := range enrichment.Authors {
		alternate, err := jsonStringList(author.AlternateNames)
		if err != nil {
			return fmt.Errorf("encode alternate names %d: %w", bookID, err)
		}
		if err := q.CreateBookEnrichmentAuthor(ctx, db.CreateBookEnrichmentAuthorParams{
			BookID:         bookID,
			AuthorKey:      author.Key,
			Name:           author.Name,
			AlternateNames: alternate,
			BirthDate:      author.BirthDate,
			DeathDate:      author.DeathDate,
			Bio:            author.Bio,
			Ordinal:        int64(i),
		}); err != nil {
			return fmt.Errorf("create enrichment author %d: %w", bookID, err)
		}
	}
	return nil
}

func jsonStringList(values []string) (string, error) {
	if len(values) == 0 {
		return "[]", nil
	}
	data, err := json.Marshal(values)
	if err != nil {
		return "", err
	}
	return string(data), nil
}
