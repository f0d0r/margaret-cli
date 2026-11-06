package processor

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/f0d0r/margaret-cli/internal/parser"
	"github.com/f0d0r/margaret-ebook-library/book"
)

// blockingDiscoveryParser blocks every parse until release is closed, so the
// test can observe whether discovery finishes independently of parsing.
type blockingDiscoveryParser struct {
	release <-chan struct{}
}

func (p blockingDiscoveryParser) Parse(ctx context.Context, _ book.Blob) (parser.Metadata, error) {
	select {
	case <-p.release:
		return parser.Metadata{}, nil
	case <-ctx.Done():
		return parser.Metadata{}, ctx.Err()
	}
}

// TestDiscoveryContinuesWhileParsingIsBlocked pins the decoupling between the
// scanner and the parsers: the scanner must discover the whole tree even when
// every parse worker is blocked on the first file.
func TestDiscoveryContinuesWhileParsingIsBlocked(t *testing.T) {
	dir := t.TempDir()
	const total = 200
	for i := 0; i < total; i++ {
		path := filepath.Join(dir, fmt.Sprintf("book-%03d.epub", i))
		if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	release := make(chan struct{})
	proc := newTestProcessor(t, ScanProcessorConfig{
		ScanWorkers:  2,
		ParseWorkers: 1,
	}, dir)
	proc.parsers = map[string]parser.Parser{
		"epub": blockingDiscoveryParser{release: release},
	}

	done := make(chan error, 1)
	go func() {
		done <- proc.Process(context.Background())
	}()

	deadline := time.After(5 * time.Second)
	for {
		stats := proc.Stats()
		if stats.Scan.Found >= total {
			break
		}
		select {
		case <-deadline:
			t.Fatalf("discovery stalled at Found=%d, want %d (stats: %+v)", stats.Scan.Found, total, stats)
		case <-time.After(5 * time.Millisecond):
		}
	}

	close(release)
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Process did not finish after releasing the parser")
	}
	if stats := proc.Stats(); stats.Parsed != total {
		t.Fatalf("parsed = %d, want %d", stats.Parsed, total)
	}
}
