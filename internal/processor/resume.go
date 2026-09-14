package processor

import (
	"context"
)

// shouldSkip reports whether displayPath was already recorded in a previous
// run, either as a successfully processed file or as a duplicate.
// Resume skips every recorded path unconditionally: a changed file is the
// owner's problem and needs a fresh run to reprocess. Failures leave no row
// behind, so they are retried on every run. Any database error answers
// false (process the file): at worst we redo work, we never silently drop
// a file.
func (p *ScanProcessor) shouldSkip(ctx context.Context, displayPath string) bool {
	if !p.cfg.Resume {
		return false
	}
	if _, err := p.q.GetBookFileByPath(ctx, displayPath); err == nil {
		return true
	}
	// Exact duplicates live in book_file_duplicates rather than book_files.
	_, err := p.q.GetBookFileDuplicateByPath(ctx, displayPath)
	return err == nil
}

// skip records a resume skip and emits progress, mirroring fail() without
// the failure bookkeeping.
func (p *ScanProcessor) skip() {
	p.skipped.Add(1)
	p.report()
}
