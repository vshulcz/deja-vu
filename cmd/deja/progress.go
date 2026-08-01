package main

import (
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/vshulcz/deja-vu/internal/index"
)

// The first build is the one moment a user waits on deja, and it used to be
// ten seconds of nothing: the harnesses are parsed concurrently and were only
// narrated once every one of them had finished. This draws the mark straight
// away and fills the column beside it as the stores land, so the wait shows
// its work and settles into the same greeting that already ships.
//
// Terminals only. In a pipe, a hook, or CI there is no cursor to move, so the
// plain lines stay exactly as they were.
type buildProgress struct {
	w    io.Writer
	stop chan struct{}
	done sync.WaitGroup

	mu       sync.Mutex
	phase    string
	total    int
	current  int
	harness  []harnessLine
	started  time.Time
	painted  int
	finished bool
}

type harnessLine struct {
	name     string
	sessions int
	messages int
}

const (
	progFrames  = "⠋⠙⠹⠸⠼⠴⠦⠧⠇⠏"
	progBarW    = 22
	progDone    = "█"
	progPartial = "▓"
	progEmpty   = "░"
)

func newBuildProgress(w io.Writer) *buildProgress {
	return &buildProgress{w: w, stop: make(chan struct{}), started: time.Now()}
}

func (p *buildProgress) Phase(name string, total int) {
	p.mu.Lock()
	p.phase, p.total, p.current = name, total, 0
	p.mu.Unlock()
}

func (p *buildProgress) Advance(units int) {
	p.mu.Lock()
	p.current += units
	p.mu.Unlock()
}

func (p *buildProgress) Harness(name string, sessions, messages int) {
	if messages == 0 {
		return
	}
	if name == "deja" {
		name = "notes"
	}
	p.mu.Lock()
	p.harness = append(p.harness, harnessLine{name, sessions, messages})
	p.mu.Unlock()
}

// start paints at a fixed cadence rather than on every update: parsing
// reports in bursts, and redrawing per burst makes the spinner stutter.
func (p *buildProgress) start() {
	p.done.Add(1)
	go func() {
		defer p.done.Done()
		tick := time.NewTicker(80 * time.Millisecond)
		defer tick.Stop()
		frame := 0
		for {
			select {
			case <-p.stop:
				return
			case <-tick.C:
				p.paint(frame)
				frame++
			}
		}
	}()
}

// finish erases the live area so the caller's own summary lands on a clean
// screen — the greeting that follows shows the same numbers, settled.
func (p *buildProgress) finish() {
	close(p.stop)
	p.done.Wait()
	p.mu.Lock()
	defer p.mu.Unlock()
	p.finished = true
	p.erase()
}

func (p *buildProgress) erase() {
	if p.painted == 0 {
		return
	}
	fmt.Fprintf(p.w, "\x1b[%dA", p.painted)
	fmt.Fprint(p.w, "\x1b[0J")
	p.painted = 0
}

func (p *buildProgress) paint(frame int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.finished {
		return
	}
	lines := p.render(frame)
	p.erase()
	for _, l := range lines {
		fmt.Fprintln(p.w, l)
	}
	p.painted = len(lines)
}

func (p *buildProgress) render(frame int) []string {
	info := brandInfo()
	info = append(info, "")

	spin := string([]rune(progFrames)[frame%len([]rune(progFrames))])
	phase := p.phase
	if phase == "" {
		phase = "starting"
	}
	info = append(info, logoAccent+spin+logoReset+" "+logoBold+phase+logoReset)
	info = append(info, p.bar())

	// Stores appear as they finish, newest last, so the column grows into the
	// same shape the finished greeting has. Columns are aligned on the widest
	// values so the list does not jitter as bigger stores land.
	nameW, sessW := 0, 0
	for _, h := range p.harness {
		if len(h.name) > nameW {
			nameW = len(h.name)
		}
		if n := len(fmt.Sprint(h.sessions)); n > sessW {
			sessW = n
		}
	}
	for _, h := range p.harness {
		info = append(info, fmt.Sprintf("%s%-*s%s  %s%*d%s sessions  %s%d%s messages",
			logoDim, nameW, h.name, logoReset, logoBold, sessW, h.sessions, logoReset, logoDim, h.messages, logoReset))
	}
	return logoLines(info)
}

func (p *buildProgress) bar() string {
	frac := 0.0
	if p.total > 0 {
		frac = float64(p.current) / float64(p.total)
		if frac > 1 {
			frac = 1
		}
	}
	filled := int(frac * progBarW)
	var b strings.Builder
	b.WriteString(logoAccent)
	b.WriteString(strings.Repeat(progDone, filled))
	if filled < progBarW {
		b.WriteString(progPartial)
		b.WriteString(logoDim)
		b.WriteString(strings.Repeat(progEmpty, progBarW-filled-1))
	}
	b.WriteString(logoReset)
	if p.total > 0 {
		fmt.Fprintf(&b, "  %s%3.0f%%%s", logoDim, frac*100, logoReset)
	}
	return b.String()
}

// withBuildProgress runs fn with a live build display when stdout is a
// terminal, and unchanged otherwise.
func withBuildProgress(fn func() error) error {
	// The detached warmup writes to /dev/null, which is a character device and
	// so passes for a terminal. The live display then replaced the file sink
	// withWarmupStatus had just installed and painted its animation into the
	// device that discards it, leaving every "memory is on its way" line —
	// hook, statusline, MCP, empty recall — with nothing to read (#862).
	if os.Getenv("DEJA_WARMUP_SENTINEL") != "" {
		return fn()
	}
	if !logoWanted(os.Stdout) {
		return fn()
	}
	p := newBuildProgress(os.Stdout)
	index.SetProgress(p)
	p.start()
	err := fn()
	p.finish()
	index.SetProgress(nil)
	return err
}
