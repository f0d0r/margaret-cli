package processor

import (
	"errors"
	"io"
	"os"
	"sync"
)

// spoolMemLimit is the default maximum number of bytes buffered in memory for
// a spooled source stream; anything beyond is spilled to a temporary file. It
// bounds peak memory while avoiding temp files for typical ebooks.
const spoolMemLimit = 64 << 20 // 64 MiB

// osCreateTemp is os.CreateTemp, swapped out by tests to observe spill files.
var osCreateTemp = os.CreateTemp

// spoolBlob is a random-access view (io.ReaderAt + io.Seeker + Size) over a
// sequential source stream such as a decompressed archive member. It pulls
// from the source on demand, keeping up to the configured memory limit in
// memory and spilling the remainder to a temporary file. Reading only the
// metadata prefix (which is all the MOBI reader needs) therefore reads only
// that much of the source stream.
type spoolBlob struct {
	prefix string
	ramCap int64
	mu     sync.Mutex
	src    io.Reader
	size   int64 // total uncompressed size; valid once known is true
	known  bool
	ram    []byte // first bytes of the content
	spill  *os.File
	spillN int64 // bytes stored in the spill file
	buf    []byte
	eof    bool
	srcErr error
	pos    int64 // cursor for Seek
	closed bool
}

// newSpoolBlob wraps r into a spoolBlob. If size >= 0 it is the uncompressed
// size and the source is consumed lazily; otherwise the whole source is
// buffered up front so the size can be determined.
func newSpoolBlob(prefix string, ramCap int64, r io.Reader, size int64) (*spoolBlob, error) {
	b := &spoolBlob{
		prefix: prefix,
		ramCap: ramCap,
		src:    r,
		size:   size,
		known:  size >= 0,
	}
	if b.ramCap <= 0 {
		b.ramCap = spoolMemLimit
	}
	if !b.known {
		// The size is required (e.g. by zip.Reader), so buffer everything up
		// front; the size is then exact.
		if err := b.ensure(int64(^uint64(0) >> 1)); err != nil {
			b.Close()
			return nil, err
		}
		b.size = b.length()
		b.known = true
	}
	return b, nil
}

// length returns the number of bytes pulled from the source so far.
func (b *spoolBlob) length() int64 {
	return int64(len(b.ram)) + b.spillN
}

// Size implements book.Blob.
func (b *spoolBlob) Size() (int64, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if !b.known {
		if err := b.ensure(int64(^uint64(0) >> 1)); err != nil {
			return 0, err
		}
		b.size = b.length()
		b.known = true
	}
	return b.size, nil
}

// ReadAt implements io.ReaderAt.
func (b *spoolBlob) ReadAt(p []byte, off int64) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.readAtLocked(p, off)
}

// readAtLocked is ReadAt with mu already held.
func (b *spoolBlob) readAtLocked(p []byte, off int64) (int, error) {
	err := b.ensure(off + int64(len(p)))
	n := b.copyFrom(off, p)
	if n < len(p) {
		if err != nil {
			return n, err
		}
		return n, io.EOF
	}
	return n, nil
}

// Read implements io.Reader by reading from the current seek position. It is
// only needed so spoolBlob can be handed to mholt's 7z reader, which requires
// the source to be an io.Reader and additionally asserts that it is an
// io.ReaderAt and io.Seeker.
func (b *spoolBlob) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	n, err := b.readAtLocked(p, b.pos)
	b.pos += int64(n)
	return n, err
}

// copyFrom copies up to len(p) bytes of content starting at off into p. It
// must be called with mu held. Returns the number of bytes copied.
func (b *spoolBlob) copyFrom(off int64, p []byte) int {
	total := 0
	for total < len(p) {
		o := off + int64(total)
		if o >= b.length() {
			break
		}
		if o < int64(len(b.ram)) {
			c := copy(p[total:], b.ram[o:])
			total += c
			continue
		}
		so := o - int64(len(b.ram))
		c, _ := b.spill.ReadAt(p[total:], so)
		total += c
	}
	return total
}

// ensure pulls from the source until length() >= n, EOF, or an error. Any
// source error is sticky.
func (b *spoolBlob) ensure(n int64) error {
	if b.srcErr != nil {
		return b.srcErr
	}
	for b.length() < n && !b.eof {
		if b.buf == nil {
			b.buf = make([]byte, 32*1024)
		}
		m, err := b.src.Read(b.buf)
		if m > 0 {
			if aerr := b.append(b.buf[:m]); aerr != nil {
				b.srcErr = aerr
				return aerr
			}
		}
		if err == io.EOF {
			b.eof = true
		} else if err != nil {
			b.srcErr = err
			return err
		}
	}
	return b.srcErr
}

// append stores data into memory (up to the cap) and spills the rest.
func (b *spoolBlob) append(data []byte) error {
	space := b.ramCap - int64(len(b.ram))
	if space > 0 {
		if int64(len(data)) <= space {
			b.ram = append(b.ram, data...)
			return nil
		}
		b.ram = append(b.ram, data[:space]...)
		data = data[space:]
	}
	if b.spill == nil {
		f, err := osCreateTemp("", b.prefix+"*")
		if err != nil {
			return err
		}
		b.spill = f
	}
	if _, err := b.spill.Write(data); err != nil {
		return err
	}
	b.spillN += int64(len(data))
	return nil
}

// Seek implements io.Seeker. For an unknown-size blob the whole source is
// already buffered, so the end offset is always the exact size.
func (b *spoolBlob) Seek(offset int64, whence int) (int64, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	var newPos int64
	switch whence {
	case io.SeekStart:
		newPos = offset
	case io.SeekCurrent:
		newPos = b.pos + offset
	case io.SeekEnd:
		if !b.known {
			if err := b.ensure(int64(^uint64(0) >> 1)); err != nil {
				return 0, err
			}
			b.size = b.length()
			b.known = true
		}
		newPos = b.size + offset
	default:
		return 0, errors.New("spoolBlob: invalid whence")
	}
	if newPos < 0 {
		return 0, errors.New("spoolBlob: negative position")
	}
	b.pos = newPos
	return newPos, nil
}

// Tell reports the current seek position.
func (b *spoolBlob) Tell() (int64, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.pos, nil
}

// Close releases the spill file. It is safe to call more than once.
func (b *spoolBlob) Close() {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return
	}
	b.closed = true
	if b.spill != nil {
		name := b.spill.Name()
		_ = b.spill.Close()
		_ = os.Remove(name)
		b.spill = nil
	}
}
