package openlibrary

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"os"
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

// OpenDump opens a dump file, decompressing it when the file is gzipped.
func OpenDump(file *File) (io.ReadCloser, error) {
	if file == nil {
		return nil, errors.New("no dump file")
	}
	raw, err := os.Open(file.Path)
	if err != nil {
		return nil, err
	}
	if !file.Compressed {
		return raw, nil
	}
	reader, err := gzip.NewReader(raw)
	if err != nil {
		_ = raw.Close()
		return nil, fmt.Errorf("open gzip dump: %w", err)
	}
	return &gzipReadCloser{reader: reader, raw: raw}, nil
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
