package parser

import "errors"

// ErrNotABook reports content that is not a book. The processor skips such
// files without recording a failure.
var ErrNotABook = errors.New("parser: not a book")

// SizeFilter is implemented by parsers that reject files smaller than a
// minimum size. MinSize returns the limit in bytes; zero or negative disables
// the filter.
type SizeFilter interface {
	MinSize() int64
}
