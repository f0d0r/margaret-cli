package parser

import (
	"slices"
	"testing"

	"github.com/f0d0r/margaret-cli/internal/identifier"
	"github.com/f0d0r/margaret-ebook-library/book"
)

func TestMergeIdentifiersDropsInvalidAndPlaceholderISBNs(t *testing.T) {
	invalid := []string{
		"9634261346",
		"igpn-28languages-01",
		"igpn-28languages-02",
		"25708",
		"0000000000000",
		"9632590509",
		"1234567890123",
		"1234567890",
		"Kit érdekel?",
		"9780000000002",
		"9780123456786",
		"9781111111113",
		"9780110110110",
		"9781111010119",
		"9782147483649",
	}

	metadata := make([]book.Identifier, 0, len(invalid)+5)
	for _, value := range invalid {
		metadata = append(metadata, book.Identifier{Scheme: "ISBN", Value: value})
	}
	metadata = append(metadata,
		book.Identifier{Scheme: "DOI", Value: "10.1000/test"},
		book.Identifier{Scheme: "UUID", Value: "uuid-1"},
		book.Identifier{Scheme: "ISBN-10", Value: "0-306-40615-2"},
		book.Identifier{Scheme: "ISBN", Value: "9789632117652"},
		book.Identifier{Scheme: "ISBN", Value: "9789633070598"},
	)

	got := mergeIdentifiers(metadata, "")
	want := []identifier.Identifier{
		{Scheme: "DOI", Value: "10.1000/test", Source: identifier.SourceMetadata},
		{Scheme: "UUID", Value: "uuid-1", Source: identifier.SourceMetadata},
		{Scheme: "ISBN", Value: "9780306406157", Source: identifier.SourceMetadata},
		{Scheme: "ISBN", Value: "9789632117652", Source: identifier.SourceMetadata},
		{Scheme: "ISBN", Value: "9789633070598", Source: identifier.SourceMetadata},
	}
	if !slices.Equal(got, want) {
		t.Errorf("mergeIdentifiers() = %v, want %v", got, want)
	}
}
