package textdoc

import (
	"errors"
	"io"
	"testing"
)

func TestHeadGateWriteChecksAtLimit(t *testing.T) {
	reject := errors.New("reject")
	gate := NewHeadGate(3, 10, func(head string, _ int64) error {
		if head != "abc" {
			t.Errorf("head = %q, want abc", head)
		}
		return reject
	})
	if _, err := io.WriteString(gate, "abc"); !errors.Is(err, reject) {
		t.Fatalf("error = %v, want reject", err)
	}
}

func TestHeadGateFinishChecksShortHead(t *testing.T) {
	reject := errors.New("reject")
	gate := NewHeadGate(4, 10, func(head string, size int64) error {
		if head != "ab" || size != 10 {
			t.Errorf("check got %q, %d", head, size)
		}
		return reject
	})
	if _, err := io.WriteString(gate, "ab"); err != nil {
		t.Fatal(err)
	}
	if err := gate.Finish(); !errors.Is(err, reject) {
		t.Fatalf("finish error = %v, want reject", err)
	}
}

func TestHeadGateTruncatesAtRuneBoundary(t *testing.T) {
	gate := NewHeadGate(1, 10, nil)
	if _, err := io.WriteString(gate, "é"); err != nil {
		t.Fatal(err)
	}
	if got := gate.Head(); got != "" {
		t.Errorf("head = %q, want an empty rune-aligned head", got)
	}
}
