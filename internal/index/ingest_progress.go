package index

import (
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/sources"
)

// safeLoad shields a cold rebuild from a panicking harness loader: one broken
// store costs that harness's sessions this pass, not the whole index.
func safeLoad(name string, load func() []model.Session, progress io.Writer) (ss []model.Session) {
	defer func() {
		if r := recover(); r != nil {
			ss = nil
			if progress != nil {
				fmt.Fprintf(progress, "deja: %s: parser crashed (%v) — skipping this harness for now\n", name, r)
			}
		}
	}()
	return load()
}

// countMessages is the unit the indexing phase works through. A message that
// strips to nothing is skipped by the loop, so the count is an upper bound and
// the bar finishes a little short rather than sitting at 99%.
func countMessages(ss []model.Session) int {
	n := 0
	for _, s := range ss {
		n += len(s.Messages)
	}
	return n
}

// progressWeights is how many files each store contributes, set by the caller
// that already walked the filesystem so the bar advances proportionally.
var progressWeights = map[string]int{}

// loadProgress narrates a full rebuild per harness: a cold pass over a large
// corpus takes seconds and used to look hung.
func loadProgress(h string, progress io.Writer) []model.Session {
	// Per file while the stores parse, and the remainder of each store's
	// weight when it lands. Advancing only per store left the bar at 0% for as
	// long as the largest one took — on a machine where Claude Code holds most
	// of the corpus, ten seconds of a thirty-second rebuild (#3372). A store
	// that parses no files through the pool (the SQLite ones) is unchanged: it
	// counts nothing here and its whole weight arrives at the end.
	var readMu sync.Mutex
	readPerHarness := map[string]int{}
	readTook := map[string]time.Duration{}
	restore := sources.SetFileProgress(func(path string) {
		name := harnessForPath(path)
		if store := sources.HarnessForKind(name); store != "" {
			name = store
		}
		readMu.Lock()
		counted := readPerHarness[name] < progressWeights[name]
		if counted {
			readPerHarness[name]++
		}
		readMu.Unlock()
		if counted {
			reportAdvance(1)
		}
	})
	defer restore()
	// Harness stores are independent files owned by different tools; parsing
	// them is CPU-bound JSON/regex work with no shared state, so the cold
	// build parses all stores concurrently. Results keep registry order so a
	// rebuild stays deterministic.
	type loaded struct {
		name string
		ss   []model.Session
	}
	// A store the reader has asked deja not to read is not walked at all.
	// Without this the only way to stop `needs-sqlite3` advice for a
	// harness they do not use was to install the package (#3499).
	reg := sources.StoresToRead()
	results := make([]loaded, len(reg))
	var wg sync.WaitGroup
	for i, hr := range reg {
		if h != "" && h != hr.Name {
			continue
		}
		wg.Add(1)
		go func(i int, name string, load func() []model.Session) {
			defer wg.Done()
			started := time.Now()
			stop := sayItIsStillReading(name, started, progress)
			ss := safeLoad(name, load, progress)
			stop()
			readMu.Lock()
			readTook[name] = time.Since(started)
			readMu.Unlock()
			results[i] = loaded{name: name, ss: ss}
			// Report as this store lands rather than after every store has,
			// so the bar moves during the parse instead of jumping at the end.
			reportHarness(name, len(ss), sources.CountMessages(ss))
			// Only what the per-file reports did not already cover, so a store
			// counts its weight once.
			readMu.Lock()
			rest := progressWeights[name] - readPerHarness[name]
			readPerHarness[name] = progressWeights[name]
			readMu.Unlock()
			if rest > 0 {
				reportAdvance(rest)
			}
		}(i, hr.Name, hr.Load)
	}
	wg.Wait()
	unreadable := malformedByHarness()
	refused := failedByHarness()
	stalled := stalledByHarness()
	var ss []model.Session
	for _, r := range results {
		if len(r.ss) == 0 {
			// A harness deja can see but could not read is worth a line: an
			// index run that narrates every store it read and stays silent
			// about the one it skipped makes an empty deja look like an empty
			// history (#794).
			//
			// Two ways to yield nothing, and the second had no line at all: a
			// store deja could not open has a skip reason, and one whose files
			// it read and refused has a count instead — computed just above and
			// then dropped, in the case where nothing else on screen mentions
			// the store (#2229).
			if progress != nil && !SuppressHarnessNarration {
				switch reason := sources.SkipReason(r.name); {
				case reason != "":
					fmt.Fprintf(progress, "deja: %s: skipped — %s\n", r.name, reason)
				case refused[r.name] > 0 && refused[r.name] == len(stalled[r.name]):
					fmt.Fprintf(progress, "deja: %s: skipped — %s\n", harnessLabel(r.name), stallNarration(stalled[r.name]))
				case unreadable[r.name] > 0 || refused[r.name] > 0:
					fmt.Fprintln(progress, nothingReadableNarration(r.name, unreadable[r.name], refused[r.name]))
				}
			}
			continue
		}
		ss = append(ss, r.ss...)
		if progress != nil && !SuppressHarnessNarration {
			readMu.Lock()
			took := readTook[r.name]
			readMu.Unlock()
			line := harnessNarration(r.name, r.ss, sources.SkipReason(r.name), unreadable[r.name], refused[r.name])
			if len(stalled[r.name]) > 0 {
				line += " — " + stallNarration(stalled[r.name])
			}
			fmt.Fprintln(progress, withReadTime(line, took))
		}
	}
	return ss
}

// readSlowAfter is when a store that is still being read says so, and the
// point above which its read time is reported when it lands. A cold pass over
// a large corpus is seconds per store; anything past this is a store worth
// naming while the wait is happening. readStillReadingEvery is how often it
// repeats after that, so a long wait stays visibly alive without filling the
// screen. Variables so a test can shorten them.
var (
	readSlowAfter         = 15 * time.Second
	readStillReadingEvery = 30 * time.Second
)

// withReadTime puts the time on a store's line once the wait was long enough
// to have been worth reporting. Every store on an ordinary pass reads in
// milliseconds, and stamping those would bury the one that did not.
func withReadTime(line string, took time.Duration) string {
	if took < readSlowAfter {
		return line
	}
	return line + " — the read took " + roundedSeconds(took)
}

// sayItIsStillReading names a store that is taking long enough for the run to
// look hung, and keeps saying so until the read lands. Returns the function
// that stops it.
//
// Without this a slow store is indistinguishable from a stuck one: an index
// run over a 520 MB opencode store printed `indexing sessions into …` and
// nothing else for thirteen minutes and fifty-four seconds, of which 0.75s was
// deja's own CPU, and `deja doctor` called the store healthy throughout
// (#3553, #3555). Which store it is, and that it is still moving, is the whole
// of what a person needs to decide between waiting and interrupting.
func sayItIsStillReading(name string, started time.Time, progress io.Writer) func() {
	if progress == nil || SuppressHarnessNarration {
		return func() {}
	}
	// The thresholds are read here rather than in the goroutine: they are
	// package variables so a test can shorten them, and reading them once
	// keeps the notice out of that race.
	after, every := readSlowAfter, readStillReadingEvery
	done := make(chan struct{})
	var once sync.Once
	go func() {
		timer := time.NewTimer(after)
		defer timer.Stop()
		for {
			select {
			case <-done:
				return
			case <-timer.C:
			}
			fmt.Fprintf(progress, "deja: %s: still reading (%s)\n", harnessLabel(name), roundedSeconds(time.Since(started)))
			timer.Reset(every)
		}
	}()
	return func() { once.Do(func() { close(done) }) }
}

// harnessLabel is the store's name as a person reads it: "deja" is the notes
// pseudo-source and narrates as "notes".
func harnessLabel(name string) string {
	if name == "deja" {
		return "notes"
	}
	return name
}

// roundedSeconds drops the sub-second noise: "42s", "13m54s".
func roundedSeconds(d time.Duration) string { return d.Round(time.Second).String() }

// harnessNarration is the line an index run prints for one store. A store can
// be half-readable — cursor keeps CLI transcripts as JSONL and its IDE sessions
// in SQLite — and the count alone then reads as the whole story while half of
// it is missing from recall. The skip reason was printed only for a store that
// yielded nothing at all (#1758, the shape of #794).
func harnessNarration(name string, ss []model.Session, skipped string, unreadable, refused int) string {
	msgs := sources.CountMessages(ss)
	// "deja" is the notes pseudo-source; it narrates as "notes".
	label := name
	if label == "deja" {
		label = "notes"
	}
	line := fmt.Sprintf("deja: %s: %d session%s, %d message%s", label, len(ss), pluralS(len(ss)), msgs, pluralS(msgs))
	if unreadable > 0 {
		line += fmt.Sprintf(" — %d %s%s skipped, deja could not read %s", unreadable, sources.SkippedNoun(name), pluralS(unreadable), pluralThem(unreadable))
	}
	// A file deja could not read at all is the third fact of this kind, beside
	// the refused lines and the missing tool. Without it a store that gave up
	// ten sessions and lost three tasks read like a store with nothing wrong
	// (#2236).
	if refused > 0 {
		line += fmt.Sprintf(" — %d path%s could not be read at all", refused, pluralS(refused))
	}
	if skipped != "" {
		line += " — part of this store could not be read: " + skipped
	}
	return line
}

// nothingReadableNarration is the line for a store that yielded no session
// because deja could not read what it found. "0 sessions, 0 messages" is the
// ordinary line's shape and says the wrong thing here — there were sessions,
// and none of them survived the read (#2229).
func nothingReadableNarration(name string, unreadable, refused int) string {
	label := name
	if label == "deja" {
		label = "notes"
	}
	// Lines and whole files are different losses and the sentence says which:
	// a task deja could not parse is a path, and calling it a line said "1
	// line" about three thousand turns (#2232).
	var what []string
	if unreadable > 0 {
		what = append(what, fmt.Sprintf("%d %s%s", unreadable, sources.SkippedNoun(name), pluralS(unreadable)))
	}
	if refused > 0 {
		what = append(what, fmt.Sprintf("%d path%s", refused, pluralS(refused)))
	}
	return fmt.Sprintf("deja: %s: nothing indexed — %s could not be read", label, strings.Join(what, " and "))
}

// stalledByHarness is failedByHarness for the reads given up for making no
// progress, with the budget each ran out of.
func stalledByHarness() map[string][]time.Duration {
	out := map[string][]time.Duration{}
	for p, idle := range sources.DiagStalledPaths() {
		if h := sources.HarnessForKind(harnessForPath(p)); h != "" {
			out[h] = append(out[h], idle)
		}
	}
	return out
}

// stallNarration says that a read was given up and how to wait instead. Said
// like a store deja could not open, it read as a permission problem, and
// before that a store given up was not mentioned at all (#5517).
func stallNarration(idle []time.Duration) string {
	var budget time.Duration
	for _, d := range idle {
		budget = max(budget, d)
	}
	what := "a read in the same directory stopped moving"
	if budget > 0 {
		what = "no progress for " + budget.String()
	}
	if n := len(idle); n > 1 {
		what = fmt.Sprintf("%d reads given up, %s", n, what)
	}
	return what + "; DEJA_STORE_TIMEOUT=0 waits"
}

// failedByHarness is malformedByHarness for the paths that would not open or
// would not parse at all.
func failedByHarness() map[string]int {
	out := map[string]int{}
	for p := range sources.DiagFailedPaths() {
		if h := sources.HarnessForKind(harnessForPath(p)); h != "" {
			out[h]++
		}
	}
	return out
}

// totalMalformed is malformedByHarness summed: the incremental line names the
// pass, not a store.
func totalMalformed() int {
	n := 0
	for _, c := range malformedByHarness() {
		n += c
	}
	return n
}

// malformedByHarness folds the per-file malformed counts the parsers reported
// this run into per-store totals, without draining them: the manifest fold that
// doctor reads from runs later and takes the same numbers.
func malformedByHarness() map[string]int {
	out := map[string]int{}
	for p, n := range sources.DiagMalformedCounts() {
		// By the store's own name, not the file kind's: harnessForPath answers
		// "cline-sdk" where the run narrates "cline", so the count never
		// reached the line that would have said it (#2229).
		if h := sources.HarnessForKind(harnessForPath(p)); h != "" {
			out[h] += n
		}
	}
	return out
}
