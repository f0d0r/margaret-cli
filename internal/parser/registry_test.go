package parser

import (
	"testing"

	"github.com/f0d0r/margaret-cli/internal/scanner"
)

func TestParsersCoversEbookFormats(t *testing.T) {
	parsers := Parsers()
	formats := scanner.EbookFormats()
	if len(parsers) != len(formats) {
		t.Fatalf("Parsers() has %d entries, EbookFormats() has %d", len(parsers), len(formats))
	}
	for _, f := range formats {
		if parsers[f] == nil {
			t.Errorf("no parser registered for format %q", f)
		}
	}
}
