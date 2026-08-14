package processor

import (
	"archive/tar"
	"archive/zip"
	"compress/bzip2"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/f0d0r/margaret-ebook-library/pkg/model"
	"github.com/f0d0r/margaret-tools/internal/scanner"
	"github.com/f0d0r/margaret-tools/internal/source"
	"github.com/mholt/archives"
)

// processArchive unpacks an archive and processes every ebook and nested
// archive found inside. depth is the archive's own nesting level: 0 for an
// archive found by the scanner, 1 for an archive inside another archive.
// src is the worker's Source, used to open the archive on disk or over FTP.
func (p *Processor) processArchive(ctx context.Context, r scanner.Result, depth int, src source.Source) {
	rc, err := src.Open(r.Path)
	if err != nil {
		p.fail(r.Path, err)
		p.report()
		return
	}
	defer func() {
		_ = rc.Close()
	}()
	if f, ok := rc.(*os.File); ok {
		p.processArchiveStream(ctx, r.Format, f, r.Path, depth, -1)
		return
	}
	// Remote (e.g. FTP) archives are streamed and spooled lazily.
	p.processArchiveStream(ctx, r.Format, rc, r.Path, depth, r.Size)
}

// processArchiveStream processes an archive read from r. r must either be
// positioned at the start of the archive (zip, tar) or wrap the archive
// contents (single-stream gz/bz2). size is the uncompressed size of the
// stream, or -1 when unknown (the gz/bz2 single-stream case).
func (p *Processor) processArchiveStream(ctx context.Context, format string, r io.Reader, displayPath string, depth int, size int64) {
	if depth >= p.cfg.MaxDepth {
		p.fail(displayPath, fmt.Errorf("archive nesting depth %d exceeds the limit of %d (raise it with --archive-depth)", depth, p.cfg.MaxDepth))
		p.report()
		return
	}
	switch format {
	case "zip":
		p.processZipStream(ctx, r, displayPath, depth, size)
	case "tar":
		p.processTar(ctx, r, displayPath, depth)
	case "tgz":
		gz, err := gzip.NewReader(r)
		if err != nil {
			p.fail(displayPath, err)
			p.report()
			return
		}
		defer func() {
			_ = gz.Close()
		}()
		p.processTar(ctx, gz, displayPath, depth)
	case "tbz2":
		p.processTar(ctx, bzip2.NewReader(r), displayPath, depth)
	case "gz":
		gz, err := gzip.NewReader(r)
		if err != nil {
			p.fail(displayPath, err)
			p.report()
			return
		}
		defer func() {
			_ = gz.Close()
		}()
		p.processSingleStream(ctx, gz, dropSuffix(displayPath), depth+1)
	case "bz2":
		p.processSingleStream(ctx, bzip2.NewReader(r), dropSuffix(displayPath), depth+1)
	case "rar":
		rar := archives.Rar{Password: p.cfg.Password}
		if err := rar.Extract(ctx, r, p.handleArchiveFile(displayPath, depth)); err != nil {
			p.fail(displayPath, err)
			p.report()
		}
	case "7z":
		p.processSevenZipStream(ctx, r, displayPath, depth, size)
	default:
		p.fail(displayPath, fmt.Errorf("unsupported archive format %q", format))
		p.report()
	}
}

// processZipStream reads a zip archive from r, adapting it to random access
// via a spoolBlob (kept in memory up to the limit, spilled to a temp file
// beyond) instead of a plain temp-file copy.
func (p *Processor) processZipStream(ctx context.Context, r io.Reader, displayPath string, depth int, size int64) {
	b, cleanup, err := p.readerAtView(displayPath, "margaret-archive-", size, r)
	if err != nil {
		p.fail(displayPath, err)
		p.report()
		return
	}
	defer cleanup()
	bsize, err := b.Size()
	if err != nil {
		p.fail(displayPath, err)
		p.report()
		return
	}
	p.processZip(ctx, b, bsize, displayPath, depth)
}

// readerAtView returns r as a random-access blob (model.Blob). An *os.File is
// used directly so no copy is made; any other stream is wrapped in a spoolBlob
// that buffers lazily in memory up to the configured limit and spills the rest
// to a temporary file. cleanup must be called when the blob is no longer
// needed.
func (p *Processor) readerAtView(displayPath, prefix string, size int64, r io.Reader) (model.Blob, func(), error) {
	if f, ok := r.(*os.File); ok {
		if size < 0 {
			st, err := f.Stat()
			if err != nil {
				return nil, nil, err
			}
			size = st.Size()
		}
		b, err := model.NewFileBlob(f)
		if err != nil {
			return nil, nil, err
		}
		return b, func() {}, nil
	}
	b, err := newSpoolBlob(prefix, p.cfg.SpoolMemLimit, r, size)
	if err != nil {
		return nil, nil, err
	}
	return b, b.Close, nil
}

// parseEbookStream adapts the ebook content read from r to a model.Blob and
// parses it. size is the member's uncompressed size, or -1 when unknown
// (single-stream gz/bz2). The content is spooled lazily: kept in memory up to
// the configured limit and spilled to a temporary file beyond. Because the
// parser reads only what it needs, formats whose metadata lives at the start
// of the file are not fully materialized. Any error while the content is read
// (for example a decryption failure in an encrypted archive member) is
// reported for displayPath.
func (p *Processor) parseEbookStream(ctx context.Context, displayPath, format string, size int64, r io.Reader) {
	if ctx.Err() != nil {
		return
	}
	b, err := newSpoolBlob("margaret-ebook-", p.cfg.SpoolMemLimit, r, size)
	if err != nil {
		p.fail(displayPath, err)
		p.report()
		return
	}
	defer b.Close()
	p.parseEbook(ctx, displayPath, format, b)
}

// seekReadAt is the interface a source must satisfy for the mholt 7z reader:
// io.Reader (as required by the Extract signature) plus the ReaderAt and
// Seeker it needs for random access.
type seekReadAt interface {
	io.Reader
	io.Seeker
	io.ReaderAt
}

// processSevenZipStream reads a 7z archive from r, adapting it to the
// seekable random access the 7z reader requires via a spoolBlob when r is not
// already an *os.File.
func (p *Processor) processSevenZipStream(ctx context.Context, r io.Reader, displayPath string, depth int, size int64) {
	sra, cleanup, err := p.seekReadAtView(displayPath, size, r)
	if err != nil {
		p.fail(displayPath, err)
		p.report()
		return
	}
	defer cleanup()
	p.processSevenZip(ctx, sra, displayPath, depth)
}

// seekReadAtView returns r as a value that is both a Reader, a Seeker, and a
// ReaderAt: the *os.File itself when r is one, otherwise a spoolBlob. cleanup
// must be called when the value is no longer needed.
func (p *Processor) seekReadAtView(displayPath string, size int64, r io.Reader) (seekReadAt, func(), error) {
	if f, ok := r.(*os.File); ok {
		return f, func() {}, nil
	}
	b, err := newSpoolBlob("margaret-archive-", p.cfg.SpoolMemLimit, r, size)
	if err != nil {
		return nil, nil, err
	}
	return b, b.Close, nil
}

// processSevenZip reads a 7z archive from a seekable random-access source.
func (p *Processor) processSevenZip(ctx context.Context, sra seekReadAt, displayPath string, depth int) {
	sz := archives.SevenZip{Password: p.cfg.Password}
	if err := sz.Extract(ctx, sra, p.handleArchiveFile(displayPath, depth)); err != nil {
		p.fail(displayPath, err)
		p.report()
	}
}

// handleArchiveFile returns a handler for archives.Extract that processes each
// member of a rar or 7z archive, mirroring how zip and tar members are
// handled: ebooks are parsed and nested archives unpacked, while member-level
// errors are reported individually without stopping the walk.
func (p *Processor) handleArchiveFile(displayPath string, depth int) func(context.Context, archives.FileInfo) error {
	return func(ctx context.Context, f archives.FileInfo) error {
		if ctx.Err() != nil {
			return fs.SkipAll
		}
		if f.IsDir() {
			return nil
		}
		if _, ok := scanner.FormatOf(f.NameInArchive); !ok {
			return nil
		}
		opened, err := f.Open()
		if err != nil {
			p.fail(displayPath+"!"+f.NameInArchive, err)
			p.report()
			return nil
		}
		p.handleMember(ctx, f.NameInArchive, opened, displayPath, depth+1, f.Size())
		_ = opened.Close()
		return nil
	}
}

func (p *Processor) processZip(ctx context.Context, ra io.ReaderAt, size int64, displayPath string, depth int) {
	zr, err := zip.NewReader(ra, size)
	if err != nil {
		p.fail(displayPath, err)
		p.report()
		return
	}
	for _, zf := range zr.File {
		if ctx.Err() != nil {
			return
		}
		if zf.FileInfo().IsDir() {
			continue
		}
		if zf.Flags&0x1 != 0 {
			p.fail(displayPath+"!"+zf.Name, errors.New("password-protected zip member; --archive-password is not supported yet"))
			p.report()
			continue
		}
		rc, err := zf.Open()
		if err != nil {
			p.fail(displayPath+"!"+zf.Name, zipMemberError(err))
			p.report()
			continue
		}
		p.handleMember(ctx, zf.Name, rc, displayPath, depth+1, int64(zf.UncompressedSize64))
		func() {
			_ = rc.Close()
		}()
	}
}

func (p *Processor) processTar(ctx context.Context, r io.Reader, displayPath string, depth int) {
	tr := tar.NewReader(r)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return
		}
		if err != nil {
			p.fail(displayPath, err)
			p.report()
			return
		}
		if ctx.Err() != nil {
			return
		}
		if hdr.Typeflag != tar.TypeReg {
			continue
		}
		p.handleMember(ctx, hdr.Name, io.LimitReader(tr, hdr.Size), displayPath, depth+1, hdr.Size)
	}
}

// processSingleStream handles the decompressed content of a gz or bz2 file,
// whose only member is a single file. Its format is derived from the name
// with the compression suffix removed.
func (p *Processor) processSingleStream(ctx context.Context, r io.Reader, innerPath string, depth int) {
	format, ok := scanner.FormatOf(innerPath)
	if !ok {
		return
	}
	if scanner.IsArchiveFormat(format) {
		p.processArchiveStream(ctx, format, r, innerPath, depth, -1)
		return
	}
	p.found.Add(1)
	p.parseEbookStream(ctx, innerPath, format, -1, r)
}

// handleMember processes a single entry of an unpacked archive. name is the
// member's name inside the archive; parentPath is the path of the archive
// that contained it. size is the member's uncompressed size, or -1 when
// unknown.
func (p *Processor) handleMember(ctx context.Context, name string, r io.Reader, parentPath string, depth int, size int64) {
	format, ok := scanner.FormatOf(name)
	if !ok {
		return
	}
	display := parentPath + "!" + name
	if scanner.IsArchiveFormat(format) {
		p.processArchiveStream(ctx, format, r, display, depth, size)
		return
	}
	p.found.Add(1)
	p.parseEbookStream(ctx, display, format, size, r)
}

// dropSuffix removes the last extension from name, so that "a.epub.gz"
// becomes "a.epub".
func dropSuffix(name string) string {
	ext := filepath.Ext(name)
	if ext == "" {
		return name
	}
	return name[:len(name)-len(ext)]
}

// zipMemberError explains why a zip member could not be opened.
func zipMemberError(err error) error {
	if errors.Is(err, zip.ErrAlgorithm) {
		return fmt.Errorf("zip member uses an unsupported compression or encryption algorithm: %w", err)
	}
	return fmt.Errorf("zip member: %w", err)
}
