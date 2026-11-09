package matcher

import (
	"context"
	"os"
	"runtime"
	"testing"

	"github.com/f0d0r/margaret-cli/internal/database"
	"github.com/f0d0r/margaret-cli/internal/db"
	"github.com/f0d0r/margaret-cli/internal/openlibrary"
)

// BenchmarkStreamEditionsReal profiles the editions pass against a real dump
// subset. It is skipped unless the paths are supplied:
//
//	MARGARET_BENCH_EDITIONS=/path/to/editions-subset.txt.gz
//	MARGARET_BENCH_DB=/path/to/margaret.db
func BenchmarkStreamEditionsReal(b *testing.B) {
	path := os.Getenv("MARGARET_BENCH_EDITIONS")
	dbPath := os.Getenv("MARGARET_BENCH_DB")
	if path == "" || dbPath == "" {
		b.Skip("set MARGARET_BENCH_EDITIONS and MARGARET_BENCH_DB")
	}
	conn, err := database.Open(dbPath)
	if err != nil {
		b.Fatal(err)
	}
	defer func() {
		_ = conn.Close()
	}()
	cache, err := Build(context.Background(), db.New(conn), Options{})
	if err != nil {
		b.Fatal(err)
	}
	dataset := &openlibrary.Dataset{
		Editions: &openlibrary.File{
			Kind:       openlibrary.KindEditions,
			Path:       path,
			Version:    "2026-10-31",
			Compressed: true,
		},
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		result, err := StreamDataset(context.Background(), dataset, cache, StreamOptions{
			Workers: runtime.GOMAXPROCS(0),
		})
		if err != nil {
			b.Fatal(err)
		}
		b.Logf("editions=%d candidates=%d books=%d", result.Stats.Editions, result.Stats.Candidates, result.Stats.BooksWithCandidates)
	}
}
