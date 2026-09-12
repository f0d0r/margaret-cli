package processor

import (
	"context"
	"os"
)

// fileStat is the observed size and mtime of a scanned file, used for resume
// change detection. For archive members it describes the outer archive file:
// members cannot change without it. The zero value means unknown: no
// tier-1 skip decision is ever based on it.
type fileStat struct {
	size    int64
	mtimeNs int64
	known   bool
}

// statOf stats a local filesystem path. Anything else (FTP URLs, nested
// archive display paths like "a.zip!b.epub", missing files) yields unknown.
func statOf(path string) fileStat {
	fi, err := os.Stat(path)
	if err != nil {
		return fileStat{}
	}
	return fileStat{size: fi.Size(), mtimeNs: fi.ModTime().UnixNano(), known: true}
}

// shouldSkip reports whether displayPath is already recorded with an
// identical size and mtime, so the file can be skipped without reading it.
// It only applies in resume mode with a known stat; a new path, an unknown
// stat or a lookup error all answer false (process the file). Leaning
// towards processing on error is deliberate: at worst we redo work, we never
// silently drop a file.
func (p *ScanProcessor) shouldSkip(ctx context.Context, displayPath string, st fileStat) bool {
	if !p.cfg.Resume || !st.known {
		return false
	}
	rec, err := p.q.GetBookFileByPath(ctx, displayPath)
	if err != nil {
		return false
	}
	return rec.Size == st.size && rec.MtimeNs == st.mtimeNs
}

// skip records a resume skip and emits progress, mirroring fail() without
// the failure bookkeeping.
func (p *ScanProcessor) skip() {
	p.skipped.Add(1)
	p.report()
}
