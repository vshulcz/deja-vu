package main

import (
	"os"
	"sort"
	"strings"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
)

// An agent's own transcript is in the index while it is still being written, so
// the best lexical match for a question asked mid-session can be the question:
// recall answered with the caller's own opening prompt and left the session
// that holds the answer off the page entirely (#3945, #3965).
//
// The prompt hook has always dropped it, because a hook payload names the
// session it came from (hook_prompt.go). The MCP tool is handed no such thing —
// the connection id is minted per process and joins to nothing — so the two
// surfaces disagreed about whose session it was.
//
// What the hooks know, they now write down: every hook that carries a session
// id stamps it here, and the MCP surfaces read the stamps back. A session
// stamped in the last few minutes is one an agent is inside right now, which is
// the one thing recall must not answer with.
//
// Kept beside the index rather than in it, the way the hook cache and the
// injection log are: this is state about the machine's running agents, not
// content, and a rebuild must not wait on it or carry it.
const (
	liveSessionsFile = ".live"
	// liveSessionWindow is how long a stamp means "still being written". A
	// session with hooks wired restamps on every prompt and every action, so
	// the window only has to outlast one turn — and it has to be short,
	// because when it is wrong it hides a prior session that could have
	// answered. Twenty minutes is one long turn.
	liveSessionWindow = 20 * time.Minute
	// liveSessionsMax bounds the file. One row per agent working on this
	// machine at once; the oldest goes when a new one arrives.
	liveSessionsMax = 12
)

func liveSessionsPath(dir string) string { return dir + liveSessionsFile }

// markSessionLive records that this session is being written right now.
//
// Best-effort throughout: it runs inside a hook the user is waiting on, so a
// read that fails, a write that fails or a disk that is full costs the caller
// nothing. The file is rewritten rather than appended to, because what is
// wanted is the newest stamp per session and not a log of every action.
func markSessionLive(dir, id string) {
	id = strings.TrimSpace(id)
	if dir == "" || id == "" || !hookseenField(id) {
		return
	}
	rows := readLiveSessions(dir)
	rows[id] = time.Now().UTC()
	writeLiveSessions(dir, rows)
}

// readLiveSessions is every stamp in the file, whatever its age.
func readLiveSessions(dir string) map[string]time.Time {
	out := map[string]time.Time{}
	b, err := os.ReadFile(liveSessionsPath(dir))
	if err != nil {
		return out
	}
	for _, line := range strings.Split(string(b), "\n") {
		parts := strings.Fields(line)
		if len(parts) != 2 {
			continue
		}
		when, err := time.Parse(time.RFC3339, parts[1])
		if err != nil {
			continue
		}
		if prev, ok := out[parts[0]]; !ok || when.After(prev) {
			out[parts[0]] = when
		}
	}
	return out
}

func writeLiveSessions(dir string, rows map[string]time.Time) {
	type row struct {
		id   string
		when time.Time
	}
	var all []row
	cutoff := time.Now().UTC().Add(-liveSessionWindow)
	for id, when := range rows {
		if when.Before(cutoff) {
			continue
		}
		all = append(all, row{id, when})
	}
	// Newest first, so the cap drops the agent that has been quiet longest.
	sort.Slice(all, func(i, j int) bool {
		if !all[i].when.Equal(all[j].when) {
			return all[i].when.After(all[j].when)
		}
		return all[i].id < all[j].id
	})
	if len(all) > liveSessionsMax {
		all = all[:liveSessionsMax]
	}
	var b strings.Builder
	for _, r := range all {
		b.WriteString(r.id)
		b.WriteByte(' ')
		b.WriteString(r.when.Format(time.RFC3339))
		b.WriteByte('\n')
	}
	if b.Len() == 0 {
		_ = os.Remove(liveSessionsPath(dir))
		return
	}
	_ = os.WriteFile(liveSessionsPath(dir), []byte(b.String()), 0o600)
}

// liveSessionIDs is the sessions an agent is inside right now: stamped inside
// the window, and nothing older. Empty on a machine with no hooks wired, where
// nothing stamps anything and every surface behaves as it did before.
func liveSessionIDs(dir string) map[string]bool {
	rows := readLiveSessions(dir)
	if len(rows) == 0 {
		return nil
	}
	cutoff := time.Now().UTC().Add(-liveSessionWindow)
	out := make(map[string]bool, len(rows))
	for id, when := range rows {
		if when.After(cutoff) {
			out[id] = true
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// withoutLiveSessions drops the sessions an agent is inside from a result.
//
// Only the MCP surfaces use it. On the CLI the reader is a person who may well
// want the session they are in — `deja last`, `deja show` and a plain search all
// answer about it on purpose — and here the reader is the agent that wrote it:
// it has that transcript in front of it already, and every byte of it on the
// page is a byte the session holding the answer did not get.
//
// Everything is dropped rather than demoted. A hit that is the caller's own
// question ranks first by wording however it is weighted, so demotion left it
// on a page of five (#3945).
func withoutLiveSessions(dir string, ss []model.Session) []model.Session {
	live := liveSessionIDs(dir)
	if len(live) == 0 {
		return ss
	}
	out := ss[:0:0]
	for _, s := range ss {
		if live[s.ID] {
			continue
		}
		out = append(out, s)
	}
	// An index holding nothing else says so through the empty answer, which is
	// the honest one: the only session that matched is the one being written.
	return out
}
