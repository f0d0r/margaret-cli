package parser

import (
	"context"

	"github.com/f0d0r/margaret-cli/internal/langid"
	ebook "github.com/f0d0r/margaret-ebook-library"
	"github.com/f0d0r/margaret-ebook-library/book"
	"github.com/f0d0r/margaret-ebook-library/tools"
)

// isbnScanWindow bounds the reading-order head used for ISBN and language
// detection. The same bytes feed both, so nothing is read twice.
const isbnScanWindow = 64 * 1024

// EbookParser reads metadata with the margaret-ebook-library package, which
// detects the format from the content and extracts the ebook metadata.
type EbookParser struct{}

// Parse reads the e-book from ebookData using the blob API and returns
// normalized metadata, a content hash and the MinHash content fingerprint.
// The plain-text reading order is read once: the fingerprint, the book-valid
// ISBN and the language sample all come from that single stream.
func (ep EbookParser) Parse(ctx context.Context, ebookData book.Blob) (Metadata, error) {
	ebookData = WithContext(ctx, ebookData)
	bk, err := ebook.ReadFromBlob(ebookData)
	if err != nil {
		return Metadata{}, ContextError(ctx, err)
	}
	md := bk.Metadata()
	hash, err := tools.CalculateFileHashFromBlob(ebookData)
	if err != nil {
		return Metadata{}, ContextError(ctx, err)
	}

	fingerprint, isbn, head, err := scanReadingOrder(ctx, bk)
	if err != nil {
		return Metadata{}, ContextError(ctx, err)
	}
	textISBN := ""
	if len(isbn) > 0 {
		if isbn13, ok := isbn[0].ToISBN13(); ok {
			textISBN = isbn13.String()
		}
	}
	identifiers := mergeIdentifiers(md.Identifiers, textISBN)

	detected, detectedOK := langid.DetectProse(head)
	languages := ApplyDetectedLanguage(md.Languages, detected, detectedOK)

	return Metadata{
		Authors:         md.Authors,
		Title:           md.Title,
		Description:     md.Description,
		Subjects:        md.Subjects,
		Languages:       languages,
		Identifiers:     identifiers,
		Publisher:       md.Publisher,
		PublicationDate: md.PublicationDate,
		Series:          md.Series,
		SeriesIndex:     md.SeriesIndex,
		Hash:            hash,
		MinHash:         fingerprint.MinHash,
		Head:            head,
	}, nil
}

// scanReadingOrder reads the book text once and returns everything the parser
// needs from it: the MinHash fingerprint, one book-valid ISBN and the head.
func scanReadingOrder(ctx context.Context, bk book.Book) (tools.FingerprintResult, []tools.ISBN, string, error) {
	fingerprints, err := tools.NewFingerprintProcessor(
		tools.WithMinHash(tools.MinHashConfig{
			NumHashes:   MinHashHashes,
			ShingleSize: MinHashShingleSize,
		}))
	if err != nil {
		return tools.FingerprintResult{}, nil, "", err
	}
	isbns := tools.NewISBNProcessor(isbnScanWindow, 1)
	head := tools.NewTextHead(isbnScanWindow)
	if err := tools.ScanReadingOrder(ctx, bk.Resources(), fingerprints, isbns, head); err != nil {
		return tools.FingerprintResult{}, nil, "", err
	}
	return fingerprints.Result(), isbns.Result(), head.Text(), nil
}
