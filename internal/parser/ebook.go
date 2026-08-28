package parser

import (
	ebook "github.com/f0d0r/margaret-ebook-library"
	"github.com/f0d0r/margaret-ebook-library/book"
	"github.com/f0d0r/margaret-ebook-library/tools"
)

// EbookParser reads metadata with the margaret-ebook-library package, which
// detects the format from the content and extracts author and title metadata.
type EbookParser struct{}

// Parse reads the e-book from ebookData using the new blob API and returns
// normalized metadata and a content hash.
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
	return Metadata{
		Authors: md.Authors,
		Title:   md.Title,
		Hash:    hash,
	}, nil
}
