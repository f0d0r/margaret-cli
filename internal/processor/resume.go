package processor

import (
	"context"

	"github.com/f0d0r/margaret-cli/internal/claim"
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
	if file, err := p.q.GetBookFileByPath(ctx, displayPath); err == nil {
		// Files recorded before content fingerprints or claim metadata were
		// added are parsed again so both can be backfilled.
		// Resolution can be recomputed from stored claims without re-opening
		// the file, so only the extraction version forces a reparse.
		return len(file.HeadBloom) > 0 && file.ClaimVersion >= claim.ClaimVersion
	}
	// Exact duplicates live in book_file_duplicates rather than book_files.
	_, err := p.q.GetBookFileDuplicateByPath(ctx, displayPath)
	return err == nil
}
