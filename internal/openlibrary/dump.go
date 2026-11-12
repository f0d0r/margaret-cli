package openlibrary

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"os"
	"sync/atomic"
)

const maxDumpLineBytes = 64 << 20

// ErrMalformedRow marks a dump line that does not have the expected TSV
// columns. Callers can skip it and continue.
var ErrMalformedRow = errors.New("malformed OpenLibrary dump row")

// Row is one parsed TSV line from a dump.
type Row struct {
	Type         string
	Key          string
	Revision     string
	LastModified string
	// Data is the JSON column. It aliases the scanner buffer and is only
	// valid until the next Scan call.
	Data []byte
}

// ByteCounter counts bytes read from a dump file. For gzipped dumps it counts
// the compressed bytes, which is what progress uses to show how far the reader
// has advanced through the file.
type ByteCounter struct {
	n atomic.Int64
}

// Load returns the bytes counted so far.
func (c *ByteCounter) Load() int64 {
	return c.n.Load()
}

func (c *ByteCounter) add(n int) {
	if n > 0 {
		c.n.Add(int64(n))
	}
}

type countingReader struct {
	reader  io.Reader
	counter *ByteCounter
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.reader.Read(p)
	c.counter.add(n)
	return n, err
}

type countingReadCloser struct {
	reader io.Reader
	closer io.Closer
}

func (c *countingReadCloser) Read(p []byte) (int, error) {
	return c.reader.Read(p)
}

func (c *countingReadCloser) Close() error {
	return c.closer.Close()
}

// OpenDump opens a dump file, decompressing it when the file is gzipped, and
// returns a counter for the bytes read from the file.
func OpenDump(file *File) (io.ReadCloser, error) {
	reader, _, err := OpenDumpWithCounter(file)
	return reader, err
}

// OpenDumpWithCounter is OpenDump with a byte counter attached. The counter
// tracks the compressed file bytes read, so callers can show dump progress
// even when the uncompressed length is unknown.
func OpenDumpWithCounter(file *File) (io.ReadCloser, *ByteCounter, error) {
	if file == nil {
		return nil, nil, errors.New("no dump file")
	}
	raw, err := os.Open(file.Path)
	if err != nil {
		return nil, nil, err
	}
	counter := &ByteCounter{}
	counted := &countingReader{reader: raw, counter: counter}
	if !file.Compressed {
		return &countingReadCloser{reader: counted, closer: raw}, counter, nil
	}
	reader, err := gzip.NewReader(counted)
	if err != nil {
		_ = raw.Close()
		return nil, nil, fmt.Errorf("open gzip dump: %w", err)
	}
	return &gzipReadCloser{reader: reader, raw: raw}, counter, nil
}

// DumpSize returns the on-disk size of a dump file, falling back to stat when
// the dataset did not record it.
func DumpSize(file *File) int64 {
	if file == nil {
		return 0
	}
	if file.Size > 0 {
		return file.Size
	}
	info, err := os.Stat(file.Path)
	if err != nil {
		return 0
	}
	return info.Size()
}

type gzipReadCloser struct {
	reader *gzip.Reader
	raw    io.Closer
}

func (g *gzipReadCloser) Read(p []byte) (int, error) {
	return g.reader.Read(p)
}

func (g *gzipReadCloser) Close() error {
	err := g.reader.Close()
	if closeErr := g.raw.Close(); err == nil {
		err = closeErr
	}
	return err
}

// NewDumpScanner returns a scanner configured for the large records found in
// OpenLibrary dumps.
func NewDumpScanner(reader io.Reader) *bufio.Scanner {
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 64*1024), maxDumpLineBytes)
	return scanner
}

// ReadRow reads the next non-empty TSV row.
func ReadRow(scanner *bufio.Scanner) (Row, error) {
	for scanner.Scan() {
		line := bytes.TrimSuffix(scanner.Bytes(), []byte{'\r'})
		if len(line) == 0 {
			continue
		}
		fields := bytes.SplitN(line, []byte{'\t'}, 5)
		if len(fields) != 5 {
			return Row{}, fmt.Errorf("%w: got %d columns", ErrMalformedRow, len(fields))
		}
		return Row{
			Type:         string(fields[0]),
			Key:          string(fields[1]),
			Revision:     string(fields[2]),
			LastModified: string(fields[3]),
			Data:         fields[4],
		}, nil
	}
	if err := scanner.Err(); err != nil {
		return Row{}, err
	}
	return Row{}, io.EOF
}
