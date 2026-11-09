package matcher

import (
	"context"

	"github.com/f0d0r/margaret-cli/internal/openlibrary"
)

// applyWorkAuthors fills candidate author keys from the work record when the
// edition has none. It returns the author keys that were newly referenced.
func applyWorkAuthors(candidates *candidateSet, works map[string]WorkRecord) map[string]struct{} {
	added := make(map[string]struct{})
	if len(works) == 0 {
		return added
	}
	for _, list := range candidates.byBook {
		for _, candidate := range list {
			if len(candidate.AuthorKeys) > 0 {
				continue
			}
			work, ok := works[candidate.WorkKey]
			if !ok || len(work.AuthorKeys) == 0 {
				continue
			}
			candidate.AuthorKeys = append([]string(nil), work.AuthorKeys...)
			for _, key := range work.AuthorKeys {
				added[key] = struct{}{}
			}
		}
	}
	return added
}

// applyMissingWorkAuthors fetches work-level authors referenced by candidates
// whose editions had no authors, then re-scores the candidates.
func applyMissingWorkAuthors(
	ctx context.Context,
	dataset *openlibrary.Dataset,
	candidates *candidateSet,
	result *StreamResult,
	cache *Cache,
	opts StreamOptions,
) error {
	added := applyWorkAuthors(candidates, result.Works)
	if len(added) == 0 {
		return nil
	}
	for key := range added {
		if _, ok := result.Authors[key]; ok {
			delete(added, key)
		}
	}
	if len(added) == 0 || dataset.Authors == nil {
		return nil
	}
	authors, err := streamAuthors(ctx, dataset.Authors, added, opts, &result.Stats)
	if err != nil {
		return err
	}
	for key, record := range authors {
		result.Authors[key] = record
	}
	applyAuthorScores(candidates, result.Authors, cache)
	return nil
}
