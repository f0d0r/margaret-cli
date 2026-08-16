package processor

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"runtime"
	"sync"
	"sync/atomic"

	"github.com/f0d0r/margaret-ebook-library/pkg/model"
	"github.com/f0d0r/margaret-tools/internal/db"
	"github.com/f0d0r/margaret-tools/internal/parser"
	"github.com/f0d0r/margaret-tools/internal/scanner"
	"github.com/f0d0r/margaret-tools/internal/source"
	"github.com/f0d0r/margaret-tools/internal/tx"
)

// ScanProcessorConfig configures a Processor. The zero value is not valid; use
// DefaultConfig as a starting point.
type ScanProcessorConfig struct {
	ScanWorkers  int
	ParseWorkers int
	QueueSize    int

	// MaxDepth is the maximum archive nesting depth that is unpacked.
	// Depth 0 means the archive itself; an ebook inside is depth 1.
	MaxDepth int

	// SpoolMemLimit bounds how many bytes of a spooled archive member are
	// buffered in memory; anything beyond is spilled to a temporary file. A
	// zero value uses the default (64 MiB).
	SpoolMemLimit int64

	// Password decrypts password-protected rar and 7z archives. The
	// standard library cannot decrypt AES-encrypted zip members, so those
	// are still reported as failures.
	Password string

	// FTPUser and FTPPass are the credentials used to reach ftp:// roots.
	// Empty values fall back to the anonymous login.
	FTPUser string
	FTPPass string

	// OnResult is invoked for every successfully parsed ebook.
	OnResult func(Result)

	// OnProgress is invoked as the pipeline progresses.
	OnProgress func(Progress)
}

// DefaultScanProcessorConfig returns the default Processor configuration.
func DefaultScanProcessorConfig() ScanProcessorConfig {
	n := runtime.NumCPU()
	return ScanProcessorConfig{
		ScanWorkers:  n,
		ParseWorkers: n,
		QueueSize:    n * 2,
		MaxDepth:     2,
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

	parsed   atomic.Int64
	failed   atomic.Int64
	found    atomic.Int64
	progMu   sync.Mutex
	lastScan scanner.Progress
	failMu   sync.Mutex
	failures []Failure

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
	if cfg.QueueSize < 1 {
		cfg.QueueSize = 1
	}
	if cfg.MaxDepth < 0 {
		cfg.MaxDepth = 0
	}
	return &ScanProcessor{
		parsers:    parser.Parsers(),
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
// via Failures.
func (p *ScanProcessor) Process(ctx context.Context) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	p.failMu.Lock()
	p.failures = p.failures[:0]
	p.failMu.Unlock()

	work := make(chan scanner.Result, p.cfg.QueueSize)

	ftpOpts := source.FTPOptions{User: p.cfg.FTPUser, Password: p.cfg.FTPPass}
	sc := scanner.New(scanner.Config{
		Workers:    p.cfg.ScanWorkers,
		FTPOptions: ftpOpts,
		OnResult: func(r scanner.Result) {
			select {
			case work <- r:
			case <-ctx.Done():
			}
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
			for r := range work {
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
	close(work)
	parseWG.Wait()

	fatalMu.Lock()
	fatal := fatalErr
	fatalMu.Unlock()

	if fatal != nil {
		return fatal
	}
	return err
}

// Failures returns the items that could not be processed in the last Process
// run.
func (p *ScanProcessor) Failures() []Failure {
	p.failMu.Lock()
	defer p.failMu.Unlock()
	return append([]Failure(nil), p.failures...)
}

// processSource handles a single unit of work: either an ebook to parse or
// an archive to unpack and process. src is the worker's Source, used to open
// the file on disk or over FTP.
func (p *ScanProcessor) processSource(ctx context.Context, r scanner.Result, src source.Source) {
	if scanner.IsArchiveFormat(r.Format) {
		p.processArchive(ctx, r, 0, src)
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
		b, err := model.NewFileBlob(f)
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
func (p *ScanProcessor) parseEbook(ctx context.Context, displayPath, format string, b model.Blob) {
	if ctx.Err() != nil {
		return
	}
	par := p.parsers[format]
	if par == nil {
		p.fail(displayPath, fmt.Errorf("no parser registered for format %q", format))
		p.report()
		return
	}
	md, err := par.Parse(b)
	if err != nil {
		p.fail(displayPath, err)
		p.report()
		return
	}

	err = tx.WithTx(ctx, p.db, p.q, func(txCtx context.Context) error {
		q := tx.QueryFrom(txCtx, p.q)

		if md.Hash == "" {
			return nil
		}

		authorIDs := make([]int64, 0, len(md.Authors))
		for _, authorName := range md.Authors {
			author, err := q.GetAuthorByName(txCtx, authorName)
			if errors.Is(err, sql.ErrNoRows) {
				if err := q.CreateAuthor(txCtx, authorName); err != nil {
					return fmt.Errorf("create author: %w", err)
				}
				author, err = q.GetAuthorByName(txCtx, authorName)
			}
			if err != nil {
				return fmt.Errorf("get author %q: %w", authorName, err)
			}
			authorIDs = append(authorIDs, author.ID)
		}

		bookID, err := q.CreateBookFile(txCtx, db.CreateBookFileParams{
			Hash:  md.Hash,
			Path:  displayPath,
			Title: md.Title,
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

		for _, authorID := range authorIDs {
			if err := q.CreateBookFileAuthor(txCtx, db.CreateBookFileAuthorParams{
				BookFileID: bookID,
				AuthorID:   authorID,
			}); err != nil {
				return fmt.Errorf("link author: %w", err)
			}
		}
		return nil
	})
	if err != nil {
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

// fail records an item that could not be processed.
func (p *ScanProcessor) fail(path string, err error) {
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
		Scan:    sp,
		Found:   sp.Found + p.found.Load(),
		Parsed:  p.parsed.Load(),
		Failed:  p.failed.Load(),
		Active:  p.active.Load(),
		Current: cur,
	}
}
