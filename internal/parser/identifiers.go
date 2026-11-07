package parser

import (
	"github.com/f0d0r/margaret-cli/internal/identifier"
	"github.com/f0d0r/margaret-ebook-library/book"
	"github.com/f0d0r/margaret-ebook-library/tools"
)

// mergeIdentifiers converts metadata identifiers to canonical, book-valid
// ISBN-13 values and folds a text-derived ISBN into the matching entry. Invalid
// and placeholder ISBNs are dropped; non-ISBN schemes pass through unchanged.
func mergeIdentifiers(metadata []book.Identifier, textISBN string) []identifier.Identifier {
	out := make([]identifier.Identifier, 0, len(metadata)+1)
	isbnIndex := make(map[string]int)
	exactIndex := make(map[string]int)

	for _, item := range metadata {
		if identifier.IsISBNScheme(item.Scheme) {
			isbn, ok := tools.ValidateBookISBN(item.Value)
			if !ok {
				continue
			}
			isbn13, ok := isbn.ToISBN13()
			if !ok {
				continue
			}
			canonical := isbn13.String()
			if index, exists := isbnIndex[canonical]; exists {
				out[index].Source = identifier.MergeSources(out[index].Source, identifier.SourceMetadata)
				continue
			}
			isbnIndex[canonical] = len(out)
			out = append(out, identifier.Identifier{
				Scheme: "ISBN",
				Value:  canonical,
				Source: identifier.SourceMetadata,
			})
			continue
		}
		key := item.Scheme + "\x00" + item.Value
		if index, exists := exactIndex[key]; exists {
			out[index].Source = identifier.MergeSources(out[index].Source, identifier.SourceMetadata)
			continue
		}
		exactIndex[key] = len(out)
		out = append(out, identifier.Identifier{
			Scheme: item.Scheme,
			Value:  item.Value,
			Source: identifier.SourceMetadata,
		})
	}

	if textISBN != "" {
		if isbn, ok := tools.ValidateBookISBN(textISBN); ok {
			if isbn13, ok := isbn.ToISBN13(); ok {
				textISBN = isbn13.String()
			} else {
				textISBN = ""
			}
		} else {
			textISBN = ""
		}
	}
	if textISBN != "" {
		if index, exists := isbnIndex[textISBN]; exists {
			out[index].Source = identifier.MergeSources(out[index].Source, identifier.SourceText)
		} else {
			out = append(out, identifier.Identifier{
				Scheme: "ISBN",
				Value:  textISBN,
				Source: identifier.SourceText,
			})
		}
	}
	return out
}
