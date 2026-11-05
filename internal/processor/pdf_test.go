package processor

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/f0d0r/margaret-cli/internal/parser/pdf"
)

func TestProcessPDF(t *testing.T) {
	tools, err := pdf.DetectTools("")
	if err != nil {
		t.Skipf("poppler is not installed: %v", err)
	}
	data, err := os.ReadFile("../parser/pdf/testdata/sample.pdf")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.pdf"), data, 0o644); err != nil {
		t.Fatal(err)
	}

	cfg := DefaultScanProcessorConfig()
	cfg.MinDocSize = -1
	cfg.PDFTools = tools
	proc := newTestProcessor(t, cfg, dir)
	if err := proc.Process(context.Background()); err != nil {
		t.Fatal(err)
	}
	if failures := proc.Failures(); len(failures) != 0 {
		t.Fatalf("unexpected failures: %v", failures)
	}
	if stats := proc.Stats(); stats.Parsed != 1 {
		t.Fatalf("parsed = %d, want 1 (stats: %+v)", stats.Parsed, stats)
	}
}

func TestProcessPDFSkipsWithoutTools(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.pdf"), []byte("%PDF-1.4\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := DefaultScanProcessorConfig()
	proc := newTestProcessor(t, cfg, dir)
	if err := proc.Process(context.Background()); err != nil {
		t.Fatal(err)
	}
	if failures := proc.Failures(); len(failures) != 0 {
		t.Fatalf("unexpected failures: %v", failures)
	}
	if stats := proc.Stats(); stats.Parsed != 0 || stats.Skipped != 1 {
		t.Fatalf("stats = %+v, want parsed=0 skipped=1", stats)
	}
}
