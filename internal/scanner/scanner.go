// Package scanner walks a directory tree in parallel and reports ebook files.
package scanner

import (
	"context"
	"fmt"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/f0d0r/margaret-tools/internal/source"
)

var knownExtensions = map[string]string{
	".epub": "epub",
	".mobi": "mobi",
	".azw3": "azw3",
	".azw":  "azw",
	".prc":  "prc",
}

var archiveExtensions = map[string]string{
	".zip":  "zip",
	".tar":  "tar",
	".tgz":  "tgz",
	".gz":   "gz",
	".bz2":  "bz2",
	".tbz2": "tbz2",
	".rar":  "rar",
	".7z":   "7z",
}

// IsArchiveFormat reports whether format is a container that needs to be
// unpacked before its ebooks can be parsed.
func IsArchiveFormat(format string) bool {
	switch format {
	case "zip", "tar", "tgz", "gz", "bz2", "tbz2", "rar", "7z":
		return true
	}
	return false
}

// FormatOf returns the format (ebook or archive) of a file name. The second
// return value is false if the name does not match any known format. Compound
// suffixes such as ".tar.gz" are matched before single extensions.
func FormatOf(name string) (string, bool) {
	lower := strings.ToLower(name)
	switch {
	case strings.HasSuffix(lower, ".tar.gz"), strings.HasSuffix(lower, ".tgz"):
		return "tgz", true
	case strings.HasSuffix(lower, ".tar.bz2"), strings.HasSuffix(lower, ".tbz2"):
		return "tbz2", true
	}
	if format, ok := archiveExtensions[filepath.Ext(lower)]; ok {
		return format, true
	}
	if format, ok := knownExtensions[filepath.Ext(lower)]; ok {
		return format, true
	}
	return "", false
}

// Result describes a single ebook or archive file found during a scan.
type Result struct {
	Path   string
	Format string
	// Size is the file size in bytes, or -1 when unknown.
	Size int64
}

// Progress reports the number of directories, files and matching books
// seen so far.
type Progress struct {
	Dirs  int64
	Files int64
	Found int64
}

// Config configures a Scanner. The zero value is not valid; use
// DefaultConfig as a starting point.
type Config struct {
	// Workers is the number of concurrent directory walkers.
	Workers int

	// SourceFactory returns a fresh Source for every walker goroutine. When
	// nil, the local filesystem is used.
	SourceFactory source.Factory

	// OnResult is invoked for every ebook found.
	OnResult func(Result)

	// OnReport is invoked after each directory is scanned.
	OnReport func(Progress)

	// OnError is invoked for every directory that could not be read.
	OnError func(path string, err error)
}

// DefaultConfig returns the default Scanner configuration.
func DefaultConfig() Config {
	return Config{Workers: runtime.NumCPU()}
}

// New creates a Scanner with the given configuration.
func New(cfg Config) *Scanner {
	if cfg.Workers < 1 {
		cfg.Workers = 1
	}
	return &Scanner{
		workers:       cfg.Workers,
		onResult:      cfg.OnResult,
		onReport:      cfg.OnReport,
		onError:       cfg.OnError,
		sourceFactory: cfg.SourceFactory,
	}
}

// Scanner walks a directory tree concurrently.
type Scanner struct {
	workers       int
	onResult      func(Result)
	onReport      func(Progress)
	onError       func(path string, err error)
	sourceFactory source.Factory

	dirs  atomic.Int64
	files atomic.Int64
	found atomic.Int64
	errs  atomic.Int64
	errMu sync.Mutex
	fatal error
}

// newSource returns the Source used by one walker goroutine. The zero value
// (nil factory) means the local filesystem.
func (s *Scanner) newSource() (source.Source, error) {
	if s.sourceFactory == nil {
		return source.LocalSource{}, nil
	}
	return s.sourceFactory()
}

// Scan walks root concurrently and reports results through the configured
// callbacks. It returns the number of directories that could not be read
// and a fatal error if root itself cannot be accessed.
func (s *Scanner) Scan(ctx context.Context, root string) (int, error) {
	rootSrc, err := s.newSource()
	if err != nil {
		return 0, fmt.Errorf("scan %s: %w", root, err)
	}
	isDir, err := rootSrc.IsDir(root)
	_ = rootSrc.Close()
	if err != nil {
		return 0, fmt.Errorf("scan %s: %w", root, err)
	}
	if !isDir {
		return 0, fmt.Errorf("scan %s: not a directory", root)
	}

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	q := newDirQueue()

	// If the context is cancelled nothing may ever be dispatched, so make
	// sure the walkers are always woken up to exit.
	go func() {
		<-ctx.Done()
		q.close()
	}()

	var (
		remaining atomic.Int64
		walkers   sync.WaitGroup
	)

	// dispatch queues a directory for a walker to process. The queue is
	// unbounded so this never blocks: walkers are both producers and
	// consumers, so a bounded channel could leave every walker blocked on
	// a send with nobody left to receive.
	dispatch := func(dir string) {
		if ctx.Err() != nil {
			return
		}
		remaining.Add(1)
		q.push(dir)
	}

	walkers.Add(s.workers)
	for i := 0; i < s.workers; i++ {
		go func() {
			defer walkers.Done()
			src, err := s.newSource()
			if err != nil {
				s.reportFatal(err)
				cancel()
				return
			}
			defer func() {
				_ = src.Close()
			}()
			for {
				dir, ok := q.pop()
				if !ok {
					return
				}
				if ctx.Err() != nil {
					if remaining.Add(-1) == 0 {
						q.close()
					}
					continue
				}
				s.scanDir(src, dir, dispatch)
				if remaining.Add(-1) == 0 {
					q.close()
				}
			}
		}()
	}

	dispatch(root)
	walkers.Wait()

	return int(s.errs.Load()), s.fatalErr()
}

// reportFatal records the first fatal error encountered by a walker.
func (s *Scanner) reportFatal(err error) {
	s.errMu.Lock()
	defer s.errMu.Unlock()
	if s.fatal == nil {
		s.fatal = err
	}
}

// fatalErr returns the fatal error recorded by reportFatal, if any.
func (s *Scanner) fatalErr() error {
	s.errMu.Lock()
	defer s.errMu.Unlock()
	return s.fatal
}

func (s *Scanner) scanDir(src source.Source, dir string, dispatch func(string)) {
	s.dirs.Add(1)

	entries, err := src.List(dir)
	if err != nil {
		s.reportError(dir, err)
		return
	}

	for _, e := range entries {
		name := e.Name
		if e.IsDir {
			dispatch(src.Join(dir, name))
			continue
		}
		s.files.Add(1)
		format, ok := FormatOf(name)
		if !ok {
			continue
		}
		if IsArchiveFormat(format) {
			if s.onResult != nil {
				s.onResult(Result{Path: src.Join(dir, name), Format: format, Size: e.Size})
			}
			continue
		}
		s.found.Add(1)
		if s.onResult != nil {
			s.onResult(Result{Path: src.Join(dir, name), Format: format, Size: e.Size})
		}
	}

	if s.onReport != nil {
		s.onReport(s.progress())
	}
}

func (s *Scanner) reportError(dir string, err error) {
	s.errs.Add(1)
	if s.onError != nil {
		s.onError(dir, err)
	}
}

func (s *Scanner) progress() Progress {
	return Progress{
		Dirs:  s.dirs.Load(),
		Files: s.files.Load(),
		Found: s.found.Load(),
	}
}

// dirQueue is an unbounded thread-safe FIFO. A channel would work as a
// queue here, but the walkers are both producers and consumers; once a
// bounded channel fills up, every walker could block on a send while no
// walker is left to receive.
type dirQueue struct {
	mu     sync.Mutex
	cond   *sync.Cond
	items  []string
	closed bool
}

func newDirQueue() *dirQueue {
	q := &dirQueue{}
	q.cond = sync.NewCond(&q.mu)
	return q
}

func (q *dirQueue) push(dir string) {
	q.mu.Lock()
	q.items = append(q.items, dir)
	q.cond.Signal()
	q.mu.Unlock()
}

// close wakes every blocked pop. It is safe to call more than once.
func (q *dirQueue) close() {
	q.mu.Lock()
	q.closed = true
	q.cond.Broadcast()
	q.mu.Unlock()
}

// pop returns the next directory. It blocks until an item is available or
// the queue is closed and drained, in which case ok is false.
func (q *dirQueue) pop() (string, bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	for len(q.items) == 0 && !q.closed {
		q.cond.Wait()
	}
	if len(q.items) == 0 {
		return "", false
	}
	dir := q.items[0]
	q.items[0] = ""
	q.items = q.items[1:]
	return dir, true
}
