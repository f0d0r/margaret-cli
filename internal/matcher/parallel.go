package matcher

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"
	"sync/atomic"
	"time"

	"github.com/f0d0r/margaret-cli/internal/openlibrary"
)

type editionJob struct {
	key  string
	data []byte
}

// streamEditionsParallel reads the editions dump in one goroutine and lets a
// set of workers run JSON decoding and candidate matching concurrently. The
// shared candidate set is only touched when a record actually matches a book.
func streamEditionsParallel(
	ctx context.Context,
	file *openlibrary.File,
	cache *Cache,
	opts StreamOptions,
	stats *StreamStats,
	tracker *editionTracker,
	candidates *candidateSet,
) (*candidateSet, error) {
	workers := opts.Workers
	if workers < 1 {
		workers = 1
	}
	jobs := make(chan editionJob, workers*64)
	localStats := make([]StreamStats, workers)
	var processed atomic.Int64
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			processor := newEditionProcessor(cache, opts, &localStats[index], tracker, candidates)
			for job := range jobs {
				if ctx.Err() != nil {
					return
				}
				processor.processRecord(job.key, job.data)
				processed.Add(1)
			}
		}(i)
	}

	totalBytes := openlibrary.DumpSize(file)
	emit := func(read int64, final bool) {
		if opts.Progress == nil {
			return
		}
		if final && totalBytes > 0 {
			read = totalBytes
		}
		total := tracker.totalCount()
		done := tracker.matchedCount()
		opts.Progress(StreamProgress{
			Pass:       "editions",
			Lines:      stats.Lines,
			Records:    processed.Load(),
			Candidates: candidates.total(),
			Total:      total,
			Done:       done,
			Current:    tracker.currentLabel(),
			BytesRead:  read,
			BytesTotal: totalBytes,
			Pending:    total - done,
		})
	}

	err := readEditionsParallel(ctx, file, jobs, tracker, stats, func(read int64) {
		emit(read, false)
	})
	close(jobs)
	wg.Wait()
	for i := range localStats {
		stats.Editions += localStats[i].Editions
		stats.Malformed += localStats[i].Malformed
		stats.ISBNMatches += localStats[i].ISBNMatches
		stats.TitleExactMatches += localStats[i].TitleExactMatches
		stats.TitleFuzzyMatches += localStats[i].TitleFuzzyMatches
	}
	if err == nil {
		emit(totalBytes, true)
	}
	return candidates, err
}

func readEditionsParallel(
	ctx context.Context,
	file *openlibrary.File,
	jobs chan<- editionJob,
	tracker *editionTracker,
	stats *StreamStats,
	emit func(int64),
) error {
	reader, counter, err := openlibrary.OpenDumpWithCounter(file)
	if err != nil {
		return fmt.Errorf("open editions dump: %w", err)
	}
	defer func() {
		_ = reader.Close()
	}()
	scanner := openlibrary.NewDumpScanner(reader)
	lastProgress := time.Now()
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		row, err := openlibrary.ReadRow(scanner)
		if errors.Is(err, io.EOF) {
			emit(counter.Load())
			return nil
		}
		if errors.Is(err, openlibrary.ErrMalformedRow) {
			stats.Malformed++
			continue
		}
		if err != nil {
			return err
		}
		stats.Lines++
		if row.Type != "/type/edition" {
			continue
		}
		data := append([]byte(nil), row.Data...)
		select {
		case jobs <- editionJob{key: row.Key, data: data}:
		case <-ctx.Done():
			return ctx.Err()
		}
		if tracker.remainingCount() == 0 {
			return nil
		}
		if time.Since(lastProgress) >= progressInterval {
			emit(counter.Load())
			lastProgress = time.Now()
		}
	}
}
