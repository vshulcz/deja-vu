package main

import (
	"encoding/json"
	"io"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/vshulcz/deja-vu/internal/atomicfile"
	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/sources"
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
// The key is the session id alone, because a hook payload names the session
// and not the harness that sent it. Ids are uuids, `ses_`-prefixed strings
// and the like, so a collision across two harnesses would cost one unrelated
// session twenty minutes of silence rather than anything worse.
func markSessionLive(dir, id string) {
	id = strings.TrimSpace(id)
	if dir == "" || id == "" || !hookseenField(id) {
		return
	}
	rows := readLiveSessions(dir)
	rows[id] = time.Now().UTC()
	writeLiveSessions(dir, rows)
}

// endSessionLive drops a session's stamp. The window is a guess at whether the
// agent is still there; a harness that says the session ended knows, and
// leaving the stamp in place hid a session finished a minute ago from the next
// one's recall for the rest of the window (#4210).
func endSessionLive(dir, id string) {
	id = strings.TrimSpace(id)
	if dir == "" || id == "" {
		return
	}
	rows := readLiveSessions(dir)
	if _, ok := rows[id]; !ok {
		return
	}
	delete(rows, id)
	writeLiveSessions(dir, rows)
}

// runHookSessionEnd is the SessionEnd hook of Claude Code, Codex, Cursor CLI,
// Gemini CLI, Qwen Code, Kimi Code, Grok and goose, and what the plugins of pi,
// omp, prime-agent, Senpi, gjc, OpenClaw, Hermes, Cline and dsh run when their
// host says a session is over. It says nothing back — none of them reads a
// reply to it — and it runs even with recall off: clearing a stamp never hands
// anyone anything. With recall on, and a project in the payload or the
// environment, it also refreshes that project's start digest. The plugins send
// a bare session id, so for them it does not yet.
func runHookSessionEnd(dir string, stdin io.Reader) {
	var input precompactHookInput
	_ = json.Unmarshal(readHookPayload(stdin, hookStdinWait), &input)
	input.adopt()
	endSessionLive(dir, input.SessionID)
	refreshDigestAfterSession(dir, sessionEndProject(input))
}

// sessionEndProject is the project a SessionEnd payload is about: what it
// names, else the directory the host exported, the way hookCWD reads it for
// the session start. Never the process's own directory: a plugin host sends a
// bare session id and spawns the hook wherever it happens to be.
func sessionEndProject(input precompactHookInput) string {
	if p := hookProjectPath(input.CWD, input.WorkspaceRoots); p != "" {
		return p
	}
	if p := os.Getenv("CLAUDE_PROJECT_DIR"); p != "" {
		return p
	}
	return os.Getenv("DEVIN_PROJECT_DIR")
}

// refreshDigestAfterSession rebuilds the project's cached start digest once a
// session there is over. The cache is served whatever its age, and its only
// other refresh runs at a session start, before that session has written
// anything, so the next session read a snapshot without the one that just
// ended (#5119). Detached, like every refresh: the host is closing.
func refreshDigestAfterSession(dir, cwd string) {
	if dir == "" || strings.TrimSpace(cwd) == "" || recallIsOff() {
		return
	}
	// No cache means the next start builds the digest itself.
	if _, err := os.Stat(hookCachePath(dir, cwd)); err != nil {
		return
	}
	requestHookRefresh(dir, cwd)
}

// runHookMCPCall is Copilot CLI's preMcpToolCall hook: it stamps the session
// that is about to call an MCP tool, so recall reads a stamp made a moment ago
// rather than one from a sessionStart that may be past the window (#4551). It
// prints nothing: Copilot reads a reply as the request's new _meta.
func runHookMCPCall(dir string, stdin io.Reader, _ io.Writer) {
	var input precompactHookInput
	_ = json.Unmarshal(readHookPayload(stdin, hookStdinWait), &input)
	input.adopt()
	markSessionLive(dir, input.SessionID)
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
	// Atomic, like the injection log and the warmup status beside it: two hooks
	// fire at once often enough — a prompt and the action it leads to — and a
	// reader that catches a half-written file would read a truncated id as a
	// live session and hide the wrong one.
	_ = atomicfile.Write(liveSessionsPath(dir), []byte(b.String()), 0o600)
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

// liveLineage is the live sessions and every session that counts as one of
// them: a sub-agent a live session spawned is being written too, under an id
// no hook stamps, since its hooks fire under the parent's (#4547). A live
// session the index does not hold yet is read off its own store in full: a
// fork's first turn comes before any build has seen it, and MCP answered it
// with its source (#5129).
func liveLineage(dir string) map[string]bool {
	ids := liveSessionIDs(dir)
	var wholes []model.Session
	for id := range ids {
		if isSpawnedReader(id) || index.HasSession(dir, id) {
			continue
		}
		if w, ok := liveWhole(id); ok {
			wholes = append(wholes, w)
		}
	}
	return index.LineageWhole(dir, ids, wholes...)
}

// liveWholes keeps each unindexed live session read for this process. What a
// fork copied is fixed when it forks, and turns it adds later are newer than
// anything indexed, so the first read answers for the rest; one recall asks
// two or three times, and a fork of a long session takes a second to read.
var liveWholes = struct {
	sync.Mutex
	m map[string]model.Session
}{m: map[string]model.Session{}}

// lineageOnly is what LineageWhole reads of a session: every message's role
// and time, and the text up to the turn it opens on. A fork of a long session
// is tens of MB whole.
func lineageOnly(s model.Session) model.Session {
	out := model.Session{ID: s.ID, Harness: s.Harness, Kind: s.Kind, Parent: s.Parent, Messages: make([]model.Message, len(s.Messages))}
	opened := false
	for i, m := range s.Messages {
		out.Messages[i] = model.Message{Role: m.Role, Time: m.Time}
		if !opened {
			out.Messages[i].Text = m.Text
			opened = index.SessionOpening(model.Session{Harness: s.Harness, Messages: out.Messages[:i+1]}) != 0
		}
	}
	return out
}

func liveWhole(id string) (model.Session, bool) {
	// Keyed on the store too: the id is only unique inside one.
	key := sources.OpencodeDB() + "\x00" + id
	liveWholes.Lock()
	w, ok := liveWholes.m[key]
	liveWholes.Unlock()
	if ok {
		return w, true
	}
	w, ok = askerHead(id, "")
	if !ok {
		return w, false
	}
	w = lineageOnly(w)
	liveWholes.Lock()
	if len(liveWholes.m) >= liveSessionsMax {
		clear(liveWholes.m)
	}
	liveWholes.m[key] = w
	liveWholes.Unlock()
	return w, true
}

// askerLineage is the session a hook speaks for and every session that counts
// as it. A spawn recalls under a reader of its own, task:<parent>:<hash>, and
// its parent is the session asking through it; a host that knows the parent
// of the session asking names it in the payload (#4548). A fork's source
// counts too (#4549).
//
// asker is the session the payload names and transcript its transcript, when
// the host sends one. An asker the index does not hold yet is read off its own
// store: a fork's first prompts come before any build has seen it, and they
// were the ones answered with the fork's source.
func askerLineage(dir, asker, transcript string, ids ...string) map[string]bool {
	set := map[string]bool{}
	for _, id := range append([]string{asker}, ids...) {
		if id = strings.TrimSpace(id); id == "" {
			continue
		}
		set[id] = true
		if p := spawnParent(id); p != "" {
			set[p] = true
		}
	}
	var heads []model.Session
	if asker = strings.TrimSpace(asker); asker != "" && !isSpawnedReader(asker) && !index.HasSession(dir, asker) {
		if h, ok := askerHead(asker, transcript); ok {
			heads = append(heads, h)
		}
	}
	return index.Lineage(dir, set, heads...)
}

// askerHead is the session read off its own store: the first records of the
// transcript the payload names, or opencode's or Hermes' database or Copilot
// CLI's session folder, which their payloads do not name a file in (#5137).
func askerHead(id, transcript string) (model.Session, bool) {
	if transcript != "" {
		s, err := sources.TranscriptHead(transcript, id)
		return s, err == nil
	}
	if s, err := sources.CopilotSessionHead(id); err == nil {
		return s, true
	}
	if strings.HasPrefix(id, "ses_") {
		for _, s := range sources.LoadOpencodeSession(id) {
			if s.ID == id {
				return s, true
			}
		}
	}
	// A Hermes branch names its source in the sessions table; that is all
	// lineage needs of it (#5141).
	if s, ok := sources.HermesSessionHead(id); ok && s.Parent != "" {
		return s, true
	}
	return model.Session{}, false
}

// spawnParent is the session a spawn reader recalls for, or "".
func spawnParent(sid string) string {
	if !isSpawnedReader(sid) {
		return ""
	}
	rest := strings.TrimPrefix(sid, spawnReaderPrefix)
	if i := strings.LastIndex(rest, ":"); i > 0 {
		return rest[:i]
	}
	return ""
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
	live := liveLineage(dir)
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
