package processor

import (
	"fmt"
	"strings"

	"github.com/f0d0r/margaret-cli/internal/parser"
)

const reasonPDFUnavailable = "PDF support unavailable: pdftotext and pdfinfo not found"

// skip records an actionable skip: the item was passed over without being
// parsed and is worth reporting.
func (p *ScanProcessor) skip(path, reason string) {
	p.skipped.Add(1)
	p.skipMu.Lock()
	p.skips = append(p.skips, Skip{Path: path, Reason: reason})
	p.skipMu.Unlock()
	p.report()
}

// skipAlreadyProcessed records a resume skip. Such files were processed in an
// earlier run, so they are counted but not written to the skipped report.
func (p *ScanProcessor) skipAlreadyProcessed() {
	p.alreadyProcessed.Add(1)
	p.report()
}

// minSizeReason explains a size-filter rejection in report form.
func minSizeReason(size, min int64) string {
	return fmt.Sprintf("below minimum document size (%d bytes, minimum %d)", size, min)
}

// notABookReason turns a parser.ErrNotABook error into a human-readable
// report reason, dropping the sentinel's package prefix.
func notABookReason(err error) string {
	sentinel := parser.ErrNotABook.Error()
	msg := strings.TrimPrefix(err.Error(), sentinel+": ")
	if msg == sentinel {
		return "not a book"
	}
	return msg
}
