package parser

import "errors"

// ErrNotABook reports content that is not a book. The processor skips such
// files without recording a failure.
var ErrNotABook = errors.New("parser: not a book")

// DefaultMinDocumentSize is the minimum size in bytes of a document-format
// file (plain text, RTF and later PDF) that is treated as a book. Side files
// such as readmes, blurbs, and distribution metadata are typically smaller
// than real books.
const DefaultMinDocumentSize int64 = 15 * 1024

// ResolveMinSize resolves a configured document size limit: zero uses
// DefaultMinDocumentSize and a negative value disables the filter.
func ResolveMinSize(configured int64) int64 {
	switch {
	case configured < 0:
		return 0
	case configured == 0:
		return DefaultMinDocumentSize
	default:
		return configured
	}
}

// SizeFilter is implemented by parsers that reject files smaller than a
// minimum size. MinSize returns the limit in bytes; zero or negative disables
// the filter.
type SizeFilter interface {
	MinSize() int64
}
