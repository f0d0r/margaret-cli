package processor

import (
	"testing"

	"github.com/f0d0r/margaret-cli/internal/scanner"
)

func TestResultQueueFIFOAndClose(t *testing.T) {
	q := newResultQueue()
	if !q.push(scanner.Result{Path: "first"}) || !q.push(scanner.Result{Path: "second"}) {
		t.Fatal("push failed")
	}
	first, ok := q.pop()
	if !ok || first.Path != "first" {
		t.Fatalf("first pop = %+v, %v", first, ok)
	}
	second, ok := q.pop()
	if !ok || second.Path != "second" {
		t.Fatalf("second pop = %+v, %v", second, ok)
	}
	q.close()
	if _, ok := q.pop(); ok {
		t.Fatal("pop returned an item after close")
	}
	if q.push(scanner.Result{Path: "late"}) {
		t.Fatal("push after close succeeded")
	}
}

func TestResultQueueAbortDropsItems(t *testing.T) {
	q := newResultQueue()
	q.push(scanner.Result{Path: "queued"})
	q.abort()
	if _, ok := q.pop(); ok {
		t.Fatal("pop returned an item after abort")
	}
}
