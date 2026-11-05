package textdoc

import (
	"github.com/f0d0r/margaret-cli/internal/identifier"
	"github.com/f0d0r/margaret-ebook-library/tools"
)

// TextIdentifiers returns at most one validated ISBN-13 found in head, marked
// as text-derived.
func TextIdentifiers(head string) []identifier.Identifier {
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
