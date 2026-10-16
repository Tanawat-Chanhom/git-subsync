package cli

import (
	"bytes"
	"io"
	"os"
	"strings"
	"testing"
	"time"
)

// testProgress returns a liveProgress without the animation goroutine, on
// a terminal of the given width, with a clock the test moves.
func testProgress(width int) (p *liveProgress, out, tty *bytes.Buffer, clock *time.Time) {
	out, tty = &bytes.Buffer{}, &bytes.Buffer{}
	t := time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)
	clock = &t
	p = &liveProgress{
		out: out, tty: tty, name: "design", scanning: true,
		width: func() int { return width },
		now:   func() time.Time { return *clock },
	}
	return p, out, tty, clock
}

// lastStatus returns the status line last drawn.
func lastStatus(tty *bytes.Buffer) string {
	s := tty.String()
	return s[strings.LastIndex(s, clearLine)+len(clearLine):]
}

func TestProgressScanning(t *testing.T) {
	p, _, tty, _ := testProgress(80)
	p.Scanned(3, 0)
	p.Scanned(3, 5)
	p.draw()
	if got, want := lastStatus(tty), "⠋ 🔍 Scanning design… 3 local · 5 on Drive"; got != want {
		t.Fatalf("status = %q, want %q", got, want)
	}
}

func TestProgressTransfer(t *testing.T) {
	p, _, tty, clock := testProgress(100)
	p.Planned(2, 2_000_000)
	if tty.Len() != 0 {
		t.Fatalf("drew %q before the first transfer", tty.String())
	}
	p.Start("photos/big.mov", 1_500_000, false)
	*clock = clock.Add(time.Second)
	p.Add(1_000_000)
	p.draw()
	got := lastStatus(tty)
	for _, want := range []string{"⠋ 📥 photos/big.mov ", "[██████████░░░░░░░░░░]  50%", "1.0/2.0 MB", "1.0 MB/s", "1/2"} {
		if !strings.Contains(got, want) {
			t.Errorf("status %q lacks %q", got, want)
		}
	}
	if w := cells(got); w != 99 {
		t.Errorf("status is %d columns, want 99 (padded so the bar stays put)", w)
	}

	p.Start("notes.md", 500_000, true)
	p.Add(-200_000) // an upload starting over
	p.draw()
	if got := lastStatus(tty); !strings.Contains(got, "📤 notes.md") || !strings.Contains(got, " 40%") || !strings.Contains(got, "2/2") {
		t.Errorf("status = %q", got)
	}
}

func TestProgressFitsNarrowTerminals(t *testing.T) {
	for _, width := range []int{20, 30, 45, 60} {
		p, _, tty, _ := testProgress(width)
		p.Planned(1, 10)
		p.Start("a/very/long/path/to/some/deeply/nested/file-with-a-long-name.txt", 10, false)
		p.Add(5)
		p.draw()
		if got := lastStatus(tty); cells(got) > width-1 {
			t.Errorf("width %d: status %q is %d columns", width, got, cells(got))
		}
	}
}

// Result lines go above the status line: it is cleared first and drawn
// again after.
func TestProgressWriteKeepsStatusBelow(t *testing.T) {
	p, out, tty, _ := testProgress(80)
	p.Scanned(1, 1)
	p.draw()
	tty.Reset()

	if _, err := io.WriteString(p, "📥 download  a.txt\n"); err != nil {
		t.Fatal(err)
	}
	if out.String() != "📥 download  a.txt\n" {
		t.Fatalf("out = %q", out.String())
	}
	if s := tty.String(); !strings.HasPrefix(s, clearLine+clearLine) || !strings.Contains(s, "Scanning") {
		t.Fatalf("tty = %q, want a clear, then the status again", s)
	}

	p.Close()
	tty.Reset()
	io.WriteString(p, "✅ 1 downloaded\n")
	if tty.Len() != 0 || !strings.HasSuffix(out.String(), "✅ 1 downloaded\n") {
		t.Fatalf("after Close: tty %q, out %q", tty.String(), out.String())
	}
	p.Close() // twice is fine
}

func TestProgressNothingToTransfer(t *testing.T) {
	p, _, tty, _ := testProgress(80)
	p.Scanned(2, 2)
	p.draw()
	p.Planned(0, 0)
	if !strings.HasSuffix(tty.String(), clearLine) || p.shown {
		t.Fatalf("tty = %q, want the status line cleared", tty.String())
	}
}

// The animation goroutine draws on its own and stops on Close.
func TestLiveProgressAnimates(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	read := make(chan string)
	go func() {
		data, _ := io.ReadAll(r)
		read <- string(data)
	}()
	var out bytes.Buffer
	p := newLiveProgress(&out, w, "design")
	p.Scanned(7, 0)
	time.Sleep(3 * progressTick)
	p.Close()
	w.Close()
	if got := <-read; !strings.Contains(got, "Scanning design… 7 local") || !strings.HasSuffix(got, clearLine) {
		t.Fatalf("tty = %q", got)
	}
}

func TestCells(t *testing.T) {
	for s, want := range map[string]int{
		"abc":      3,
		"📥":        2,
		"ไฟล์ที่":  4, // Thai vowel and tone marks take no column
		"資料.pdf":   8,
		"…/a.txt":  7,
		"⠋ [██░░]": 8,
	} {
		if got := cells(s); got != want {
			t.Errorf("cells(%q) = %d, want %d", s, got, want)
		}
	}
}

func TestFit(t *testing.T) {
	for _, tc := range []struct {
		s    string
		n    int
		want string
	}{
		{"a.txt", 8, "a.txt   "},
		{"photos/2026/big.mov", 10, "…6/big.mov"},
		{"รายงาน/ไฟล์ที่สำคัญ.pdf", 10, "…ที่สำคัญ.pdf"}, // never starts with a lone mark
	} {
		got := fitStart(tc.s, tc.n)
		if got != tc.want || cells(got) != tc.n {
			t.Errorf("fitStart(%q, %d) = %q (%d columns), want %q", tc.s, tc.n, got, cells(got), tc.want)
		}
	}
	if got := fitEnd("hello world", 6); got != "hello…" {
		t.Errorf("fitEnd = %q", got)
	}
}

func TestFormatBytes(t *testing.T) {
	for n, want := range map[int64]string{0: "0 B", 999: "999 B", 1000: "1.0 KB", 12_400_000: "12.4 MB", 3_200_000_000: "3.2 GB"} {
		if got := formatBytes(n); got != want {
			t.Errorf("formatBytes(%d) = %q, want %q", n, got, want)
		}
	}
	if got := formatPair(12_400_000, 40_000_000); got != "12.4/40.0 MB" {
		t.Errorf("formatPair = %q", got)
	}
	if got := formatPair(5, 900); got != "5/900 B" {
		t.Errorf("formatPair = %q", got)
	}
}
