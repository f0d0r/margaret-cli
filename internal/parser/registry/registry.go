// Package registry maps scanner formats to the parser that reads them.
package registry

import (
	"github.com/f0d0r/margaret-cli/internal/parser"
	"github.com/f0d0r/margaret-cli/internal/parser/rtf"
	"github.com/f0d0r/margaret-cli/internal/parser/txt"
	"github.com/f0d0r/margaret-cli/internal/scanner"
)

// Options configures the parsers built by this package.
type Options struct {
	// MinDocumentSize is the minimum size in bytes for a document-format file
	// (plain text, RTF and later PDF) to be treated as a book. Zero uses the
	// parser package default; a negative value disables the size filter.
	MinDocumentSize int64
}

// Parsers returns the mapping from ebook format to the parser that reads it.
func Parsers() map[string]parser.Parser {
	return ParsersWithOptions(Options{})
}

// ParsersWithOptions is like Parsers but applies opts to the format-specific
// parsers. It derives from scanner.EbookFormats, the single source of truth
// for the format taxonomy, so a newly recognized format automatically has a
// parser. Formats read by the format-agnostic EbookParser (the library detects
// the format from the content) need no entry; document formats with a
// dedicated parser override the default below.
func ParsersWithOptions(opts Options) map[string]parser.Parser {
	formats := scanner.EbookFormats()
	m := make(map[string]parser.Parser, len(formats))
	for _, f := range formats {
		m[f] = parser.EbookParser{}
	}
	m["txt"] = txt.TxtParser{MinBookSize: opts.MinDocumentSize}
	m["rtf"] = rtf.RtfParser{MinBookSize: opts.MinDocumentSize}
	return m
}
