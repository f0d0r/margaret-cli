package textdoc

import "unicode/utf8"

// HeadGate captures up to limit bytes of a decoded text stream and runs check
// once the head is complete. Returning an error from Write aborts the stream,
// so a caller can reject a document before it is read to the end. A nil check
// is allowed and only captures the head.
type HeadGate struct {
	limit   int
	size    int64
	check   func(head string, size int64) error
	buf     []byte
	checked bool
}

// NewHeadGate creates a gate that keeps at most limit bytes and passes the
// rune-aligned head and size to check.
func NewHeadGate(limit int, size int64, check func(head string, size int64) error) *HeadGate {
	return &HeadGate{
		limit: limit,
		size:  size,
		check: check,
		buf:   make([]byte, 0, limit),
	}
}

// Write implements io.Writer over the decoded text stream.
func (g *HeadGate) Write(p []byte) (int, error) {
	if !g.checked && len(g.buf) < g.limit {
		g.buf = append(g.buf, p[:min(len(p), g.limit-len(g.buf))]...)
	}
	if !g.checked && len(g.buf) >= g.limit {
		if err := g.run(); err != nil {
			return 0, err
		}
	}
	return len(p), nil
}

// Finish runs the check when the stream ended before the head limit.
func (g *HeadGate) Finish() error {
	if g.checked {
		return nil
	}
	return g.run()
}

// Head returns the captured head cut to a rune boundary.
func (g *HeadGate) Head() string {
	return string(truncateUTF8(g.buf, g.limit))
}

func (g *HeadGate) run() error {
	g.checked = true
	if g.check == nil {
		return nil
	}
	return g.check(g.Head(), g.size)
}

// truncateUTF8 cuts b to at most n bytes without splitting a rune.
func truncateUTF8(b []byte, n int) []byte {
	if len(b) > n {
		b = b[:n]
	}
	for len(b) > 0 && !utf8.Valid(b) {
		b = b[:len(b)-1]
	}
	return b
}
