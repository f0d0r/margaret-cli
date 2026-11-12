package report

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"

	"github.com/f0d0r/margaret-cli/internal/claim"
	"github.com/f0d0r/margaret-cli/internal/db"
	"github.com/f0d0r/margaret-cli/internal/filemeta"
	"github.com/f0d0r/margaret-cli/internal/identifier"
	"github.com/f0d0r/margaret-cli/internal/tx"
)

// FileLegacy is the pre-claim title and author snapshot of a file.
type FileLegacy struct {
	Title   string   `json:"title"`
	Authors []string `json:"authors"`
}

// BookFileClaim is the JSON representation of one title or author claim.
type BookFileClaim struct {
	Value               string   `json:"value"`
	Source              string   `json:"source"`
	Set                 string   `json:"set"`
	Origin              string   `json:"origin"`
	Trust               string   `json:"trust"`
	Witness             string   `json:"witness"`
	Junk                bool     `json:"junk"`
	Attested            bool     `json:"attested"`
	Attestation         float64  `json:"attestation"`
	AttestationMethod   string   `json:"attestation_method"`
	AttestationLevel    string   `json:"attestation_level,omitempty"`
	AttestationResidual []string `json:"attestation_residual,omitempty"`
	Suspicious          bool     `json:"suspicious,omitempty"`
	SuspiciousSignals   int64    `json:"suspicious_signals,omitempty"`
}

// BookFileClaims groups the title and author claims of a file.
type BookFileClaims struct {
	Title  []BookFileClaim `json:"title"`
	Author []BookFileClaim `json:"author"`
}

// BookFileReport is the JSON representation of one file belonging to a book.
type BookFileReport struct {
	Path         string           `json:"path"`
	Authors      []string         `json:"authors"`
	Title        string           `json:"title"`
	Legacy       *FileLegacy      `json:"legacy,omitempty"`
	TitleStatus  string           `json:"title_status"`
	AuthorStatus string           `json:"author_status"`
	TitleSet     string           `json:"title_set"`
	AuthorSet    string           `json:"author_set"`
	Claims       *BookFileClaims  `json:"claims,omitempty"`
	Identifiers  []BookIdentifier `json:"identifiers"`
}

// BookIdentifier is the JSON representation of an identifier with provenance.
type BookIdentifier struct {
	Scheme string            `json:"scheme"`
	Value  string            `json:"value"`
	Source identifier.Source `json:"source"`
}

// BookPublication is one publisher/publication-date pair of a book.
type BookPublication struct {
	Publisher       string `json:"publisher"`
	PublicationDate string `json:"publication_date"`
}

// BookClaimCandidate is one book-level title or author hypothesis.
type BookClaimCandidate struct {
	Value                  string   `json:"value"`
	Authors                []string `json:"authors,omitempty"`
	Status                 string   `json:"status"`
	Disposition            string   `json:"disposition"`
	Reason                 string   `json:"reason,omitempty"`
	SupportingWitnesses    int      `json:"supporting_witnesses"`
	StrictSupportingClaims int      `json:"strict_supporting_claims"`
	SourceGroups           int      `json:"source_groups"`
	IndependentWitnesses   int      `json:"independent_witnesses"`
	Attested               bool     `json:"attested"`
	Suspicious             bool     `json:"suspicious,omitempty"`
	AttestationLevel       string   `json:"attestation_level,omitempty"`
	MaxAttestation         float64  `json:"max_attestation,omitempty"`
	FoundIn                []int64  `json:"found_in,omitempty"`
	Residual               []string `json:"residual,omitempty"`
	UseForMatching         bool     `json:"use_for_matching"`
	UseForAgreement        bool     `json:"use_for_agreement"`
	Evidence               string   `json:"evidence,omitempty"`
}

// BookReport is the JSON representation of one book: the chosen
// (book-level) metadata plus every file grouped under it.
// BookReport is the consolidated metadata of one book.
type BookReport struct {
	ID               int64                `json:"id"`
	Authors          []string             `json:"authors"`
	Title            string               `json:"title"`
	Description      string               `json:"description"`
	Series           string               `json:"series"`
	SeriesIndex      string               `json:"series_index"`
	Subjects         []string             `json:"subjects"`
	Languages        []string             `json:"languages"`
	Identifiers      []BookIdentifier     `json:"identifiers"`
	Publications     []BookPublication    `json:"publications"`
	TitleStatus      string               `json:"title_status"`
	AuthorStatus     string               `json:"author_status"`
	GroupingSuspect  bool                 `json:"grouping_suspect"`
	GroupingReason   string               `json:"grouping_reason,omitempty"`
	TitleCandidates  []BookClaimCandidate `json:"title_candidates,omitempty"`
	AuthorCandidates []BookClaimCandidate `json:"author_candidates,omitempty"`
	LegacyBook       *FileLegacy          `json:"legacy_book,omitempty"`
	Files            []BookFileReport     `json:"files"`
}

// Consolidate derives the canonical book-level metadata from the raw
// per-file metadata and persists it: books holds the consensus washed title
// (updating books_fts via triggers) and the merged, validated metadata;
// book_authors, book_subjects, book_languages, book_identifiers and
// book_publications hold the picked values. It first canonicalizes exact
// duplicates so that the lexicographically smallest path represents each
// content hash; the per-file metadata itself is preserved. Finally, authors
// referenced by neither book_authors nor book_file_authors are deleted
// (cleaning authors_fts via triggers). The pass is idempotent: it always
// recomputes from the raw file rows.
func Consolidate(ctx context.Context, sqldb *sql.DB, q *db.Queries) error {
	run := func(txCtx context.Context, txQ *db.Queries) error {
		if err := canonicalizeBookFiles(txCtx, txQ); err != nil {
			return err
		}
		reports, order, err := buildBookReports(txCtx, txQ)
		if err != nil {
			return err
		}
		for _, id := range order {
			r := reports[id]
			if err := persistBookMetadata(txCtx, txQ, id, r); err != nil {
				return err
			}
			if err := txQ.DeleteBookAuthorsByBookID(txCtx, id); err != nil {
				return fmt.Errorf("delete book %d authors: %w", id, err)
			}
			for _, name := range r.Authors {
				if err := txQ.CreateAuthor(txCtx, name); err != nil {
					return fmt.Errorf("create author %q: %w", name, err)
				}
				author, err := txQ.GetAuthorByName(txCtx, name)
				if err != nil {
					return fmt.Errorf("get author %q: %w", name, err)
				}
				if err := txQ.CreateBookAuthor(txCtx, db.CreateBookAuthorParams{
					BookID:   id,
					AuthorID: author.ID,
				}); err != nil {
					return fmt.Errorf("link book %d author %d: %w", id, author.ID, err)
				}
			}
		}
		if err := txQ.DeleteOrphanAuthors(txCtx); err != nil {
			return fmt.Errorf("delete orphan authors: %w", err)
		}
		return nil
	}

	if sqldb != nil {
		return tx.WithTx(ctx, sqldb, q, func(txCtx context.Context) error {
			return run(txCtx, tx.QueryFrom(txCtx, q))
		})
	}
	return run(ctx, q)
}

// canonicalizeBookFiles promotes the lexicographically smallest path of each
// content hash to the canonical book_files row. The scan stores whichever path
// it sees first, so without this pass the canonical path — and every report
// built from it — depends on scan order. The promoted path leaves the
// duplicates table and the previous canonical path takes its place there. The
// row keeps its ID, so all per-file metadata stays attached.
func canonicalizeBookFiles(ctx context.Context, q *db.Queries) error {
	rows, err := q.ListBookFileHashesNeedingCanonicalization(ctx)
	if err != nil {
		return fmt.Errorf("list canonicalization candidates: %w", err)
	}
	for _, r := range rows {
		if err := q.DeleteBookFileDuplicate(ctx, db.DeleteBookFileDuplicateParams{
			Hash: r.Hash,
			Path: r.SmallestDuplicatePath,
		}); err != nil {
			return fmt.Errorf("remove promoted duplicate %q: %w", r.SmallestDuplicatePath, err)
		}
		if err := q.UpdateBookFilePath(ctx, db.UpdateBookFilePathParams{
			Hash: r.Hash,
			Path: r.SmallestDuplicatePath,
		}); err != nil {
			return fmt.Errorf("promote canonical path %q: %w", r.SmallestDuplicatePath, err)
		}
		if err := q.CreateBookFileDuplicate(ctx, db.CreateBookFileDuplicateParams{
			Hash: r.Hash,
			Path: r.CanonicalPath,
		}); err != nil {
			return fmt.Errorf("demote canonical path %q: %w", r.CanonicalPath, err)
		}
	}
	return nil
}

func persistBookMetadata(ctx context.Context, q *db.Queries, id int64, r BookReport) error {
	if err := q.UpdateBookMetadata(ctx, db.UpdateBookMetadataParams{
		ID:          id,
		Title:       r.Title,
		Description: r.Description,
		Series:      r.Series,
		SeriesIndex: r.SeriesIndex,
	}); err != nil {
		return fmt.Errorf("update book %d metadata: %w", id, err)
	}
	if err := q.UpdateBookClaimResolution(ctx, db.UpdateBookClaimResolutionParams{
		TitleStatus:        r.TitleStatus,
		AuthorStatus:       r.AuthorStatus,
		GroupingSuspect:    boolInt(r.GroupingSuspect),
		GroupingReason:     r.GroupingReason,
		BookResolveVersion: claim.BookResolveVersion,
		ID:                 id,
	}); err != nil {
		return fmt.Errorf("update book %d claim resolution: %w", id, err)
	}
	if err := q.DeleteBookClaimCandidatesByBookID(ctx, id); err != nil {
		return fmt.Errorf("delete book %d claim candidates: %w", id, err)
	}
	persistCandidates := func(field string, candidates []BookClaimCandidate) error {
		for i, candidate := range candidates {
			if err := q.CreateBookClaimCandidate(ctx, db.CreateBookClaimCandidateParams{
				BookID:                 id,
				Field:                  field,
				Rank:                   int64(i + 1),
				Value:                  candidate.Value,
				Authors:                encodeStrings(candidate.Authors),
				Status:                 candidate.Status,
				Disposition:            candidate.Disposition,
				Reason:                 candidate.Reason,
				SupportingWitnesses:    int64(candidate.SupportingWitnesses),
				StrictSupportingClaims: int64(candidate.StrictSupportingClaims),
				SourceGroups:           int64(candidate.SourceGroups),
				IndependentWitnesses:   int64(candidate.IndependentWitnesses),
				Attested:               boolInt(candidate.Attested),
				Suspicious:             boolInt(candidate.Suspicious),
				AttestationLevel:       candidate.AttestationLevel,
				MaxAttestation:         candidate.MaxAttestation,
				FoundIn:                encodeInt64s(candidate.FoundIn),
				Residual:               encodeStrings(candidate.Residual),
				UseForMatching:         boolInt(candidate.UseForMatching),
				UseForAgreement:        boolInt(candidate.UseForAgreement),
				Evidence:               candidate.Evidence,
			}); err != nil {
				return fmt.Errorf("create book %d claim candidate %q: %w", id, candidate.Value, err)
			}
		}
		return nil
	}
	if err := persistCandidates("title", r.TitleCandidates); err != nil {
		return err
	}
	if err := persistCandidates("author", r.AuthorCandidates); err != nil {
		return err
	}
	if err := q.DeleteBookSubjectsByBookID(ctx, id); err != nil {
		return fmt.Errorf("delete book %d subjects: %w", id, err)
	}
	for i, name := range r.Subjects {
		if err := q.CreateBookSubject(ctx, db.CreateBookSubjectParams{
			BookID:  id,
			Name:    name,
			Ordinal: int64(i),
		}); err != nil {
			return fmt.Errorf("create book %d subject %q: %w", id, name, err)
		}
	}
	if err := q.DeleteBookLanguagesByBookID(ctx, id); err != nil {
		return fmt.Errorf("delete book %d languages: %w", id, err)
	}
	for i, name := range r.Languages {
		if err := q.CreateBookLanguage(ctx, db.CreateBookLanguageParams{
			BookID:  id,
			Name:    name,
			Ordinal: int64(i),
		}); err != nil {
			return fmt.Errorf("create book %d language %q: %w", id, name, err)
		}
	}
	if err := q.DeleteBookIdentifiersByBookID(ctx, id); err != nil {
		return fmt.Errorf("delete book %d identifiers: %w", id, err)
	}
	for i, identifier := range r.Identifiers {
		if err := q.CreateBookIdentifier(ctx, db.CreateBookIdentifierParams{
			BookID:  id,
			Scheme:  identifier.Scheme,
			Value:   identifier.Value,
			Source:  string(identifier.Source),
			Ordinal: int64(i),
		}); err != nil {
			return fmt.Errorf("create book %d identifier %q: %w", id, identifier.Value, err)
		}
	}
	if err := q.DeleteBookPublicationsByBookID(ctx, id); err != nil {
		return fmt.Errorf("delete book %d publications: %w", id, err)
	}
	for i, publication := range r.Publications {
		if err := q.CreateBookPublication(ctx, db.CreateBookPublicationParams{
			BookID:          id,
			Publisher:       publication.Publisher,
			PublicationDate: publication.PublicationDate,
			Ordinal:         int64(i),
		}); err != nil {
			return fmt.Errorf("create book %d publication %q: %w", id, publication.Publisher, err)
		}
	}
	return nil
}

func boolInt(v bool) int64 {
	if v {
		return 1
	}
	return 0
}

func encodeStrings(values []string) string {
	if len(values) == 0 {
		return "[]"
	}
	data, err := json.Marshal(values)
	if err != nil {
		return "[]"
	}
	return string(data)
}

func encodeInt64s(values []int64) string {
	if len(values) == 0 {
		return "[]"
	}
	data, err := json.Marshal(values)
	if err != nil {
		return "[]"
	}
	return string(data)
}

func decodeStrings(raw string) []string {
	if raw == "" {
		return nil
	}
	var values []string
	if err := json.Unmarshal([]byte(raw), &values); err != nil {
		return nil
	}
	return values
}

func encodeEvidence(evidence map[string]string) string {
	if len(evidence) == 0 {
		return "{}"
	}
	data, err := json.Marshal(evidence)
	if err != nil {
		return "{}"
	}
	return string(data)
}

func decodeInt64s(raw string) []int64 {
	if raw == "" {
		return nil
	}
	var values []int64
	if err := json.Unmarshal([]byte(raw), &values); err != nil {
		return nil
	}
	return values
}

// WriteBooks writes a JSON report of the books found during the scan to path.
// Book-level metadata is read from the consolidated books and book_* tables
// (populated by Consolidate during scan); file entries keep their raw
// per-file metadata. Exact duplicates (same content hash) are
// listed under their parent book, inheriting the canonical file's metadata.
// When no books were found nothing is written, so no empty file is left
// behind.
func WriteBooks(ctx context.Context, q *db.Queries, path string) error {
	books, err := GetBooks(ctx, q)
	if err != nil {
		return err
	}
	if len(books) == 0 {
		return nil
	}
	data, err := json.MarshalIndent(books, "", "  ")
	if err != nil {
		return err
	}
	if err := writeFile(path, data); err != nil {
		return fmt.Errorf("write books report: %w", err)
	}
	return nil
}

// GetBooks returns the reports of all books ordered by title, then by their
// first file path. Book IDs follow scan order, so ordering by ID would make the
// report depend on the order in which the scanner happened to finish files.
func GetBooks(ctx context.Context, q *db.Queries) ([]BookReport, error) {
	ids, err := q.ListBookIDs(ctx)
	if err != nil {
		return nil, fmt.Errorf("list book ids: %w", err)
	}
	out := make([]BookReport, 0, len(ids))
	for _, id := range ids {
		r, ok, err := getBookReport(ctx, q, id)
		if err != nil {
			return nil, err
		}
		if ok {
			out = append(out, r)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Title != out[j].Title {
			return out[i].Title < out[j].Title
		}
		return firstPath(out[i]) < firstPath(out[j])
	})
	return out, nil
}

// firstPath returns the first file path of a report, or "" when it has none.
func firstPath(r BookReport) string {
	if len(r.Files) == 0 {
		return ""
	}
	return r.Files[0].Path
}

// GetBooksFiltered returns the book reports for the given book IDs in the
// requested order. Unknown IDs are skipped. When ids is empty, nil is returned
// without reading the database.
//
// Book-level metadata is read directly from the consolidated books/book_*
// tables (populated by Consolidate during scan); only the requested books are
// loaded. File entries keep their raw per-file metadata.
func GetBooksFiltered(ctx context.Context, q *db.Queries, ids []int64) ([]BookReport, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	seen := make(map[int64]bool, len(ids))
	var out []BookReport
	for _, id := range ids {
		if seen[id] {
			continue
		}
		seen[id] = true
		r, ok, err := getBookReport(ctx, q, id)
		if err != nil {
			return nil, err
		}
		if ok {
			out = append(out, r)
		}
	}
	return out, nil
}

// getBookReport reads one consolidated book report: the canonical metadata
// plus the raw member files and exact duplicates. It reports false when the
// book ID is unknown.
func getBookReport(ctx context.Context, q *db.Queries, id int64) (BookReport, bool, error) {
	book, err := q.GetBookByID(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return BookReport{}, false, nil
	}
	if err != nil {
		return BookReport{}, false, fmt.Errorf("get book %d: %w", id, err)
	}
	authorRows, err := q.ListAuthorsByBookID(ctx, id)
	if err != nil {
		return BookReport{}, false, fmt.Errorf("list authors of book %d: %w", id, err)
	}
	authors := make([]string, 0, len(authorRows))
	for _, a := range authorRows {
		authors = append(authors, a.Name)
	}
	subjects, err := q.ListBookSubjectsByBookID(ctx, id)
	if err != nil {
		return BookReport{}, false, fmt.Errorf("list subjects of book %d: %w", id, err)
	}
	if subjects == nil {
		subjects = []string{}
	}
	languages, err := q.ListBookLanguagesByBookID(ctx, id)
	if err != nil {
		return BookReport{}, false, fmt.Errorf("list languages of book %d: %w", id, err)
	}
	if languages == nil {
		languages = []string{}
	}
	identifierRows, err := q.ListBookIdentifiersByBookID(ctx, id)
	if err != nil {
		return BookReport{}, false, fmt.Errorf("list identifiers of book %d: %w", id, err)
	}
	identifiers := make([]BookIdentifier, 0, len(identifierRows))
	for _, row := range identifierRows {
		identifiers = append(identifiers, BookIdentifier{
			Scheme: row.Scheme,
			Value:  row.Value,
			Source: identifier.Source(row.Source),
		})
	}
	fileIdentifierRows, err := q.ListBookFileIdentifiersByBookID(ctx, id)
	if err != nil {
		return BookReport{}, false, fmt.Errorf("list file identifiers of book %d: %w", id, err)
	}
	fileIdentifiers := make(map[int64][]BookIdentifier)
	for _, row := range fileIdentifierRows {
		fileIdentifiers[row.BookFileID] = append(fileIdentifiers[row.BookFileID], BookIdentifier{
			Scheme: row.Scheme,
			Value:  row.Value,
			Source: identifier.Source(row.Source),
		})
	}
	publicationRows, err := q.ListBookPublicationsByBookID(ctx, id)
	if err != nil {
		return BookReport{}, false, fmt.Errorf("list publications of book %d: %w", id, err)
	}
	publications := make([]BookPublication, 0, len(publicationRows))
	for _, publication := range publicationRows {
		publications = append(publications, BookPublication{
			Publisher:       publication.Publisher,
			PublicationDate: publication.PublicationDate,
		})
	}
	fileRows, err := q.ListBookFilesWithAuthorsByBookID(ctx, id)
	if err != nil {
		return BookReport{}, false, fmt.Errorf("list files of book %d: %w", id, err)
	}
	files := make([]BookFileReport, 0, len(fileRows))
	byPath := make(map[string]bool, len(fileRows))
	byHash := make(map[string]BookFileReport, len(fileRows))
	legacyTitles := make([]string, 0, len(fileRows))
	legacyAuthorSets := make([][]string, 0, len(fileRows))
	for _, r := range fileRows {
		fileAuthors := filterFileAuthors(r.FileAuthors, r.FileTitle)
		ids := fileIdentifiers[r.FileID]
		if ids == nil {
			ids = []BookIdentifier{}
		}
		legacyRows, err := q.ListBookFileLegacyAuthorsByBookFileID(ctx, r.FileID)
		if err != nil {
			return BookReport{}, false, fmt.Errorf("list legacy authors of file %d: %w", r.FileID, err)
		}
		legacyNames := make([]string, 0, len(legacyRows))
		for _, row := range legacyRows {
			legacyNames = append(legacyNames, row.Name)
		}
		claimRows, err := q.ListBookFileClaimsByBookFileID(ctx, r.FileID)
		if err != nil {
			return BookReport{}, false, fmt.Errorf("list claims of file %d: %w", r.FileID, err)
		}
		claims := &BookFileClaims{}
		for _, row := range claimRows {
			item := BookFileClaim{
				Value:               row.Value,
				Source:              row.Source,
				Set:                 row.SetKey,
				Origin:              row.Origin,
				Trust:               row.Trust,
				Witness:             row.Witness,
				Junk:                row.Junk != 0,
				Attested:            row.Attested != 0,
				Attestation:         row.Attestation,
				AttestationMethod:   row.AttestationMethod,
				AttestationLevel:    row.AttestationLevel,
				AttestationResidual: decodeStrings(row.AttestationResidual),
				Suspicious:          claim.IsSuspicious(row.SuspiciousSignals, row.Attested != 0),
				SuspiciousSignals:   row.SuspiciousSignals,
			}
			if row.Field == "title" {
				claims.Title = append(claims.Title, item)
			} else {
				claims.Author = append(claims.Author, item)
			}
		}
		file := BookFileReport{
			Path:         r.FilePath,
			Authors:      fileAuthors,
			Title:        normalizeMeta(r.FileTitle),
			Legacy:       &FileLegacy{Title: normalizeMeta(r.FileLegacyTitle), Authors: legacyNames},
			TitleStatus:  r.FileTitleStatus,
			AuthorStatus: r.FileAuthorStatus,
			TitleSet:     r.FileTitleSet,
			AuthorSet:    r.FileAuthorSet,
			Claims:       claims,
			Identifiers:  ids,
		}
		legacyTitles = append(legacyTitles, file.Title)
		legacyAuthorSets = append(legacyAuthorSets, fileAuthors)
		files = append(files, file)
		byPath[r.FilePath] = true
		byHash[r.FileHash] = file
	}
	if len(files) > 0 {
		dups, err := q.ListDuplicatesByBookID(ctx, id)
		if err != nil {
			return BookReport{}, false, fmt.Errorf("list duplicates of book %d: %w", id, err)
		}
		for _, dup := range dups {
			if byPath[dup.DuplicatePath] {
				continue
			}
			parent, ok := byHash[dup.Hash]
			if !ok {
				continue
			}
			files = append(files, BookFileReport{
				Path:         dup.DuplicatePath,
				Authors:      parent.Authors,
				Title:        parent.Title,
				Legacy:       parent.Legacy,
				TitleStatus:  parent.TitleStatus,
				AuthorStatus: parent.AuthorStatus,
				TitleSet:     parent.TitleSet,
				AuthorSet:    parent.AuthorSet,
				Claims:       parent.Claims,
				Identifiers:  append([]BookIdentifier{}, parent.Identifiers...),
			})
			byPath[dup.DuplicatePath] = true
		}
	}
	legacyAuthorNames := make([]string, 0)
	for _, list := range legacyAuthorSets {
		legacyAuthorNames = append(legacyAuthorNames, list...)
	}
	legacyTitle := pickTitle(legacyTitles, legacyAuthorNames)
	legacyAuthors := pickAuthors(legacyAuthorSets)

	var titleCandidates, authorCandidates []BookClaimCandidate
	candidateRows, err := q.ListBookClaimCandidatesByBookID(ctx, id)
	if err != nil {
		return BookReport{}, false, fmt.Errorf("list claim candidates of book %d: %w", id, err)
	}
	for _, row := range candidateRows {
		item := BookClaimCandidate{
			Value:                  row.Value,
			Authors:                decodeStrings(row.Authors),
			Status:                 row.Status,
			Disposition:            row.Disposition,
			Reason:                 row.Reason,
			SupportingWitnesses:    int(row.SupportingWitnesses),
			StrictSupportingClaims: int(row.StrictSupportingClaims),
			SourceGroups:           int(row.SourceGroups),
			IndependentWitnesses:   int(row.IndependentWitnesses),
			Attested:               row.Attested != 0,
			Suspicious:             row.Suspicious != 0,
			AttestationLevel:       row.AttestationLevel,
			MaxAttestation:         row.MaxAttestation,
			FoundIn:                decodeInt64s(row.FoundIn),
			Residual:               decodeStrings(row.Residual),
			UseForMatching:         row.UseForMatching != 0,
			UseForAgreement:        row.UseForAgreement != 0,
			Evidence:               row.Evidence,
		}
		if row.Field == "title" {
			titleCandidates = append(titleCandidates, item)
		} else {
			authorCandidates = append(authorCandidates, item)
		}
	}
	return BookReport{
		ID:               id,
		Authors:          authors,
		Title:            book.Title,
		Description:      book.Description,
		Series:           book.Series,
		SeriesIndex:      book.SeriesIndex,
		Subjects:         subjects,
		Languages:        languages,
		Identifiers:      identifiers,
		Publications:     publications,
		TitleStatus:      book.TitleStatus,
		AuthorStatus:     book.AuthorStatus,
		GroupingSuspect:  book.GroupingSuspect != 0,
		GroupingReason:   book.GroupingReason,
		TitleCandidates:  titleCandidates,
		AuthorCandidates: authorCandidates,
		LegacyBook:       &FileLegacy{Title: legacyTitle, Authors: legacyAuthors},
		Files:            files,
	}, true, nil
}

// filterFileAuthors splits a GROUP_CONCAT file-authors aggregate the same way
// buildBookReports does for file entries: placeholders are dropped and
// converter artifacts echoing the file title are filtered out.
func filterFileAuthors(aggregate, fileTitle string) []string {
	authors := splitAuthors(aggregate)
	fileTitle = normalizeMeta(fileTitle)
	kept := authors[:0]
	for _, name := range authors {
		if !isTitleEcho(name, fileTitle) {
			kept = append(kept, name)
		}
	}
	if kept == nil {
		return []string{}
	}
	return kept
}

// WriteBooksFiltered writes the same JSON shape as WriteBooks but only for
// the given book IDs and in the given order (e.g. FTS relevance order for
// search results). Unknown IDs are skipped. When the selection is empty
// nothing is written, so no empty file is left behind.
func WriteBooksFiltered(ctx context.Context, q *db.Queries, ids []int64, path string) error {
	if len(ids) == 0 {
		return nil
	}
	books, err := GetBooksFiltered(ctx, q, ids)
	if err != nil {
		return err
	}
	if len(books) == 0 {
		return nil
	}
	data, err := json.MarshalIndent(books, "", "  ")
	if err != nil {
		return err
	}
	if err := writeFile(path, data); err != nil {
		return fmt.Errorf("write books report: %w", err)
	}
	return nil
}

// buildBookReports runs the shared books-report pipeline: it groups member
// files under their books, attaches exact duplicates, merges and validates
// the remaining metadata, and resolves book-level authors and title with the
// pickAuthors/pickTitle heuristics. It returns the per-book reports keyed by
// book ID plus the natural (book ID) order.
func buildBookReports(ctx context.Context, q *db.Queries) (map[int64]BookReport, []int64, error) {
	rows, err := q.ListBooksWithFiles(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("list books with files: %w", err)
	}
	if len(rows) == 0 {
		return nil, nil, nil
	}

	dups, err := q.ListBookFileDuplicatesWithBook(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("list duplicates with book: %w", err)
	}

	subjectRows, err := q.ListBookFileSubjectsWithBook(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("list file subjects with book: %w", err)
	}
	subjectsByBook := make(map[int64][]string)
	for _, r := range subjectRows {
		subjectsByBook[r.BookID] = append(subjectsByBook[r.BookID], r.Name)
	}

	languageRows, err := q.ListBookFileLanguagesWithBook(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("list file languages with book: %w", err)
	}
	languagesByBook := make(map[int64][]string)
	for _, r := range languageRows {
		languagesByBook[r.BookID] = append(languagesByBook[r.BookID], r.Name)
	}

	identifierRows, err := q.ListBookFileIdentifiersWithBook(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("list file identifiers with book: %w", err)
	}
	identifiersByBook := make(map[int64][]BookIdentifier)
	fileIdentifiers := make(map[int64][]BookIdentifier)
	for _, r := range identifierRows {
		item := BookIdentifier{
			Scheme: r.Scheme,
			Value:  r.Value,
			Source: identifier.Source(r.Source),
		}
		identifiersByBook[r.BookID] = append(identifiersByBook[r.BookID], item)
		fileIdentifiers[r.BookFileID] = append(fileIdentifiers[r.BookFileID], item)
	}

	legacyRows, err := q.ListBookFileLegacyAuthorsWithBook(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("list legacy authors with book: %w", err)
	}
	legacyAuthors := make(map[int64][]string)
	for _, r := range legacyRows {
		legacyAuthors[r.BookFileID] = append(legacyAuthors[r.BookFileID], r.Name)
	}

	claimRows, err := q.ListBookFileClaimsWithBook(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("list claims with book: %w", err)
	}
	fileClaims := make(map[int64]*BookFileClaims)
	fileClaimModels := make(map[int64][]claim.Claim)
	for _, r := range claimRows {
		fileClaimModels[r.BookFileID] = append(fileClaimModels[r.BookFileID], claim.Claim{
			Field:               claim.Field(r.Field),
			Value:               r.Value,
			Source:              claim.Source(r.Source),
			Set:                 r.SetKey,
			Origin:              r.Origin,
			Path:                r.Path,
			Ordinal:             int(r.Ordinal),
			FileID:              r.BookFileID,
			Junk:                r.Junk != 0,
			Trust:               claim.Trust(r.Trust),
			Witness:             claim.Witness(r.Witness),
			Attested:            r.Attested != 0,
			Attestation:         r.Attestation,
			AttestationMethod:   r.AttestationMethod,
			AttestationLevel:    claim.AttestLevel(r.AttestationLevel),
			AttestationResidual: decodeStrings(r.AttestationResidual),
			SuspiciousSignals:   r.SuspiciousSignals,
		})
		claims := fileClaims[r.BookFileID]
		if claims == nil {
			claims = &BookFileClaims{}
			fileClaims[r.BookFileID] = claims
		}
		item := BookFileClaim{
			Value:               r.Value,
			Source:              r.Source,
			Set:                 r.SetKey,
			Origin:              r.Origin,
			Trust:               r.Trust,
			Witness:             r.Witness,
			Junk:                r.Junk != 0,
			Attested:            r.Attested != 0,
			Attestation:         r.Attestation,
			AttestationMethod:   r.AttestationMethod,
			AttestationLevel:    r.AttestationLevel,
			AttestationResidual: decodeStrings(r.AttestationResidual),
			Suspicious:          claim.IsSuspicious(r.SuspiciousSignals, r.Attested != 0),
			SuspiciousSignals:   r.SuspiciousSignals,
		}
		if r.Field == "title" {
			claims.Title = append(claims.Title, item)
		} else {
			claims.Author = append(claims.Author, item)
		}
	}

	type entry struct {
		titles        []string
		authorSet     [][]string
		metadataFiles []fileMetadata
		files         []BookFileReport
		claimFiles    []claim.BookFile
		byPath        map[string]bool
		byHash        map[string]BookFileReport
	}
	books := make(map[int64]*entry)
	var order []int64
	// A book's files are ordered by path so that every order-sensitive step
	// below (title tie-breaks, fallback titles) is stable across runs; the
	// database returns them in insertion order.
	sort.SliceStable(rows, func(i, j int) bool {
		if rows[i].BookID != rows[j].BookID {
			return rows[i].BookID < rows[j].BookID
		}
		return rows[i].FilePath < rows[j].FilePath
	})
	for _, r := range rows {
		b, ok := books[r.BookID]
		if !ok {
			b = &entry{
				byPath: make(map[string]bool),
				byHash: make(map[string]BookFileReport),
			}
			books[r.BookID] = b
			order = append(order, r.BookID)
		}
		authors := splitAuthors(r.FileAuthors)
		fileTitle := normalizeMeta(r.FileTitle)
		// Drop converter artifacts where the file title was copied into
		// the author field (e.g. both read "Patkánykirály"): they carry
		// no author information and would block the Calibre post-pass.
		kept := authors[:0]
		for _, name := range authors {
			if !isTitleEcho(name, fileTitle) {
				kept = append(kept, name)
			}
		}
		authors = kept
		if authors == nil {
			authors = []string{}
		}
		b.titles = append(b.titles, r.FileTitle)
		b.authorSet = append(b.authorSet, authors)
		b.metadataFiles = append(b.metadataFiles, fileMetadata{
			description:     r.FileDescription,
			publisher:       r.FilePublisher,
			publicationDate: r.FilePublicationDate,
			series:          r.FileSeries,
			seriesIndex:     r.FileSeriesIndex,
		})
		ids := fileIdentifiers[r.FileID]
		if ids == nil {
			ids = []BookIdentifier{}
		}
		file := BookFileReport{
			Path:         r.FilePath,
			Authors:      authors,
			Title:        fileTitle,
			Legacy:       &FileLegacy{Title: normalizeMeta(r.FileLegacyTitle), Authors: append([]string{}, legacyAuthors[r.FileID]...)},
			TitleStatus:  r.FileTitleStatus,
			AuthorStatus: r.FileAuthorStatus,
			TitleSet:     r.FileTitleSet,
			AuthorSet:    r.FileAuthorSet,
			Claims:       fileClaims[r.FileID],
			Identifiers:  ids,
		}
		b.files = append(b.files, file)
		b.claimFiles = append(b.claimFiles, claim.BookFile{
			ID:           r.FileID,
			Hash:         r.FileHash,
			Path:         r.FilePath,
			SourceGroup:  filemeta.SourceGroup(r.FilePath),
			Window:       claim.Window{FileID: r.FileID, Path: r.FilePath, Lines: decodeStrings(r.FileTitleWindow)},
			Claims:       fileClaimModels[r.FileID],
			TitleStatus:  claim.Status(r.FileTitleStatus),
			AuthorStatus: claim.Status(r.FileAuthorStatus),
		})
		b.byPath[r.FilePath] = true
		b.byHash[r.FileHash] = file
	}

	// Attach exact duplicates under their parent book. A duplicate carries
	// no metadata of its own, so it inherits the metadata of the canonical
	// row with the same content hash. Orphan duplicates (no parent row, which
	// cannot happen in a scan but keeps the report robust) are skipped.
	for _, d := range dups {
		b := books[d.BookID]
		if b == nil || b.byPath[d.DuplicatePath] {
			continue
		}
		parent, ok := b.byHash[d.Hash]
		if !ok {
			continue
		}
		b.files = append(b.files, BookFileReport{
			Path:         d.DuplicatePath,
			Authors:      parent.Authors,
			Title:        parent.Title,
			Legacy:       parent.Legacy,
			TitleStatus:  parent.TitleStatus,
			AuthorStatus: parent.AuthorStatus,
			TitleSet:     parent.TitleSet,
			AuthorSet:    parent.AuthorSet,
			Claims:       parent.Claims,
			Identifiers:  append([]BookIdentifier{}, parent.Identifiers...),
		})
		b.byPath[d.DuplicatePath] = true
	}

	reports := make(map[int64]BookReport, len(order))
	for _, id := range order {
		b := books[id]
		authors := pickAuthors(b.authorSet)
		// Union of the files' (placeholder-filtered) author names, so the
		// title heuristic can penalize "Author - Title" patterns.
		var authorNames []string
		seen := make(map[string]struct{})
		for _, list := range b.authorSet {
			for _, name := range list {
				if key := metaKey(name); key != "" {
					if _, ok := seen[key]; !ok {
						seen[key] = struct{}{}
						authorNames = append(authorNames, name)
					}
				}
			}
		}
		title := pickTitle(b.titles, authorNames)
		titleFromMetadata := title != ""
		needTitle := !titleFromMetadata
		if needTitle && len(b.files) > 0 {
			// No file carried a usable title: fall back to the first
			// file's name. Book-level only; the file entries keep their
			// original titles.
			title = titleFromPath(b.files[0].Path)
		}
		calibreAuthor := ""
		// Natural-order "Author - Title" names are the strongest remaining
		// source: they can supply both fields when the metadata has
		// neither, and an author when only the title survived. A filename
		// title never replaces a title that came from the files.
		if needTitle || len(authors) == 0 {
			for _, f := range b.files {
				parsedTitle, parsedAuthor, ok := parseNaturalFilename(f.Path)
				if !ok {
					continue
				}
				if titleFromMetadata && !titlesAgree(parsedTitle, title) {
					continue
				}
				if needTitle {
					title = parsedTitle
				}
				if len(authors) == 0 {
					authors = []string{parsedAuthor}
					calibreAuthor = parsedAuthor
				}
				break
			}
		}
		if len(authors) == 0 {
			if needTitle {
				// Neither usable authors nor a usable title: a strict
				// Calibre-style "Title - Last, First" file name is the best
				// remaining source for both.
				for _, f := range b.files {
					if t, a, ok := parseCalibreFilename(f.Path); ok {
						title = t
						authors = []string{a}
						calibreAuthor = a
						break
					}
				}
			} else {
				// The title is known-good, only authors are missing: the
				// relaxed Calibre parse recovers a comma-less "First
				// Last" author without ever touching the title, so a
				// subtitle can never be corrupted here.
				for _, f := range b.files {
					if a, ok := parseCalibreAuthorOnly(f.Path); ok {
						authors = []string{a}
						calibreAuthor = a
						break
					}
				}
			}
		}
		if len(authors) == 0 {
			// Last resort: the parent directory or archive name often holds
			// the author, especially in unpacked author/year layouts.
			for _, f := range b.files {
				if a, ok := authorFromPath(f.Path, title); ok {
					authors = []string{a}
					calibreAuthor = a
					break
				}
			}
		}
		if len(authors) > 0 {
			// Wash a credit affix ("Author - X", "X - Author",
			// "X by Author") off the picked title, whatever its source:
			// single-file (or noisy-majority) "Author - Title"
			// metadata has no clean rival to vote against it. The
			// guards inside washAuthorAffix keep genuine titles such
			// as "Stephen King Goes to the Movies" intact.
			washNames := authorNames
			if calibreAuthor != "" {
				washNames = append(washNames, calibreAuthor)
			}
			var sets [][]string
			for _, n := range washNames {
				sets = append(sets, matchTokens(n))
			}
			title = washAuthorAffix(title, sets)
		}
		legacyTitle := title
		legacyAuthors := append([]string{}, authors...)
		titleStatus := "none"
		authorStatus := "none"
		if legacyTitle != "" {
			titleStatus = "single"
		}
		if len(legacyAuthors) > 0 {
			authorStatus = "single"
		}
		groupingSuspect := false
		groupingReason := ""
		var titleCandidates, authorCandidates []BookClaimCandidate
		if len(b.claimFiles) > 0 {
			bookRes := claim.ResolveBook(b.claimFiles)
			if bookRes.Title != "" || bookRes.TitleStatus != claim.StatusNone {
				title = bookRes.Title
				titleStatus = string(bookRes.TitleStatus)
			}
			if len(bookRes.Authors) > 0 {
				authors = bookRes.Authors
				authorStatus = string(bookRes.AuthorStatus)
			}
			groupingSuspect = bookRes.GroupingSuspect
			groupingReason = bookRes.GroupingReason
			for _, c := range bookRes.Candidates {
				item := BookClaimCandidate{
					Value:                  c.Value,
					Authors:                append([]string{}, c.Authors...),
					Status:                 string(c.Status),
					Disposition:            string(c.Disposition),
					Reason:                 c.Reason,
					SupportingWitnesses:    c.SupportingWitnesses,
					StrictSupportingClaims: c.StrictSupportingClaims,
					SourceGroups:           c.SourceGroups,
					IndependentWitnesses:   c.IndependentWitnesses,
					Attested:               c.Attested,
					Suspicious:             c.Suspicious,
					AttestationLevel:       string(c.AttestationLevel),
					MaxAttestation:         c.MaxAttestation,
					FoundIn:                append([]int64{}, c.FoundIn...),
					Residual:               append([]string{}, c.Residual...),
					UseForMatching:         c.UseForMatching,
					UseForAgreement:        c.UseForAgreement,
					Evidence:               encodeEvidence(c.Evidence),
				}
				if c.Field == claim.FieldTitle {
					titleCandidates = append(titleCandidates, item)
				} else {
					authorCandidates = append(authorCandidates, item)
				}
			}
		}
		metadata := mergeBookMetadata(
			b.metadataFiles,
			subjectsByBook[id],
			languagesByBook[id],
			identifiersByBook[id],
		)
		reports[id] = BookReport{
			ID:               id,
			Authors:          authors,
			Title:            title,
			Description:      metadata.description,
			Series:           metadata.series,
			SeriesIndex:      metadata.seriesIndex,
			Subjects:         metadata.subjects,
			Languages:        metadata.languages,
			Identifiers:      metadata.identifiers,
			Publications:     metadata.publications,
			TitleStatus:      titleStatus,
			AuthorStatus:     authorStatus,
			GroupingSuspect:  groupingSuspect,
			GroupingReason:   groupingReason,
			TitleCandidates:  titleCandidates,
			AuthorCandidates: authorCandidates,
			LegacyBook:       &FileLegacy{Title: legacyTitle, Authors: legacyAuthors},
			Files:            b.files,
		}
	}
	return reports, order, nil
}
