package processor

import (
	"bytes"
	"errors"
	"io"
	"os"
	"strings"
	"testing"
)

// countingReader wraps r and records how much of it was consumed.
type countingReader struct {
	r io.Reader
	n int64
}

func (c *countingReader) Read(p []byte) (int, error) {
	m, err := c.r.Read(p)
	c.n += int64(m)
	return m, err
}

func TestSpoolBlobLazyRead(t *testing.T) {
	content := make([]byte, 256*1024)
	for i := range content {
		content[i] = byte(i)
	}
	cr := &countingReader{r: bytes.NewReader(content)}
	b, err := newSpoolBlob("margaret-test-", 4096, cr, int64(len(content)))
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()

	got := make([]byte, 8)
	if _, err := b.ReadAt(got, 0); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, content[:8]) {
		t.Fatalf("got %x, want %x", got, content[:8])
	}
	// The lazy blob must not pull the whole source for a small prefix read.
	// ensure pulls in 32 KiB chunks, so a small read should consume far less
	// than the full 256 KiB.
	if cr.n >= int64(len(content)) {
		t.Fatalf("lazy blob consumed the whole source (%d bytes) for a prefix read", cr.n)
	}
	size, err := b.Size()
	if err != nil {
		t.Fatal(err)
	}
	if size != int64(len(content)) {
		t.Fatalf("size = %d, want %d", size, len(content))
	}
}

func TestSpoolBlobSpillAndCleanup(t *testing.T) {
	dir, err := os.MkdirTemp("", "spooltest-")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.RemoveAll(dir) }()
	// Point the spill at dir so we can observe it.
	defer func(old func(string, string) (*os.File, error)) { osCreateTemp = old }(osCreateTemp)
	osCreateTemp = func(p, pat string) (*os.File, error) {
		return os.CreateTemp(dir, pat)
	}

	content := make([]byte, 300*1024)
	for i := range content {
		content[i] = byte(i * 7)
	}
	cr := &countingReader{r: bytes.NewReader(content)}
	b, err := newSpoolBlob("margaret-ebook-", 64*1024, cr, int64(len(content)))
	if err != nil {
		t.Fatal(err)
	}
	// Nothing should have been read yet (size is known).
	if cr.n != 0 {
		t.Fatalf("expected no reads before first access, got %d", cr.n)
	}

	got := make([]byte, len(content))
	if _, err := b.ReadAt(got, 0); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, content) {
		t.Fatal("content mismatch after full read")
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || !strings.HasPrefix(entries[0].Name(), "margaret-ebook-") {
		t.Fatalf("expected one spool file in %s, got %v", dir, namesOf(entries))
	}

	b.Close()
	entries, err = os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("spool file leaked after Close: %v", namesOf(entries))
	}
}

func TestSpoolBlobUnknownSize(t *testing.T) {
	content := []byte("content of unknown length")
	cr := &countingReader{r: bytes.NewReader(content)}
	b, err := newSpoolBlob("margaret-test-", 1024, cr, -1)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	if cr.n != int64(len(content)) {
		t.Fatalf("unknown-size blob must buffer everything up front, consumed %d", cr.n)
	}
	size, err := b.Size()
	if err != nil {
		t.Fatal(err)
	}
	if size != int64(len(content)) {
		t.Fatalf("size = %d, want %d", size, len(content))
	}
	got := make([]byte, len(content))
	if _, err := b.ReadAt(got, 0); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, content) {
		t.Fatal("content mismatch")
	}
}

func TestSpoolBlobSeek(t *testing.T) {
	content := bytes.Repeat([]byte("ab"), 32*1024)
	b, err := newSpoolBlob("margaret-test-", 1024, bytes.NewReader(content), int64(len(content)))
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()

	end, err := b.Seek(0, io.SeekEnd)
	if err != nil {
		t.Fatal(err)
	}
	if end != int64(len(content)) {
		t.Fatalf("seek end = %d, want %d", end, len(content))
	}
	pos, err := b.Seek(3, io.SeekStart)
	if err != nil {
		t.Fatal(err)
	}
	if pos != 3 {
		t.Fatalf("seek start = %d, want 3", pos)
	}
	got := make([]byte, 2)
	if _, err := b.ReadAt(got, 3); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, content[3:5]) {
		t.Fatalf("read after seek got %q, want %q", got, content[3:5])
	}
}

// errReader fails after n bytes.
type errReader struct {
	n int
}

func (e *errReader) Read(p []byte) (int, error) {
	if e.n <= 0 {
		return 0, errors.New("boom")
	}
	if len(p) > e.n {
		p = p[:e.n]
	}
	e.n -= len(p)
	return len(p), nil
}

func TestSpoolBlobSourceError(t *testing.T) {
	b, err := newSpoolBlob("margaret-test-", 4096, &errReader{n: 10}, 1000)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	got := make([]byte, 1000)
	n, err := b.ReadAt(got, 0)
	if err == nil {
		t.Fatalf("expected source error, got nil (read %d bytes)", n)
	}
	if n != 10 {
		t.Fatalf("read %d bytes, want 10 before the error", n)
	}
	// The error must be sticky for reads that still need more source data.
	if _, err := b.ReadAt(got, 0); err == nil {
		t.Fatal("expected sticky source error on subsequent read")
	}
}

func namesOf(entries []os.DirEntry) []string {
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return names
}
