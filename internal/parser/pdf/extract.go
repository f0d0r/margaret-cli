package pdf

import (
	"bytes"
	"context"
	"fmt"
	"hash"
	"io"
	"os/exec"
	"strings"
	"sync/atomic"

	"github.com/f0d0r/margaret-ebook-library/book"
)

const (
	// infoOutputLimit bounds the pdfinfo output. The Info dictionary is tiny;
	// the XMP packet is somewhat larger, but neither should exceed this.
	infoOutputLimit = 8 << 20
	// stderrLimit bounds the captured tool diagnostics.
	stderrLimit = 64 << 10
)

// runInfo runs pdfinfo with the given arguments and returns its stdout.
func runInfo(ctx context.Context, data book.Blob, size int64, tool string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, tool, args...)
	cmd.Stdin = io.NewSectionReader(data, 0, size)
	stdout := &limitedBuffer{limit: infoOutputLimit}
	stderr := &limitedBuffer{limit: stderrLimit}
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		return "", toolError("pdfinfo", err, stderr.String())
	}
	return stdout.String(), nil
}

// startTextExtraction starts pdftotext and returns its stdout plus a wait
// function. The caller must read the stream to EOF before calling wait.
func startTextExtraction(ctx context.Context, stdin io.Reader, tool string) (io.ReadCloser, func() error, error) {
	cmd := exec.CommandContext(ctx, tool,
		"-enc", "UTF-8", "-nodiag", "-eol", "unix", "-", "-")
	cmd.Stdin = stdin
	stderr := &limitedBuffer{limit: stderrLimit}
	cmd.Stderr = stderr
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, nil, fmt.Errorf("pdf: pdftotext stdout: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return nil, nil, fmt.Errorf("pdf: start pdftotext: %w", err)
	}
	wait := func() error {
		err := cmd.Wait()
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return toolError("pdftotext", err, stderr.String())
		}
		return nil
	}
	return stdout, wait, nil
}

func toolError(name string, err error, stderr string) error {
	if stderr = strings.TrimSpace(stderr); stderr != "" {
		return fmt.Errorf("pdf: %s: %w: %s", name, err, stderr)
	}
	return fmt.Errorf("pdf: %s: %w", name, err)
}

// limitedBuffer keeps at most limit bytes and silently discards the rest.
type limitedBuffer struct {
	buf   bytes.Buffer
	limit int
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	if b.limit <= 0 {
		b.limit = 1 << 20
	}
	remaining := b.limit - b.buf.Len()
	if remaining > 0 {
		if len(p) > remaining {
			_, _ = b.buf.Write(p[:remaining])
		} else {
			_, _ = b.buf.Write(p)
		}
	}
	return len(p), nil
}

func (b *limitedBuffer) String() string {
	return b.buf.String()
}

// cleanReader removes ASCII control characters except tab, newline and
// carriage return. pdftotext uses form feeds as page separators; those are
// not document text.
type cleanReader struct {
	reader io.Reader
}

func (r *cleanReader) Read(p []byte) (int, error) {
	for {
		n, err := r.reader.Read(p)
		j := 0
		for i := 0; i < n; i++ {
			b := p[i]
			if b < 0x20 && b != '\t' && b != '\n' && b != '\r' {
				continue
			}
			p[j] = b
			j++
		}
		if j > 0 || err != nil {
			return j, err
		}
	}
}

// countingReader counts the bytes read from the wrapped reader.
type countingReader struct {
	reader io.Reader
	n      int64
}

func (r *countingReader) Read(p []byte) (int, error) {
	n, err := r.reader.Read(p)
	r.n += int64(n)
	return n, err
}

// hashingReader hashes the bytes as they are read from the wrapped reader.
// pdftotext consumes its stdin sequentially, so the tracked offset lets the
// caller hash only the part the process did not read.
type hashingReader struct {
	reader io.Reader
	sum    hash.Hash
	offset atomic.Int64
}

func (r *hashingReader) Read(p []byte) (int, error) {
	n, err := r.reader.Read(p)
	if n > 0 {
		_, _ = r.sum.Write(p[:n])
		r.offset.Add(int64(n))
	}
	return n, err
}

// finishHash hashes the bytes after offset. This covers files that pdftotext
// stopped reading before EOF (for example because it skipped embedded
// images), so the resulting digest is always over the complete raw blob.
func finishHash(data book.Blob, size int64, sum hash.Hash, offset int64) error {
	if offset > size {
		return fmt.Errorf("pdf: hash offset %d exceeds size %d", offset, size)
	}
	if offset == size {
		return nil
	}
	_, err := io.Copy(sum, io.NewSectionReader(data, offset, size-offset))
	return err
}
