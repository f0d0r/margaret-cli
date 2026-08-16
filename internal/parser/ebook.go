package parser

import (
	"github.com/f0d0r/margaret-ebook-library/pkg/ebook"
	"github.com/f0d0r/margaret-ebook-library/pkg/model"
)

// EbookParser reads metadata with the margaret-ebook-library package, which
// detects the format from the content and extracts author and title metadata.
type EbookParser struct{}

// Parse always succeeds and returns empty metadata.
func (ep EbookParser) Parse(ebookData model.Blob) (Metadata, error) {
	meta, err := ebook.ReadMetadataFromBlob(ebookData)
	if err != nil {
		return Metadata{}, err
	}
	hash, err := ebook.CalculateFileHashFromBlob(ebookData)
	if err != nil {
		return Metadata{}, err
	}
	return Metadata{
		Authors: meta.Authors,
		Title:   meta.Title,
		Hash:    hash,
	}, nil
}
