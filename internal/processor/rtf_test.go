package processor

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestProcessRTF(t *testing.T) {
	dir := t.TempDir()
	content := `{\rtf1\ansi{\info{\title The Great Novel}{\author Jane Doe}}` +
		strings.Repeat("The quick brown fox jumps over the lazy dog. ", 60) + `}`
	path := filepath.Join(dir, "a.rtf")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	var (
		mu      sync.Mutex
		results []Result
	)
	cfg := DefaultScanProcessorConfig()
	cfg.MinDocSize = -1
	cfg.OnResult = func(r Result) {
		mu.Lock()
		results = append(results, r)
		mu.Unlock()
	}
	proc := newTestProcessor(t, cfg, dir)
	if err := proc.Process(context.Background()); err != nil {
		t.Fatal(err)
	}
	if failures := proc.Failures(); len(failures) != 0 {
		t.Fatalf("unexpected failures: %v", failures)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d: %v", len(results), pathsOf(results))
	}
	r := results[0]
	if r.File.Format != "rtf" {
		t.Errorf("format = %q, want rtf", r.File.Format)
	}
	if r.Metadata.Title != "The Great Novel" {
		t.Errorf("title = %q, want %q", r.Metadata.Title, "The Great Novel")
	}
	if len(r.Metadata.Authors) != 1 || r.Metadata.Authors[0] != "Jane Doe" {
		t.Errorf("authors = %v, want [Jane Doe]", r.Metadata.Authors)
	}
	if len(r.Metadata.MinHash) != 128 {
		t.Errorf("minhash has %d hashes, want 128", len(r.Metadata.MinHash))
	}
	if r.Metadata.Hash == "" {
		t.Error("empty hash")
	}
}

func TestProcessRTFSizeThresholdOverride(t *testing.T) {
	dir := t.TempDir()
	tiny := `{\rtf1\ansi Title: Tiny\par Author: Jane Doe\par ` +
		strings.Repeat("A reggel hűvös volt. ", 20) + `}`
	if err := os.WriteFile(filepath.Join(dir, "tiny.rtf"), []byte(tiny), 0o644); err != nil {
		t.Fatal(err)
	}

	run := func(minSize int64) (parsed, skipped int64) {
		cfg := DefaultScanProcessorConfig()
		cfg.MinDocSize = minSize
		proc := newTestProcessor(t, cfg, dir)
		if err := proc.Process(context.Background()); err != nil {
			t.Fatal(err)
		}
		s := proc.Stats()
		return s.Parsed, s.Skipped
	}

	if parsed, skipped := run(0); parsed != 0 || skipped != 1 {
		t.Errorf("default threshold: parsed=%d skipped=%d, want 0, 1", parsed, skipped)
	}
	if parsed, skipped := run(-1); parsed != 1 || skipped != 0 {
		t.Errorf("disabled threshold: parsed=%d skipped=%d, want 1, 0", parsed, skipped)
	}
}
