package report

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/f0d0r/margaret-cli/internal/processor"
)

func TestWriteSkips(t *testing.T) {
	path := filepath.Join(t.TempDir(), "skipped.json")
	skips := []processor.Skip{
		{Path: "/books/b.txt", Reason: "not a book"},
		{Path: "/books/a.txt", Reason: "below minimum document size (1 bytes, minimum 10)"},
	}

	if err := WriteSkips(skips, path); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	var got []skipReport
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("entries = %d, want 2", len(got))
	}
	// The report is sorted by path.
	if got[0].Path != "/books/a.txt" || got[1].Path != "/books/b.txt" {
		t.Errorf("entries = %+v, want path-sorted output", got)
	}
	if got[0].Reason != skips[1].Reason {
		t.Errorf("reason = %q, want %q", got[0].Reason, skips[1].Reason)
	}
}

func TestWriteSkipsEmpty(t *testing.T) {
	path := filepath.Join(t.TempDir(), "skipped.json")
	if err := WriteSkips(nil, path); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "[]\n" {
		t.Errorf("empty report = %q, want %q", data, "[]\n")
	}
}
