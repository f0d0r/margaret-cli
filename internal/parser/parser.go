// Package parser reads author and title metadata out of ebook files.
package parser

import (
	"github.com/f0d0r/margaret-ebook-library/book"
)

// Metadata holds the metadata extracted from an ebook file.
type Metadata struct {
	Authors []string
	Title   string
	Hash    string
}

// Parser reads metadata from a single ebook. ebookData provides random access
// to the raw bytes of the ebook; the parser must not consume more than it
// needs and should stop reading once the metadata has been found.
type Parser interface {
	// Parse extracts the metadata of the ebook read from ebookData. The format
	// (such as "epub", "mobi" or "pdf") is detected from the content.
	Parse(ebookData book.Blob) (Metadata, error)
}
