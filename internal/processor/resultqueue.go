package processor

import (
	"sync"

	"github.com/f0d0r/margaret-cli/internal/scanner"
)

// resultQueue decouples discovery from parsing. The scanner pushes each
// discovered result and immediately continues walking; parse workers pop at
// their own pace. The queue is unbounded, so a slow parser cannot stop the
// scanner from making progress (only available memory bounds it).
type resultQueue struct {
	mu     sync.Mutex
	cond   *sync.Cond
	items  []scanner.Result
	closed bool
}

func newResultQueue() *resultQueue {
	q := &resultQueue{}
	q.cond = sync.NewCond(&q.mu)
	return q
}

// push adds a result without blocking. It reports false when the queue has
// already been closed.
func (q *resultQueue) push(r scanner.Result) bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.closed {
		return false
	}
	q.items = append(q.items, r)
	q.cond.Signal()
	return true
}

// pop returns the next result, waiting until one is available or the queue is
// closed and drained.
func (q *resultQueue) pop() (scanner.Result, bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	for len(q.items) == 0 && !q.closed {
		q.cond.Wait()
	}
	if len(q.items) == 0 {
		return scanner.Result{}, false
	}
	r := q.items[0]
	q.items[0] = scanner.Result{}
	q.items = q.items[1:]
	if len(q.items) == 0 {
		q.items = nil
	}
	return r, true
}

// close stops accepting new results and wakes the workers so they can drain
// the remaining items.
func (q *resultQueue) close() {
	q.mu.Lock()
	q.closed = true
	q.cond.Broadcast()
	q.mu.Unlock()
}

// abort stops accepting results and drops everything still queued. It is used
// on cancellation so workers do not have to walk a large backlog just to
// discard it.
func (q *resultQueue) abort() {
	q.mu.Lock()
	q.closed = true
	q.items = nil
	q.cond.Broadcast()
	q.mu.Unlock()
}
