package processor

import (
	"context"
	"database/sql"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"runtime"
	"sync"
	"sync/atomic"

	"github.com/f0d0r/margaret-cli/internal/claim"
	"github.com/f0d0r/margaret-cli/internal/db"
	"github.com/f0d0r/margaret-cli/internal/filemeta"
	"github.com/f0d0r/margaret-cli/internal/headprint"
	"github.com/f0d0r/margaret-cli/internal/identifier"
	"github.com/f0d0r/margaret-cli/internal/lsh"
	"github.com/f0d0r/margaret-cli/internal/parser"
	"github.com/f0d0r/margaret-cli/internal/parser/pdf"
	"github.com/f0d0r/margaret-cli/internal/parser/registry"
	"github.com/f0d0r/margaret-cli/internal/scanner"
	"github.com/f0d0r/margaret-cli/internal/source"
	"github.com/f0d0r/margaret-cli/internal/tx"
	"github.com/f0d0r/margaret-ebook-library/book"
	"github.com/f0d0r/margaret-ebook-library/tools"
)

// ScanProcessorConfig configures a Processor. The zero value is not valid; use
// DefaultConfig as a starting point.
type ScanProcessorConfig struct {
	ScanWorkers  int
	ParseWorkers int

	// MaxDepth is the maximum archive nesting depth that is unpacked.
	// Depth 0 means the archive itself; an ebook inside is depth 1.
	MaxDepth int

	// SpoolMemLimit bounds how many bytes of a spooled archive member are
	// buffered in memory; anything beyond is spilled to a temporary file. A
	// zero value uses the default (64 MiB).
	SpoolMemLimit int64

	// MinDocSize is the minimum size in bytes for a document-format file (plain
	// text, RTF, PDF and later others) to be treated as a book. Zero uses
	// DefaultMinDocumentSize; a negative value disables the size filter.
	MinDocSize int64

	// PDFTools are the external Poppler binaries used for PDF extraction.
	// When either is missing, PDF files are skipped instead of processed.
	PDFTools pdf.Tools

	// Password decrypts password-protected rar and 7z archives. The
	// standard library cannot decrypt AES-encrypted zip members, so those
	// are still reported as failures.
	Password string

	// FTPUser and FTPPass are the credentials used to reach ftp:// roots.
	// Empty values fall back to the anonymous login.
	FTPUser string
	FTPPass string

	// Resume makes Process skip files whose path is already recorded and
	// only process never-seen paths. When false every scan writes all files
	// (callers typically Clear the database first).
	Resume bool

	// CollectExtStats makes Process count per-extension files (supported +
	// unsupported, archives excluded) on the filesystem and inside
	// archives. The counts are exposed via Stats().ExtCounts.
	CollectExtStats bool

	// OnResult is invoked for every successfully parsed ebook.
	OnResult func(Result)

	// OnProgress is invoked as the pipeline progresses.
	OnProgress func(Progress)
}

// DefaultMinDocumentSize is the default minimum size of a document-format
// book. It mirrors parser.DefaultMinDocumentSize so callers do not need to
// import the parser package.
const DefaultMinDocumentSize = parser.DefaultMinDocumentSize

// DefaultScanProcessorConfig returns the default Processor configuration.
func DefaultScanProcessorConfig() ScanProcessorConfig {
	n := runtime.NumCPU()
	return ScanProcessorConfig{
		ScanWorkers:  n,
		ParseWorkers: n,
		MaxDepth:     2,
		MinDocSize:   DefaultMinDocumentSize,
	}
}

// ScanProcessor ties the scanner and the parser together.
type ScanProcessor struct {
	cfg        ScanProcessorConfig
	parsers    map[string]parser.Parser
	root       string
	onResult   func(Result)
	onProgress func(Progress)

	db *sql.DB
	q  *db.Queries

	// ctx is the context of the current Process call, used to tell
	// cancellation errors apart from real failures.
	ctx context.Context

	parsed           atomic.Int64
	failed           atomic.Int64
	found            atomic.Int64
	skipped          atomic.Int64
	alreadyProcessed atomic.Int64
	progMu           sync.Mutex
	lastScan         scanner.Progress
	failMu           sync.Mutex
	failures         []Failure
	skipMu           sync.Mutex
	skips            []Skip

	extMu     sync.Mutex
	extCounts map[string]int64

	active  atomic.Int64
	curMu   sync.Mutex
	current []string
}

// NewScanProcessor creates a Processor that scans root with the given
// configuration.
func NewScanProcessor(sqldb *sql.DB, q *db.Queries, cfg ScanProcessorConfig, root string) *ScanProcessor {
	if cfg.ScanWorkers < 1 {
		cfg.ScanWorkers = 1
	}
	if cfg.ParseWorkers < 1 {
		cfg.ParseWorkers = 1
	}
	if cfg.MaxDepth < 0 {
		cfg.MaxDepth = 0
	}
	return &ScanProcessor{
		parsers:    registry.ParsersWithOptions(registry.Options{MinDocumentSize: cfg.MinDocSize, PDFTools: cfg.PDFTools}),
		cfg:        cfg,
		root:       root,
		onResult:   cfg.OnResult,
		onProgress: cfg.OnProgress,
		db:         sqldb,
		q:          q,
	}
}

// newSource returns the Source used by one parse worker.
func (p *ScanProcessor) newSource() (source.Source, error) {
	return source.SourceFactory(p.root, source.FTPOptions{User: p.cfg.FTPUser, Password: p.cfg.FTPPass})
}

// Process scans the configured root, extracts the metadata of every ebook
// found and invokes the configured callbacks. It returns a fatal error if the
// root itself is inaccessible; items that could not be processed are reported
// via Failures. Discovery and parsing are decoupled: the scanner pushes
// results into an unbounded queue, so a slow parser never stops the walk.
func (p *ScanProcessor) Process(ctx context.Context) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	p.ctx = ctx

	p.failMu.Lock()
	p.failures = p.failures[:0]
	p.failMu.Unlock()

	p.skipMu.Lock()
	p.skips = p.skips[:0]
	p.skipMu.Unlock()

	results := newResultQueue()

	ftpOpts := source.FTPOptions{User: p.cfg.FTPUser, Password: p.cfg.FTPPass}
	sc := scanner.New(scanner.Config{
		Workers:    p.cfg.ScanWorkers,
		FTPOptions: ftpOpts,
		OnResult: func(r scanner.Result) {
			if ctx.Err() != nil {
				return
			}
			results.push(r)
		},
		OnFile: func(name string) {
			p.recordExt(name)
		},
		OnReport: func(sp scanner.Progress) {
			p.setScanProgress(sp)
			p.report()
		},
		OnError: func(path string, err error) {
			p.fail(path, err)
			p.report()
		},
	})

	var (
		parseWG  sync.WaitGroup
		fatalMu  sync.Mutex
		fatalErr error
	)
	reportFatal := func(err error) {
		fatalMu.Lock()
		if fatalErr == nil {
			fatalErr = err
		}
		fatalMu.Unlock()
		cancel()
	}

	parseWG.Add(p.cfg.ParseWorkers)
	for i := 0; i < p.cfg.ParseWorkers; i++ {
		go func() {
			defer parseWG.Done()
			src, err := p.newSource()
			if err != nil {
				reportFatal(err)
				return
			}
			defer func() {
				_ = src.Close()
			}()
			for {
				r, ok := results.pop()
				if !ok {
					return
				}
				if ctx.Err() != nil {
					continue
				}
				p.beginWork(r.Path)
				p.processSource(ctx, r, src)
				p.endWork(r.Path)
			}
		}()
	}

	_, err := sc.Scan(ctx, p.root)
	if ctx.Err() != nil {
		results.abort()
	} else {
		results.close()
	}
	parseWG.Wait()

	fatalMu.Lock()
	fatal := fatalErr
	fatalMu.Unlock()

	if fatal != nil {
		return fatal
	}
	if err != nil {
		return err
	}
	return ctx.Err()
}

// Failures returns the items that could not be processed in the last Process
// run.
func (p *ScanProcessor) Failures() []Failure {
	p.failMu.Lock()
	defer p.failMu.Unlock()
	return append([]Failure(nil), p.failures...)
}

// Skips returns the actionable skips of the last Process run. Resume skips
// are not included; see Progress.AlreadyProcessed.
func (p *ScanProcessor) Skips() []Skip {
	p.skipMu.Lock()
	defer p.skipMu.Unlock()
	return append([]Skip(nil), p.skips...)
}

// pdfDisabled reports whether format is PDF and the Poppler tools are not
// available, in which case PDF files are skipped without being opened.
func (p *ScanProcessor) pdfDisabled(format string) bool {
	return format == "pdf" && !p.cfg.PDFTools.Available()
}

// belowMinSize lets a format-specific parser reject files that are too small
// to be a book before the processor opens or spools them. Unknown sizes are
// checked by the parser after reading. It returns the applicable size limit
// and whether size is below it.
func (p *ScanProcessor) belowMinSize(format string, size int64) (int64, bool) {
	if size < 0 {
		return 0, false
	}
	par := p.parsers[format]
	if par == nil {
		return 0, false
	}
	filter, ok := par.(parser.SizeFilter)
	if !ok {
		return 0, false
	}
	min := filter.MinSize()
	return min, min > 0 && size < min
}

// processSource handles a single unit of work: either an ebook to parse or
// an archive to unpack and process. src is the worker's Source, used to open
// the file on disk or over FTP.
func (p *ScanProcessor) processSource(ctx context.Context, r scanner.Result, src source.Source) {
	if ctx.Err() != nil {
		return
	}
	if scanner.IsArchiveFormat(r.Format) {
		p.processArchive(ctx, r, 0, src)
		return
	}
	if p.shouldSkip(ctx, r.Path) {
		p.skipAlreadyProcessed()
		return
	}
	if p.pdfDisabled(r.Format) {
		p.skip(r.Path, reasonPDFUnavailable)
		return
	}
	if minSize, skip := p.belowMinSize(r.Format, r.Size); skip {
		p.skip(r.Path, minSizeReason(r.Size, minSize))
		return
	}
	rc, err := src.Open(r.Path)
	if err != nil {
		p.fail(r.Path, err)
		p.report()
		return
	}
	defer func() {
		_ = rc.Close()
	}()
	if f, ok := rc.(*os.File); ok {
		b, err := book.NewFileBlob(f)
		if err != nil {
			p.fail(r.Path, err)
			p.report()
			return
		}
		p.parseEbook(ctx, r.Path, r.Format, b)
		return
	}
	// Remote (e.g. FTP) files are streamed and spooled lazily.
	p.parseEbookStream(ctx, r.Path, r.Format, r.Size, rc)
}

// parseEbook reads the metadata of one ebook and reports the result. b is the
// ebook content: for top-level files it is a blob over the file on disk, for
// archive and stream members it is a spoolBlob over the extracted content.
// The parser reads only what it needs, so members whose metadata lives at the
// start of the file are not fully materialized. displayPath is the
// user-facing path (an archive member like "a.zip!b.epub") used in reports.
func (p *ScanProcessor) parseEbook(ctx context.Context, displayPath, format string, b book.Blob) {
	if ctx.Err() != nil {
		return
	}
	par := p.parsers[format]
	if par == nil {
		if ctx.Err() != nil {
			return
		}
		err := fmt.Errorf("no parser registered for format %q", format)
		p.fail(displayPath, err)
		p.report()
		return
	}
	md, err := par.Parse(ctx, b)
	if err != nil {
		if ctx.Err() != nil {
			return
		}
		if errors.Is(err, parser.ErrNotABook) {
			p.skip(displayPath, notABookReason(err))
			return
		}
		if errors.Is(err, pdf.ErrToolsUnavailable) {
			p.skip(displayPath, reasonPDFUnavailable)
			return
		}
		p.fail(displayPath, err)
		p.report()
		return
	}

	claimSets := append([]claim.Set{}, md.Claims...)
	// Legacy parsers do not populate claims at all. Treat their existing
	// title/author result as a metadata hypothesis; parsers that do populate
	// claims must mark their own metadata source explicitly.
	if len(claimSets) == 0 && (md.Title != "" || len(md.Authors) > 0) {
		claimSets = append(claimSets, claim.Set{
			Key:     "metadata",
			Source:  claim.SourceMetadata,
			Origin:  "parser",
			Title:   md.Title,
			Authors: md.Authors,
			Trust:   claim.TrustStrong,
			Witness: claim.WitnessIndependent,
		})
	}
	titleWindow := encodeStringList(claim.WindowLines(md.Head))
	claims := claim.Build(append(claimSets, filenameClaims(displayPath)...), md.Head, displayPath)
	resolution := claim.Resolve(claims, displayPath)

	err = tx.WithTx(ctx, p.db, p.q, func(txCtx context.Context) error {
		q := tx.QueryFrom(txCtx, p.q)

		if md.Hash == "" {
			return nil
		}

		headBloom := headprint.Build(md.Head)
		// A recorded path is backfilled when its content fingerprint or claim
		// state is missing; a changed file keeps its old row until a fresh run.
		if existing, err := q.GetBookFileByPath(txCtx, displayPath); err == nil {
			if existing.Hash != md.Hash {
				return nil
			}
			needsHead := len(existing.HeadBloom) == 0
			needsWindow := existing.TitleWindow == "" && titleWindow != ""
			needsClaims := existing.ClaimVersion < claim.ClaimVersion ||
				existing.ResolveVersion < claim.ResolveVersion
			if needsHead {
				if err := q.UpdateBookFileHeadBloom(txCtx, db.UpdateBookFileHeadBloomParams{
					HeadBloom: headBloom,
					ID:        existing.ID,
				}); err != nil {
					return fmt.Errorf("update content head: %w", err)
				}
			}
			if needsWindow {
				if err := q.UpdateBookFileTitleWindow(txCtx, db.UpdateBookFileTitleWindowParams{
					TitleWindow: titleWindow,
					ID:          existing.ID,
				}); err != nil {
					return fmt.Errorf("update title window: %w", err)
				}
			}
			if needsClaims {
				if err := updateBookFileClaims(txCtx, q, existing.ID, md, resolution, claims, titleWindow); err != nil {
					return err
				}
			}
			return nil
		}

		// Resume already skipped every recorded path, so this path is new:
		// insert it directly.
		authorIDs, err := ensureAuthorIDs(txCtx, q, resolution.Authors)
		if err != nil {
			return err
		}

		bookFileID, err := q.CreateBookFile(txCtx, db.CreateBookFileParams{
			Hash:            md.Hash,
			Path:            displayPath,
			Title:           resolution.Title,
			Minhash:         encodeMinHash(md.MinHash),
			HeadBloom:       headBloom,
			TitleWindow:     titleWindow,
			Description:     md.Description,
			Publisher:       md.Publisher,
			PublicationDate: md.PublicationDate,
			Series:          md.Series,
			SeriesIndex:     md.SeriesIndex,
			LegacyTitle:     md.Title,
			TitleStatus:     string(resolution.TitleStatus),
			AuthorStatus:    string(resolution.AuthorStatus),
			TitleSet:        resolution.TitleSet,
			AuthorSet:       resolution.AuthorSet,
			ClaimVersion:    claim.ClaimVersion,
			ResolveVersion:  claim.ResolveVersion,
		})
		if errors.Is(err, sql.ErrNoRows) {
			if err := q.CreateBookFileDuplicate(txCtx, db.CreateBookFileDuplicateParams{
				Hash: md.Hash,
				Path: displayPath,
			}); err != nil {
				return fmt.Errorf("record duplicate: %w", err)
			}
			return nil
		}
		if err != nil {
			return fmt.Errorf("create book file: %w", err)
		}

		if err := storeBookFileMetadata(txCtx, q, bookFileID, md); err != nil {
			return err
		}
		if err := storeBookFileClaims(txCtx, q, bookFileID, claims); err != nil {
			return err
		}
		if err := storeLegacyAuthors(txCtx, q, bookFileID, md.Authors); err != nil {
			return err
		}

		for _, authorID := range authorIDs {
			if err := q.CreateBookFileAuthor(txCtx, db.CreateBookFileAuthorParams{
				BookFileID: bookFileID,
				AuthorID:   authorID,
			}); err != nil {
				return fmt.Errorf("link author: %w", err)
			}
		}

		bands := lsh.ComputeBands(md.MinHash)
		var bestMatchFileID int64
		var bestSimilarity float64

		if len(bands) > 0 {
			candidates := make(map[int64][]byte)
			for _, b := range bands {
				rows, err := q.FindCandidateMinHashesByBand(txCtx, db.FindCandidateMinHashesByBandParams{
					BandIdx:    int64(b.BandIdx),
					BucketHash: b.BucketHash,
				})
				if err != nil {
					return fmt.Errorf("find lsh candidates: %w", err)
				}
				for _, row := range rows {
					if row.BookFileID != bookFileID {
						candidates[row.BookFileID] = row.Minhash
					}
				}
			}

			for candID, candBlob := range candidates {
				sim := tools.Jaccard(md.MinHash, decodeMinHash(candBlob))
				if sim >= lsh.MinSimilarityThreshold && sim > bestSimilarity {
					bestSimilarity = sim
					bestMatchFileID = candID
				}
			}
		}

		var bookID int64
		if bestMatchFileID != 0 {
			// The canonical title is derived later by report.Consolidate
			// from all member files, so the transient raw title is left
			// untouched here.
			existingBookID, err := q.GetBookIDByBookFileID(txCtx, bestMatchFileID)
			if err == nil {
				bookID = existingBookID
			}
		}

		if bookID == 0 {
			title := resolution.Title
			if title == "" {
				title = md.Title
			}
			newBookID, err := q.CreateBook(txCtx, title)
			if err != nil {
				return fmt.Errorf("create book: %w", err)
			}
			bookID = newBookID
		}

		if err := q.CreateBookBookFile(txCtx, db.CreateBookBookFileParams{
			BookID:     bookID,
			BookFileID: bookFileID,
		}); err != nil {
			return fmt.Errorf("link book file: %w", err)
		}

		for _, authorID := range authorIDs {
			if err := q.CreateBookAuthor(txCtx, db.CreateBookAuthorParams{
				BookID:   bookID,
				AuthorID: authorID,
			}); err != nil {
				return fmt.Errorf("link book author: %w", err)
			}
		}

		for _, b := range bands {
			if err := q.CreateLSHBucket(txCtx, db.CreateLSHBucketParams{
				BandIdx:    int64(b.BandIdx),
				BucketHash: b.BucketHash,
				BookFileID: bookFileID,
			}); err != nil {
				return fmt.Errorf("create lsh bucket: %w", err)
			}
		}

		return nil
	})
	if err != nil {
		if ctx.Err() != nil {
			return
		}
		p.fail(displayPath, err)
		p.report()
		return
	}

	p.parsed.Add(1)
	if p.onResult != nil {
		p.onResult(Result{File: scanner.Result{Path: displayPath, Format: format}, Metadata: md})
	}
	p.report()
}

// storeBookFileMetadata stores the multi-valued metadata of a book file.
func storeBookFileMetadata(ctx context.Context, q *db.Queries, bookFileID int64, md parser.Metadata) error {
	for i, name := range md.Subjects {
		if err := q.CreateBookFileSubject(ctx, db.CreateBookFileSubjectParams{
			BookFileID: bookFileID,
			Name:       name,
			Ordinal:    int64(i),
		}); err != nil {
			return fmt.Errorf("store subject %q: %w", name, err)
		}
	}
	for i, name := range md.Languages {
		if err := q.CreateBookFileLanguage(ctx, db.CreateBookFileLanguageParams{
			BookFileID: bookFileID,
			Name:       name,
			Ordinal:    int64(i),
		}); err != nil {
			return fmt.Errorf("store language %q: %w", name, err)
		}
	}
	for i, item := range md.Identifiers {
		scheme, value := item.Scheme, item.Value
		if identifier.IsISBNScheme(scheme) {
			isbn, ok := tools.ValidateBookISBN(value)
			if !ok {
				continue
			}
			isbn13, ok := isbn.ToISBN13()
			if !ok {
				continue
			}
			scheme, value = "ISBN", isbn13.String()
		}
		if err := q.CreateBookFileIdentifier(ctx, db.CreateBookFileIdentifierParams{
			BookFileID: bookFileID,
			Scheme:     scheme,
			Value:      value,
			Source:     string(item.Source),
			Ordinal:    int64(i),
		}); err != nil {
			return fmt.Errorf("store identifier %s:%s: %w", scheme, value, err)
		}
	}
	return nil
}

// filenameClaims turns the display path into low-trust filename hypotheses.
func filenameClaims(path string) []claim.Set {
	repaired := filemeta.RepairDisplayPath(path)
	origin := func(name string) string {
		if repaired != path {
			return name + ":repaired"
		}
		return name
	}
	var sets []claim.Set
	add := func(key, origin, title string, authors []string) {
		if title == "" && len(authors) == 0 {
			return
		}
		sets = append(sets, claim.Set{
			Key:     key,
			Source:  claim.SourceFilename,
			Origin:  origin,
			Path:    path,
			Title:   title,
			Authors: authors,
			Trust:   claim.TrustWeak,
			Witness: claim.WitnessSupporting,
		})
	}
	if title, author, ok := filemeta.ParseNaturalFilename(repaired); ok {
		add("filename:natural", origin("filename:natural"), title, []string{author})
	}
	if title, author, ok := filemeta.ParseCalibreFilename(repaired); ok {
		add("filename:calibre", origin("filename:calibre"), title, []string{author})
	}
	if author, ok := filemeta.ParseCalibreAuthorOnly(repaired); ok {
		add("filename:calibre-author", origin("filename:calibre-author"), "", []string{author})
	}
	if title := filemeta.TitleFromPath(repaired); title != "" {
		add("filename:stem", origin("filename:stem"), title, nil)
	}
	for i, segment := range filemeta.ArchiveNames(repaired) {
		if title, author, ok := filemeta.ParseArchiveFilename(segment); ok {
			key := "filename:archive"
			if i > 0 {
				key = fmt.Sprintf("filename:archive:%d", i)
			}
			add(key, origin("filename:archive"), title, []string{author})
		}
	}
	if author, ok := filemeta.ParentAuthor(repaired); ok {
		add("filename:path-author", origin("filename:path-author"), "", []string{author})
	}
	return sets
}

func ensureAuthorIDs(ctx context.Context, q *db.Queries, names []string) ([]int64, error) {
	ids := make([]int64, 0, len(names))
	seen := make(map[string]struct{}, len(names))
	for _, name := range names {
		if name == "" {
			continue
		}
		if _, ok := seen[name]; ok {
			continue
		}
		seen[name] = struct{}{}
		author, err := q.GetAuthorByName(ctx, name)
		if errors.Is(err, sql.ErrNoRows) {
			if err := q.CreateAuthor(ctx, name); err != nil {
				return nil, fmt.Errorf("create author: %w", err)
			}
			author, err = q.GetAuthorByName(ctx, name)
		}
		if err != nil {
			return nil, fmt.Errorf("get author %q: %w", name, err)
		}
		ids = append(ids, author.ID)
	}
	return ids, nil
}

func storeBookFileClaims(ctx context.Context, q *db.Queries, bookFileID int64, claims []claim.Claim) error {
	for _, c := range claims {
		if err := q.CreateBookFileClaim(ctx, db.CreateBookFileClaimParams{
			BookFileID:          bookFileID,
			Path:                c.Path,
			SetKey:              c.Set,
			Source:              string(c.Source),
			Origin:              c.Origin,
			Field:               string(c.Field),
			Value:               c.Value,
			Ordinal:             int64(c.Ordinal),
			Junk:                boolInt(c.Junk),
			Trust:               string(c.Trust),
			Witness:             string(c.Witness),
			Attested:            boolInt(c.Attested),
			Attestation:         c.Attestation,
			AttestationMethod:   c.AttestationMethod,
			AttestationLevel:    string(c.AttestationLevel),
			AttestationResidual: encodeStringList(c.AttestationResidual),
			SuspiciousSignals:   c.SuspiciousSignals,
		}); err != nil {
			return fmt.Errorf("store claim %s %q: %w", c.Field, c.Value, err)
		}
	}
	return nil
}

func storeLegacyAuthors(ctx context.Context, q *db.Queries, bookFileID int64, names []string) error {
	for i, name := range names {
		if name == "" {
			continue
		}
		if err := q.CreateBookFileLegacyAuthor(ctx, db.CreateBookFileLegacyAuthorParams{
			BookFileID: bookFileID,
			Name:       name,
			Ordinal:    int64(i),
		}); err != nil {
			return fmt.Errorf("store legacy author %q: %w", name, err)
		}
	}
	return nil
}

func updateBookFileClaims(ctx context.Context, q *db.Queries, bookFileID int64, md parser.Metadata, res claim.Resolution, claims []claim.Claim, titleWindow string) error {
	if err := q.UpdateBookFileTitleWindow(ctx, db.UpdateBookFileTitleWindowParams{
		TitleWindow: titleWindow,
		ID:          bookFileID,
	}); err != nil {
		return fmt.Errorf("update title window: %w", err)
	}
	if err := q.UpdateBookFileLegacyTitle(ctx, db.UpdateBookFileLegacyTitleParams{
		LegacyTitle: md.Title,
		ID:          bookFileID,
	}); err != nil {
		return fmt.Errorf("update legacy title: %w", err)
	}
	if err := q.UpdateBookFileResolution(ctx, db.UpdateBookFileResolutionParams{
		Title:          res.Title,
		TitleStatus:    string(res.TitleStatus),
		AuthorStatus:   string(res.AuthorStatus),
		TitleSet:       res.TitleSet,
		AuthorSet:      res.AuthorSet,
		ClaimVersion:   claim.ClaimVersion,
		ResolveVersion: claim.ResolveVersion,
		ID:             bookFileID,
	}); err != nil {
		return fmt.Errorf("update claims resolution: %w", err)
	}
	if err := q.DeleteBookFileClaimsByBookFileID(ctx, bookFileID); err != nil {
		return fmt.Errorf("delete claims: %w", err)
	}
	if err := storeBookFileClaims(ctx, q, bookFileID, claims); err != nil {
		return err
	}
	if err := q.DeleteBookFileLegacyAuthorsByBookFileID(ctx, bookFileID); err != nil {
		return fmt.Errorf("delete legacy authors: %w", err)
	}
	if err := storeLegacyAuthors(ctx, q, bookFileID, md.Authors); err != nil {
		return err
	}
	authorIDs, err := ensureAuthorIDs(ctx, q, res.Authors)
	if err != nil {
		return err
	}
	if err := q.DeleteBookFileAuthorsByBookFileID(ctx, bookFileID); err != nil {
		return fmt.Errorf("delete file authors: %w", err)
	}
	for _, authorID := range authorIDs {
		if err := q.CreateBookFileAuthor(ctx, db.CreateBookFileAuthorParams{
			BookFileID: bookFileID,
			AuthorID:   authorID,
		}); err != nil {
			return fmt.Errorf("link author: %w", err)
		}
	}
	return nil
}

// ResolveStaleClaims re-runs the resolution step for files whose claims are
// current but whose resolve_version is stale. It needs no file access, so
// threshold and conflict-rule tuning does not require a reparse.
func ResolveStaleClaims(ctx context.Context, conn *sql.DB, q *db.Queries) error {
	rows, err := q.ListBookFilesNeedingResolution(ctx, db.ListBookFilesNeedingResolutionParams{
		ClaimVersion:   claim.ClaimVersion,
		ResolveVersion: claim.ResolveVersion,
	})
	if err != nil {
		return fmt.Errorf("list files needing claim resolution: %w", err)
	}
	if len(rows) == 0 {
		return nil
	}
	return tx.WithTx(ctx, conn, q, func(txCtx context.Context) error {
		txq := tx.QueryFrom(txCtx, q)
		for _, row := range rows {
			stored, err := txq.ListBookFileClaimsByBookFileID(txCtx, row.ID)
			if err != nil {
				return fmt.Errorf("list claims of file %d: %w", row.ID, err)
			}
			if len(stored) == 0 {
				continue
			}
			res := claim.Resolve(dbClaims(stored), row.Path)
			if err := txq.UpdateBookFileResolution(txCtx, db.UpdateBookFileResolutionParams{
				Title:          res.Title,
				TitleStatus:    string(res.TitleStatus),
				AuthorStatus:   string(res.AuthorStatus),
				TitleSet:       res.TitleSet,
				AuthorSet:      res.AuthorSet,
				ClaimVersion:   claim.ClaimVersion,
				ResolveVersion: claim.ResolveVersion,
				ID:             row.ID,
			}); err != nil {
				return fmt.Errorf("update resolution of file %d: %w", row.ID, err)
			}
			authorIDs, err := ensureAuthorIDs(txCtx, txq, res.Authors)
			if err != nil {
				return err
			}
			if err := txq.DeleteBookFileAuthorsByBookFileID(txCtx, row.ID); err != nil {
				return fmt.Errorf("delete file authors: %w", err)
			}
			for _, authorID := range authorIDs {
				if err := txq.CreateBookFileAuthor(txCtx, db.CreateBookFileAuthorParams{
					BookFileID: row.ID,
					AuthorID:   authorID,
				}); err != nil {
					return fmt.Errorf("link author: %w", err)
				}
			}
		}
		return nil
	})
}

func dbClaims(stored []db.BookFileClaim) []claim.Claim {
	out := make([]claim.Claim, 0, len(stored))
	for _, c := range stored {
		out = append(out, claim.Claim{
			Field:               claim.Field(c.Field),
			Value:               c.Value,
			Source:              claim.Source(c.Source),
			Set:                 c.SetKey,
			Origin:              c.Origin,
			Path:                c.Path,
			Ordinal:             int(c.Ordinal),
			Junk:                c.Junk != 0,
			Trust:               claim.Trust(c.Trust),
			Witness:             claim.Witness(c.Witness),
			Attested:            c.Attested != 0,
			Attestation:         c.Attestation,
			AttestationMethod:   c.AttestationMethod,
			AttestationLevel:    claim.AttestLevel(c.AttestationLevel),
			AttestationResidual: decodeStringList(c.AttestationResidual),
			SuspiciousSignals:   c.SuspiciousSignals,
		})
	}
	return out
}

func boolInt(v bool) int64 {
	if v {
		return 1
	}
	return 0
}

func encodeStringList(values []string) string {
	if len(values) == 0 {
		return "[]"
	}
	data, err := json.Marshal(values)
	if err != nil {
		return "[]"
	}
	return string(data)
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

// encodeMinHash encodes a MinHash signature ([]uint64) as a BLOB (little-endian).
func encodeMinHash(sig []uint64) []byte {
	if len(sig) == 0 {
		return nil
	}
	b := make([]byte, len(sig)*8)
	for i, v := range sig {
		binary.LittleEndian.PutUint64(b[i*8:], v)
	}
	return b
}

// decodeMinHash decodes a BLOB back to a MinHash signature.
func decodeMinHash(b []byte) []uint64 {
	if len(b) == 0 {
		return nil
	}
	n := len(b) / 8
	sig := make([]uint64, n)
	for i := range n {
		sig[i] = binary.LittleEndian.Uint64(b[i*8:])
	}
	return sig
}

// recordExt counts one file name towards the per-extension statistics.
// Supported archives are treated as folders and excluded. It is a no-op
// unless CollectExtStats is enabled. It is safe for concurrent use.
func (p *ScanProcessor) recordExt(name string) {
	if !p.cfg.CollectExtStats {
		return
	}
	ext, isArchive := scanner.ExtKey(name)
	if isArchive {
		return
	}
	p.extMu.Lock()
	if p.extCounts == nil {
		p.extCounts = make(map[string]int64)
	}
	p.extCounts[ext]++
	p.extMu.Unlock()
}

// extCountsCopy returns a copy of the collected extension counts, or nil
// when collection was disabled or nothing was counted.
func (p *ScanProcessor) extCountsCopy() map[string]int64 {
	p.extMu.Lock()
	defer p.extMu.Unlock()
	if len(p.extCounts) == 0 {
		return nil
	}
	out := make(map[string]int64, len(p.extCounts))
	maps.Copy(out, p.extCounts)
	return out
}

// fail records an item that could not be processed. Cancellation is not a
// failure: an interrupted item is left unrecorded so a resume run retries it.
func (p *ScanProcessor) fail(path string, err error) {
	if p.ctx != nil && p.ctx.Err() != nil {
		return
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return
	}
	p.failed.Add(1)
	p.failMu.Lock()
	p.failures = append(p.failures, Failure{Path: path, Err: err})
	p.failMu.Unlock()
}

// beginWork marks a unit of work as in flight so report consumers can show
// which item is being processed and keep an animation running while a slow
// item produces no counter movement.
func (p *ScanProcessor) beginWork(path string) {
	p.active.Add(1)
	p.curMu.Lock()
	p.current = append(p.current, path)
	if len(p.current) > 4 {
		p.current = p.current[len(p.current)-4:]
	}
	p.curMu.Unlock()
}

// endWork removes a unit of work tracked by beginWork.
func (p *ScanProcessor) endWork(path string) {
	p.active.Add(-1)
	p.curMu.Lock()
	for i, c := range p.current {
		if c == path {
			p.current = append(p.current[:i], p.current[i+1:]...)
			break
		}
	}
	p.curMu.Unlock()
}

func (p *ScanProcessor) report() {
	if p.onProgress == nil {
		return
	}
	p.onProgress(p.Stats())
}

func (p *ScanProcessor) setScanProgress(sp scanner.Progress) {
	p.progMu.Lock()
	p.lastScan = sp
	p.progMu.Unlock()
}

// Stats returns the current progress counters. After Process has returned
// the values are final and can be used to build a summary report.
func (p *ScanProcessor) Stats() Progress {
	p.progMu.Lock()
	sp := p.lastScan
	p.progMu.Unlock()
	p.curMu.Lock()
	cur := append([]string(nil), p.current...)
	p.curMu.Unlock()
	return Progress{
		Scan:             sp,
		Found:            sp.Found + p.found.Load(),
		Parsed:           p.parsed.Load(),
		Failed:           p.failed.Load(),
		Skipped:          p.skipped.Load(),
		AlreadyProcessed: p.alreadyProcessed.Load(),
		ExtCounts:        p.extCountsCopy(),
		Active:           p.active.Load(),
		Current:          cur,
	}
}
