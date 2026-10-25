package parser

import (
	"context"

	ebook "github.com/f0d0r/margaret-ebook-library"
	"github.com/f0d0r/margaret-ebook-library/book"
	"github.com/f0d0r/margaret-ebook-library/tools"
)

// EbookParser reads metadata with the margaret-ebook-library package, which
// detects the format from the content and extracts author and title metadata.
type EbookParser struct{}

// parseError prefers the context error: format detection may report its own
// error when reads are aborted, but callers need to recognize cancellation.
func parseError(ctx context.Context, err error) error {
	if ctxErr := ctx.Err(); ctxErr != nil {
		return ctxErr
	}
	return err
}

// Parse reads the e-book from ebookData using the new blob API and returns
// normalized metadata, a content hash and the MinHash content fingerprint.
func (ep EbookParser) Parse(ctx context.Context, ebookData book.Blob) (Metadata, error) {
	ebookData = withContext(ctx, ebookData)
	bk, err := ebook.ReadFromBlob(ebookData)
	if err != nil {
		return Metadata{}, parseError(ctx, err)
	}
	md := bk.Metadata()
	hash, err := tools.CalculateFileHashFromBlob(ebookData)
	if err != nil {
		return Metadata{}, parseError(ctx, err)
	}
	fp, err := tools.FingerprintContent(ctx, bk)
	if err != nil {
		return Metadata{}, parseError(ctx, err)
	}
	return Metadata{
		Authors: md.Authors,
		Title:   md.Title,
		Hash:    hash,
		MinHash: fp.MinHash,
	}, nil
}
