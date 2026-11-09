package cli

import (
	"strings"
	"testing"

	"github.com/f0d0r/margaret-cli/internal/enrich"
	"github.com/f0d0r/margaret-cli/internal/matcher"
)

func TestProgressBarUpdate(t *testing.T) {
	var buf strings.Builder
	bar := &progressBar{w: &buf, desc: "matching editions", width: 10, cols: 120}
	bar.update("matching editions", 3, 10, "Tolkien: The Hobbit", true)
	got := buf.String()
	for _, want := range []string{"matching editions", "3/10", "Tolkien: The Hobbit", "█"} {
		if !strings.Contains(got, want) {
			t.Fatalf("progress output %q does not contain %q", got, want)
		}
	}
	if strings.Contains(got, "\n") {
		t.Fatalf("progress output contains a newline: %q", got)
	}
}

func TestProgressBarSpinnerWithoutMax(t *testing.T) {
	var buf strings.Builder
	bar := &progressBar{w: &buf, desc: "ranking matches", width: 10, cols: 120}
	bar.update("ranking matches", 0, 0, "", true)
	got := buf.String()
	if !strings.Contains(got, "ranking matches") {
		t.Fatalf("progress output %q does not contain the phase", got)
	}
	if strings.Contains(got, "█") || strings.Contains(got, "/") {
		t.Fatalf("spinner-only output contains a bar: %q", got)
	}
}

func TestEnrichProgressReporter(t *testing.T) {
	var buf strings.Builder
	progress := &enrichProgress{bar: &progressBar{
		w:        &buf,
		desc:     "matching",
		width:    10,
		cols:     120,
		throttle: 0,
	}}
	progress.stream(matcher.StreamProgress{
		Pass: "editions", Done: 2, Total: 4, Current: "Tolkien: The Hobbit",
	})
	got := buf.String()
	for _, want := range []string{"matching editions", "2/4", "Tolkien: The Hobbit"} {
		if !strings.Contains(got, want) {
			t.Fatalf("stream progress %q does not contain %q", got, want)
		}
	}

	buf.Reset()
	progress.enrichment(enrich.Progress{
		Phase: "enriching books", Done: 1, Total: 2,
		CurrentAuthor: "Orwell", CurrentTitle: "1984",
	})
	got = buf.String()
	for _, want := range []string{"enriching books", "1/2", "Orwell: 1984"} {
		if !strings.Contains(got, want) {
			t.Fatalf("enrich progress %q does not contain %q", got, want)
		}
	}

	buf.Reset()
	progress.enrichment(enrich.Progress{Phase: "reading editions"})
	got = buf.String()
	if !strings.Contains(got, "reading editions") {
		t.Fatalf("phase progress %q does not contain the phase", got)
	}
	if strings.Contains(got, "█") {
		t.Fatalf("phase progress unexpectedly contains a bar: %q", got)
	}
}

func TestBookProgressLabel(t *testing.T) {
	if got := bookProgressLabel("Orwell, George", "1984"); got != "Orwell: 1984" {
		t.Fatalf("label = %q", got)
	}
	if got := bookProgressLabel("", "1984"); got != "1984" {
		t.Fatalf("title-only label = %q", got)
	}
	if got := bookProgressLabel("Orwell", ""); got != "Orwell: (untitled)" {
		t.Fatalf("untitled label = %q", got)
	}
}
