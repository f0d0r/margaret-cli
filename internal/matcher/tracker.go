package matcher

import (
	"sync"
	"sync/atomic"
)

// editionTracker holds the shared editions-pass state. A dense index per book
// lets the hot candidate path use atomic bit arrays instead of a lock.
type editionTracker struct {
	pending      []atomic.Bool
	seen         []atomic.Bool
	pendingCount atomic.Int64
	matched      atomic.Int64
	total        int

	mu      sync.Mutex
	current string
}

func newEditionTracker(cache *Cache) *editionTracker {
	tracker := &editionTracker{
		pending: make([]atomic.Bool, len(cache.Books)),
		seen:    make([]atomic.Bool, len(cache.Books)),
	}
	for _, book := range cache.Books {
		if book.TitleKey != "" || len(book.ISBNs) > 0 {
			tracker.pending[book.Index].Store(true)
			tracker.pendingCount.Add(1)
		}
	}
	tracker.total = int(tracker.pendingCount.Load())
	return tracker
}

func (t *editionTracker) has(book *Book) bool {
	return book != nil && t.pending[book.Index].Load()
}

func (t *editionTracker) remove(book *Book) {
	if book == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.pending[book.Index].CompareAndSwap(true, false) {
		t.pendingCount.Add(-1)
	}
}

// addIfPending runs add only while the book is still pending. It closes the
// race between a strong ISBN match removing the book and a worker adding a
// later edition candidate for it.
func (t *editionTracker) addIfPending(book *Book, add func()) {
	if book == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.pending[book.Index].Load() {
		add()
	}
}

// mark records that a book received a candidate and updates the progress label
// only for the first candidate. Editions of the same work therefore no longer
// make the label repeat.
func (t *editionTracker) mark(book *Book, label string) {
	if book == nil || !t.seen[book.Index].CompareAndSwap(false, true) {
		return
	}
	t.matched.Add(1)
	if label == "" {
		return
	}
	t.mu.Lock()
	t.current = label
	t.mu.Unlock()
}

func (t *editionTracker) remainingCount() int {
	return int(t.pendingCount.Load())
}

func (t *editionTracker) matchedCount() int {
	return int(t.matched.Load())
}

func (t *editionTracker) totalCount() int {
	return t.total
}

func (t *editionTracker) currentLabel() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.current
}
