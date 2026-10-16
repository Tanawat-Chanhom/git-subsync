package cli

import (
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"
	"unicode"

	"golang.org/x/term"
	"golang.org/x/text/width"

	"github.com/Tanawat-Chanhom/git-subsync/internal/syncer"
)

const (
	barCells     = 20
	progressTick = 120 * time.Millisecond
	clearLine    = "\r\x1b[K"
)

var spinnerFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

// liveProgress draws a status line on a terminal while a folder syncs:
// file counts while scanning, then a progress bar for the transfers.
// Result lines written to it go to out, above the status line.
type liveProgress struct {
	out   io.Writer // result lines
	tty   io.Writer // the status line
	width func() int
	now   func() time.Time
	name  string // the folder being synced

	mu        sync.Mutex
	closed    bool
	shown     bool // a status line is on screen
	frame     int
	scanning  bool
	local     int
	remote    int
	transfers int
	index     int // of the current transfer, from 1
	total     int64
	done      int64
	path      string
	up        bool
	started   time.Time // of the first transfer

	stop    chan struct{}
	stopped chan struct{}
}

var _ syncer.Progress = (*liveProgress)(nil)

// newLiveProgress starts drawing on tty, which must be a terminal, until
// Close.
func newLiveProgress(out io.Writer, tty *os.File, name string) *liveProgress {
	p := &liveProgress{
		out: out, tty: tty, name: name, now: time.Now, scanning: true,
		width: func() int {
			if w, _, err := term.GetSize(int(tty.Fd())); err == nil && w > 0 {
				return w
			}
			return 80
		},
		stop: make(chan struct{}), stopped: make(chan struct{}),
	}
	go p.animate()
	return p
}

// animate redraws the status line regularly, to turn the spinner and
// update the counts.
func (p *liveProgress) animate() {
	defer close(p.stopped)
	t := time.NewTicker(progressTick)
	defer t.Stop()
	for {
		select {
		case <-p.stop:
			return
		case <-t.C:
			p.mu.Lock()
			if !p.closed {
				p.frame++
				p.draw()
			}
			p.mu.Unlock()
		}
	}
}

// Close stops drawing and clears the status line. Later writes go straight
// to out.
func (p *liveProgress) Close() {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return
	}
	p.closed = true
	p.mu.Unlock()
	if p.stop != nil {
		close(p.stop)
		<-p.stopped
	}
	p.mu.Lock()
	p.clear()
	p.mu.Unlock()
}

// Write prints a result line above the status line.
func (p *liveProgress) Write(b []byte) (int, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.clear()
	n, err := p.out.Write(b)
	if !p.closed {
		p.draw()
	}
	return n, err
}

func (p *liveProgress) Scanned(local, remote int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.local, p.remote = local, remote
}

func (p *liveProgress) Planned(transfers int, bytes int64) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.scanning, p.transfers, p.total = false, transfers, bytes
	p.draw()
}

func (p *liveProgress) Start(path string, size int64, up bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.index++
	p.path, p.up = path, up
	if p.started.IsZero() {
		p.started = p.now()
	}
	p.draw()
}

func (p *liveProgress) Add(n int64) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.done += n
}

func (p *liveProgress) Done() {}

func (p *liveProgress) draw() {
	line := p.status()
	if line == "" {
		p.clear()
		return
	}
	fmt.Fprint(p.tty, clearLine+line)
	p.shown = true
}

func (p *liveProgress) clear() {
	if p.shown {
		fmt.Fprint(p.tty, clearLine)
		p.shown = false
	}
}

// status returns the status line, fitted to the terminal width.
func (p *liveProgress) status() string {
	room := p.width() - 1 // writing the last column makes some terminals wrap
	spin := spinnerFrames[p.frame%len(spinnerFrames)]
	if p.scanning {
		return fitEnd(fmt.Sprintf("%s 🔍 Scanning %s… %d local · %d on Drive", spin, p.name, p.local, p.remote), room)
	}
	if p.index == 0 {
		return "" // nothing to transfer, or not started yet
	}

	pct := 0
	switch {
	case p.total > 0:
		pct = int(p.done * 100 / p.total)
	case p.transfers > 0:
		pct = p.index * 100 / p.transfers
	}
	pct = min(max(pct, 0), 100)
	filled := pct * barCells / 100
	bar := "[" + strings.Repeat("█", filled) + strings.Repeat("░", barCells-filled) + "]"

	icon := "📥"
	if p.up {
		icon = "📤"
	}
	head := spin + " " + icon + " "
	// Fixed widths keep the bar in place as the numbers change.
	count := fmt.Sprintf("%d/%d", p.index, p.transfers)
	sizes := fmt.Sprintf("%14s", formatPair(p.done, p.total))
	speed := ""
	if elapsed := p.now().Sub(p.started); elapsed >= 500*time.Millisecond {
		speed = formatBytes(int64(float64(max(p.done, 0))/elapsed.Seconds())) + "/s"
	}
	speed = fmt.Sprintf("%10s", speed)

	// Drop details until the file name has some room.
	for _, tail := range []string{
		fmt.Sprintf("  %s %3d%%  %s  %s  %s", bar, pct, sizes, speed, count),
		fmt.Sprintf("  %s %3d%%  %s  %s", bar, pct, sizes, count),
		fmt.Sprintf("  %s %3d%%  %s", bar, pct, count),
		fmt.Sprintf("  %3d%%  %s", pct, count),
	} {
		if name := room - cells(head) - cells(tail); name >= 12 {
			return head + fitStart(p.path, name) + tail
		}
	}
	return fitEnd(fmt.Sprintf("%s %s %3d%%", spin, icon, pct), room)
}

var byteUnits = []string{"B", "KB", "MB", "GB", "TB"}

// unitFor returns the decimal unit that suits n, and its size in bytes.
func unitFor(n int64) (string, float64) {
	scale, i := 1.0, 0
	for float64(n) >= scale*1000 && i < len(byteUnits)-1 {
		scale *= 1000
		i++
	}
	return byteUnits[i], scale
}

// formatBytes formats n like 12.4 MB.
func formatBytes(n int64) string {
	unit, scale := unitFor(n)
	if scale == 1 {
		return fmt.Sprintf("%d B", n)
	}
	return fmt.Sprintf("%.1f %s", float64(n)/scale, unit)
}

// formatPair formats done and total in total's unit, like 12.4/40.0 MB.
func formatPair(done, total int64) string {
	unit, scale := unitFor(total)
	if scale == 1 {
		return fmt.Sprintf("%d/%d B", done, total)
	}
	return fmt.Sprintf("%.1f/%.1f %s", float64(done)/scale, float64(total)/scale, unit)
}

// cells returns how many terminal columns s takes: combining marks (such
// as Thai vowel and tone marks) take none, and wide characters and emoji
// take two.
func cells(s string) int {
	n := 0
	for _, r := range s {
		n += runeCells(r)
	}
	return n
}

func runeCells(r rune) int {
	switch {
	case unicode.In(r, unicode.Mn, unicode.Me, unicode.Cf):
		return 0
	case r >= 0x1F300 && r <= 0x1FAFF:
		return 2
	}
	switch width.LookupRune(r).Kind() {
	case width.EastAsianWide, width.EastAsianFullwidth:
		return 2
	}
	return 1
}

// fitStart pads s to n columns, or cuts it from the start, keeping the end
// of a path, which names the file.
func fitStart(s string, n int) string {
	if w := cells(s); w <= n {
		return s + strings.Repeat(" ", n-w)
	}
	runes := []rune(s)
	w, i := 1, len(runes) // 1 for the ellipsis
	for i > 0 && w+runeCells(runes[i-1]) <= n {
		i--
		w += runeCells(runes[i])
	}
	for i < len(runes) && runeCells(runes[i]) == 0 {
		i++ // a mark whose letter was cut off
	}
	return "…" + string(runes[i:]) + strings.Repeat(" ", n-w)
}

// fitEnd cuts s to at most n columns.
func fitEnd(s string, n int) string {
	if cells(s) <= n {
		return s
	}
	w := 1
	var b strings.Builder
	for _, r := range s {
		if w+runeCells(r) > n {
			break
		}
		w += runeCells(r)
		b.WriteRune(r)
	}
	return b.String() + "…"
}
