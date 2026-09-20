package main

import (
	"fmt"
	"io"
	"strings"
	"sync"
	"time"
)

// A sync over ssh is four phases and used to narrate only the ones that had
// already finished. On a real exchange — 319,469 records to a Mac mini — that
// left minutes of nothing on screen between two lines, which reads as a hang
// and invites Ctrl-C while the transfer is running (#3801).
//
// So each phase says what it is before it starts, says it is still going every
// heartbeat while it runs, and the run ends with where the time went. Plain
// lines, not a bar: this output is read as often from a launchd log or a CI job
// as from a terminal, and the per-phase timings are what a later performance
// report needs.
//
// Nothing here prints a count per record or a path. The phase name, a byte
// total for the transfer, and elapsed time are the whole vocabulary.
type syncPhases struct {
	w io.Writer
	// beat is how often a running phase says so; floor is the shortest phase
	// the report names. Both are fields so a test can shorten them.
	beat  time.Duration
	floor time.Duration

	mu      sync.Mutex
	name    string
	started time.Time
	stop    chan struct{}
	done    sync.WaitGroup
	times   []phaseTime
	begun   time.Time
}

type phaseTime struct {
	name string
	took time.Duration
}

// syncHeartbeat is how often a phase that is still running says so. Ten
// seconds is long enough that a fast exchange prints none of them and short
// enough that a slow one never looks stopped.
const syncHeartbeat = 10 * time.Second

func newSyncPhases(w io.Writer) *syncPhases {
	return &syncPhases{w: w, beat: syncHeartbeat, floor: time.Second, begun: time.Now()}
}

// start announces a phase and begins its heartbeat. Starting a phase ends the
// one before it, so a caller cannot leave a phase open by returning early.
func (p *syncPhases) start(format string, args ...any) {
	p.finishPhase()
	name := fmt.Sprintf(format, args...)
	p.mu.Lock()
	p.name = name
	p.started = time.Now()
	p.stop = make(chan struct{})
	stop := p.stop
	p.mu.Unlock()
	fmt.Fprintf(p.w, "deja: %s…\n", name)
	p.done.Add(1)
	go func() {
		defer p.done.Done()
		tick := time.NewTicker(p.beat)
		defer tick.Stop()
		for {
			select {
			case <-stop:
				return
			case <-tick.C:
				p.mu.Lock()
				since := time.Since(p.started)
				still := p.name
				p.mu.Unlock()
				fmt.Fprintf(p.w, "deja: still %s… %s\n", still, roundSecs(since))
			}
		}
	}()
}

// note is one line inside the current phase — a batch count, a byte total —
// without ending it.
func (p *syncPhases) note(format string, args ...any) {
	fmt.Fprintf(p.w, "deja:   %s\n", fmt.Sprintf(format, args...))
}

func (p *syncPhases) finishPhase() {
	p.mu.Lock()
	stop, name, started := p.stop, p.name, p.started
	p.stop, p.name = nil, ""
	p.mu.Unlock()
	if stop == nil {
		return
	}
	close(stop)
	p.done.Wait()
	p.mu.Lock()
	p.times = append(p.times, phaseTime{name: name, took: time.Since(started)})
	p.mu.Unlock()
}

// summary ends the run: where the time went, in the order it was spent. Quiet
// when the whole exchange was quick — a sync that took two seconds does not
// need a report about it.
func (p *syncPhases) summary(what string) {
	p.finishPhase()
	p.mu.Lock()
	times := p.times
	total := time.Since(p.begun)
	p.mu.Unlock()
	if len(times) == 0 || total < p.beat {
		return
	}
	parts := make([]string, 0, len(times))
	for _, t := range times {
		// A phase that took no measurable time is not where the time went, and
		// "indexing what changed 0s" is three words of noise in a report about
		// minutes.
		if t.took < p.floor {
			continue
		}
		parts = append(parts, fmt.Sprintf("%s %s", t.name, roundSecs(t.took)))
	}
	if len(parts) == 0 {
		return
	}
	fmt.Fprintf(p.w, "deja: %s in %s — %s\n", what, roundSecs(total), strings.Join(parts, ", "))
}

// roundSecs is a duration a person reads at a glance: 42s, 3m12s. Milliseconds
// in a report about minutes are noise.
func roundSecs(d time.Duration) string { return d.Round(time.Second).String() }
