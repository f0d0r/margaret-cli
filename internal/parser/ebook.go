package parser

import (
	"context"

	ebook "github.com/f0d0r/margaret-ebook-library"
	"github.com/f0d0r/margaret-ebook-library/book"
	"github.com/f0d0r/margaret-ebook-library/tools"
)

const isbnScanWindow = 64 * 1024

// EbookParser reads metadata with the margaret-ebook-library package, which
// detects the format from the content and extracts the ebook metadata.
type EbookParser struct{}

// parseError prefers the context error: format detection may report its own
// error when reads are aborted, but callers need to recognize cancellation.
func parseError(ctx context.Context, err error) error {
	if ctxErr := ctx.Err(); ctxErr != nil {
		return ctxErr
	}
	return err
}

// Parse reads the e-book from ebookData using the blob API and returns
// normalized metadata, a content hash and the MinHash content fingerprint.
// The plain-text reading order and non-linear textual resources are always
// scanned for one ISBN candidate, and provenance is recorded per identifier.
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

	fingerprint, err := tools.FingerprintContent(ctx, bk,
		tools.WithMinHash(tools.MinHashConfig{NumHashes: 128, ShingleSize: 5}))
	if err != nil {
		return Metadata{}, parseError(ctx, err)
	}

	isbns, err := tools.FindBookISBNs(ctx, bk,
		tools.WithISBNScanBytes(isbnScanWindow),
		tools.WithISBNLimit(1),
	)
	if err != nil {
		return Metadata{}, parseError(ctx, err)
	}
	textISBN := ""
	if len(isbns) > 0 {
		if isbn13, ok := isbns[0].ToISBN13(); ok {
			textISBN = isbn13.String()
		}
	}
	identifiers := mergeIdentifiers(md.Identifiers, textISBN)

	return Metadata{
		Authors:         md.Authors,
		Title:           md.Title,
		Description:     md.Description,
		Subjects:        md.Subjects,
		Languages:       md.Languages,
		Identifiers:     identifiers,
		Publisher:       md.Publisher,
		PublicationDate: md.PublicationDate,
		Series:          md.Series,
		SeriesIndex:     md.SeriesIndex,
		Hash:            hash,
		MinHash:         fingerprint.MinHash,
	}, nil
}
