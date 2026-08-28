package main

import (
	"encoding/json"
	"fmt"
	"github.com/vshulcz/deja-vu/internal/policy"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/vshulcz/deja-vu/internal/index"

	"github.com/vshulcz/deja-vu/internal/atomicfile"
)

// The first build takes about ten seconds on a real corpus. It already runs
// detached, so the agent starts at full speed — but until it finishes there is
// no memory and, worse, no explanation. Publishing progress to a small file
// lets every host say so in its own idiom instead of staying silent.
type warmupStatus struct {
	Phase   string `json:"phase"`
	Done    int    `json:"done"`
	Total   int    `json:"total"`
	Stores  int    `json:"stores"`
	Started int64  `json:"started"`
	Updated int64  `json:"updated"`
}

// The status lives beside the index, not inside it: a first build has not
// created the index directory yet, and the atomic swap that publishes a
// rebuild would replace anything stored within it.
func warmupStatusPath(dir string) string { return dir + ".warmup" }

// warmupStatusStale is how long a status file is believed after its last
// update. A warmup killed mid-build must not leave the agent claiming forever
// that memory is on its way.
const warmupStatusStale = 30 * time.Second

// fileProgress publishes build progress for other processes to read. Writes
// are throttled: the build reports thousands of times and the file is only
// read a few times a minute.
type fileProgress struct {
	path string

	mu     sync.Mutex
	st     warmupStatus
	lastWr time.Time
}

func newFileProgress(dir string) *fileProgress {
	return &fileProgress{path: warmupStatusPath(dir), st: warmupStatus{Started: time.Now().UnixNano()}}
}

func (f *fileProgress) Phase(name string, total int) {
	f.mu.Lock()
	f.st.Phase, f.st.Total, f.st.Done = name, total, 0
	f.mu.Unlock()
	f.flush(true)
}

func (f *fileProgress) Advance(units int) {
	f.mu.Lock()
	f.st.Done += units
	f.mu.Unlock()
	f.flush(false)
}

func (f *fileProgress) Harness(name string, sessions, messages int) {
	if messages == 0 {
		return
	}
	f.mu.Lock()
	f.st.Stores++
	f.mu.Unlock()
	f.flush(true)
}

// flush holds the lock across the write and the rename. Harness parsing runs
// concurrently, so two goroutines reporting at once would otherwise race for
// the same temporary file and one would rename a file the other had already
// moved away.
func (f *fileProgress) flush(force bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if !force && time.Since(f.lastWr) < 250*time.Millisecond {
		return
	}
	f.lastWr = time.Now()
	f.st.Updated = f.lastWr.UnixNano()
	b, err := json.Marshal(f.st)
	if err != nil {
		return
	}
	// Written whole and renamed: a reader must never see half a record.
	if err := os.MkdirAll(filepath.Dir(f.path), 0o700); err != nil {
		return
	}
	_ = atomicfile.Write(f.path, b, 0o600)
}

func (f *fileProgress) done() { _ = os.Remove(f.path) }

// readWarmupStatus reports an in-flight build, or nil when none is running.
func readWarmupStatus(dir string) *warmupStatus {
	b, err := os.ReadFile(warmupStatusPath(dir))
	if err != nil {
		return nil
	}
	var st warmupStatus
	if json.Unmarshal(b, &st) != nil || st.Updated == 0 {
		return nil
	}
	// Ahead of the clock counts as stale, not as fresh forever: time.Since is
	// negative there, so a status stamped in the future — a skewed clock, a
	// file copied from another machine — kept every surface saying "indexing
	// your history" with no build running (#889).
	if age := time.Since(time.Unix(0, st.Updated)); age > warmupStatusStale || age < -warmupStatusStale {
		return nil
	}
	return &st
}

// phaseName is the phase to show. A status file can exist with its phase not
// yet written, and every caller wraps this fragment in a parenthetical or a
// sentence, so an empty phase renders as "()" — a sentence deja wrote about
// itself, with nothing in it. Both fragments substitute the same word rather
// than each guarding separately, so they cannot drift apart again.
func (s *warmupStatus) phaseName() string {
	if s.Phase == "" {
		return "starting"
	}
	return s.Phase
}

// progress is the bare "phase 42%" fragment, for callers that supply their
// own sentence.
func (s *warmupStatus) progress() string {
	// The status file exists before the first phase is written to it, and every
	// caller drops this into parentheses or a sentence — so that window read
	// "indexing this machine's history ()" and, with a count already known,
	// "( 30%)". The word line() has always used for the same gap (#1731).
	if s.Total <= 0 {
		return s.phaseName()
	}
	p := 100 * s.Done / s.Total
	if p > 100 {
		p = 100
	}
	return fmt.Sprintf("%s %d%%", s.phaseName(), p)
}

// line is the one-sentence status a host can show its user. It names what is
// happening rather than just a number, because "deja" alone means nothing to
// someone who installed it once and forgot.
func (s *warmupStatus) line() string {
	pct := ""
	if s.Total > 0 {
		p := 100 * s.Done / s.Total
		if p > 100 {
			p = 100
		}
		pct = fmt.Sprintf(" %d%%", p)
	}
	return fmt.Sprintf("deja: indexing your history (%s%s) — recall comes online in a few seconds", s.phaseName(), pct)
}

// publishBuildProgress installs the file sink for the work that happens before
// the build proper — the freshness walk over every store, which on a slow
// volume is the longest part of the command. It published nothing until the
// build began, so every "memory is on its way" surface said nothing meanwhile.
// Measured on 6000 sessions: first published at 0.76s before, 0.02s after
// (#1021).
func publishBuildProgress(dir string) func() {
	if os.Getenv("DEJA_WARMUP_SENTINEL") == "" {
		return func() {}
	}
	p := newFileProgress(dir)
	p.flush(true)
	index.SetProgress(p)
	return func() {
		index.SetProgress(nil)
		p.done()
	}
}

// withWarmupStatus publishes progress while fn builds, when this process is
// the detached warmup.
func withWarmupStatus(dir string, fn func() error) error {
	if os.Getenv("DEJA_WARMUP_SENTINEL") == "" {
		return fn()
	}
	p := newFileProgress(dir)
	// Publish before the build starts: the walk over the stores comes first
	// and, on a slow volume, takes the longest, so waiting for its first
	// report left every "memory is on its way" surface saying nothing (#1021).
	p.flush(true)
	index.SetProgress(p)
	err := fn()
	index.SetProgress(nil)
	p.done()
	return err
}

// cmdWarmupStatus prints one line when a build is in flight and nothing
// otherwise. Hosts whose plugins cannot read the hook envelope — opencode
// pushes its hook output into the model's context, not the UI — call this to
// find out whether to tell the user memory is still coming.
func cmdWarmupStatus(dir string, _ []string) error {
	if st := readWarmupStatus(dir); st != nil {
		fmt.Fprintln(os.Stdout, st.line())
	}
	return nil
}

// emptyRecallAnswer distinguishes "there is nothing" from "there is nothing
// YET". A harness with no auto-recall wiring reaches deja over MCP and nothing
// else, so a tool result is the one place it can learn that the first index is
// still being built. Without this the agent reads a confident negative and
// tells the user they have no history.
func emptyRecallAnswer(dir, q string) string { return emptyRecallAnswerPolicy(dir, q, 0) }

// emptyRecallAnswerPolicy adds the reason when a rule, not the query, is why
// the answer is empty. An agent told "no prior sessions matched" concludes the
// work was never done and starts over (#680).
func emptyRecallAnswerPolicy(dir, q string, hidden int) string {
	q = clampEcho(q)
	if st := readWarmupStatus(dir); st != nil {
		return fmt.Sprintf("deja is still building its index (%s). Nothing can be recalled yet — it finishes within a few seconds, so ask again later in this session rather than concluding there is no history.", st.progress())
	}
	if hidden > 0 {
		return fmt.Sprintf("%d prior session%s matched %q, but this machine's trust policy (%s) does not allow them on this path. There is prior work here; it is not being shown.",
			hidden, pluralS(hidden), q, policy.Load().Describe(policy.ActivationMCP))
	}
	// A confident negative over an index that is behind and cannot catch up:
	// the session is on disk, deja simply could not add it. The hook says this
	// at session start; over MCP it is the only place to say it (#1005).
	if !indexCanCatchUp(dir) {
		return fmt.Sprintf("No indexed session matched %q — the index is behind and cannot be updated (%s is not writable), so recent work may be missing from this answer.",
			q, filepath.Dir(dir))
	}
	return fmt.Sprintf("No prior deja sessions matched %q.", q)
}

// clampEcho bounds a query before it is quoted back to the agent. Echoing the
// whole thing made the answer as large as the caller's input — a 64 KB query
// came back as a 64 KB tool result, against the ~4 KB the tool promises — and
// the description tells agents to query with whole error strings (#1070). The
// tail is kept as well as the head: the distinguishing part of a pasted stack
// trace is rarely at the front.
func clampEcho(q string) string {
	const width = 120
	r := []rune(q)
	if len(r) <= width {
		return q
	}
	head := width * 2 / 3
	return string(r[:head]) + "…" + string(r[len(r)-(width-head-1):])
}
