// Package processor runs the scan and metadata extraction pipeline.
package processor

import (
	"context"
	"os"
	"runtime"
	"sync"
	"sync/atomic"

	"github.com/f0d0r/margaret-ebook-library/pkg/model"
	"github.com/f0d0r/margaret-tools/internal/parser"
	"github.com/f0d0r/margaret-tools/internal/scanner"
	"github.com/f0d0r/margaret-tools/internal/source"
)

// Result is an ebook together with its extracted metadata.
type Result struct {
	File     scanner.Result
	Metadata parser.Metadata
}

// Failure is an item (a directory or a file) that could not be processed.
type Failure struct {
	Path string
	Err  error
}

// Progress reports the state of a running Process call.
type Progress struct {
	Scan   scanner.Progress
	Found  int64
	Parsed int64
	Failed int64

	// Active is the number of units of work currently in flight. Current
	// holds the paths of the items being processed, newest last, capped so
	// the report stays small. These let consumers keep showing progress (and
	// an animated cursor) even while a single slow item produces no counter
	// movement.
	Active  int64
	Current []string
}

// Config configures a Processor. The zero value is not valid; use
// DefaultConfig as a starting point.
type Config struct {
	ScanWorkers  int
	ParseWorkers int
	QueueSize    int

	// MaxDepth is the maximum archive nesting depth that is unpacked.
	// Depth 0 means the archive itself; an ebook inside is depth 1.
	MaxDepth int

	// SourceFactory returns a fresh Source for every parse worker. When nil,
	// the local filesystem is used.
	SourceFactory source.Factory

	// SpoolMemLimit bounds how many bytes of a spooled archive member are
	// buffered in memory; anything beyond is spilled to a temporary file. A
	// zero value uses the default (64 MiB).
	SpoolMemLimit int64

	// Password decrypts password-protected rar and 7z archives. The
	// standard library cannot decrypt AES-encrypted zip members, so those
	// are still reported as failures.
	Password string

	// OnResult is invoked for every successfully parsed ebook.
	OnResult func(Result)

	// OnProgress is invoked as the pipeline progresses.
	OnProgress func(Progress)
}

// DefaultConfig returns the default Processor configuration.
func DefaultConfig() Config {
	n := runtime.NumCPU()
	return Config{
		ScanWorkers:  n,
		ParseWorkers: n,
		QueueSize:    n * 2,
		MaxDepth:     2,
	}
}

// Processor ties the scanner and the parser together.
type Processor struct {
	cfg           Config
	parser        parser.Parser
	onResult      func(Result)
	onProgress    func(Progress)
	sourceFactory source.Factory

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

// New creates a Processor that uses the given parser and configuration.
func New(p parser.Parser, cfg Config) *Processor {
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
	return &Processor{
		parser:        p,
		cfg:           cfg,
		onResult:      cfg.OnResult,
		onProgress:    cfg.OnProgress,
		sourceFactory: cfg.SourceFactory,
	}
}

// newSource returns the Source used by one parse worker. The zero value (nil
// factory) means the local filesystem.
func (p *Processor) newSource() (source.Source, error) {
	if p.sourceFactory == nil {
		return source.LocalSource{}, nil
	}
	return p.sourceFactory()
}

// Process scans root, extracts the metadata of every ebook found and
// invokes the configured callbacks. It returns the list of items that
// could not be processed and a fatal error if root itself is inaccessible.
func (p *Processor) Process(ctx context.Context, root string) ([]Failure, error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	p.failMu.Lock()
	p.failures = p.failures[:0]
	p.failMu.Unlock()

	work := make(chan scanner.Result, p.cfg.QueueSize)

	sc := scanner.New(scanner.Config{
		Workers:       p.cfg.ScanWorkers,
		SourceFactory: p.cfg.SourceFactory,
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

	_, err := sc.Scan(ctx, root)
	close(work)
	parseWG.Wait()

	fatalMu.Lock()
	fatal := fatalErr
	fatalMu.Unlock()

	p.failMu.Lock()
	failures := append([]Failure(nil), p.failures...)
	p.failMu.Unlock()
	if fatal != nil {
		return failures, fatal
	}
	return failures, err
}

// processSource handles a single unit of work: either an ebook to parse or
// an archive to unpack and process. src is the worker's Source, used to open
// the file on disk or over FTP.
func (p *Processor) processSource(ctx context.Context, r scanner.Result, src source.Source) {
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
func (p *Processor) parseEbook(ctx context.Context, displayPath, format string, b model.Blob) {
	if ctx.Err() != nil {
		return
	}
	md, err := p.parser.Parse(b)
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
func (p *Processor) fail(path string, err error) {
	p.failed.Add(1)
	p.failMu.Lock()
	p.failures = append(p.failures, Failure{Path: path, Err: err})
	p.failMu.Unlock()
}

// beginWork marks a unit of work as in flight so report consumers can show
// which item is being processed and keep an animation running while a slow
// item produces no counter movement.
func (p *Processor) beginWork(path string) {
	p.active.Add(1)
	p.curMu.Lock()
	p.current = append(p.current, path)
	if len(p.current) > 4 {
		p.current = p.current[len(p.current)-4:]
	}
	p.curMu.Unlock()
}

// endWork removes a unit of work tracked by beginWork.
func (p *Processor) endWork(path string) {
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

func (p *Processor) report() {
	if p.onProgress == nil {
		return
	}
	p.onProgress(p.Stats())
}

func (p *Processor) setScanProgress(sp scanner.Progress) {
	p.progMu.Lock()
	p.lastScan = sp
	p.progMu.Unlock()
}

// Stats returns the current progress counters. After Process has returned
// the values are final and can be used to build a summary report.
func (p *Processor) Stats() Progress {
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
