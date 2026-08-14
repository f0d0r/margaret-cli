package scanner

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

func TestScanNestedNoDeadlock(t *testing.T) {
	dir := t.TempDir()
	total := 0
	for d := range 20 {
		sub := filepath.Join(dir, fmt.Sprintf("d%02d", d))
		if err := os.MkdirAll(sub, 0o755); err != nil {
			t.Fatal(err)
		}
		for s := range 20 {
			sub2 := filepath.Join(sub, fmt.Sprintf("s%02d", s))
			if err := os.MkdirAll(sub2, 0o755); err != nil {
				t.Fatal(err)
			}
			for f := range 10 {
				name := filepath.Join(sub2, fmt.Sprintf("b%03d.epub", f))
				if err := os.WriteFile(name, []byte("x"), 0o644); err != nil {
					t.Fatal(err)
				}
				total++
			}
		}
	}

	var found atomic.Int64
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, err := New(Config{
			Workers:  2,
			OnResult: func(r Result) { found.Add(1) },
		}).Scan(context.Background(), dir)
		if err != nil {
			t.Errorf("err: %v", err)
		}
	}()

	select {
	case <-done:
		if found.Load() != int64(total) {
			t.Fatalf("expected %d found, got %d", total, found.Load())
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Scan hung (worker dispatch deadlock)")
	}
}
