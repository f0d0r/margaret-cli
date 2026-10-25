package parser

import (
	"github.com/f0d0r/margaret-cli/internal/scanner"
)

// Parsers returns the mapping from ebook format to the parser that reads
// it. It derives from scanner.EbookFormats, the single source of truth for
// the format taxonomy, so a newly recognized format automatically has a
// parser. All formats share the format-agnostic EbookParser (the library
// detects the format from the content); a future per-format exception goes
// here as a single divergent entry after the loop.
func Parsers() map[string]Parser {
	formats := scanner.EbookFormats()
	m := make(map[string]Parser, len(formats))
	for _, f := range formats {
		m[f] = EbookParser{}
	}
	return m
}
