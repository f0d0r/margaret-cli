package registry

import (
	"testing"

	"github.com/f0d0r/margaret-cli/internal/parser"
	"github.com/f0d0r/margaret-cli/internal/parser/rtf"
	"github.com/f0d0r/margaret-cli/internal/parser/txt"
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

func TestParsersUsesDocumentParsers(t *testing.T) {
	parsers := Parsers()
	if _, ok := parsers["txt"].(txt.TxtParser); !ok {
		t.Errorf("txt format uses %T, want txt.TxtParser", parsers["txt"])
	}
	if _, ok := parsers["rtf"].(rtf.RtfParser); !ok {
		t.Errorf("rtf format uses %T, want rtf.RtfParser", parsers["rtf"])
	}
	for format, par := range parsers {
		if format == "txt" || format == "rtf" {
			continue
		}
		if _, ok := par.(parser.EbookParser); !ok {
			t.Errorf("%s format uses %T, want parser.EbookParser", format, par)
		}
	}
}

func TestParsersWithOptionsConfiguresDocumentMinSize(t *testing.T) {
	parsers := ParsersWithOptions(Options{MinDocumentSize: 1234})
	tp, ok := parsers["txt"].(txt.TxtParser)
	if !ok {
		t.Fatalf("txt format uses %T, want txt.TxtParser", parsers["txt"])
	}
	if got := tp.MinSize(); got != 1234 {
		t.Errorf("txt MinSize() = %d, want 1234", got)
	}
	rp, ok := parsers["rtf"].(rtf.RtfParser)
	if !ok {
		t.Fatalf("rtf format uses %T, want rtf.RtfParser", parsers["rtf"])
	}
	if got := rp.MinSize(); got != 1234 {
		t.Errorf("rtf MinSize() = %d, want 1234", got)
	}
}
