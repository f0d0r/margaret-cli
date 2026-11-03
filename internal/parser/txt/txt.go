// Package txt reads metadata from plain-text books.
package txt

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/f0d0r/margaret-cli/internal/identifier"
	"github.com/f0d0r/margaret-cli/internal/langid"
	"github.com/f0d0r/margaret-cli/internal/parser"
	"github.com/f0d0r/margaret-ebook-library/book"
	"github.com/f0d0r/margaret-ebook-library/tools"
)

// DefaultMinSize is the minimum size in bytes of a plain-text file that is
// treated as a book. Side files such as readmes, blurbs, and distribution
// metadata are typically smaller than real books.
const DefaultMinSize int64 = 15 * 1024

// ErrEmptyText reports a file with no textual content.
var ErrEmptyText = errors.New("txt: empty or whitespace-only text")

// ErrBinaryText reports content that does not look like plain text.
var ErrBinaryText = errors.New("txt: binary content")

// TxtParser extracts metadata from plain-text books. The title and the authors
// come from a header block at the start of the text (label lines, YAML front
// matter or a Project Gutenberg preamble) or from Calibre's title-page
// pattern; the ISBN is taken from the text head. The whole decoded text feeds
// the MinHash fingerprint, so a plain-text book is indexed like any other
// ebook.
type TxtParser struct {
	// MinBookSize is the minimum size in bytes for a plain-text file to be
	// treated as a book. Zero uses DefaultMinSize; a negative value disables
	// the size filter.
	MinBookSize int64
}

var (
	_ parser.Parser     = TxtParser{}
	_ parser.SizeFilter = TxtParser{}
)

// MinSize implements parser.SizeFilter.
func (p TxtParser) MinSize() int64 {
	switch {
	case p.MinBookSize < 0:
		return 0
	case p.MinBookSize == 0:
		return DefaultMinSize
	default:
		return p.MinBookSize
	}
}

// Parse reads the plain text from ebookData and returns its metadata, content
// hash and MinHash fingerprint. Empty, whitespace-only and binary content is
// rejected.
func (p TxtParser) Parse(ctx context.Context, ebookData book.Blob) (parser.Metadata, error) {
	if ebookData == nil {
		return parser.Metadata{}, errors.New("txt: nil blob")
	}
	if err := ctx.Err(); err != nil {
		return parser.Metadata{}, err
	}
	data := parser.WithContext(ctx, ebookData)

	size, err := data.Size()
	if err != nil {
		return parser.Metadata{}, parser.ContextError(ctx, fmt.Errorf("txt: file size: %w", err))
	}
	if size < 0 {
		return parser.Metadata{}, fmt.Errorf("txt: invalid size %d", size)
	}
	if min := p.MinSize(); min > 0 && size < min {
		return parser.Metadata{}, parser.ErrNotABook
	}
	if size == 0 {
		return parser.Metadata{}, ErrEmptyText
	}

	enc, err := detectEncoding(data, size)
	if err != nil {
		return parser.Metadata{}, parser.ContextError(ctx, fmt.Errorf("txt: detect encoding: %w", err))
	}

	head, err := readHead(data, size, enc)
	if err != nil {
		return parser.Metadata{}, parser.ContextError(ctx, fmt.Errorf("txt: read text: %w", err))
	}
	if strings.TrimSpace(head) == "" {
		return parser.Metadata{}, ErrEmptyText
	}
	if looksBinary(head) {
		return parser.Metadata{}, ErrBinaryText
	}
	if isJunkContent(head, size) {
		return parser.Metadata{}, parser.ErrNotABook
	}

	hash, err := tools.CalculateFileHashFromBlob(data)
	if err != nil {
		return parser.Metadata{}, parser.ContextError(ctx, fmt.Errorf("txt: hash: %w", err))
	}

	fingerprint, err := tools.FingerprintReader(decodedReader(data, size, enc),
		tools.WithMinHash(tools.MinHashConfig{NumHashes: 128, ShingleSize: 5}))
	if err != nil {
		return parser.Metadata{}, parser.ContextError(ctx, fmt.Errorf("txt: fingerprint: %w", err))
	}
	if err := ctx.Err(); err != nil {
		return parser.Metadata{}, err
	}

	md := extractMetadata(head)
	detected, detectedOK := langid.DetectProse(head)
	md.Languages = parser.ApplyDetectedLanguage(md.Languages, detected, detectedOK)
	md.Hash = hash
	md.MinHash = fingerprint.MinHash
	md.Identifiers = textIdentifiers(head)
	return md, nil
}

// textIdentifiers returns at most one validated ISBN-13 found in head, marked
// as text-derived.
func textIdentifiers(head string) []identifier.Identifier {
	for _, isbn := range tools.ExtractISBNs(head) {
		isbn13, ok := isbn.ToISBN13()
		if !ok {
			continue
		}
		return []identifier.Identifier{{
			Scheme: "ISBN",
			Value:  isbn13.String(),
			Source: identifier.SourceText,
		}}
	}
	return nil
}
