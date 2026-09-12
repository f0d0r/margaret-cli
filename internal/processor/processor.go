// Package processor runs the scan and metadata extraction pipeline.
package processor

import (
	"context"

	"github.com/f0d0r/margaret-tools/internal/parser"
	"github.com/f0d0r/margaret-tools/internal/scanner"
)

// Processor runs the pipeline of a CLI command to completion, reporting
// progress along the way. Implementations are constructed with their inputs
// and expose the outcome of the run through Stats and Failures.
type Processor interface {
	Process(ctx context.Context) error
	Stats() Progress
	Failures() []Failure
}

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

	// Skipped counts files that resume mode passed over without reading:
	// already recorded with an identical size and mtime.
	Skipped int64

	// Active is the number of units of work currently in flight. Current
	// holds the paths of the items being processed, newest last, capped so
	// the report stays small. These let consumers keep showing progress (and
	// an animated cursor) even while a single slow item produces no counter
	// movement.
	Active  int64
	Current []string
}
