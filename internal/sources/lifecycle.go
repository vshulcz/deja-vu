package sources

import (
	"bufio"
	"encoding/json"
	"os"
	"sort"
	"strings"
	"time"
)

// Lifecycle is what a session's own history says about whether it still holds:
// a decision that was accepted, one that was tried and rejected, one a later
// session replaced, or one nobody has revisited in long enough to trust.
type Lifecycle struct {
	State string    // accepted, rejected, superseded, stale
	Note  string    // why, in the words of whoever recorded it
	At    time.Time // when the state was last set
}

// PromotedLifecycles maps a session key ("claude:a1f2c93b") to its latest
// recorded state.
//
// deja already stores this — `deja promote` writes it and the note is indexed —
// but a search that hit the raw transcript never learned about it. Promoting a
// session as rejected and then asking about it returned the transcript, with
// its original conclusion, and nothing else: the correction only surfaced if
// its own wording happened to match the query. So the tool knew a decision had
// been reverted and answered as though it still stood.
//
// Binding the state to the session rather than hoping the correction wins the
// ranking is the difference between recording a lifecycle and using one.
func PromotedLifecycles() map[string]Lifecycle {
	return promotedLifecyclesFrom(NotesFile())
}

func promotedLifecyclesFrom(path string) map[string]Lifecycle {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer func() { _ = f.Close() }()

	type entry struct {
		Lifecycle
		seq int
	}
	byKey := map[string]entry{}
	s := bufio.NewScanner(f)
	s.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	seq := 0
	for s.Scan() {
		line := strings.TrimSpace(s.Text())
		if line == "" {
			continue
		}
		var n struct {
			Kind    string `json:"kind"`
			Session string `json:"session"`
			State   string `json:"state"`
			Text    string `json:"text"`
			TS      string `json:"ts"`
		}
		if json.Unmarshal([]byte(line), &n) != nil {
			continue
		}
		seq++
		if n.Kind != "promoted" || n.Session == "" || !NoteStates[n.State] {
			continue
		}
		at, _ := time.Parse(time.RFC3339Nano, n.TS)
		prev, seen := byKey[n.Session]
		// Later wins. Ties on timestamp fall back to file order, because a
		// correction written in the same second as the note it corrects is
		// still the correction.
		if seen && !at.After(prev.At) && seq < prev.seq {
			continue
		}
		byKey[n.Session] = entry{Lifecycle{State: n.State, Note: strings.TrimSpace(n.Text), At: at}, seq}
	}
	if len(byKey) == 0 {
		return nil
	}
	out := make(map[string]Lifecycle, len(byKey))
	for k, e := range byKey {
		out[k] = e.Lifecycle
	}
	return out
}

// LifecycleKeys returns the keys in a stable order, for tests and for anything
// that reports what is recorded.
func LifecycleKeys(m map[string]Lifecycle) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
