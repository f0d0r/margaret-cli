package pdf

import (
	"bytes"
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
)

const (
	// headLimit is how much extracted text is kept for metadata, ISBN and
	// language extraction.
	headLimit = 64 << 10
	// signatureWindow is enough to find the PDF header in a file with leading
	// junk.
	signatureWindow = 1024
)

var (
	// ErrNotPDF reports a file without a PDF header.
	ErrNotPDF = errors.New("pdf: not a PDF document")
	// ErrEncrypted reports a password-protected PDF.
	ErrEncrypted = errors.New("pdf: encrypted document (password required)")
	// ErrNoText reports a PDF with no extractable text.
	ErrNoText = errors.New("pdf: no extractable text")
)

// PDFParser extracts metadata and plain text from PDF books using pdftotext
// and pdfinfo. PDF support is conditional: when the Poppler tools are missing,
// the scan skips PDF files and the CLI warns about it.
type PDFParser struct {
	// MinBookSize is the minimum size in bytes for a PDF file, and also for
	// its extracted text, to be treated as a book. Zero uses
	// parser.DefaultMinDocumentSize; a negative value disables both filters.
	MinBookSize int64
	// Tools are the Poppler binaries. Both are required.
	Tools Tools
}

var (
	_ parser.Parser     = PDFParser{}
	_ parser.SizeFilter = PDFParser{}
)

// MinSize implements parser.SizeFilter.
func (p PDFParser) MinSize() int64 {
	return parser.ResolveMinSize(p.MinBookSize)
}

// Parse extracts the PDF metadata and text, returning the raw content hash
// and the MinHash of the extracted text. The text stream is read once and
// fed through the shared head gate and fingerprint pipeline. The raw hash is
// computed while pdftotext consumes its input; any bytes the extractor skips
// are hashed afterwards. PDFs without enough extractable text are reported
// as not a book.
func (p PDFParser) Parse(ctx context.Context, ebookData book.Blob) (parser.Metadata, error) {
	if ebookData == nil {
		return parser.Metadata{}, errors.New("pdf: nil blob")
	}
	if err := ctx.Err(); err != nil {
		return parser.Metadata{}, err
	}
	data := parser.WithContext(ctx, ebookData)

	size, err := data.Size()
	if err != nil {
		return parser.Metadata{}, parser.ContextError(ctx, fmt.Errorf("pdf: file size: %w", err))
	}
	if size < 0 {
		return parser.Metadata{}, fmt.Errorf("pdf: invalid size %d", size)
	}
	if min := p.MinSize(); min > 0 && size < min {
		return parser.Metadata{}, fmt.Errorf("%w: file is smaller than %d bytes", parser.ErrNotABook, min)
	}
	if size == 0 {
		return parser.Metadata{}, ErrNotPDF
	}
	ok, err := hasPDFHeader(data, size)
	if err != nil {
		return parser.Metadata{}, parser.ContextError(ctx, fmt.Errorf("pdf: read header: %w", err))
	}
	if !ok {
		return parser.Metadata{}, ErrNotPDF
	}
	if !p.Tools.Available() {
		return parser.Metadata{}, ErrToolsUnavailable
	}

	meta, metaErr := readMetadata(ctx, data, size, p.Tools)
	if metaErr != nil {
		if ctx.Err() != nil {
			return parser.Metadata{}, ctx.Err()
		}
		if errors.Is(metaErr, ErrEncrypted) {
			return parser.Metadata{}, metaErr
		}
		// A pdfinfo failure is not fatal: the text extractor may still work,
		// and the text head supplies fallback metadata.
		meta = metadata{}
	}

	sum := sha256.New()
	input := &hashingReader{reader: io.NewSectionReader(data, 0, size), sum: sum}
	text, wait, err := startTextExtraction(ctx, input, p.Tools.Text)
	if err != nil {
		return parser.Metadata{}, parser.ContextError(ctx, err)
	}
	defer func() {
		_ = text.Close()
	}()

	cleaned := &countingReader{reader: &cleanReader{reader: text}}
	gate := textdoc.NewHeadGate(headLimit, size, func(head string, size int64) error {
		if strings.TrimSpace(head) == "" {
			return ErrNoText
		}
		return nil
	})
	fingerprint, err := tools.FingerprintReader(io.TeeReader(cleaned, gate),
		tools.WithMinHash(tools.MinHashConfig{
			NumHashes:   parser.MinHashHashes,
			ShingleSize: parser.MinHashShingleSize,
		}))
	if err != nil {
		_ = text.Close()
		waitErr := wait()
		if waitErr != nil {
			return parser.Metadata{}, parser.ContextError(ctx, waitErr)
		}
		if errors.Is(err, ErrNoText) {
			return parser.Metadata{}, fmt.Errorf("%w: no extractable text", parser.ErrNotABook)
		}
		if errors.Is(err, parser.ErrNotABook) {
			return parser.Metadata{}, err
		}
		return parser.Metadata{}, parser.ContextError(ctx, fmt.Errorf("pdf: read text: %w", err))
	}
	if err := wait(); err != nil {
		return parser.Metadata{}, parser.ContextError(ctx, err)
	}
	if err := gate.Finish(); err != nil {
		if errors.Is(err, ErrNoText) {
			return parser.Metadata{}, fmt.Errorf("%w: no extractable text", parser.ErrNotABook)
		}
		return parser.Metadata{}, err
	}
	if err := ctx.Err(); err != nil {
		return parser.Metadata{}, err
	}
	if min := p.MinSize(); min > 0 && cleaned.n < min {
		return parser.Metadata{}, fmt.Errorf("%w: extracted text is shorter than %d bytes", parser.ErrNotABook, min)
	}

	head := gate.Head()
	textMD := textdoc.ExtractMetadata(head)
	md := parser.Metadata{
		Title:   textMD.Title,
		Authors: textMD.Authors,
		Claims:  append(metadataClaims(meta), textdoc.ExtractClaims(head)...),
	}
	applyMetadata(&md, meta)

	detected, detectedOK := langid.DetectProse(head)
	md.Languages = parser.ApplyDetectedLanguage(md.Languages, detected, detectedOK)
	if err := finishHash(data, size, sum, input.offset.Load()); err != nil {
		return parser.Metadata{}, parser.ContextError(ctx, fmt.Errorf("pdf: hash: %w", err))
	}
	md.Hash = hex.EncodeToString(sum.Sum(nil))
	md.MinHash = fingerprint.MinHash
	md.Identifiers = mergeIdentifierLists(md.Identifiers, textdoc.TextIdentifiers(head))
	md.Head = head
	return md, nil
}

// hasPDFHeader reports whether the PDF signature occurs in the first KiB.
func hasPDFHeader(b book.Blob, size int64) (bool, error) {
	n := min(size, int64(signatureWindow))
	buf := make([]byte, n)
	if _, err := b.ReadAt(buf, 0); err != nil && !errors.Is(err, io.EOF) {
		return false, err
	}
	return bytes.Contains(buf, []byte("%PDF-")), nil
}
