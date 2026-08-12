// Package parser reads author and title metadata out of ebook files.
package parser

import (
	"context"
	"io"
)

// Metadata holds the metadata extracted from an ebook file.
type Metadata struct {
	Author string
	Title  string
}

// Parser reads metadata from a single ebook. r provides the ebook content;
// the parser must not consume more than it needs and should stop reading
// once the metadata has been found.
type Parser interface {
	// Parse extracts the metadata of the ebook read from r. format is one of
	// the detected formats such as "epub", "mobi" or "pdf".
	Parse(ctx context.Context, r io.Reader, format string) (Metadata, error)
}

// Dummy is a placeholder implementation that does nothing. It is meant to
// be replaced by real format-specific parsers.
type Dummy struct{}

// Parse always succeeds and returns empty metadata.
func (Dummy) Parse(context.Context, io.Reader, string) (Metadata, error) {
	return Metadata{}, nil
}
