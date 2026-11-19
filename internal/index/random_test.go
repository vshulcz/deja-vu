package index

import (
	"flag"
	"fmt"
	"io"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// The random histories of TestRandomHistoriesMatchARebuild, indexed the way
// `deja index` does it: a freshness check, then the pass it hands its walk
// to, with a store changing in between, or another deja's pass. The fixed
// seeds lost a session and a command to the stale walk (#5503). A sweep:
//
//	go test ./internal/index -run RandomCheckedHistories -args -checked-histories=200
var checkedHistories = flag.Int("checked-histories", 0, "random checked histories to run from seed 2000, in place of the fixed ones")

func TestRandomCheckedHistoriesMatchARebuild(t *testing.T) {
	seeds := []int64{2123, 5016}
	if *historySeed != 0 {
		seeds = []int64{*historySeed}
	} else if *checkedHistories > 0 {
		seeds = seeds[:0]
		for i := range *checkedHistories {
			seeds = append(seeds, int64(2000+i))
		}
	}
	for _, seed := range seeds {
		t.Run(strconv.FormatInt(seed, 10), func(t *testing.T) { runCheckedHistory(t, seed) })
	}
}

func runCheckedHistory(t *testing.T, seed int64) {
	t.Helper()
	h := newTwoWayEnv(t)
	r := &historyRun{t: t, h: h, rng: rand.New(rand.NewSource(seed)), index: filepath.Join(h.tmp, "a.db"), walls: seed%3 == 0} //nolint:gosec // a seeded fixture, not crypto
	// The fixed seeds replay #5503's histories as they were found.
	if seed != 2123 && seed != 5016 {
		r.shapes = rand.New(rand.NewSource(-seed)) //nolint:gosec // a seeded fixture, not crypto
	}
	r.openStores()
	// The sweep that found the seeds above made a copy of a session on one
	// step in eight; the draw stays so they replay the same histories.
	step := func() {
		r.rng.Intn(8)
		r.anyStep()
	}
	steps := 48
	for i := range steps {
		step()
		var err error
		switch mode := r.rng.Intn(4); mode {
		case 0:
			err = Ensure(r.index, "", false, nil)
		case 1, 2:
			fresh, _, c := CheckUpToDate(r.index, "")
			if mode == 2 && c != nil {
				step()
				r.log[len(r.log)-1] += " (between check and pass)"
			}
			if !fresh {
				if c != nil {
					err = EnsureChecked(c, false, io.Discard)
				} else {
					err = Ensure(r.index, "", false, nil)
				}
			}
		case 3:
			fresh, _, c := CheckUpToDate(r.index, "")
			step()
			r.log[len(r.log)-1] += " (another pass between check and pass)"
			if err = Ensure(r.index, "", false, nil); err != nil {
				t.Fatal(err)
			}
			if !fresh && c != nil {
				err = EnsureChecked(c, false, io.Discard)
			}
		}
		if err != nil {
			t.Fatalf("seed %d, pass after %q: %v", seed, r.log[len(r.log)-1], err)
		}
		if i%12 == 11 || i == steps-1 {
			// One more ordinary pass picks up what the check's walk could not
			// have seen.
			if err := Ensure(r.index, "", false, nil); err != nil {
				t.Fatal(err)
			}
			r.compare(seed, i)
		}
	}
}

// The histories with walls, with now and then a second transcript of a live
// session's id in another project, which then grows, is written again or goes
// (#699). Fixes and frictext.bin took both copies' records in the order a
// pass left them in the log, which a rebuild does not (#5504).
func TestRandomHistoriesWithSharedCopiesMatchARebuild(t *testing.T) {
	for _, seed := range historySeeds() {
		t.Run(strconv.FormatInt(seed, 10), func(t *testing.T) { runCopiesHistory(t, seed, false) })
	}
}

// The same with each copy written on its session's own day, so a copy can
// hold a turn its session holds, identical to the second. Which copy such a
// turn is filed under, and in what order a deleted copy's turns sit among
// the live ones', moved the command tables (#5509, #5508).
func TestRandomHistoriesWithSameDayCopiesMatchARebuild(t *testing.T) {
	seeds := []int64{15, 18, 19, 24}
	if *historySeed != 0 {
		seeds = []int64{*historySeed}
	}
	for _, seed := range seeds {
		t.Run(strconv.FormatInt(seed, 10), func(t *testing.T) { runCopiesHistory(t, seed, true) })
	}
}

func runCopiesHistory(t *testing.T, seed int64, sameDay bool) {
	t.Helper()
	h := newTwoWayEnv(t)
	r := &historyRun{t: t, h: h, rng: rand.New(rand.NewSource(seed)), index: filepath.Join(h.tmp, "a.db"), walls: true} //nolint:gosec // a seeded fixture, not crypto
	r.openStores()
	var copies []*historySession
	copyStep := func() {
		if len(copies) > 0 && r.rng.Intn(2) == 0 {
			i := r.rng.Intn(len(copies))
			c := copies[i]
			switch r.rng.Intn(3) {
			case 0:
				h.add(c.project, c.sid, r.turns(c))
				r.log = append(r.log, "append to the copy of "+c.sid+" in "+c.project)
			case 1:
				h.put(c.project, c.sid, hPrompt(c.project, c.sid, c.day, 0, "copy again: "+r.words(4))+r.turns(c))
				r.log = append(r.log, "rewrite the copy of "+c.sid+" in "+c.project)
			default:
				if err := os.Remove(h.path(c.project, c.sid)); err != nil {
					t.Fatal(err)
				}
				copies = append(copies[:i], copies[i+1:]...)
				r.log = append(r.log, "delete the copy of "+c.sid+" in "+c.project)
			}
			return
		}
		s := r.pick()
		if s == nil {
			r.step()
			return
		}
		proj := []string{"fork", "aa"}[r.rng.Intn(2)]
		for _, c := range copies {
			if c.project == proj && c.sid == s.sid {
				r.step()
				return
			}
		}
		// Days after any the sessions reach, so no turn of a copy is one its
		// session holds: which copy a shared turn is filed under is #5492's.
		c := &historySession{project: proj, sid: s.sid, day: s.day}
		if !sameDay {
			c.day = 25 + r.rng.Intn(3)
		}
		h.put(proj, s.sid, hPrompt(proj, s.sid, s.day, 0, "copy: "+r.words(5))+r.turns(c))
		copies = append(copies, c)
		r.log = append(r.log, "copy of "+s.sid+" in "+proj)
	}
	steps := 36
	for i := range steps {
		if r.rng.Intn(5) == 0 {
			copyStep()
		} else {
			r.anyStep()
		}
		if err := Ensure(r.index, "", false, nil); err != nil {
			t.Fatalf("seed %d, pass after %q: %v", seed, r.log[len(r.log)-1], err)
		}
		if i%12 == 11 || i == steps-1 {
			r.compare(seed, i)
		}
	}
}

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
	// codexLogged is the sessions known from Codex's history.jsonl alone.
	codexLogged []string
	codexLines  int
	// db is empty when there is no sqlite3 to write the store with.
	db       string
	opencode []*opencodeSession
	// grokDB is a store whose records name the database, as cursor's and
	// goose's do; opencode's name the project.
	grokDB string
	grok   []*opencodeSession
	clock  int64
	// claudeHistory is Claude Code's prompt log.
	claudeHistory string
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
		m.grokDB = filepath.Join(r.h.tmp, "grok.db")
		t.Setenv("DEJA_GROK_DB", m.grokDB)
		r.sqlOn(m.grokDB, `CREATE TABLE workspaces (id TEXT PRIMARY KEY, canonical_path TEXT);
CREATE TABLE sessions (id TEXT PRIMARY KEY, workspace_id TEXT, title TEXT, cwd_last TEXT, created_at TEXT);
CREATE TABLE messages (session_id TEXT, seq INTEGER, role TEXT, message_json TEXT, created_at TEXT);`)
		r.sql(`create table session(id text primary key, directory text, title text, time_created integer, time_updated integer);
create table message(id text primary key, session_id text, time_created integer, time_updated integer, data text);
create table part(id text primary key, message_id text, session_id text, time_created integer, time_updated integer, data text);`)
	}
	m.clock = time.Now().UnixMilli()
	// Claude Code's prompt log beside the transcripts: a deleted one leaves
	// its first prompt there, where the log is the only thing left (#5046).
	m.claudeHistory = filepath.Join(r.h.tmp, "claude-history.jsonl")
	t.Setenv("DEJA_CLAUDE_HISTORY_FILE", m.claudeHistory)
}

// promptLogged writes a Claude session's opening prompt to the prompt log, a
// moment after the transcript stamps it, as Claude Code does.
func (r *historyRun) promptLogged(s *historySession, text string) {
	at := time.Date(2026, 6, s.day, 10, 0, 0, 300_000_000, time.UTC)
	line := hjson(map[string]any{"display": text, "pastedContents": map[string]any{}, "timestamp": at.UnixMilli(), "project": "/Users/me/" + s.project, "sessionId": s.sid})
	f, err := os.OpenFile(r.more.claudeHistory, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		r.t.Fatal(err)
	}
	if _, err := f.WriteString(line); err != nil {
		r.t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		r.t.Fatal(err)
	}
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
		if r.rng.Intn(3) == 0 {
			r.grokStep()
		} else {
			r.opencodeStep()
		}
		// A store and a transcript changed in one pass: the pass carries
		// the store's sessions by key and the transcript's by path (#5476).
		if r.rng.Intn(2) == 0 {
			r.step()
		}
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
			text := r.prompt(4)
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
	op := r.rng.Intn(9)
	if c == nil && op < 6 {
		op = 0
	}
	switch op {
	case 6, 7, 8:
		r.codexHistoryStep(op)
	case 0:
		r.next++
		c = &codexRollout{id: fmt.Sprintf("cx%d", r.next), day: 1 + r.rng.Intn(20)}
		body := codexLine(c.at(), "session_meta", map[string]any{"id": c.id, "cwd": "/w/" + historyProjects[r.rng.Intn(len(historyProjects))]})
		if r.rng.Intn(3) == 0 {
			body += codexLine(c.at(), "response_item", map[string]any{"type": "message", "role": "user",
				"content": []any{map[string]any{"type": "input_text", "text": historyThin[r.rng.Intn(len(historyThin))]}}})
		}
		body += r.codexTurns(c)
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

// codexHistoryStep writes Codex's prompt log: prompts of sessions known from
// the log alone, often in the same pass as another one's rollout landing, so
// the log is read by the replacement path; or the log trimmed from the front
// the way Codex keeps it under max_bytes (#5403).
func (r *historyRun) codexHistoryStep(op int) {
	m := &r.more
	hist := filepath.Join(m.codexRoot, "history.jsonl")
	if op == 8 && fileExists(hist) {
		b, err := os.ReadFile(hist)
		if err != nil {
			r.t.Fatal(err)
		}
		lines := strings.SplitAfter(string(b), "\n")
		cut := 1 + r.rng.Intn(len(lines))
		writeHistoryFile(r, hist, strings.Join(lines[min(cut, len(lines)):], ""), false)
		r.log = append(r.log, fmt.Sprintf("codex history trimmed by %d lines", cut))
		return
	}
	var did []string
	for n := 1 + r.rng.Intn(3); n > 0; n-- {
		var id string
		// Codex logs the prompts of sessions with a rollout too.
		var withRollout []string
		for _, c := range m.codex {
			if len(c.id) == 36 {
				withRollout = append(withRollout, c.id)
			}
		}
		switch {
		case len(withRollout) > 0 && r.rng.Intn(4) == 0:
			id = withRollout[r.rng.Intn(len(withRollout))]
		case len(m.codexLogged) < 3 || r.rng.Intn(3) == 0:
			r.next++
			id = fmt.Sprintf("019a0000-0000-7000-8000-%012d", r.next)
			m.codexLogged = append(m.codexLogged, id)
		default:
			id = m.codexLogged[r.rng.Intn(len(m.codexLogged))]
		}
		m.codexLines++
		at := time.Date(2026, 6, 1, 10, 0, 0, 0, time.UTC).Add(time.Duration(m.codexLines) * time.Minute)
		writeHistoryFile(r, hist, hjson(map[string]any{"session_id": id, "ts": at.Unix(), "text": r.prompt(3)}), fileExists(hist))
		did = append(did, "a codex history prompt for "+id)
	}
	if op == 7 && len(m.codexLogged) > 1 {
		i := r.rng.Intn(len(m.codexLogged))
		id := m.codexLogged[i]
		m.codexLogged = append(m.codexLogged[:i], m.codexLogged[i+1:]...)
		c := &codexRollout{id: id, day: 1 + r.rng.Intn(20)}
		body := codexLine(c.at(), "session_meta", map[string]any{"id": c.id, "cwd": "/w/app"})
		// Now and then the rollout holds no message yet: the session leaves
		// the history parse with nothing to take its place.
		if r.rng.Intn(3) > 0 {
			body += r.codexTurns(c)
		}
		writeHistoryFile(r, r.codexPath(c), body, false)
		m.codex = append(m.codex, c)
		did = append(did, "the rollout of logged "+id+" lands")
	}
	r.log = append(r.log, strings.Join(did, ", "))
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
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
	r.sqlOn(r.more.db, stmts)
}

func (r *historyRun) sqlOn(db, stmts string) {
	r.t.Helper()
	if out, err := exec.CommandContext(r.t.Context(), "sqlite3", db, stmts).CombinedOutput(); err != nil {
		r.t.Fatalf("sqlite3: %v %s", err, out)
	}
	// The store is noticed by its mtime, and two writes in one second would
	// otherwise look like none.
	at := time.Now().Add(time.Duration(len(r.log)+1) * time.Second)
	_ = os.Chtimes(db, at, at)
}

// grokStep starts a grok session or adds turns to one, at the store's clock.
func (r *historyRun) grokStep() {
	m := &r.more
	m.clock = max(m.clock+1000, time.Now().UnixMilli())
	turn := func(s *opencodeSession) string {
		s.msgs++
		role, msg := "user", `{"content":"why does the `+r.words(4)+` fail"}`
		if s.msgs%2 == 0 {
			role, msg = "assistant", `{"content":[{"type":"text","text":"The `+r.words(5)+` needs a fix."}]}`
		}
		at := time.UnixMilli(m.clock + int64(s.msgs)).UTC().Format("2006-01-02T15:04:05.000Z")
		return fmt.Sprintf("INSERT INTO messages VALUES ('%s',%d,'%s','%s','%s');\n", s.id, s.msgs, role, msg, at)
	}
	if len(m.grok) == 0 || r.rng.Intn(3) == 0 {
		r.next++
		s := &opencodeSession{id: fmt.Sprintf("gk%d", r.next)}
		at := time.UnixMilli(m.clock).UTC().Format("2006-01-02T15:04:05.000Z")
		r.sqlOn(m.grokDB, fmt.Sprintf("INSERT INTO sessions VALUES ('%s','w','start: %s','/w/%s','%s');\n", s.id, r.words(3), historyProjects[r.rng.Intn(len(historyProjects))], at)+turn(s))
		m.grok = append(m.grok, s)
		r.log = append(r.log, "new grok "+s.id)
		return
	}
	s := m.grok[r.rng.Intn(len(m.grok))]
	r.sqlOn(m.grokDB, turn(s)+turn(s))
	r.log = append(r.log, "grow grok "+s.id)
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
	ids = append(ids, m.codexLogged...)
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

// A random history of appends, new sessions, torn and unterminated last
// lines, rewrites, moves and deletions, indexed one pass at a time, must
// leave what a forced rebuild of the same stores leaves. The fixed seeds run
// on every test run. Flags, since TestMain clears the environment:
//
//	go test ./internal/index -run RandomHistories -args -histories=200
//	go test ./internal/index -run RandomHistories -args -history-seed=17
var (
	historyCount = flag.Int("histories", 4, "random histories to run")
	historySeed  = flag.Int64("history-seed", 0, "run this one history")
)

func TestRandomHistoriesMatchARebuild(t *testing.T) {
	for _, seed := range historySeeds() {
		t.Run(strconv.FormatInt(seed, 10), func(t *testing.T) { runRandomHistory(t, seed, false) })
	}
}

// The same histories with error lines a few sessions share, so the walls and
// frictext.bin have something to hold.
func TestRandomHistoriesWithWallsMatchARebuild(t *testing.T) {
	for _, seed := range historySeeds() {
		t.Run(strconv.FormatInt(seed, 10), func(t *testing.T) { runRandomHistory(t, seed, true) })
	}
}

func historySeeds() []int64 {
	seeds := []int64{*historySeed}
	if *historySeed == 0 {
		seeds = seeds[:0]
		for i := 1; i <= *historyCount; i++ {
			seeds = append(seeds, int64(i))
		}
	}
	return seeds
}

var historyWords = strings.Fields(`pool queue cache webhook retry ledger outbox kafka
	migration deadlock mutex config timeout pgbouncer worker billing payments
	signature rotation schema index tokenizer parser flaky linker`)

var historyProjects = []string{"app", "svc", "infra"}

// Openings too thin to name a session: the title waits for a later question.
var historyThin = []string{"hi", "ok", "continue", "fix it", "pgbouncer?"}

// prompt is a question, or now and then a thin turn.
func (r *historyRun) prompt(thinOdds int) string {
	if r.rng.Intn(thinOdds) == 0 {
		return historyThin[r.rng.Intn(len(historyThin))]
	}
	return "why does the " + r.words(4) + " fail"
}

type historySession struct {
	project, sid string
	day, min     int
	calls        int
	// torn holds the second half of a line written without it.
	torn string
	// said is the last line the agent said, kept to be written again verbatim,
	// time and all, as a store that duplicates a line does.
	said string
	// first is the body the transcript was first written with.
	first string
}

type historyRun struct {
	t     *testing.T
	h     *twoWayEnv
	rng   *rand.Rand
	live  []*historySession
	next  int
	log   []string
	index string
	more  historyStores
	walls bool
	// awaiting holds sessions whose prompt is in the log and whose transcript
	// the next step writes.
	awaiting []awaitingTranscript
	// shapes, when set, draws the steps a pass lands in the middle of: a
	// transcript rewritten and moved at once, which keeps the old file beside
	// the new one, a call whose result lands a pass later, and a rewrite that
	// replays the turns the transcript started with. Its own source, so the
	// histories without it replay as they were.
	shapes *rand.Rand
}

type awaitingTranscript struct {
	s    *historySession
	body string
}

func runRandomHistory(t *testing.T, seed int64, walls bool) {
	t.Helper()
	h := newTwoWayEnv(t)
	r := &historyRun{t: t, h: h, rng: rand.New(rand.NewSource(seed)), index: filepath.Join(h.tmp, "a.db"), walls: walls, shapes: rand.New(rand.NewSource(-seed))} //nolint:gosec // a seeded fixture, not crypto
	r.openStores()
	// Even seeds extend the offset table on every pass; odd ones leave the
	// tail to the readers' walk, as a small append does on a real store.
	if seed%2 == 0 {
		defer func(floor, share int64) { recordOffsetsRefreshFloor, recordOffsetsRefreshShare = floor, share }(recordOffsetsRefreshFloor, recordOffsetsRefreshShare)
		recordOffsetsRefreshFloor, recordOffsetsRefreshShare = 0, 1<<62
	}
	steps := 24
	for i := range steps {
		r.anyStep()
		if err := Ensure(r.index, "", false, nil); err != nil {
			t.Fatalf("seed %d, pass after %q: %v", seed, r.log[len(r.log)-1], err)
		}
		if i%12 == 11 || i == steps-1 {
			r.compare(seed, i)
		}
	}
}

// compare rebuilds a copy of the index from the same stores and diffs it with
// the one kept up to date pass by pass.
func (r *historyRun) compare(seed int64, step int) {
	t := r.t
	t.Helper()
	b := filepath.Join(r.h.tmp, fmt.Sprintf("rebuild-%d.db", step))
	copyDir(t, r.index, b)
	if err := Ensure(b, "", true, nil); err != nil {
		t.Fatal(err)
	}
	label := fmt.Sprintf("seed %d after step %d", seed, step)
	got, want := takeSnap(t, r.index), takeSnap(t, b)
	if diffSnaps(t, label, got, want) > 0 {
		t.Fatalf("%s; replay with -args -history-seed=%d. Steps:\n  %s", label, seed, strings.Join(r.log, "\n  "))
	}
	samePostings(t, r.index, b)
	offsetTableMatchesAWalk(t, r.index, false)
}

// wall is an error line a few sessions share, so friction has walls to name.
// The port differs between runs and not in the wall's hash, so which run's
// line a wall shows depends on which session is read first. Nothing in a
// history without walls.
func (r *historyRun) wall() string {
	if !r.walls {
		return ""
	}
	if r.rng.Intn(2) == 0 {
		return "zsh:1: command not found: pgbouncer\n"
	}
	return fmt.Sprintf("dial tcp 127.0.0.1:%d: connect: connection refused\n", 5432+r.rng.Intn(3)*1000)
}

func (r *historyRun) words(n int) string {
	w := make([]string, n)
	for i := range w {
		w[i] = historyWords[r.rng.Intn(len(historyWords))]
	}
	return strings.Join(w, " ")
}

// turns writes one to three turns of s and moves its clock on.
func (r *historyRun) turns(s *historySession) string {
	var b strings.Builder
	for n := 1 + r.rng.Intn(3); n > 0; n-- {
		if s.said != "" && r.rng.Intn(6) == 0 {
			b.WriteString(s.said)
		}
		// Some turns share a minute with the one before.
		if r.rng.Intn(4) > 0 {
			s.min++
		}
		if s.min > 59 {
			s.day, s.min = s.day+1, 0
		}
		s.calls++
		id := fmt.Sprintf("%s-%d", s.sid, s.calls)
		switch r.rng.Intn(5) {
		case 0:
			b.WriteString(hPrompt(s.project, s.sid, s.day, s.min, r.prompt(4)))
		case 1:
			s.said = hSay(s.project, s.sid, s.day, s.min, "The "+r.words(5)+" needs a fix.")
			b.WriteString(s.said)
		case 2:
			b.WriteString(hBash(s.project, s.sid, s.day, s.min, id, "go test ./"+historyWords[r.rng.Intn(len(historyWords))]+"/...",
				"--- FAIL: TestPool (0.01s)\n    pool_test.go:12: "+r.words(3)+"\n"+r.wall()+"FAIL", true))
		case 3:
			b.WriteString(hBash(s.project, s.sid, s.day, s.min, id, "go build ./...", r.wall(), false))
		default:
			b.WriteString(hEdit(s.project, s.sid, s.day, s.min, id, historyWords[r.rng.Intn(len(historyWords))]+".go", r.words(2), r.words(2)))
		}
		if r.walls && r.rng.Intn(2) == 0 && s.min < 59 {
			s.min++
			b.WriteString(hBash(s.project, s.sid, s.day, s.min, id+"w", "make migrate", r.wall(), true))
		}
	}
	return b.String()
}

func (r *historyRun) pick() *historySession {
	if len(r.live) == 0 {
		return nil
	}
	return r.live[r.rng.Intn(len(r.live))]
}

func (r *historyRun) step() {
	h := r.h
	// A session whose prompt was logged a pass before its transcript: the
	// transcript lands now, as Claude Code writes it after the prompt hook.
	if len(r.awaiting) > 0 {
		a := r.awaiting[0]
		r.awaiting = r.awaiting[1:]
		h.put(a.s.project, a.s.sid, a.body)
		r.live = append(r.live, a.s)
		r.log = append(r.log, "the transcript of "+a.s.sid+" lands")
		return
	}
	s := r.pick()
	op := r.rng.Intn(10)
	if s == nil {
		op = 0
	}
	// A torn line is finished before anything else touches its file: an
	// agent writes the rest of a line before it writes the next one.
	if s != nil && s.torn != "" && op != 0 {
		h.add(s.project, s.sid, s.torn)
		s.torn = ""
		r.log = append(r.log, "finish the torn line of "+s.sid)
		return
	}
	if s != nil && op != 0 && op != 1 && r.shapes != nil && r.shapes.Intn(6) == 0 {
		r.shapeStep(s)
		return
	}
	switch op {
	case 0, 1:
		r.next++
		s = &historySession{project: historyProjects[r.rng.Intn(len(historyProjects))], sid: fmt.Sprintf("s%d", r.next), day: 1 + r.rng.Intn(20)}
		first := "start: " + r.words(5)
		if r.rng.Intn(3) == 0 {
			first = historyThin[r.rng.Intn(len(historyThin))]
		}
		body := hPrompt(s.project, s.sid, s.day, 0, first) + r.turns(s)
		s.first = body
		r.promptLogged(s, first)
		if r.next%3 == 0 {
			r.awaiting = append(r.awaiting, awaitingTranscript{s, body})
			r.log = append(r.log, "the prompt of "+s.sid+" in "+s.project+" logged before its transcript")
			return
		}
		h.put(s.project, s.sid, body)
		r.live = append(r.live, s)
		r.log = append(r.log, "new "+s.sid+" in "+s.project)
	case 2, 3, 4:
		h.add(s.project, s.sid, r.turns(s))
		r.log = append(r.log, "append to "+s.sid)
	case 5:
		// The last line complete but its newline not written yet.
		h.add(s.project, s.sid, strings.TrimSuffix(r.turns(s), "\n"))
		s.torn = "\n"
		r.log = append(r.log, "append to "+s.sid+" without the final newline")
	case 6:
		// Half a line on disk, the rest on a later pass.
		line := r.turns(s)
		cut := len(line) / 2
		h.add(s.project, s.sid, line[:cut])
		s.torn = line[cut:]
		r.log = append(r.log, "append half a line to "+s.sid)
	case 7:
		// Written again shorter, the way a rewrite or a compaction leaves it.
		s.day++
		s.min = 0
		h.put(s.project, s.sid, hPrompt(s.project, s.sid, s.day, 0, "again: "+r.words(5))+r.turns(s))
		r.log = append(r.log, "rewrite "+s.sid)
	case 8:
		to := historyProjects[r.rng.Intn(len(historyProjects))]
		if to == s.project {
			r.log = append(r.log, "touch "+s.sid)
			at := time.Now().Add(time.Duration(r.rng.Intn(5)+1) * time.Second)
			if err := os.Chtimes(h.path(s.project, s.sid), at, at); err != nil {
				r.t.Fatal(err)
			}
			return
		}
		from := h.path(s.project, s.sid)
		if err := os.MkdirAll(filepath.Dir(h.path(to, s.sid)), 0o755); err != nil {
			r.t.Fatal(err)
		}
		if err := os.Rename(from, h.path(to, s.sid)); err != nil {
			r.t.Fatal(err)
		}
		r.log = append(r.log, "move "+s.sid+" from "+s.project+" to "+to)
		s.project = to
	case 9:
		if err := os.Remove(h.path(s.project, s.sid)); err != nil {
			r.t.Fatal(err)
		}
		for i, l := range r.live {
			if l == s {
				r.live = append(r.live[:i], r.live[i+1:]...)
				break
			}
		}
		r.log = append(r.log, "delete "+s.sid)
	}
}

// shapeStep is one of the steps shapes draws, on s.
func (r *historyRun) shapeStep(s *historySession) {
	h := r.h
	switch r.shapes.Intn(3) {
	case 0:
		// Written again and moved to another project before the next pass:
		// no rename, the old file is kept and the new one is a copy.
		to := historyProjects[r.shapes.Intn(len(historyProjects))]
		if to == s.project {
			to = "moved"
		}
		s.day++
		s.min = 0
		h.put(s.project, s.sid, hPrompt(s.project, s.sid, s.day, 0, "again: "+r.words(5))+r.turns(s))
		if err := os.MkdirAll(filepath.Dir(h.path(to, s.sid)), 0o755); err != nil {
			r.t.Fatal(err)
		}
		if err := os.Rename(h.path(s.project, s.sid), h.path(to, s.sid)); err != nil {
			r.t.Fatal(err)
		}
		r.log = append(r.log, "rewrite "+s.sid+" and move it from "+s.project+" to "+to)
		s.project = to
	case 1:
		// A command whose result lands a pass after its call.
		if s.min > 58 {
			s.day, s.min = s.day+1, 0
		}
		s.min++
		s.calls++
		out, failed := "ok", false
		switch r.shapes.Intn(3) {
		case 0:
			out, failed = "Exit code 1\n--- FAIL: TestPool\n"+r.wall()+"FAIL", true
		case 1:
			out = r.wall() + "ok"
		}
		call, result, _ := strings.Cut(hBash(s.project, s.sid, s.day, s.min, fmt.Sprintf("%s-%d", s.sid, s.calls), "go test ./"+historyWords[r.shapes.Intn(len(historyWords))]+"/...", out, failed), "\n")
		h.add(s.project, s.sid, call+"\n")
		s.torn = result
		r.log = append(r.log, "a call of "+s.sid+" whose result lands a pass later")
	default:
		// Written again with the turns it started with, as a resume that
		// replays them does, then more.
		if s.first == "" {
			s.first = hPrompt(s.project, s.sid, s.day, 0, "replayed: "+r.words(3))
		}
		h.put(s.project, s.sid, s.first+r.turns(s))
		r.log = append(r.log, "rewrite "+s.sid+" replaying its first turns")
	}
}
