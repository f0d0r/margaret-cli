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

// Parse reads the e-book from ebookData using the new blob API and returns
// normalized metadata, a content hash and the MinHash content fingerprint.
func (ep EbookParser) Parse(ebookData book.Blob) (Metadata, error) {
	bk, err := ebook.ReadFromBlob(ebookData)
	if err != nil {
		return Metadata{}, err
	}
	md := bk.Metadata()
	hash, err := tools.CalculateFileHashFromBlob(ebookData)
	if err != nil {
		return Metadata{}, err
	}
	fp, err := tools.FingerprintContent(context.Background(), bk)
	if err != nil {
		return Metadata{}, err
	}
	return Metadata{
		Authors: md.Authors,
		Title:   md.Title,
		Hash:    hash,
		MinHash: fp.MinHash,
	}, nil
}
