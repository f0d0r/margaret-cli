// Package identifier defines book identifier provenance.
package identifier

import "strings"

// Source records where an identifier value came from.
type Source string

const (
	SourceMetadata        Source = "METADATA"
	SourceText            Source = "TEXT"
	SourceMetadataAndText Source = "METADATA_AND_TEXT"
)

// Identifier is a book identifier together with its provenance.
type Identifier struct {
	Scheme string `json:"scheme"`
	Value  string `json:"value"`
	Source Source `json:"source"`
}

// MergeSources combines provenance from two occurrences of the same value.
func MergeSources(a, b Source) Source {
	a = normalizeSource(a)
	b = normalizeSource(b)
	if a == b {
		return a
	}
	if a == SourceMetadataAndText || b == SourceMetadataAndText {
		return SourceMetadataAndText
	}
	return SourceMetadataAndText
}

func normalizeSource(source Source) Source {
	switch source {
	case SourceMetadata, SourceText, SourceMetadataAndText:
		return source
	default:
		return SourceMetadata
	}
}

// IsISBNScheme reports whether scheme names an ISBN identifier.
func IsISBNScheme(scheme string) bool {
	switch strings.ToUpper(strings.TrimSpace(scheme)) {
	case "ISBN", "ISBN-10", "ISBN-13":
		return true
	default:
		return false
	}
}
