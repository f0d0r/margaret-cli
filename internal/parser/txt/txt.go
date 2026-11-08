// Package txt reads metadata from plain-text books.
package txt

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/f0d0r/margaret-cli/internal/langid"
	"github.com/f0d0r/margaret-cli/internal/parser"
	"github.com/f0d0r/margaret-cli/internal/parser/textdoc"
	"github.com/f0d0r/margaret-ebook-library/book"
	"github.com/f0d0r/margaret-ebook-library/tools"
	"golang.org/x/text/transform"
)

var (
	// ErrEmptyText reports a file with no textual content.
	ErrEmptyText = fmt.Errorf("%w: empty or whitespace-only text", parser.ErrNotABook)
	// ErrBinaryText reports content that does not look like plain text.
	ErrBinaryText = errors.New("txt: binary content")
)

// TxtParser extracts metadata from plain-text books. The title and the authors
// come from a header block at the start of the text (label lines, YAML front
// matter or a Project Gutenberg preamble) or from Calibre's title-page
// pattern; the ISBN is taken from the text head. The whole decoded text feeds
// the MinHash fingerprint, so a plain-text book is indexed like any other
// ebook.
type TxtParser struct {
	// MinBookSize is the minimum size in bytes for a plain-text file to be
	// treated as a book. Zero uses parser.DefaultMinDocumentSize; a negative
	// value disables the size filter.
	MinBookSize int64
}

var (
	_ parser.Parser     = TxtParser{}
	_ parser.SizeFilter = TxtParser{}
)

// MinSize implements parser.SizeFilter.
func (p TxtParser) MinSize() int64 {
	return parser.ResolveMinSize(p.MinBookSize)
}

// Parse reads the plain text from ebookData and returns its metadata, content
// hash and MinHash fingerprint. The raw bytes are read once: the content hash
// and the decoded text stream are produced in the same pass. Empty,
// whitespace-only and binary content is rejected.
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
		return parser.Metadata{}, fmt.Errorf("%w: file is smaller than %d bytes", parser.ErrNotABook, min)
	}
	if size == 0 {
		return parser.Metadata{}, ErrEmptyText
	}

	enc, err := detectEncoding(data, size)
	if err != nil {
		return parser.Metadata{}, parser.ContextError(ctx, fmt.Errorf("txt: detect encoding: %w", err))
	}

	// The raw bytes are hashed and decoded in one pass: the decoder consumes
	// the whole section for the fingerprint, so the tee sees the whole file.
	// The BOM is hashed but skipped before decoding.
	rawHash := sha256.New()
	raw := io.TeeReader(io.NewSectionReader(data, 0, size), rawHash)
	if enc.bom > 0 {
		if _, err := io.CopyN(io.Discard, raw, enc.bom); err != nil {
			return parser.Metadata{}, parser.ContextError(ctx, fmt.Errorf("txt: skip BOM: %w", err))
		}
	}
	gate := textdoc.NewHeadGate(headLimit, size, func(head string, size int64) error {
		switch {
		case strings.TrimSpace(head) == "":
			return ErrEmptyText
		case looksBinary(head):
			return ErrBinaryText
		case textdoc.IsJunkContent(head, size):
			return parser.ErrNotABook
		}
		return nil
	})
	fingerprint, err := tools.FingerprintReader(
		io.TeeReader(transform.NewReader(raw, enc.enc.NewDecoder()), gate),
		tools.WithMinHash(tools.MinHashConfig{
			NumHashes:   parser.MinHashHashes,
			ShingleSize: parser.MinHashShingleSize,
		}))
	if err != nil {
		for _, target := range []error{ErrEmptyText, ErrBinaryText, parser.ErrNotABook} {
			if errors.Is(err, target) {
				return parser.Metadata{}, target
			}
		}
		return parser.Metadata{}, parser.ContextError(ctx, fmt.Errorf("txt: read text: %w", err))
	}
	if err := gate.Finish(); err != nil {
		return parser.Metadata{}, err
	}
	if err := ctx.Err(); err != nil {
		return parser.Metadata{}, err
	}

	head := gate.Head()
	md := textdoc.ExtractMetadata(head)
	detected, detectedOK := langid.DetectProse(head)
	md.Languages = parser.ApplyDetectedLanguage(md.Languages, detected, detectedOK)
	md.Hash = hex.EncodeToString(rawHash.Sum(nil))
	md.MinHash = fingerprint.MinHash
	md.Identifiers = textdoc.TextIdentifiers(head)
	return md, nil
}
