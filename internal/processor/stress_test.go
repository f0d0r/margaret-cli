package processor

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/f0d0r/margaret-ebook-library/book"
	"github.com/f0d0r/margaret-tools/internal/parser"
)

type slowParser struct{}

func (slowParser) Parse(_ book.Blob) (parser.Metadata, error) {
	time.Sleep(2 * time.Millisecond)
	return parser.Metadata{}, nil
}

func TestProcessStress(t *testing.T) {
	dir := t.TempDir()
	total := 0
	for d := range 50 {
		sub := filepath.Join(dir, fmt.Sprintf("d%02d", d))
		if err := os.MkdirAll(sub, 0o755); err != nil {
			t.Fatal(err)
		}
		for f := range 40 {
			name := filepath.Join(sub, fmt.Sprintf("b%03d.epub", f))
			if err := os.WriteFile(name, []byte("x"), 0o644); err != nil {
				t.Fatal(err)
			}
			total++
		}
	}

	var parsed atomic.Int64
	proc := newTestProcessor(t, ScanProcessorConfig{
		ScanWorkers:  4,
		ParseWorkers: 4,
		OnResult:     func(r Result) { parsed.Add(1) },
	}, dir)
	proc.parsers = map[string]parser.Parser{"epub": slowParser{}}

	done := make(chan struct{})
	go func() {
		defer close(done)
		err := proc.Process(context.Background())
		if err != nil {
			t.Errorf("err: %v", err)
		}
		if failures := proc.Failures(); len(failures) != 0 {
			t.Errorf("failures: %v", failures)
		}
	}()

	select {
	case <-done:
		if parsed.Load() != int64(total) {
			t.Fatalf("expected %d parsed, got %d", total, parsed.Load())
		}
	case <-time.After(30 * time.Second):
		t.Fatal("Process hung")
	}
}
