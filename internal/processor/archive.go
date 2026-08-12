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
	"os"
	"path/filepath"

	"github.com/f0d0r/margaret-tools/internal/scanner"
)

// processArchive unpacks an archive and processes every ebook and nested
// archive found inside. depth is the archive's own nesting level: 0 for an
// archive found by the scanner, 1 for an archive inside another archive.
func (p *Processor) processArchive(ctx context.Context, r scanner.Result, depth int) {
	f, err := os.Open(r.Path)
	if err != nil {
		p.fail(r.Path, err)
		p.report()
		return
	}
	defer func() {
		_ = f.Close()
	}()
	p.processArchiveStream(ctx, r.Format, f, r.Path, depth)
}

// processArchiveStream processes an archive read from r. r must either be
// positioned at the start of the archive (zip, tar) or wrap the archive
// contents (single-stream gz/bz2).
func (p *Processor) processArchiveStream(ctx context.Context, format string, r io.Reader, displayPath string, depth int) {
	if depth >= p.cfg.MaxDepth {
		p.fail(displayPath, fmt.Errorf("archive nesting depth %d exceeds the limit of %d (raise it with --archive-depth)", depth, p.cfg.MaxDepth))
		p.report()
		return
	}
	switch format {
	case "zip":
		p.processZipStream(ctx, r, displayPath, depth)
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
	default:
		p.fail(displayPath, fmt.Errorf("unsupported archive format %q", format))
		p.report()
	}
}

// processZipStream reads a zip archive from r. The zip format needs random
// access, so a stream that is not already an *os.File is first spooled to a
// temporary file.
func (p *Processor) processZipStream(ctx context.Context, r io.Reader, displayPath string, depth int) {
	if f, ok := r.(*os.File); ok {
		fi, err := f.Stat()
		if err != nil {
			p.fail(displayPath, err)
			p.report()
			return
		}
		p.processZip(ctx, f, fi.Size(), displayPath, depth)
		return
	}
	tmp, err := os.CreateTemp("", "margaret-archive-*.zip")
	if err != nil {
		p.fail(displayPath, err)
		p.report()
		return
	}
	defer func() {
		_ = tmp.Close()
		_ = os.Remove(tmp.Name())
	}()
	if _, err := io.Copy(tmp, r); err != nil {
		p.fail(displayPath, err)
		p.report()
		return
	}
	fi, err := tmp.Stat()
	if err != nil {
		p.fail(displayPath, err)
		p.report()
		return
	}
	p.processZip(ctx, tmp, fi.Size(), displayPath, depth)
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
		p.handleMember(ctx, zf.Name, rc, displayPath, depth+1)
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
		p.handleMember(ctx, hdr.Name, io.LimitReader(tr, hdr.Size), displayPath, depth+1)
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
		p.processArchiveStream(ctx, format, r, innerPath, depth)
		return
	}
	p.found.Add(1)
	p.parseEbook(ctx, innerPath, format, r)
}

// handleMember processes a single entry of an unpacked archive. name is the
// member's name inside the archive; parentPath is the path of the archive
// that contained it.
func (p *Processor) handleMember(ctx context.Context, name string, r io.Reader, parentPath string, depth int) {
	format, ok := scanner.FormatOf(name)
	if !ok {
		return
	}
	display := parentPath + "!" + name
	if scanner.IsArchiveFormat(format) {
		p.processArchiveStream(ctx, format, r, display, depth)
		return
	}
	p.found.Add(1)
	p.parseEbook(ctx, display, format, r)
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
