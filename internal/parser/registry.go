package parser

// Parsers returns the default mapping from ebook format to the parser
// that reads it.
func Parsers() map[string]Parser {
	ebookParser := EbookParser{}
	return map[string]Parser{
		"epub": ebookParser,
		"mobi": ebookParser,
		"azw3": ebookParser,
		"azw":  ebookParser,
		"prc":  ebookParser,
	}
}
