package parser

import (
	"context"

	"github.com/f0d0r/margaret-ebook-library/book"
)

// contextBlob stops reads once the context is done. The ebook library reads
// through book.Blob, so wrapping the blob makes format detection, hashing and
// resource extraction abandon the current file on cancellation.
type contextBlob struct {
	ctx context.Context
	b   book.Blob
}

func withContext(ctx context.Context, b book.Blob) book.Blob {
	if ctx == nil || b == nil {
		return b
	}
	return contextBlob{ctx: ctx, b: b}
}

func (c contextBlob) Size() (int64, error) {
	if err := c.ctx.Err(); err != nil {
		return 0, err
	}
	return c.b.Size()
}

func (c contextBlob) ReadAt(p []byte, off int64) (int, error) {
	if err := c.ctx.Err(); err != nil {
		return 0, err
	}
	n, err := c.b.ReadAt(p, off)
	if ctxErr := c.ctx.Err(); ctxErr != nil {
		return n, ctxErr
	}
	return n, err
}
