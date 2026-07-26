package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/vshulcz/deja-vu/internal/index"
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
	tmp := f.path + ".tmp"
	if os.WriteFile(tmp, b, 0o600) == nil {
		_ = os.Rename(tmp, f.path)
	}
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
	if time.Since(time.Unix(0, st.Updated)) > warmupStatusStale {
		return nil
	}
	return &st
}

// progress is the bare "phase 42%" fragment, for callers that supply their
// own sentence.
func (s *warmupStatus) progress() string {
	if s.Total <= 0 {
		return s.Phase
	}
	p := 100 * s.Done / s.Total
	if p > 100 {
		p = 100
	}
	return fmt.Sprintf("%s %d%%", s.Phase, p)
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
	phase := s.Phase
	if phase == "" {
		phase = "starting"
	}
	return fmt.Sprintf("deja: indexing your history (%s%s) — recall comes online in a few seconds", phase, pct)
}

// withWarmupStatus publishes progress while fn builds, when this process is
// the detached warmup.
func withWarmupStatus(dir string, fn func() error) error {
	if os.Getenv("DEJA_WARMUP_SENTINEL") == "" {
		return fn()
	}
	p := newFileProgress(dir)
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
// YET". Thirteen of the sixteen harnesses have no hook to speak through and
// reach deja only over MCP, so a tool result is the one place they can learn
// that the first index is still being built. Without this the agent reads a
// confident negative and tells the user they have no history.
func emptyRecallAnswer(dir, q string) string {
	if st := readWarmupStatus(dir); st != nil {
		return fmt.Sprintf("deja is still building its index (%s). Nothing can be recalled yet — it finishes within a few seconds, so ask again later in this session rather than concluding there is no history.", st.progress())
	}
	return fmt.Sprintf("No prior deja sessions matched %q.", q)
}
