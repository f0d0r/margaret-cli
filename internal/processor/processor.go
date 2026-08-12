// Package processor runs the scan and metadata extraction pipeline.
package processor

import (
	"context"
	"io"
	"os"
	"runtime"
	"sync"
	"sync/atomic"

	"github.com/f0d0r/margaret-tools/internal/parser"
	"github.com/f0d0r/margaret-tools/internal/scanner"
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
	cfg        Config
	parser     parser.Parser
	onResult   func(Result)
	onProgress func(Progress)

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
		parser:     p,
		cfg:        cfg,
		onResult:   cfg.OnResult,
		onProgress: cfg.OnProgress,
	}
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
		Workers: p.cfg.ScanWorkers,
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

	var parseWG sync.WaitGroup
	parseWG.Add(p.cfg.ParseWorkers)
	for i := 0; i < p.cfg.ParseWorkers; i++ {
		go func() {
			defer parseWG.Done()
			for r := range work {
				if ctx.Err() != nil {
					continue
				}
				p.beginWork(r.Path)
				p.processSource(ctx, r)
				p.endWork(r.Path)
			}
		}()
	}

	_, err := sc.Scan(ctx, root)
	close(work)
	parseWG.Wait()

	p.failMu.Lock()
	failures := append([]Failure(nil), p.failures...)
	p.failMu.Unlock()
	return failures, err
}

// processSource handles a single unit of work: either an ebook to parse or
// an archive to unpack and process.
func (p *Processor) processSource(ctx context.Context, r scanner.Result) {
	if scanner.IsArchiveFormat(r.Format) {
		p.processArchive(ctx, r, 0)
		return
	}
	f, err := os.Open(r.Path)
	if err != nil {
		p.fail(r.Path, err)
		p.report()
		return
	}
	defer func () {
		_ = f.Close()
	}()
	p.parseEbook(ctx, r.Path, r.Format, f)
}

// parseEbook reads the metadata of one ebook from r and reports the result.
func (p *Processor) parseEbook(ctx context.Context, displayPath, format string, r io.Reader) {
	if ctx.Err() != nil {
		return
	}
	md, err := p.parser.Parse(ctx, r, format)
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
