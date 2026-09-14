package cli

import (
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/f0d0r/margaret-tools/internal/processor"
	"golang.org/x/term"
)

const barWidth = 20
const spinnerFrames = "|/-\\"

const anSiClearLine = "\x1b[K" // erase to end of line

// progressBar renders a self-updating, single-line progress display to a
// writer. It is safe for concurrent use: every update is serialized through a
// mutex so that reports arriving from several worker goroutines cannot
// interleave or drop updates. The maximum may change at any time (e.g. while
// new files keep being discovered) without affecting the work already counted.
//
// The display occupies exactly one terminal line. Each frame is rewritten in
// place with a carriage return and a clear-to-end-of-line, and the text is
// truncated to the terminal width, so it never wraps and never accumulates
// lines — regardless of how many workers report at once or how long the path
// of the file being processed is. It writes to stderr so stdout stays clean
// for the final report and the failures JSON.
type progressBar struct {
	mu       sync.Mutex
	w        io.Writer
	desc     string
	width    int
	cols     int // terminal width in cells; keeps the line from wrapping
	max      int64
	cur      int64
	throttle time.Duration
	last     time.Time
	done     bool
	spin     int
	active   bool
	names    []string
}

func newProgressBar() *progressBar {
	// Default to a sane width when the output is not a terminal (e.g. in
	// tests or when piped); the renderer re-queries this every frame so the
	// bar tracks window resizes too.
	cols := 80
	if w, _, err := term.GetSize(int(os.Stderr.Fd())); err == nil && w > 0 {
		cols = w
	}
	return &progressBar{
		w:        os.Stderr,
		desc:     "scanning ebooks",
		width:    barWidth,
		cols:     cols,
		throttle: 50 * time.Millisecond,
	}
}

// setMax updates the bar's maximum. It never shrinks, and it never drops below
// the current value, so the ratio can never show more work done than the total
// (which can otherwise happen while parsing outpaces an ongoing scan).
func (b *progressBar) setMax(max int64) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.done {
		return
	}
	if max < b.cur {
		max = b.cur
	}
	if max <= b.max {
		return
	}
	b.max = max
	b.draw()
}

// add advances the current value by n. If parsing has run ahead of the scan
// (so the current count exceeds the discovered maximum), the maximum is raised
// to match so the ratio can never show more done than the total.
func (b *progressBar) add(n int64) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.done || n <= 0 {
		return
	}
	b.cur += n
	if b.cur > b.max {
		b.max = b.cur
	}
	b.draw()
}

// finish clears the progress line and moves past it so the final report below
// starts on a fresh, clean line.
func (b *progressBar) finish() {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.done {
		return
	}
	b.done = true
	_, _ = io.WriteString(b.w, "\r"+anSiClearLine)
	_, _ = fmt.Fprintln(b.w)
}

// draw redraws the bar in place unless too little time has passed since the
// last draw. It must be called with the mutex already held.
func (b *progressBar) draw() {
	if b.done {
		return
	}
	now := time.Now()
	if !b.last.IsZero() && now.Sub(b.last) < b.throttle {
		return
	}
	b.last = now
	b.render()
}

// render writes the single progress line, overwriting whatever was there
// before via carriage return and clear-to-end-of-line. It must be called with
// the mutex already held.
func (b *progressBar) render() {
	// Re-read the terminal width each frame so a window resize while running
	// stays aligned. Fall back to the last known width (or 80) on error.
	if w, _, err := term.GetSize(int(os.Stderr.Fd())); err == nil && w > 0 {
		b.cols = w
	}
	if b.cols < 1 {
		b.cols = 80
	}

	fill := 0
	if b.max > 0 {
		fill = int(float64(b.cur) / float64(b.max) * float64(b.width))
	}
	if fill > b.width {
		fill = b.width
	}
	if fill < 0 {
		fill = 0
	}
	cursor := ' '
	if b.active {
		cursor = rune(spinnerFrames[b.spin%len(spinnerFrames)])
		b.spin++
	}
	bar := strings.Repeat("█", fill) + strings.Repeat("░", b.width-fill)

	line := fmt.Sprintf("%s %c [%s] %d/%d", b.desc, cursor, bar, b.cur, b.max)
	if len(b.names) > 0 {
		line += "  " + b.names[len(b.names)-1]
	}
	// Truncate to the terminal width so the line can never wrap to a second
	// physical row; a wrapped line would break the single-line in-place
	// rewrite and smear across the final report.
	line = fit(line, b.cols)

	_, _ = io.WriteString(b.w, "\r"+line+anSiClearLine)
}

// fit truncates s so it occupies at most n terminal cells, appending an
// ellipsis when something had to be cut off. It is safe for lines that hold
// wide characters: it counts runes, so a multibyte sequence is never split.
func fit(s string, n int) string {
	r := []rune(s)
	if n <= 0 || len(r) <= n {
		return s
	}
	if n == 1 {
		return string(r[:1])
	}
	return string(r[:n-1]) + "…"
}

// progressReporter serializes progress bar updates. Reports arrive from
// several worker goroutines at once. The scanner reports Found incrementally
// after each directory, so the bar's maximum is only raised as new files are
// discovered; parsing may briefly outrun the scan, which setMax guards against.
type progressReporter struct {
	bar         *progressBar
	mu          sync.Mutex
	lastParsed  int64 // last reported Parsed
	lastSkipped int64 // last reported Skipped
	maxFound    int64 // highest Found seen (used as max once parsing starts)
}

func (r *progressReporter) report(p processor.Progress) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if p.Found > r.maxFound {
		r.maxFound = p.Found
		r.bar.setMax(r.maxFound)
	}

	if p.Parsed > r.lastParsed {
		r.bar.add(p.Parsed - r.lastParsed)
		r.lastParsed = p.Parsed
	}

	// Skipped units are seen work too: without this the bar would stall
	// below its maximum on resume runs where most units skip.
	if p.Skipped > r.lastSkipped {
		r.bar.add(p.Skipped - r.lastSkipped)
		r.lastSkipped = p.Skipped
	}

	r.applyActive(p)
}

// tick redraws the bar to reflect the given snapshot. It is used by a CLI
// side ticker so the bar keeps animating (spinner + the item currently being
// processed) even when a long-running item emits no progress event.
func (r *progressReporter) tick(p processor.Progress) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.applyActive(p)
}

// applyActive copies the in-flight state from p onto the bar and forces a
// redraw so a running item stays visible even while the counters are idle.
func (r *progressReporter) applyActive(p processor.Progress) {
	r.bar.active = p.Active > 0
	r.bar.names = append(r.bar.names[:0], p.Current...)
	r.bar.draw()
}

// sync forces the bar to the final totals from stats, so the bar ends at the
// true numbers even if the last live progress event was throttled away or the
// very last items were reported after it.
func (r *progressReporter) sync(p processor.Progress) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.bar.setMax(p.Found)
	if p.Parsed > r.lastParsed {
		r.bar.add(p.Parsed - r.lastParsed)
		r.lastParsed = p.Parsed
	}
	if p.Skipped > r.lastSkipped {
		r.bar.add(p.Skipped - r.lastSkipped)
		r.lastSkipped = p.Skipped
	}
	r.bar.active = false
	r.bar.names = nil
}
