package index

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// The random history beyond Claude transcripts: Codex rollouts, an opencode
// database, forget and unforget, and batches synced from another machine.

type codexRollout struct {
	id       string
	day, sec int
	calls    int
	archived bool
}

type opencodeSession struct {
	id   string
	msgs int
}

type historyStores struct {
	codexRoot string
	codex     []*codexRollout
	// db is empty when there is no sqlite3 to write the store with.
	db       string
	opencode []*opencodeSession
	clock    int64
	// forgotten holds session ids forget took out, for unforget to bring back.
	forgotten []string
	peer      *historyPeer
}

type historyPeer struct {
	root, config, notes, index string
	sessions                   []*historySession
	batches                    []string
}

func (r *historyRun) openStores() {
	t := r.t
	m := &r.more
	m.codexRoot = filepath.Join(r.h.tmp, "codex")
	t.Setenv("DEJA_CODEX_ROOT", m.codexRoot)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(r.h.tmp, "config"))
	if _, err := exec.LookPath("sqlite3"); err == nil {
		m.db = filepath.Join(r.h.tmp, "opencode.db")
		t.Setenv("DEJA_OPENCODE_DB", m.db)
		r.sql(`create table session(id text primary key, directory text, title text, time_created integer, time_updated integer);
create table message(id text primary key, session_id text, time_created integer, time_updated integer, data text);
create table part(id text primary key, message_id text, session_id text, time_created integer, time_updated integer, data text);`)
	}
	m.clock = time.Now().UnixMilli()
}

// anyStep is one change to one of the stores, Claude transcripts most often.
func (r *historyRun) anyStep() {
	switch n := r.rng.Intn(20); {
	case n < 10:
		r.step()
	case n < 14:
		r.codexStep()
	case n < 17:
		if r.more.db == "" {
			r.step()
			return
		}
		r.opencodeStep()
	case n < 18:
		r.privacyStep()
	default:
		r.syncStep()
	}
}

func codexLine(at time.Time, typ string, payload map[string]any) string {
	return hjson(map[string]any{"timestamp": at.UTC().Format("2006-01-02T15:04:05.000Z"), "type": typ, "payload": payload})
}

func (c *codexRollout) at() time.Time {
	return time.Date(2026, 6, c.day, 10, 0, 0, 0, time.UTC).Add(time.Duration(c.sec) * time.Second)
}

func (r *historyRun) codexPath(c *codexRollout) string {
	name := fmt.Sprintf("rollout-2026-06-%02dT10-00-00-%s.jsonl", c.day, c.id)
	if c.archived {
		return filepath.Join(r.more.codexRoot, "archived_sessions", name)
	}
	return filepath.Join(r.more.codexRoot, "sessions", "2026", "06", fmt.Sprintf("%02d", c.day), name)
}

// codexTurns is one to three turns of a rollout. A message comes as the
// response item and, half the time, the event codex also writes for it.
func (r *historyRun) codexTurns(c *codexRollout) string {
	var b strings.Builder
	for n := 1 + r.rng.Intn(3); n > 0; n-- {
		c.sec += 7
		at := c.at()
		switch r.rng.Intn(4) {
		case 0:
			text := "why does the " + r.words(4) + " fail"
			b.WriteString(codexLine(at, "response_item", map[string]any{"type": "message", "role": "user",
				"content": []any{map[string]any{"type": "input_text", "text": text}}}))
			if r.rng.Intn(2) == 0 {
				b.WriteString(codexLine(at, "event_msg", map[string]any{"type": "user_message", "message": text}))
			}
		case 1:
			text := "The " + r.words(5) + " needs a fix."
			b.WriteString(codexLine(at, "response_item", map[string]any{"type": "message", "role": "assistant",
				"content": []any{map[string]any{"type": "output_text", "text": text}}}))
			if r.rng.Intn(2) == 0 {
				b.WriteString(codexLine(at, "event_msg", map[string]any{"type": "agent_message", "message": text}))
			}
		default:
			c.calls++
			id := fmt.Sprintf("%s-c%d", c.id, c.calls)
			cmd, out := "go build ./...", "Process exited with code 0\nOutput:\nok\n"
			if r.rng.Intn(2) == 0 {
				cmd = "go test ./" + historyWords[r.rng.Intn(len(historyWords))] + "/..."
				out = "Process exited with code 1\nOutput:\n--- FAIL: TestPool (0.01s)\n    " + r.words(3) + "\nFAIL\n"
			}
			args := hjson(map[string]any{"cmd": cmd, "workdir": "/w/" + c.id})
			b.WriteString(codexLine(at, "response_item", map[string]any{"type": "function_call", "name": "exec_command",
				"call_id": id, "arguments": strings.TrimSuffix(args, "\n")}))
			b.WriteString(codexLine(at.Add(time.Second), "response_item", map[string]any{"type": "function_call_output", "call_id": id, "output": out}))
		}
	}
	return b.String()
}

func (r *historyRun) codexStep() {
	m := &r.more
	var c *codexRollout
	if len(m.codex) > 0 {
		c = m.codex[r.rng.Intn(len(m.codex))]
	}
	op := r.rng.Intn(6)
	if c == nil {
		op = 0
	}
	switch op {
	case 0:
		r.next++
		c = &codexRollout{id: fmt.Sprintf("cx%d", r.next), day: 1 + r.rng.Intn(20)}
		body := codexLine(c.at(), "session_meta", map[string]any{"id": c.id, "cwd": "/w/" + historyProjects[r.rng.Intn(len(historyProjects))]}) + r.codexTurns(c)
		writeHistoryFile(r, r.codexPath(c), body, false)
		m.codex = append(m.codex, c)
		r.log = append(r.log, "new codex "+c.id)
	case 1, 2, 3:
		writeHistoryFile(r, r.codexPath(c), r.codexTurns(c), true)
		r.log = append(r.log, "append to codex "+c.id)
	case 4:
		if c.archived {
			writeHistoryFile(r, r.codexPath(c), r.codexTurns(c), true)
			r.log = append(r.log, "append to archived codex "+c.id)
			return
		}
		// `codex archive` moves the rollout, name and bytes unchanged.
		from := r.codexPath(c)
		c.archived = true
		if err := os.MkdirAll(filepath.Dir(r.codexPath(c)), 0o755); err != nil {
			r.t.Fatal(err)
		}
		if err := os.Rename(from, r.codexPath(c)); err != nil {
			r.t.Fatal(err)
		}
		r.log = append(r.log, "archive codex "+c.id)
	case 5:
		if err := os.Remove(r.codexPath(c)); err != nil {
			r.t.Fatal(err)
		}
		for i, l := range m.codex {
			if l == c {
				m.codex = append(m.codex[:i], m.codex[i+1:]...)
				break
			}
		}
		r.log = append(r.log, "delete codex "+c.id)
	}
}

func writeHistoryFile(r *historyRun, p, body string, appendTo bool) {
	r.t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		r.t.Fatal(err)
	}
	flag := os.O_WRONLY | os.O_CREATE | os.O_TRUNC
	if appendTo {
		flag = os.O_WRONLY | os.O_APPEND
	}
	f, err := os.OpenFile(p, flag, 0o644)
	if err != nil {
		r.t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	if _, err := f.WriteString(body); err != nil {
		r.t.Fatal(err)
	}
}

func (r *historyRun) sql(stmts string) {
	r.t.Helper()
	if out, err := exec.Command("sqlite3", r.more.db, stmts).CombinedOutput(); err != nil {
		r.t.Fatalf("sqlite3: %v %s", err, out)
	}
	// The store is noticed by its mtime, and two writes in one second would
	// otherwise look like none.
	at := time.Now().Add(time.Duration(len(r.log)+1) * time.Second)
	_ = os.Chtimes(r.more.db, at, at)
}

// opencodeMessage adds one message with one text part, at the store's clock.
func (r *historyRun) opencodeMessage(s *opencodeSession, at int64) string {
	s.msgs++
	role := "user"
	text := "why does the " + r.words(4) + " fail"
	if s.msgs%2 == 0 {
		role, text = "assistant", "The "+r.words(5)+" needs a fix."
	}
	mid := fmt.Sprintf("%s-m%d", s.id, s.msgs)
	return fmt.Sprintf(`insert into message values('%[1]s','%[2]s',%[3]d,%[3]d,'{"role":"%[4]s","time":{"created":%[3]d}}');
insert into part values('%[1]s-p','%[1]s','%[2]s',%[3]d,%[3]d,'{"type":"text","text":"%[5]s"}');
update session set time_updated=%[3]d where id='%[2]s';
`, mid, s.id, at, role, text)
}

func (r *historyRun) opencodeStep() {
	m := &r.more
	// opencode stamps with the wall clock, and so moves forward with it.
	m.clock = max(m.clock+1000, time.Now().UnixMilli())
	var s *opencodeSession
	if len(m.opencode) > 0 {
		s = m.opencode[r.rng.Intn(len(m.opencode))]
	}
	op := r.rng.Intn(6)
	if s == nil {
		op = 0
	}
	switch op {
	case 0:
		r.next++
		s = &opencodeSession{id: fmt.Sprintf("oc%d", r.next)}
		stmts := fmt.Sprintf("insert into session values('%s','/w/%s','start: %s',%d,%d);\n",
			s.id, historyProjects[r.rng.Intn(len(historyProjects))], r.words(3), m.clock, m.clock)
		stmts += r.opencodeMessage(s, m.clock)
		r.sql(stmts)
		m.opencode = append(m.opencode, s)
		r.log = append(r.log, "new opencode "+s.id)
	case 1, 2, 3:
		r.sql(r.opencodeMessage(s, m.clock) + r.opencodeMessage(s, m.clock+1000))
		r.log = append(r.log, "grow opencode "+s.id)
	case 4:
		// A machine whose clock runs ahead stamps its turn a day later.
		r.sql(r.opencodeMessage(s, time.Now().Add(24*time.Hour).UnixMilli()))
		r.log = append(r.log, "grow opencode "+s.id+" stamped a day ahead")
	case 5:
		r.sql(fmt.Sprintf("delete from part where session_id='%[1]s'; delete from message where session_id='%[1]s'; delete from session where id='%[1]s';", s.id))
		for i, l := range m.opencode {
			if l == s {
				m.opencode = append(m.opencode[:i], m.opencode[i+1:]...)
				break
			}
		}
		r.log = append(r.log, "delete opencode "+s.id)
	}
}

// privacyStep forgets a session that is in the index, or brings back one
// forgotten earlier. Its transcript stays where it is and may keep growing.
func (r *historyRun) privacyStep() {
	m := &r.more
	if len(m.forgotten) > 0 && r.rng.Intn(2) == 0 {
		i := r.rng.Intn(len(m.forgotten))
		id := m.forgotten[i]
		m.forgotten = append(m.forgotten[:i], m.forgotten[i+1:]...)
		if _, err := Unforget(r.index, id, nil); err != nil {
			r.t.Fatalf("unforget %s: %v", id, err)
		}
		r.log = append(r.log, "unforget "+id)
		return
	}
	var ids []string
	for _, s := range r.live {
		ids = append(ids, s.sid)
	}
	for _, c := range m.codex {
		ids = append(ids, c.id)
	}
	for _, s := range m.opencode {
		ids = append(ids, s.id)
	}
	if len(ids) == 0 {
		r.step()
		return
	}
	id := ids[r.rng.Intn(len(ids))]
	if !HasSession(r.index, id) {
		r.step()
		return
	}
	if _, err := Forget(r.index, ForgetOptions{Session: id}); err != nil {
		r.t.Fatalf("forget %s: %v", id, err)
	}
	m.forgotten = append(m.forgotten, id)
	r.log = append(r.log, "forget "+id)
}

// syncStep imports a batch another machine exported: its sessions grow on
// that machine between batches, and a batch can arrive twice.
func (r *historyRun) syncStep() {
	m := &r.more
	if m.peer == nil {
		m.peer = &historyPeer{
			root:   filepath.Join(r.h.tmp, "peer-claude"),
			config: filepath.Join(r.h.tmp, "peer-config"),
			notes:  filepath.Join(r.h.tmp, "peer-notes.jsonl"),
			index:  filepath.Join(r.h.tmp, "peer.db"),
		}
	}
	p := m.peer
	if len(p.batches) > 0 && r.rng.Intn(3) == 0 {
		b := p.batches[r.rng.Intn(len(p.batches))]
		if _, err := Import(r.index, b); err != nil {
			r.t.Fatalf("import again %s: %v", filepath.Base(b), err)
		}
		r.log = append(r.log, "import "+filepath.Base(b)+" again")
		return
	}
	peer := &twoWayEnv{t: r.t, tmp: r.h.tmp, root: p.root}
	if len(p.sessions) == 0 || r.rng.Intn(2) == 0 {
		r.next++
		s := &historySession{project: historyProjects[r.rng.Intn(len(historyProjects))], sid: fmt.Sprintf("p%d", r.next), day: 1 + r.rng.Intn(20)}
		peer.put(s.project, s.sid, hPrompt(s.project, s.sid, s.day, 0, "peer: "+r.words(5))+r.turns(s))
		p.sessions = append(p.sessions, s)
	} else {
		s := p.sessions[r.rng.Intn(len(p.sessions))]
		peer.add(s.project, s.sid, r.turns(s))
	}
	batch := filepath.Join(r.h.tmp, fmt.Sprintf("batch-%d", len(p.batches)+1))
	r.asPeer(func() {
		if err := Ensure(p.index, "", false, nil); err != nil {
			r.t.Fatalf("peer index: %v", err)
		}
		if err := os.MkdirAll(batch, 0o755); err != nil {
			r.t.Fatal(err)
		}
		if _, err := Export(p.index, batch); err != nil {
			r.t.Fatalf("peer export: %v", err)
		}
	})
	p.batches = append(p.batches, batch)
	if _, err := Import(r.index, batch); err != nil {
		r.t.Fatalf("import %s: %v", filepath.Base(batch), err)
	}
	r.log = append(r.log, "import "+filepath.Base(batch)+" from the peer")
}

// asPeer runs f with the environment of the other machine: its own Claude
// store, config (and so identity) and nothing else.
func (r *historyRun) asPeer(f func()) {
	p := r.more.peer
	env := map[string]string{
		"DEJA_CLAUDE_ROOT": p.root,
		"DEJA_CODEX_ROOT":  filepath.Join(r.h.tmp, "peer-no-codex"),
		"DEJA_OPENCODE_DB": filepath.Join(r.h.tmp, "peer-no-opencode.db"),
		"XDG_CONFIG_HOME":  p.config,
		"DEJA_NOTES_FILE":  p.notes,
	}
	old := map[string]string{}
	for k, v := range env {
		old[k] = os.Getenv(k)
		_ = os.Setenv(k, v)
	}
	defer func() {
		for k, v := range old {
			_ = os.Setenv(k, v)
		}
	}()
	f()
}
