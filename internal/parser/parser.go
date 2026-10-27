// Package parser reads metadata out of ebook files.
package parser

import (
	"context"

	"github.com/f0d0r/margaret-cli/internal/identifier"
	"github.com/f0d0r/margaret-ebook-library/book"
)

// Metadata holds the metadata extracted from an ebook file.
type Metadata struct {
	Authors     []string
	Title       string
	Description string
	Subjects    []string
	Languages   []string
	// Identifiers holds metadata identifiers plus at most one validated
	// ISBN-13 extracted from the linear text head or the head of a non-linear
	// textual resource, with provenance for each value.
	Identifiers     []identifier.Identifier
	Publisher       string
	PublicationDate string
	Series          string
	SeriesIndex     string
	Hash            string
	MinHash         []uint64
}

// Parser reads metadata from a single ebook. ebookData provides random access
// to the raw bytes of the ebook; the parser must not consume more than it
// needs and should stop reading once the metadata has been found.
type Parser interface {
	// Parse extracts the metadata of the ebook read from ebookData. The format
	// (such as "epub", "mobi" or "pdf") is detected from the content. Parsing
	// stops with ctx.Err() once ctx is canceled so an interrupted scan does
	// not wait for an in-flight file.
	Parse(ctx context.Context, ebookData book.Blob) (Metadata, error)
}
