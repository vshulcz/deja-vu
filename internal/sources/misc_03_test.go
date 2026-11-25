package sources

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"github.com/vshulcz/deja-vu/internal/model"
	"io/fs"
	"math"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"
)

// writeFramedLog writes body the way dsh does by default: one zstd frame per
// line, the frames concatenated.
func writeFramedLog(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	for _, line := range strings.SplitAfter(body, "\n") {
		if line == "" {
			continue
		}
		cmd := exec.CommandContext(t.Context(), "zstd", "-q", "-c")
		cmd.Stdin = strings.NewReader(line)
		frame, err := cmd.Output()
		if err != nil {
			t.Fatalf("zstd: %v", err)
		}
		out.Write(frame)
	}
	if err := os.WriteFile(path, out.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
}

const deepSeekV0Log = `{"type":"session","version":0,"id":"session-7c2e9f40-3b1a-4d5e-8f60-1a2b3c4d5e6f","createdAt":1790506922540,"cwd":"/work/pgbouncer-lab","delegationDepth":0}
{"type":"user/message","seq":1,"time":1790506922600,"data":{"content":[{"type":"text","text":"oldneedle what holds the pool size"}],"source":{"kind":"user"},"role":"user"}}
{"type":"assistant/message","seq":2,"time":1790506922700,"data":{"turn":1,"step":1,"message":{"role":"assistant","content":[{"type":"text","text":"pgbouncer.ini does"}],"source":{"kind":"model","provider":"mini"}}}}
`

// dsh 2.0.15 (dsh-base 0.1.7-rc.2) migrates a session it opens into a new
// generation file with the whole history and leaves the old one as it was. v4
// lifts a tool result out of its tool-result wrapper into a tool-role message
// that carries isError itself.
const deepSeekV4Log = `{"type":"session","version":4,"id":"session-7c2e9f40-3b1a-4d5e-8f60-1a2b3c4d5e6f","createdAt":1790506922540,"cwd":"/work/pgbouncer-lab","isSeeded":false,"delegationDepth":0}
{"type":"user/message","seq":1,"time":1790506922600,"data":{"content":[{"type":"text","text":"oldneedle what holds the pool size"}],"source":{"kind":"user"},"role":"user"}}
{"type":"assistant/message","seq":2,"time":1790506922700,"data":{"turn":1,"step":1,"message":{"role":"assistant","content":[{"type":"text","text":"pgbouncer.ini does"}],"source":{"kind":"model","provider":"mini"}}}}
{"type":"user/message","seq":3,"time":1790593322600,"data":{"content":[{"type":"text","text":"v4needle raise it to 60"}],"source":{"kind":"user"},"role":"user"}}
{"type":"tool/call","seq":4,"time":1790593322700,"data":{"turn":2,"step":1,"callId":"denied","name":"edit","arguments":"{\"file_path\": \"/work/pgbouncer-lab/denied.ini\", \"old_string\": \"pool_size = 40\", \"new_string\": \"pool_size = 60\"}"}}
{"type":"tool/result","seq":5,"time":1790593322710,"data":{"turn":2,"step":1,"message":{"role":"tool","source":{"kind":"tool","callId":"denied"},"toolCallId":"denied","content":[{"type":"text","text":"Error: rejected"}],"isError":true,"id":"t1"}}}
{"type":"tool/call","seq":6,"time":1790593322720,"data":{"turn":2,"step":1,"callId":"good","name":"edit","arguments":"{\"file_path\": \"/work/pgbouncer-lab/pgbouncer.ini\", \"old_string\": \"pool_size = 40\", \"new_string\": \"pool_size = 60\"}"}}
{"type":"tool/result","seq":7,"time":1790593322730,"data":{"turn":2,"step":1,"message":{"role":"tool","source":{"kind":"tool","callId":"good"},"toolCallId":"good","content":[{"type":"text","text":"v4output the file has been updated"}],"id":"t2"}}}
`

// A session dsh has migrated keeps its old log beside the new generation, and
// the new one holds everything. Reading the old one froze the session at the
// upgrade; reading both listed it twice (#4600).
func TestDeepSeekReadsTheNewestLogGeneration(t *testing.T) {
	if !ZstdAvailable() {
		t.Skip("zstd CLI not available")
	}
	tmp := hermeticSourcesEnv(t)
	root := filepath.Join(tmp, "dsh", "sessions")
	t.Setenv("DSH_HOME", filepath.Join(tmp, "dsh"))
	t.Setenv("DEJA_DEEPSEEK_ROOT", root)
	ws := filepath.Join(root, "--work-pgbouncer-lab--")

	migrated := filepath.Join(ws, "session-7c2e9f40-3b1a-4d5e-8f60-1a2b3c4d5e6f")
	writeFramedLog(t, filepath.Join(migrated, "session.jsonl.zstd"), deepSeekV0Log)
	writeFramedLog(t, filepath.Join(migrated, "session.v4.jsonl.zstd"), deepSeekV4Log)
	fresh := filepath.Join(ws, "session-v4only")
	writeFramedLog(t, filepath.Join(fresh, "session.v4.jsonl.zstd"),
		strings.ReplaceAll(deepSeekV4Log, "session-7c2e9f40-3b1a-4d5e-8f60-1a2b3c4d5e6f", "session-v4only"))
	// A generation this reader has not seen: dsh's own filename grammar is
	// open-ended and the event envelope did not change across v3 and v4, so it
	// is read rather than dropped.
	future := filepath.Join(ws, "session-v5")
	writeFramedLog(t, filepath.Join(future, "session.v5.jsonl.zstd"),
		strings.ReplaceAll(strings.ReplaceAll(deepSeekV4Log, `"version":4`, `"version":5`), "session-7c2e9f40-3b1a-4d5e-8f60-1a2b3c4d5e6f", "session-v5"))

	files := DeepSeekSessionFiles()
	got := make([]string, 0, len(files))
	for _, f := range files {
		got = append(got, filepath.Join(filepath.Base(filepath.Dir(f)), filepath.Base(f)))
	}
	slices.Sort(got)
	want := []string{
		"session-7c2e9f40-3b1a-4d5e-8f60-1a2b3c4d5e6f/session.v4.jsonl.zstd",
		"session-v4only/session.v4.jsonl.zstd",
		"session-v5/session.v5.jsonl.zstd",
	}
	for i := range want {
		want[i] = filepath.FromSlash(want[i])
	}
	if !slices.Equal(got, want) {
		t.Fatalf("DeepSeekSessionFiles = %q, want %q", got, want)
	}

	ss := LoadDeepSeek()
	if len(ss) != 3 {
		t.Fatalf("LoadDeepSeek = %d sessions, want 3", len(ss))
	}
	for _, s := range ss {
		var text []string
		for _, m := range s.Messages {
			text = append(text, m.Text)
		}
		all := strings.Join(text, "\n")
		if !strings.Contains(all, "oldneedle") || !strings.Contains(all, "v4needle") || !strings.Contains(all, "v4output") {
			t.Errorf("%s read as %q, want the whole migrated history", s.ID, all)
		}
	}
}

func TestDeepSeekLogNameGrammar(t *testing.T) {
	for name, want := range map[string]bool{
		"session.jsonl":          true,
		"session.jsonl.zstd":     true,
		"session.v3.jsonl.zstd":  true,
		"session.v4.jsonl":       true,
		"session.v4.jsonl.zstd":  true,
		"session.v12.jsonl.zstd": true,
		"session.v0.jsonl":       false,
		"session.v04.jsonl":      false,
		"session.V4.jsonl":       false,
		"session.v4.jsonl.tmp":   false,
		"session.vx.jsonl":       false,
	} {
		if got := isDeepSeekLog(filepath.Join("s", "session-a", name)); got != want {
			t.Errorf("isDeepSeekLog(%s) = %v, want %v", name, got, want)
		}
	}
}

// v4 moved isError from the tool-result block onto the message, so a refused
// edit has to be read off the message or it is credited as made.
func TestParseDeepSeekFileReadsAV4ErrorResult(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session-x", "session.v4.jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(deepSeekV4Log), 0o644); err != nil {
		t.Fatal(err)
	}
	ss, err := ParseDeepSeekFile(path)
	if err != nil || len(ss) != 1 {
		t.Fatalf("parse: %v, %d sessions", err, len(ss))
	}
	var edits, out []string
	for _, m := range ss[0].Messages {
		switch m.Role {
		case RoleEdit:
			edits = append(edits, m.Text)
		case RoleToolOutput:
			out = append(out, m.Text)
		}
	}
	if got := strings.Join(edits, "|"); got != "/work/pgbouncer-lab/pgbouncer.ini\npool_size = 40" {
		t.Errorf("edits = %q, want only the edit whose result came back clean", got)
	}
	if got := strings.Join(out, "|"); got != "Error: rejected|v4output the file has been updated" {
		t.Errorf("tool output = %q", got)
	}
}

// A v4 result's blocks are the content itself, so an image or a block a plugin
// wrote sits beside the text. Only the text is what the tool printed.
func TestParseDeepSeekFileKeepsOnlyTextFromAV4Result(t *testing.T) {
	log := `{"type":"session","version":4,"id":"session-blocks","createdAt":1790506922540,"cwd":"/work/pgbouncer-lab","isSeeded":false,"delegationDepth":0}
{"type":"user/message","seq":1,"time":1790506922600,"data":{"content":[{"type":"text","text":"show the chart"}],"source":{"kind":"user"},"role":"user"}}
{"type":"tool/call","seq":2,"time":1790506922700,"data":{"callId":"c1","name":"read_image","arguments":"{\"file_path\": \"/work/pgbouncer-lab/pool.png\"}"}}
{"type":"tool/result","seq":3,"time":1790506922710,"data":{"message":{"role":"tool","source":{"kind":"tool","callId":"c1"},"toolCallId":"c1","content":[{"type":"text","text":"chartoutput pool usage"},{"type":"image","text":"imageleak","data":"AAAA","mediaType":"image/png"},{"type":"plugin:annotate","text":"pluginleak","content":[{"type":"text","text":"nestedleak"}]}],"id":"t1"}}}
`
	path := filepath.Join(t.TempDir(), "session-blocks", "session.v4.jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(log), 0o644); err != nil {
		t.Fatal(err)
	}
	ss, err := ParseDeepSeekFile(path)
	if err != nil || len(ss) != 1 {
		t.Fatalf("parse: %v, %d sessions", err, len(ss))
	}
	var out []string
	for _, m := range ss[0].Messages {
		if m.Role == RoleToolOutput {
			out = append(out, m.Text)
		}
	}
	if got := strings.Join(out, "|"); got != "chartoutput pool usage" {
		t.Errorf("tool output = %q, want only the text block", got)
	}
}

// One generation held raw and framed means dsh's encoding setting changed, and
// dsh reads only the one it is set to: the file written last is the session.
func TestDeepSeekKeepsOneEncodingOfAGeneration(t *testing.T) {
	tmp := hermeticSourcesEnv(t)
	root := filepath.Join(tmp, "dsh", "sessions")
	t.Setenv("DSH_HOME", filepath.Join(tmp, "dsh"))
	t.Setenv("DEJA_DEEPSEEK_ROOT", root)
	dir := filepath.Join(root, "--work-pgbouncer-lab--", "session-tie")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	framed, raw := filepath.Join(dir, "session.v4.jsonl.zstd"), filepath.Join(dir, "session.v4.jsonl")
	for _, p := range []string{framed, raw} {
		if err := os.WriteFile(p, nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	old := time.Now().Add(-time.Hour)
	if err := os.Chtimes(framed, old, old); err != nil {
		t.Fatal(err)
	}
	if got := DeepSeekSessionFiles(); !slices.Equal(got, []string{raw}) {
		t.Errorf("DeepSeekSessionFiles = %q, want only the raw log written last", got)
	}
	if !DeepSeekLogSupersedes(raw, framed) {
		t.Error("the raw log written last does not supersede the framed one")
	}
}

// devinHome isolates a test from a real Devin CLI install. The store location
// and the home it is derived from are both pinned, so a contributor running
// `devin` locally gets the same test as CI.
func devinHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("LOCALAPPDATA", filepath.Join(home, "AppData", "Local"))
	t.Setenv("APPDATA", filepath.Join(home, "AppData", "Roaming"))
	t.Setenv("XDG_DATA_HOME", "")
	t.Setenv("DEJA_DEVIN_DB", "")
	return home
}

// devinTestDB builds a store from a SQL script at <tmp>/cli/sessions.db, so
// its sibling <tmp>/summaries is where the real store's summaries directory
// sits — devinSummariesDir walks the same two levels up the CLI's layout does.
func devinTestDB(t *testing.T, sql string) (db, dataDir string) {
	t.Helper()
	if !SQLite3Available() {
		t.Skip("sqlite3 not installed")
	}
	dataDir = t.TempDir()
	db = filepath.Join(dataDir, "cli", "sessions.db")
	if err := os.MkdirAll(filepath.Dir(db), 0o755); err != nil {
		t.Fatal(err)
	}
	// The script goes in on stdin, not as an argument: the fixture opens with
	// a `--` comment and the sqlite3 CLI reads a leading `--` as an option.
	cmd := exec.CommandContext(t.Context(), "sqlite3", db)
	cmd.Stdin = strings.NewReader(sql)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("create sqlite fixture: %v: %s", err, out)
	}
	return db, dataDir
}

func devinFixtureSQL(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "fixtures", "registry", "devin", "devin.sql"))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestParseDevinDBReadsMainChainAndDedupes(t *testing.T) {
	devinHome(t)
	db, _ := devinTestDB(t, devinFixtureSQL(t))
	sessions, err := ParseDevinDB(db)
	if err != nil {
		t.Fatal(err)
	}
	s := findDevinSession(sessions, "registry-devin")
	if s == nil {
		t.Fatalf("registry-devin not in %#v", sessions)
	}
	if s.Harness != "devin" || s.Project != "w/api" || s.Path != db {
		t.Fatalf("identity = %#v", s)
	}
	if s.Started.IsZero() || s.Updated.IsZero() || !s.Started.Before(s.Updated) {
		t.Fatalf("window = %v..%v", s.Started, s.Updated)
	}
	roles := make([]string, 0, len(s.Messages))
	texts := make([]string, 0, len(s.Messages))
	for _, m := range s.Messages {
		roles = append(roles, m.Role)
		texts = append(texts, m.Text)
	}
	joined := strings.Join(texts, "\n")

	// The rebuilt prefix rows are scaffolding, never the conversation.
	if strings.Contains(joined, "system_info") || strings.Contains(joined, "<rules>") {
		t.Fatalf("prefix rows leaked into the session: %s", joined)
	}
	// The user turn arrives once: node 8 replays node 3 under the same
	// message_id, and keeping both reads the compacted copy as a second ask.
	if n := strings.Count(joined, "the retry loop drops the last attempt"); n != 1 {
		t.Fatalf("user turn appears %d times, want 1: %s", n, joined)
	}
	// The exec call is a command record with its tool result behind it,
	// stamped with the tool row's own exit code rather than a second
	// message. prompt_history commands stand alone, so only the exec one is
	// asked for a result.
	var cmd string
	var hasResult bool
	for i, m := range s.Messages {
		if m.Role == "command" && strings.HasPrefix(m.Text, "$ go test") {
			cmd = m.Text
			hasResult = i+1 < len(s.Messages) && s.Messages[i+1].Role == "tool-output"
		}
	}
	if !hasResult {
		t.Fatalf("command record has no tool result behind it: %#v", roles)
	}
	if !strings.HasPrefix(cmd, "$ go test ./internal/loop") {
		t.Fatalf("command record = %q", cmd)
	}
	if !strings.Contains(cmd, "exit 1") {
		t.Fatalf("command record carries no exit stamp: %q", cmd)
	}
	// The edit call leaves files/edit work records on its file.
	var sawEdit bool
	for _, m := range s.Messages {
		if m.Role == "edit" && strings.HasPrefix(m.Text, "/w/api/loop.go") {
			sawEdit = true
		}
	}
	if !sawEdit {
		t.Fatalf("no edit record for /w/api/loop.go: %s", joined)
	}
	// An unmarked system row is context something injected — the hook's
	// additionalContext lands verbatim — and it is kept as tool output.
	var sawInjected bool
	for _, m := range s.Messages {
		if m.Role == "tool-output" && m.Text == "DEJA-REGISTRY-INJECTED-CONTEXT" {
			sawInjected = true
		}
	}
	if !sawInjected {
		t.Fatalf("injected context dropped: %s", joined)
	}
	// prompt_history: the is_shell rows worth keeping are commands; ls is
	// not, and the prompt itself must not duplicate the user turn.
	var shellCmds int
	for _, m := range s.Messages {
		if m.Role == "command" && strings.HasPrefix(m.Text, "$ ") {
			if strings.Contains(m.Text, "git log --oneline -5") || strings.Contains(m.Text, "make build") {
				shellCmds++
			}
			if strings.Contains(m.Text, "ls -la") {
				t.Fatalf("trivial shell row indexed: %q", m.Text)
			}
		}
	}
	if shellCmds != 2 {
		t.Fatalf("prompt_history commands = %d, want 2: %s", shellCmds, joined)
	}
}

func TestParseDevinDBReadsSideChainsAsSubagents(t *testing.T) {
	devinHome(t)
	db, _ := devinTestDB(t, devinFixtureSQL(t))
	sessions, err := ParseDevinDB(db)
	if err != nil {
		t.Fatal(err)
	}
	var parent, worker, rewind int
	for i := range sessions {
		switch {
		case sessions[i].ID == "registry-devin-sub":
			parent = i
		case sessions[i].ID == "registry-devin-sub:ag-worker":
			worker = i
		case strings.HasPrefix(sessions[i].ID, "registry-devin-sub:"):
			rewind = i
		}
	}
	if sessions[parent].Kind != "" || sessions[parent].Parent != "" {
		t.Fatalf("main chain = %#v", sessions[parent])
	}
	// A subagent_heads row names the run: its sub-session takes the stable
	// agent id, not a message-derived one.
	w := sessions[worker]
	if w.Kind != "subagent" || w.Parent != "registry-devin-sub" || w.Harness != "devin" {
		t.Fatalf("subagent = %#v", w)
	}
	var sawWorker bool
	for _, m := range w.Messages {
		if m.Text == "registry-worker printed" {
			sawWorker = true
		}
	}
	if !sawWorker {
		t.Fatalf("worker chain has no assistant turn: %#v", w.Messages)
	}
	// The orphan chain no head row declares still surfaces through the leaf
	// walk — a rewind left it behind, and silent loss is not the trade.
	r := sessions[rewind]
	if r.Kind != "subagent" || r.Parent != "registry-devin-sub" {
		t.Fatalf("rewind chain = %#v", r)
	}
	var sawRewind bool
	for _, m := range r.Messages {
		if m.Text == "earlier draft of the answer" {
			sawRewind = true
		}
	}
	if !sawRewind {
		t.Fatalf("orphan chain dropped: %#v", r.Messages)
	}
	// The main chain must not carry the worker's rows, and vice versa.
	for _, m := range sessions[parent].Messages {
		if strings.Contains(m.Text, "registry-worker printed") {
			t.Fatalf("worker turn leaked into the parent chain")
		}
	}
}

func TestParseDevinDBSkipsHiddenSessions(t *testing.T) {
	devinHome(t)
	db, _ := devinTestDB(t, devinFixtureSQL(t))
	sessions, err := ParseDevinDB(db)
	if err != nil {
		t.Fatal(err)
	}
	for i := range sessions {
		if sessions[i].ID == "registry-devin-hidden" {
			t.Fatalf("hidden session returned: %#v", sessions[i])
		}
		if strings.Contains(sessionText(&sessions[i]), "must not be read") {
			t.Fatalf("hidden session's nodes leaked into %q", sessions[i].ID)
		}
	}
}

func TestParseDevinDBSinceFiltersStaleSessions(t *testing.T) {
	devinHome(t)
	db, _ := devinTestDB(t, devinFixtureSQL(t))
	// Between registry-devin's last activity and registry-devin-sub's. The
	// fresh session brings its subagent side chain with it.
	sessions, err := ParseDevinDBSince(db, time.Unix(1785600120, 0))
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 3 || sessions[0].ID != "registry-devin-sub" ||
		sessions[1].Parent != "registry-devin-sub" || sessions[2].Parent != "registry-devin-sub" {
		t.Fatalf("since-filtered sessions = %#v", sessions)
	}
	all, err := ParseDevinDBSince(db, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 4 {
		t.Fatalf("zero since = %d sessions, want 4", len(all))
	}
}

func TestParseDevinDBAttachesSummary(t *testing.T) {
	devinHome(t)
	db, dataDir := devinTestDB(t, devinFixtureSQL(t))
	summaries := filepath.Join(dataDir, "summaries")
	if err := os.MkdirAll(summaries, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(summaries, "registry-devin.md"), []byte("# Session summary\n\ncompaction happened\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	sessions, err := ParseDevinDB(db)
	if err != nil {
		t.Fatal(err)
	}
	s := findDevinSession(sessions, "registry-devin")
	var saw bool
	for _, m := range s.Messages {
		if m.Role == "summary" && strings.Contains(m.Text, "compaction happened") {
			saw = true
		}
	}
	if !saw {
		t.Fatalf("summary file not attached: %#v", s.Messages)
	}
}

func TestParseDevinDBMissingTablesErrors(t *testing.T) {
	devinHome(t)
	// devin creates its tables lazily, so a store without them is an empty
	// store — no sessions, no error.
	db, _ := devinTestDB(t, "create table unrelated(x);")
	sessions, err := ParseDevinDB(db)
	if err != nil || len(sessions) != 0 {
		t.Fatalf("table-less store = %v, %#v", err, sessions)
	}
	// A store that has sessions but not message_nodes is a schema that
	// changed, and that failure is reported.
	db2, _ := devinTestDB(t, "create table sessions(id text, working_directory text, title text, created_at integer, last_activity_at integer, main_chain_id integer, hidden integer not null default 0); insert into sessions values('s','/w','t',1,2,null,0);")
	if _, err := ParseDevinDB(db2); err == nil {
		t.Fatal("a store missing message_nodes parsed without complaint")
	}
}

func TestDevinFilesHonoursEnvAndDiscovers(t *testing.T) {
	devinHome(t)
	if got := DevinSessionsDB(); got != filepath.Join(devinDataDir(), "cli", "sessions.db") {
		t.Fatalf("default store = %q", got)
	}
	if DevinFiles() != nil {
		t.Fatal("empty home still reports stores")
	}
	db, _ := devinTestDB(t, "create table sessions(id text);")
	t.Setenv("DEJA_DEVIN_DB", db)
	files := DevinFiles()
	if len(files) != 1 || files[0] != db {
		t.Fatalf("DEJA_DEVIN_DB discovery = %#v", files)
	}
	if !devinDBMatch(db) {
		t.Fatalf("matcher refuses the env store")
	}
	// The legacy name is read beside the canonical one.
	legacy := filepath.Join(devinDataDir(), "cli", "cli_sessions.db")
	if err := os.MkdirAll(filepath.Dir(legacy), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_DEVIN_DB", "")
	cmd := exec.CommandContext(t.Context(), "sqlite3", legacy)
	cmd.Stdin = strings.NewReader("create table sessions(id text);")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("create legacy fixture: %v: %s", err, out)
	}
	files = DevinFiles()
	if len(files) != 1 || files[0] != legacy {
		t.Fatalf("legacy discovery = %#v", files)
	}
}

func TestDevinSessionDir(t *testing.T) {
	devinHome(t)
	db, _ := devinTestDB(t, devinFixtureSQL(t))
	if got := DevinSessionDir(db, "registry-devin"); got != "/w/api" {
		t.Fatalf("recorded dir = %q", got)
	}
	if got := DevinSessionDir(db, "nobody"); got != "" {
		t.Fatalf("missing session dir = %q", got)
	}
}

func TestLoadDevinReadsDiscoveredStores(t *testing.T) {
	devinHome(t)
	if got := LoadDevin(); got != nil {
		t.Fatalf("empty home LoadDevin = %#v, want nil", got)
	}
	db, _ := devinTestDB(t, devinFixtureSQL(t))
	t.Setenv("DEJA_DEVIN_DB", db)
	got := LoadDevin()
	if findDevinSession(got, "registry-devin") == nil {
		t.Fatalf("LoadDevin = %#v, want the fixture session", got)
	}
	// A store sqlite3 refuses is reported, not fatal: the pass still answers.
	t.Setenv("DEJA_DEVIN_DB", filepath.Join(t.TempDir(), "absent.db"))
	if got := LoadDevin(); got != nil {
		t.Fatalf("absent store LoadDevin = %#v, want nil", got)
	}
}

func TestDevinDataDirFollowsXDG(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows keeps its own root under APPDATA, not XDG")
	}
	home := devinHome(t)
	if got := devinDataDir(); got != filepath.Join(home, ".local", "share", "devin") {
		t.Fatalf("default data dir = %q", got)
	}
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, "xdg"))
	if got := devinDataDir(); got != filepath.Join(home, "xdg", "devin") {
		t.Fatalf("XDG_DATA_HOME ignored: %q", got)
	}
}

func TestDevinDataDirFollowsPlatformConventions(t *testing.T) {
	home := devinHome(t)
	for _, c := range []struct {
		name string
		goos string
		xdg  string
		app  string
		want string
	}{
		{"linux default", "linux", "", "", filepath.Join(home, ".local", "share", "devin")},
		{"linux xdg", "linux", filepath.Join(home, "xdg"), "", filepath.Join(home, "xdg", "devin")},
		{"macos xdg honoured", "darwin", filepath.Join(home, "xdg-mac"), "", filepath.Join(home, "xdg-mac", "devin")},
		{"windows appdata", "windows", "", filepath.Join(home, "custom-roaming"), filepath.Join(home, "custom-roaming", "devin")},
		{"windows fallback", "windows", "", "", filepath.Join(home, "AppData", "Roaming", "devin")},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Setenv("XDG_DATA_HOME", c.xdg)
			t.Setenv("APPDATA", c.app)
			if got := devinDataDirFor(c.goos); got != c.want {
				t.Errorf("devinDataDirFor(%q) = %q, want %q", c.goos, got, c.want)
			}
		})
	}
}

func TestDevinContentTextReadsStringsAndParts(t *testing.T) {
	if got := devinContentText("plain"); got != "plain" {
		t.Fatalf("string content = %q", got)
	}
	got := devinContentText([]any{
		map[string]any{"text": "first"},
		"not-a-part",
		map[string]any{"type": "tool_use"},
		map[string]any{"text": "second"},
	})
	if got != "first\nsecond" {
		t.Fatalf("content parts = %q", got)
	}
	if got := devinContentText(42); got != "" {
		t.Fatalf("unknown content = %q, want empty", got)
	}
}

func TestDevinExitCodeReadsTerminalMetadata(t *testing.T) {
	term := map[string]any{"exit": map[string]any{"exit_code": float64(7)}}
	msg := map[string]any{"metadata": map[string]any{"extensions": map[string]any{"chisel/terminal_output": term}}}
	if c, ok := devinExitCode(msg); !ok || c != 7 {
		t.Fatalf("exit code = %d, %v", c, ok)
	}
	term["exit"] = map[string]any{"exit_code": json.Number("3")}
	if c, ok := devinExitCode(msg); !ok || c != 3 {
		t.Fatalf("json.Number exit code = %d, %v", c, ok)
	}
	if _, ok := devinExitCode(map[string]any{}); ok {
		t.Fatal("metadata-less message reports an exit code")
	}
	if _, ok := devinExitCode(map[string]any{"metadata": map[string]any{}}); ok {
		t.Fatal("extension-less message reports an exit code")
	}
}

func TestDevinToolCallsKeepsNamedCalls(t *testing.T) {
	calls := devinToolCalls([]any{
		map[string]any{"name": "exec", "arguments": map[string]any{"command": "ls"}},
		map[string]any{"arguments": map[string]any{}},
		map[string]any{"name": "read"},
		"junk",
	})
	if len(calls) != 1 {
		t.Fatalf("tool calls = %#v", calls)
	}
	if got := devinToolCalls("not-a-list"); got != nil {
		t.Fatalf("non-list tool calls = %#v", got)
	}
}

func TestShortDevinID(t *testing.T) {
	if got := shortDevinID("abcdefghijkl"); got != "abcdefgh" {
		t.Fatalf("long id = %q", got)
	}
	if got := shortDevinID("abc"); got != "abc" {
		t.Fatalf("short id = %q", got)
	}
	if got := shortDevinID(""); got != "sub" {
		t.Fatalf("empty id = %q", got)
	}
}

func findDevinSession(ss []model.Session, id string) *model.Session {
	for i := range ss {
		if ss[i].ID == id {
			return &ss[i]
		}
	}
	return nil
}

// sessionText joins a session's messages for contains-style checks.
func sessionText(s *model.Session) string {
	var b strings.Builder
	for _, m := range s.Messages {
		b.WriteString(m.Text)
		b.WriteByte('\n')
	}
	return b.String()
}

func TestParseDevinDBReadsStoreWithoutHiddenColumn(t *testing.T) {
	devinHome(t)
	// Stores the first builds wrote have no `hidden` column; a query naming
	// it fails the whole read, so the schema is probed before it is named.
	db, _ := devinTestDB(t, `
create table sessions (
    id text primary key, working_directory text, title text,
    created_at integer, last_activity_at integer, main_chain_id integer
);
create table message_nodes (
    row_id integer primary key autoincrement,
    session_id text not null, node_id integer not null,
    parent_node_id integer, chat_message text not null,
    created_at integer, metadata text
);
insert into sessions values ('old-devin','/w','old schema store',1785600000,1785600010,1);
insert into message_nodes (session_id, node_id, parent_node_id, chat_message, created_at) values
('old-devin', 1, null, '{"message_id":"o-u","role":"user","content":"schema predates hidden"}', 1785600001);
`)
	sessions, err := ParseDevinDB(db)
	if err != nil {
		t.Fatal(err)
	}
	s := findDevinSession(sessions, "old-devin")
	if s == nil {
		t.Fatalf("old-schema store not read: %#v", sessions)
	}
	if !strings.Contains(sessionText(s), "schema predates hidden") {
		t.Fatalf("old-schema store dropped its chain: %#v", s.Messages)
	}
}

func TestParseDevinDBHonoursIndexToolOutput(t *testing.T) {
	devinHome(t)
	t.Setenv("DEJA_INDEX_TOOL_OUTPUT", "0")
	db, _ := devinTestDB(t, devinFixtureSQL(t))
	sessions, err := ParseDevinDB(db)
	if err != nil {
		t.Fatal(err)
	}
	s := findDevinSession(sessions, "registry-devin")
	if s == nil {
		t.Fatalf("registry-devin not in %#v", sessions)
	}
	// With tool output off, neither the tool result nor the unmarked system
	// context lands — the switch is one switch, not one per role.
	for _, m := range s.Messages {
		if m.Role == "tool-output" {
			t.Fatalf("tool output indexed with the flag off: %q", m.Text)
		}
	}
}

func TestDevinToolCallsReadsSerializedArguments(t *testing.T) {
	// Some builds persist arguments as the encoded JSON, not an object;
	// dropping the call loses its whole work record.
	calls := devinToolCalls([]any{
		map[string]any{"id": "c1", "name": "exec", "arguments": `{"command":"go test ./x"}`},
		map[string]any{"id": "c2", "name": "exec", "arguments": "not json"},
		map[string]any{"id": "c3", "name": "read", "arguments": map[string]any{"file_path": "/w/x.go"}},
	})
	if len(calls) != 2 {
		t.Fatalf("tool calls = %#v", calls)
	}
	in, _ := calls[0].(map[string]any)["input"].(map[string]any)
	if in["command"] != "go test ./x" {
		t.Fatalf("serialized arguments = %#v", calls[0])
	}
}

// A subagent_heads table that exists but reads wrong — a schema drifted, a
// locked page — cannot fail the session: the declared heads come back empty
// and the leaf heuristic still surfaces the side chains.
func TestParseDevinDBFallsBackWhenSubagentHeadsMisread(t *testing.T) {
	devinHome(t)
	db, _ := devinTestDB(t, `
create table sessions (
    id text primary key, working_directory text, backend_type text,
    model text, agent_mode text, created_at integer, last_activity_at integer,
    title text, main_chain_id integer, shell_last_seen_index integer,
    cogs_json text, workspace_dirs text, hidden integer not null default 0,
    metadata text
);
create table message_nodes (
    row_id integer primary key autoincrement, session_id text not null,
    node_id integer not null, parent_node_id integer,
    chat_message text not null, created_at integer, metadata text
);
create table subagent_heads (wrong_column text);
insert into sessions values (
    's1', '/w/api', 'cli', 'swe-2', 'normal', 1785600000, 1785600090,
    'a session', 2, 0, null, '["/w/api"]', 0, null
);
insert into message_nodes (session_id, node_id, parent_node_id, chat_message, created_at, metadata) values
    ('s1', 1, null, '{"message_id":"m1","role":"user","content":"the ask"}', 1785600001, '{}'),
    ('s1', 2, 1,    '{"message_id":"m2","role":"assistant","content":[{"type":"text","text":"the answer"}]}', 1785600002, '{}'),
    ('s1', 3, null, '{"message_id":"w1","role":"assistant","content":[{"type":"text","text":"a side chain nobody declared"}]}', 1785600003, '{}');
`)
	if heads := devinSubagentHeads(db, "s1", true); heads != nil {
		t.Fatalf("a mis-shaped subagent_heads must read empty, got %#v", heads)
	}
	sessions, err := ParseDevinDB(db)
	if err != nil {
		t.Fatal(err)
	}
	side := findDevinSession(sessions, "s1:w1")
	if side == nil || !strings.Contains(side.Messages[0].Text, "a side chain nobody declared") {
		t.Fatalf("the undeclared chain did not fall back to the leaf heuristic: %#v", sessions)
	}
}

// devinStoreDDL is the store schema, copied from
// fixtures/registry/devin/devin.sql, for tests that build their own rows.
const devinStoreDDL = `
create table sessions (
    id text primary key,
    working_directory text,
    backend_type text,
    model text,
    agent_mode text,
    created_at integer,
    last_activity_at integer,
    title text,
    main_chain_id integer,
    shell_last_seen_index integer,
    cogs_json text,
    workspace_dirs text,
    hidden integer not null default 0,
    metadata text
);
create table message_nodes (
    row_id integer primary key autoincrement,
    session_id text not null,
    node_id integer not null,
    parent_node_id integer,
    chat_message text not null,
    created_at integer,
    metadata text
);
create table subagent_heads (
    session_id text not null,
    agent_id text not null,
    chain_node_id integer not null,
    updated_at integer not null,
    primary key (session_id, agent_id)
);
create table prompt_history (
    id integer primary key autoincrement,
    content text,
    timestamp integer,
    session_id text,
    is_shell integer not null default 0
);
`

// A branch off the trunk — an edited ask forking at a node the main chain
// already carries — is one subagent session holding only its own turns, not
// a second copy of the shared prefix keyed on the trunk's root.
func TestParseDevinDBBranchOffTrunkKeepsOnlySuffix(t *testing.T) {
	devinHome(t)
	db, _ := devinTestDB(t, devinStoreDDL+`
insert into sessions values (
    'b1', '/w/api', 'cli', 'swe-2', 'normal', 1785600000, 1785600100,
    'branched session', 5, 0, null, '["/w/api"]', 0, null
);
insert into message_nodes (session_id, node_id, parent_node_id, chat_message, created_at, metadata) values
    ('b1', 1, null, '{"message_id":"u1","role":"user","content":"trunk ask"}', 1785600001, '{}'),
    ('b1', 2, 1,    '{"message_id":"a2","role":"assistant","content":[{"type":"text","text":"trunk answer"}]}', 1785600002, '{}'),
    ('b1', 3, 2,    '{"message_id":"u3","role":"user","content":"branch ask"}', 1785600003, '{}'),
    ('b1', 4, 3,    '{"message_id":"a4","role":"assistant","content":[{"type":"text","text":"branch answer"}]}', 1785600004, '{}'),
    ('b1', 5, 2,    '{"message_id":"u5","role":"user","content":"edited ask a"}', 1785600005, '{}'),
    ('b1', 6, 2,    '{"message_id":"u6","role":"user","content":"edited ask b"}', 1785600006, '{}');
`)
	sessions, err := ParseDevinDB(db)
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 3 {
		t.Fatalf("sessions = %#v", sessions)
	}
	main := findDevinSession(sessions, "b1")
	if main == nil {
		t.Fatalf("main session missing: %#v", sessions)
	}
	mainText := sessionText(main)
	for _, want := range []string{"trunk ask", "trunk answer", "edited ask a"} {
		if !strings.Contains(mainText, want) {
			t.Fatalf("main chain missing %q: %s", want, mainText)
		}
	}
	if strings.Contains(mainText, "branch ask") {
		t.Fatalf("branch turns leaked into the main chain: %s", mainText)
	}
	branch := findDevinSession(sessions, "b1:"+shortDevinID("u3"))
	edited := findDevinSession(sessions, "b1:"+shortDevinID("u6"))
	if branch == nil || edited == nil || branch.ID == edited.ID {
		t.Fatalf("subagent sessions = %#v", sessions)
	}
	for _, sub := range []*model.Session{branch, edited} {
		if sub.Kind != "subagent" || sub.Parent != "b1" {
			t.Fatalf("subagent = %#v", sub)
		}
		text := sessionText(sub)
		if strings.Contains(text, "trunk ask") || strings.Contains(text, "trunk answer") {
			t.Fatalf("%s repeats the shared prefix: %s", sub.ID, text)
		}
	}
	branchText, editedText := sessionText(branch), sessionText(edited)
	if !strings.Contains(branchText, "branch ask") || !strings.Contains(branchText, "branch answer") {
		t.Fatalf("branch subagent lost its own turns: %s", branchText)
	}
	if !strings.Contains(editedText, "edited ask b") {
		t.Fatalf("edited subagent lost its own turn: %s", editedText)
	}
}

// Many branches off one trunk each become their own subagent session under
// their own id — keying on the chain root collapses every one of them onto
// the trunk's first message.
func TestParseDevinDBManyBranchesGetDistinctIDs(t *testing.T) {
	devinHome(t)
	var b strings.Builder
	b.WriteString(devinStoreDDL)
	b.WriteString(`insert into sessions values (
    'big', '/w', 'cli', 'swe-2', 'normal', 1785600000, 1785700000,
    'many branches', 2000, 0, null, '["/w"]', 0, null
);`)
	var rows []string
	for i := 1; i <= 2000; i++ {
		role, parent := "user", "null"
		if i%2 == 0 {
			role = "assistant"
		}
		if i > 1 {
			parent = strconv.Itoa(i - 1)
		}
		rows = append(rows, fmt.Sprintf(
			`('big',%d,%s,'{"message_id":"m%d","role":%q,"content":"trunk %d"}',%d,'{}')`,
			i, parent, i, role, i, 1785600000+i))
	}
	for i := 1; i <= 1000; i++ {
		node := 2000 + i
		rows = append(rows, fmt.Sprintf(
			`('big',%d,%d,'{"message_id":"b%d","role":"user","content":"branch ask %d"}',%d,'{}')`,
			node, i, node, i, 1785700000+i))
	}
	b.WriteString("\ninsert into message_nodes (session_id, node_id, parent_node_id, chat_message, created_at, metadata) values\n")
	b.WriteString(strings.Join(rows, ",\n"))
	b.WriteString(";\n")
	db, _ := devinTestDB(t, b.String())
	sessions, err := ParseDevinDB(db)
	if err != nil {
		t.Fatal(err)
	}
	ids := map[string]bool{}
	subs := 0
	for i := range sessions {
		s := &sessions[i]
		if s.Kind != "subagent" {
			continue
		}
		subs++
		if ids[s.ID] {
			t.Fatalf("subagent id %q is not unique", s.ID)
		}
		ids[s.ID] = true
		if len(s.Messages) != 1 {
			t.Fatalf("%s carries %d messages — the shared trunk prefix repeated: %#v", s.ID, len(s.Messages), s.Messages)
		}
	}
	if subs != 1000 {
		t.Fatalf("subagent sessions = %d, want 1000", subs)
	}
}

// What a pass reported survives a manifest write in the middle of it (#5110).
func TestDiagSaveRestoreSurvivesSnapshot(t *testing.T) {
	DiagSnapshot()
	t.Cleanup(func() { DiagSnapshot() })
	diagMalformedLine("bad.jsonl")
	diagUnusableRecord("store.db", "r1", "unknown data_type brotli")
	diagNoZstd("cold.jsonl.zst")
	saved := DiagSave()
	DiagSnapshot()
	if len(DiagMalformedCounts()) != 0 || len(DiagNoZstdPaths()) != 0 {
		t.Fatal("snapshot kept the counters")
	}
	DiagRestore(saved)
	if got := DiagMalformedCounts(); got["bad.jsonl"] != 1 || got["store.db"] != 1 {
		t.Fatalf("malformed after restore = %v", got)
	}
	if DiagUnusableRecords()["store.db"]["r1"] == "" || DiagReasons()["store.db"] == "" {
		t.Fatal("unusable record not restored")
	}
	if !DiagNoZstdPaths()["cold.jsonl.zst"] {
		t.Fatal("zstd path not restored")
	}
}

// An edit span is recorded as "path\nspan" and read back by splitting on the
// first newline, so a path carrying one puts the rest of itself at the head of
// the span — and restore hands that back as the exact bytes that stopped
// existing (#2042). The format cannot express such a path, so the span is not
// recorded rather than recorded wrong.
func TestAnEditSpanIsNotRecordedForAPathTheFormatCannotHold(t *testing.T) {
	t.Setenv("DEJA_INDEX_EDITS", "1")
	content := func(path string) any {
		var v any
		raw := `[{"type":"tool_use","name":"Edit","input":` +
			`{"file_path":` + quote(t, path) + `,"old_string":"size = 20","new_string":"size = 40"}}]`
		if err := json.Unmarshal([]byte(raw), &v); err != nil {
			t.Fatal(err)
		}
		return v
	}

	// The premise: an ordinary path records the span, and the span is the old
	// text alone.
	got := editSpansFromContent(content("/tmp/app/pool.go"))
	if len(got) != 1 {
		t.Fatalf("an ordinary edit recorded %d spans, so this measures nothing", len(got))
	}
	if recorded, body, _ := strings.Cut(got[0], "\n"); recorded != "/tmp/app/pool.go" || body != "size = 20" {
		t.Fatalf("the ordinary edit records %q / %q", recorded, body)
	}

	for _, path := range []string{
		"/tmp/app/pool.go\ndeja forget --all",
		"/tmp/app/pool.go\rrm -rf /",
	} {
		if got := editSpansFromContent(content(path)); len(got) != 0 {
			_, body, _ := strings.Cut(got[0], "\n")
			t.Errorf("a path the record cannot hold produced a span whose body is %q", body)
		}
		// The parser that actually runs for claude has to agree with the
		// reference one — that is what the differential test in #502 rests on.
		raw := `[{"type":"tool_use","name":"Edit","input":` +
			`{"file_path":` + quote(t, path) + `,"old_string":"size = 20","new_string":"size = 40"}}]`
		if got := claudeEditSpans([]byte(raw)); len(got) != 0 {
			_, body, _ := strings.Cut(got[0], "\n")
			t.Errorf("the fast parser recorded a span whose body is %q", body)
		}
	}
}

func quote(t *testing.T, s string) string {
	t.Helper()
	b, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// The files a session touched are recorded one per line and split back apart by
// the index, so the same newline that corrupted a span makes a session claim it
// touched a file it never opened — which reaches the files listing, blame, and
// the project the session is filed under (#2042).
func TestATouchedPathTheRecordCannotHoldIsNotRecorded(t *testing.T) {
	t.Setenv("DEJA_INDEX_TOOL_PATHS", "1")
	content := func(path string) (any, []byte) {
		raw := `[{"type":"tool_use","name":"Read","input":{"file_path":` + quote(t, path) + `}}]`
		var v any
		if err := json.Unmarshal([]byte(raw), &v); err != nil {
			t.Fatal(err)
		}
		return v, []byte(raw)
	}

	// The premise: an ordinary path is recorded, by both parsers.
	ref, raw := content("/tmp/app/pool.go")
	if got := toolPathsFromContent(ref); got != "/tmp/app/pool.go" {
		t.Fatalf("an ordinary read records %q, so this measures nothing", got)
	}
	if got := claudeToolPaths(raw); got != "/tmp/app/pool.go" {
		t.Fatalf("the fast parser records %q for an ordinary read", got)
	}

	for _, path := range []string{
		"/tmp/app/pool.go\n/etc/passwd",
		"/tmp/app/pool.go\r/etc/passwd",
	} {
		ref, raw := content(path)
		if got := toolPathsFromContent(ref); got != "" {
			t.Errorf("a path the record cannot hold was recorded as %q", got)
		}
		if got := claudeToolPaths(raw); got != "" {
			t.Errorf("the fast parser recorded %q", got)
		}
	}
}

// encodedFixtureSuffix is a path segment nobody can have on disk, for fixtures
// that mean to exercise the dash fallback in decodeProjectBase.
//
// The decoder resolves an encoded project name against the filesystem on
// purpose — that is what recovers `deja-vu` from `-Users-me-deja-vu` instead of
// calling it `deja/vu`. So a fixture that encodes a plausible path is asserting
// the fallback only for as long as nobody creates that path: two tests encoded
// `/tmp/deja-vu` and failed on a contributor's machine twice, on unmodified
// main, because they had a checkout there (#3512).
func encodedFixtureSuffix(t *testing.T) string {
	t.Helper()
	var b [6]byte
	if _, err := rand.Read(b[:]); err != nil {
		t.Fatal(err)
	}
	// Hex, so the segment survives the split on "-" as one piece.
	return "fx" + hex.EncodeToString(b[:])
}

// encodeProjectPath spells a path the way a harness stores it as a directory
// name. On unix that is every separator replaced by a dash. On windows the
// drive colon goes the same way — `C:\Users\me\app` is stored `C--Users-me-app`
// — and a fixture that replaced separators only left `C:-Users-…`, which
// resolveEncodedPath cannot resolve and the decoder then read through its dash
// fallback. The test below asserts the resolving branch, so on windows it was
// asserting the wrong one, and main went red there.
func encodeProjectPath(path string) string {
	encoded := strings.ReplaceAll(path, string(filepath.Separator), "-")
	if vol := filepath.VolumeName(path); strings.HasSuffix(vol, ":") {
		encoded = strings.Replace(encoded, vol, strings.TrimSuffix(vol, ":")+"-", 1)
	}
	return encoded
}

// The other side of the same rule, pinned on a directory the test owns rather
// than on the absence of one: an encoded name that does resolve keeps the
// hyphen the encoding lost.
func TestAnEncodedProjectThatResolvesKeepsItsHyphen(t *testing.T) {
	root := t.TempDir()
	resolved, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	project := filepath.Join(resolved, "deja-vu")
	if err := os.MkdirAll(project, 0o755); err != nil {
		t.Fatal(err)
	}

	encoded := encodeProjectPath(project)
	if got := decodeProjectBase(encoded); got != filepath.Base(resolved)+"/deja-vu" {
		t.Errorf("decodeProjectBase(%q) = %q, want the hyphen kept under %q",
			encoded, got, filepath.Base(resolved))
	}

	// And with the directory gone, the same name falls back to the dash rule —
	// which is the branch the fixtures elsewhere are asserting.
	if err := os.RemoveAll(project); err != nil {
		t.Fatal(err)
	}
	if got := decodeProjectBase(encoded); got != "deja/vu" {
		t.Errorf("with the directory gone, decodeProjectBase(%q) = %q, want deja/vu", encoded, got)
	}
}

// Each harness that documents a relocation variable gets the same contract:
// the upstream variable moves the default, DEJA_* still wins.
func TestUpstreamHomeVariables(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	t.Run("codex CODEX_HOME", func(t *testing.T) {
		t.Setenv("DEJA_CODEX_ROOT", "")
		t.Setenv("CODEX_HOME", filepath.Join(home, "codex-profile"))
		if got := CodexRoot(); got != filepath.Join(home, "codex-profile") {
			t.Fatalf("CodexRoot=%q", got)
		}
		t.Setenv("DEJA_CODEX_ROOT", filepath.Join(home, "override"))
		if got := CodexRoot(); got != filepath.Join(home, "override") {
			t.Fatalf("DEJA_CODEX_ROOT must win, got %q", got)
		}
	})

	t.Run("gemini GEMINI_CLI_HOME appends .gemini", func(t *testing.T) {
		t.Setenv("DEJA_GEMINI_ROOT", "")
		t.Setenv("GEMINI_CLI_HOME", filepath.Join(home, "ghome"))
		if got := GeminiRoot(); got != filepath.Join(home, "ghome", ".gemini") {
			t.Fatalf("GeminiRoot=%q", got)
		}
		t.Setenv("DEJA_GEMINI_ROOT", filepath.Join(home, "gr"))
		if got := GeminiRoot(); got != filepath.Join(home, "gr") {
			t.Fatalf("DEJA_GEMINI_ROOT must win, got %q", got)
		}
	})

	t.Run("cursor CURSOR_CONFIG_DIR", func(t *testing.T) {
		t.Setenv("DEJA_CURSOR_CLI_ROOT", "")
		t.Setenv("CURSOR_CONFIG_DIR", filepath.Join(home, "cursor-cfg"))
		if got := CursorCLIRoot(); got != filepath.Join(home, "cursor-cfg") {
			t.Fatalf("CursorCLIRoot=%q", got)
		}
	})

	t.Run("opencode XDG_DATA_HOME", func(t *testing.T) {
		t.Setenv("DEJA_OPENCODE_DB", "")
		t.Setenv("OPENCODE_DB", "")
		t.Setenv("XDG_DATA_HOME", filepath.Join(home, "xdg"))
		got := OpencodeDB()
		want := filepath.Join(home, "xdg", "opencode", "opencode.db")
		if got != want {
			t.Fatalf("OpencodeDB=%q want %q", got, want)
		}
	})

	t.Run("aider AIDER_CHAT_HISTORY_FILE", func(t *testing.T) {
		t.Setenv("DEJA_AIDER_ROOTS", "")
		hist := filepath.Join(home, "elsewhere", "history.md")
		if err := os.MkdirAll(filepath.Dir(hist), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(hist, []byte("# aider chat started at 2026-01-01 00:00:00\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		t.Setenv("AIDER_CHAT_HISTORY_FILE", hist)
		found := false
		for _, f := range AiderFiles() {
			if f == hist {
				found = true
			}
		}
		if !found {
			t.Fatalf("AiderFiles missed AIDER_CHAT_HISTORY_FILE: %v", AiderFiles())
		}
	})

	t.Run("qwen DEJA_QWEN_ROOT", func(t *testing.T) {
		t.Setenv("DEJA_QWEN_ROOT", filepath.Join(home, "qwen-read"))
		if got := QwenRoot(); got != filepath.Join(home, "qwen-read") {
			t.Fatalf("QwenRoot=%q", got)
		}
		if got := QwenConfigDir(); got != filepath.Join(home, ".qwen") {
			t.Fatalf("QwenConfigDir=%q", got)
		}
	})

	t.Run("gigacode GIGACODE_HOME and DEJA_GIGACODE_ROOT", func(t *testing.T) {
		t.Setenv("GIGACODE_HOME", filepath.Join(home, "gc-home"))
		t.Setenv("DEJA_GIGACODE_ROOT", "")
		if got := GigaCodeConfigDir(); got != filepath.Join(home, "gc-home") {
			t.Fatalf("GigaCodeConfigDir=%q", got)
		}
		if got := GigaCodeRoot(); got != filepath.Join(home, "gc-home") {
			t.Fatalf("GigaCodeRoot=%q", got)
		}
		t.Setenv("DEJA_GIGACODE_ROOT", filepath.Join(home, "gc-read"))
		if got := GigaCodeRoot(); got != filepath.Join(home, "gc-read") {
			t.Fatalf("GigaCodeRoot=%q", got)
		}
		if got := GigaCodeConfigDir(); got != filepath.Join(home, "gc-home") {
			t.Fatalf("GigaCodeConfigDir=%q", got)
		}
	})
}

func TestDejaHomeRootsDiscoversReadablePeerHomes(t *testing.T) {
	home := t.TempDir()
	peer := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("DEJA_CLAUDE_ROOT", "")
	t.Setenv("DEJA_CODEX_ROOT", "")
	t.Setenv("DEJA_GEMINI_ROOT", "")
	t.Setenv("DEJA_HOME_ROOTS", peer+string(os.PathListSeparator)+"relative/skip")

	peerClaude := filepath.Join(peer, ".claude", "projects")
	peerCodex := filepath.Join(peer, ".codex")
	peerGemini := filepath.Join(peer, ".gemini", "tmp")
	for _, d := range []string{peerClaude, peerCodex, peerGemini} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}

	if got := HomeRoots(); len(got) != 2 || got[1] != filepath.Clean(peer) {
		t.Fatalf("HomeRoots() = %v, want [%s %s]", got, home, peer)
	}
	if !UnderClaudeRoot(filepath.Join(peerClaude, "proj", "s1.jsonl")) {
		t.Fatalf("UnderClaudeRoot should match peer home transcript")
	}
	if !UnderGeminiRoot(filepath.Join(peerGemini, "proj", "chats", "c1.json")) {
		t.Fatalf("UnderGeminiRoot should match peer home chat")
	}
	foundCodex := false
	for _, r := range CodexRoots() {
		if r == filepath.Clean(peerCodex) {
			foundCodex = true
		}
	}
	if !foundCodex {
		t.Fatalf("CodexRoots() = %v, missing %s", CodexRoots(), peerCodex)
	}
}

// unixGuess is the front door for every numeric timestamp deja reads, and it
// knew two units: anything larger than milliseconds was read as milliseconds,
// so a microsecond store was dated to the year 58136 and a nanosecond one to
// the year 56 million. A session dated in the future takes the top of every
// recency surface and keeps it (#2063), so one such store pins them all (#2102).
func TestAStampIsReadInWhicheverUnitItWasWritten(t *testing.T) {
	when := time.Date(2026, 3, 2, 12, 0, 0, 0, time.UTC)
	for _, u := range []struct {
		name string
		n    int64
	}{
		{"seconds", when.Unix()},
		{"milliseconds", when.UnixMilli()},
		{"microseconds", when.UnixMicro()},
		{"nanoseconds", when.UnixNano()},
	} {
		got := parseTimeAny(json.Number(strconv.FormatInt(u.n, 10)))
		if !got.UTC().Equal(when) {
			t.Errorf("%s (%d) read as %s, want %s", u.name, u.n, got.UTC().Format(time.RFC3339), when.Format(time.RFC3339))
		}
	}
}

// The bands are judged by magnitude, so the edges are worth pinning: a value
// deja cannot place is better read as the unit that puts it in a plausible year
// than as one that puts it past the year 5000.
func TestTheUnitBandsHoldAtTheirEdges(t *testing.T) {
	for _, c := range []struct {
		name string
		n    int64
		want time.Time
	}{
		{"an old seconds stamp", 915148800, time.Date(1999, 1, 1, 0, 0, 0, 0, time.UTC)},
		{"an old millisecond stamp", 915148800000, time.Date(1999, 1, 1, 0, 0, 0, 0, time.UTC)},
		{"a 2001 millisecond stamp", 1_000_000_000_000, time.Date(2001, 9, 9, 1, 46, 40, 0, time.UTC)},
		{"zero is no stamp", 0, time.Time{}},
		{"a negative is no stamp", -1, time.Time{}},
		// The largest thing an int64 can carry: nanoseconds run out in 2262,
		// and the call takes it rather than panicking.
		{"the largest int64", math.MaxInt64, time.Unix(0, math.MaxInt64)},
	} {
		got := parseTimeAny(json.Number(strconv.FormatInt(c.n, 10)))
		if !got.UTC().Equal(c.want.UTC()) {
			t.Errorf("%s (%d) read as %s, want %s", c.name, c.n, got.UTC().Format(time.RFC3339), c.want.UTC().Format(time.RFC3339))
		}
	}
}

// Kilo Code writes two spaces after "terminal", an Antigravity step may lay
// its output out as Stdout:/Stderr:, and a planner row may ask for a command
// too trivial to record (#4530).
func TestExitFramingKiloSpacingAndAntigravityAttribution(t *testing.T) {
	runExitRows(t, []exitRow{
		// kilocode-legacy executeCommandTool: `Command executed in terminal ${workingDirInfo}`
		// where workingDirInfo starts with a space.
		{"kilo double space", "kilocode-task", rooExitTask("Command executed in terminal  within working directory '/tmp/proj'. Command execution was not successful, inspect the cause and adjust as needed.\nExit code: 1\nOutput:\nFAIL"), "$ go test ./...  → exit 1"},
		{"antigravity stdout shape without exit header", "antigravity", antigravityExitTranscript("Created At: 2026-09-30T10:00:05Z\n\nStdout:\nThe command exited with code 1.\n"), "$ go test ./..."},
	})
	// A planner asking for two commands, one too trivial to record: the step
	// for the trivial one must not stamp the other.
	t.Run("antigravity unrecorded sibling call", func(t *testing.T) {
		dir := t.TempDir()
		p := antigravityExitTranscript("Created At: 2026-09-30T10:00:05Z\n\nThe command exited with code 0.\nOutput:\nM retry.go", "Created At: 2026-09-30T10:00:06Z\n\nThe command exited with code 1.\nOutput:\nFAIL")(t, dir)
		b, _ := os.ReadFile(p)
		body := strings.Replace(string(b), `"tool_calls":[{"name":"run_command"`, `"tool_calls":[{"name":"run_command","args":{"CommandLine":"\"ls\"","Cwd":"\"/tmp/proj\""}},{"name":"run_command"`, 1)
		writeExitFixture(t, p, body)
		got := commandsOf(parseKindForTest(t, "antigravity", p))
		for _, c := range got {
			if c == "$ go test ./...  → exit 0" {
				t.Fatalf("commands = %q: ls's exit landed on go test", got)
			}
		}
	})
}

// exitRow is one client's store holding a single `go test ./...` and the
// result that client writes for it, read through the registry kind.
type exitRow struct {
	name, kind string
	fixture    func(t *testing.T, dir string) string
	want       string
}

func writeExitFixture(t *testing.T, p, body string) string {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func commandsOf(ss []model.Session) []string {
	var out []string
	for _, s := range ss {
		for _, m := range s.Messages {
			if m.Role == RoleCommand {
				out = append(out, m.Text)
			}
		}
	}
	return out
}

func runExitRows(t *testing.T, rows []exitRow) {
	t.Helper()
	for _, r := range rows {
		t.Run(r.name, func(t *testing.T) {
			got := commandsOf(parseKindForTest(t, r.kind, r.fixture(t, t.TempDir())))
			if len(got) != 1 || got[0] != r.want {
				t.Errorf("commands = %q, want [%q]", got, r.want)
			}
		})
	}
}

// rooTask is a Roo (or Kilo Code) task directory whose one execute_command
// came back with result.
func rooExitTask(result string) func(*testing.T, string) string {
	return func(t *testing.T, dir string) string {
		t.Helper()
		task := filepath.Join(dir, "tasks", "1788845325718")
		writeExitFixture(t, filepath.Join(task, "history_item.json"), `{"id":"1788845325718","ts":1788845325718,"task":"fix the retry loop","workspace":"/tmp/proj"}`)
		return writeExitFixture(t, filepath.Join(task, "api_conversation_history.json"), `[
 {"role":"user","content":[{"type":"text","text":"<task>\nfix the retry loop\n</task>"}]},
 {"role":"assistant","content":[{"type":"tool_use","id":"t1","name":"execute_command","input":{"command":"go test ./...","cwd":"/tmp/proj"}}]},
 {"role":"user","content":[{"type":"tool_result","tool_use_id":"t1","content":`+vocabJSON(result)+`}]}]`)
	}
}

func continueExitStore(state string) func(*testing.T, string) string {
	return func(t *testing.T, dir string) string {
		t.Helper()
		return writeExitFixture(t, filepath.Join(dir, "sessions", "s1.json"), `{"sessionId":"s1","title":"fix the retry loop","workspaceDirectory":"/tmp/proj","history":[`+
			`{"message":{"role":"user","content":"fix the retry loop"},"contextItems":[]},`+
			`{"message":{"role":"assistant","content":"","toolCalls":[]},"contextItems":[],"toolCallStates":[`+state+`]}]}`)
	}
}

func ampExitThread(run string) func(*testing.T, string) string {
	return func(t *testing.T, dir string) string {
		t.Helper()
		return writeExitFixture(t, filepath.Join(dir, "T-0f3c.json"), `{"v":7,"id":"T-0f3c","created":1774950000000,"title":"Fix the retry loop","env":{"initial":{"trees":[{"uri":"file:///tmp/proj"}]}},"messages":[`+
			`{"role":"user","content":[{"type":"text","text":"fix the retry loop"}],"meta":{"sentAt":1774950001000}},`+
			`{"role":"assistant","content":[{"type":"tool_use","id":"toolu_01","name":"Bash","complete":true,"input":{"cmd":"go test ./...","cwd":"/tmp/proj"}}],"usage":{"timestamp":"2026-03-31T09:40:05Z"}},`+
			`{"role":"user","content":[{"type":"tool_result","toolUseID":"toolu_01","run":`+run+`}]}]}`)
	}
}

func antigravityExitTranscript(steps ...string) func(*testing.T, string) string {
	return func(t *testing.T, dir string) string {
		t.Helper()
		lines := `{"step_index":0,"source":"USER_EXPLICIT","type":"USER_INPUT","status":"DONE","created_at":"2026-09-30T10:00:00Z","content":"<USER_REQUEST>\nfix the retry loop\n</USER_REQUEST>"}` + "\n" +
			`{"step_index":1,"source":"MODEL","type":"PLANNER_RESPONSE","status":"DONE","created_at":"2026-09-30T10:00:05Z","content":"","tool_calls":[{"name":"run_command","args":{"CommandLine":"\"go test ./...\"","Cwd":"\"/tmp/proj\""}}]}` + "\n"
		var linesSb89 strings.Builder
		for _, s := range steps {
			linesSb89.WriteString(`{"step_index":2,"source":"MODEL","type":"GENERIC","status":"DONE","created_at":"2026-09-30T10:00:09Z","content":` + vocabJSON(s) + `}` + "\n")
		}
		lines += linesSb89.String()
		return writeExitFixture(t, filepath.Join(dir, "brain", "b0c1d2e3", ".system_generated", "logs", "transcript.jsonl"), lines)
	}
}

// Roo, Kilo Code, Continue, Amp and Antigravity each write how a command
// ended, in their own words, and the command record never carried it (#4530).
// A status is read only where the client frames it; output that merely says
// "exit code" leaves the command as it was.
func TestRooContinueAmpAntigravityKeepTheExitCode(t *testing.T) {
	const roo = "Command executed in terminal within working directory '/tmp/proj'. "
	cnBash := func(status, out string) string {
		return `{"toolCall":{"id":"c1","type":"function","function":{"name":"Bash","arguments":"{\"command\":\"go test ./...\"}"}},"status":"` + status + `","parsedArgs":{"command":"go test ./..."},"output":[{"name":"Tool Result","description":"Tool output","content":` + vocabJSON(out) + `}]}`
	}
	ideRun := func(status string) string {
		return `{"toolCall":{"id":"c1","type":"function","function":{"name":"run_terminal_command","arguments":"{\"command\":\"go test ./...\"}"}},"status":"done","parsedArgs":{"command":"go test ./...","waitForCompletion":true},"output":[{"name":"Terminal","description":"Terminal command output","content":"./retry.go:12:5: undefined: backoffJitter","status":` + vocabJSON(status) + `}]}`
	}
	runExitRows(t, []exitRow{
		{"roo failed", "roo", rooExitTask(roo + "Command execution was not successful, inspect the cause and adjust as needed.\nExit code: 1\nOutput:\n./retry.go:12:5: undefined: backoffJitter"), "$ go test ./...  → exit 1"},
		{"roo clean", "roo", rooExitTask(roo + "Exit code: 0\nOutput:\nok  \tproj\t0.01s"), "$ go test ./...  → exit 0"},
		{"roo persisted output", "roo", rooExitTask("Command executed in '/tmp/proj'. Command execution was not successful, inspect the cause and adjust as needed.\nExit code: 2\n\nOutput (2.1MB) persisted. Artifact ID: a1"), "$ go test ./...  → exit 2"},
		{"roo signal", "roo", rooExitTask(roo + "Process terminated by signal SIGKILL\nOutput:\n"), "$ go test ./..."},
		{"roo output naming a code", "roo", rooExitTask("Exit code: 1\nOutput:\nfine"), "$ go test ./..."},
		{"kilo code task", "kilocode-task", rooExitTask(roo + "Command execution was not successful, inspect the cause and adjust as needed.\nExit code: 1\nOutput:\nFAIL"), "$ go test ./...  → exit 1"},
		{"continue cn failed", "continue", continueExitStore(cnBash("errored", "Error executing tool Bash: Error (exit code 1): ./retry.go:12:5: undefined: backoffJitter")), "$ go test ./...  → exit 1"},
		// A cn Bash that exits non-zero with nothing on stderr resolves as
		// done, so done says nothing about the code.
		{"continue cn done", "continue", continueExitStore(cnBash("done", "ok  \tproj\t0.01s")), "$ go test ./..."},
		{"continue cn other error", "continue", continueExitStore(cnBash("errored", "Error executing tool Bash: spawn sh ENOENT")), "$ go test ./..."},
		{"continue ide failed", "continue", continueExitStore(ideRun("Command failed with exit code 1")), "$ go test ./...  → exit 1"},
		{"continue ide failed, exec path", "continue", continueExitStore(ideRun("Command failed with: Command failed with exit code 2")), "$ go test ./...  → exit 2"},
		{"continue ide completed", "continue", continueExitStore(ideRun("Command completed")), "$ go test ./..."},
		{"amp failed", "amp", ampExitThread(`{"status":"done","result":{"output":"./retry.go:12:5: undefined: backoffJitter","exitCode":1}}`), "$ go test ./...  → exit 1"},
		{"amp clean", "amp", ampExitThread(`{"status":"done","result":{"output":"ok","exitCode":0}}`), "$ go test ./...  → exit 0"},
		{"amp unknown", "amp", ampExitThread(`{"status":"done","result":{"output":"","exitCode":-1}}`), "$ go test ./..."},
		{"amp cancelled", "amp", ampExitThread(`{"status":"cancelled"}`), "$ go test ./..."},
		{"antigravity failed", "antigravity", antigravityExitTranscript("Created At: 2026-09-30T10:00:05Z\nCompleted At: 2026-09-30T10:00:09Z\n\nThe command exited with code 1.\nOutput:\n./retry.go:12:5: undefined: backoffJitter"), "$ go test ./...  → exit 1"},
		{"antigravity clean", "antigravity", antigravityExitTranscript("Created At: 2026-09-30T10:00:05Z\n\nThe command exited with code 0.\nOutput:\nok"), "$ go test ./...  → exit 0"},
		{"antigravity planner speaking first", "antigravity", func(t *testing.T, dir string) string {
			t.Helper()
			p := antigravityExitTranscript("The command exited with code 1.\nOutput:\nFAIL")(t, dir)
			b, _ := os.ReadFile(p)
			return writeExitFixture(t, p, strings.Replace(string(b), `"content":"","tool_calls"`, `"content":"Running the tests.","tool_calls"`, 1))
		}, "$ go test ./...  → exit 1"},
		{"antigravity output naming a code", "antigravity", antigravityExitTranscript("Created At: 2026-09-30T10:00:05Z\nOutput:\nThe command exited with code 1."), "$ go test ./..."},
	})
}

// crush.db keeps a call and its result as separate parts on separate rows, and
// the reader recorded each on its own: a failed bash lost the "Exit code N"
// its result ends with, and an edit crush refused left edit and wrote records
// of text the file never held (#4532).
func TestCrushJoinsACallToItsResult(t *testing.T) {
	const oldLoop = "\tfor {"
	edit := `{"file_path":"/tmp/proj/retry.go","old_string":"\tfor {","new_string":"\tfor attempt := 0; attempt < maxAttempts; attempt++ {"}`
	cases := []struct {
		id, tool, input, result string
		isErr                   bool
		want                    []string
	}{
		{"bash_fail", "bash", `{"command":"go vet ./retry"}`, "./retry.go:12:5: undefined: backoffJitter\nExit code 1\n\n<cwd>/tmp/proj</cwd>", false, []string{"command go vet ./retry  → exit 1"}},
		{"bash_ok", "bash", `{"command":"go test ./retry"}`, "ok  retry 0.01s\n\n<cwd>/tmp/proj</cwd>", false, []string{"command go test ./retry"}},
		// The code is crush's last line, not one quoted in the output.
		{"bash_quoting", "bash", `{"command":"go test ./retry"}`, "Exit code 1\nok  retry 0.01s\n\n<cwd>/tmp/proj</cwd>", false, []string{"command go test ./retry"}},
		{"edit_ok", "edit", edit, "Content replaced in file", false, []string{"files /tmp/proj/retry.go", "edit /tmp/proj/retry.go\n" + oldLoop, "wrote"}},
		{"edit_refused", "edit", edit, "you must read the file before editing it. Use the View tool first", true, []string{"files /tmp/proj/retry.go"}},
		{"edit_unanswered", "edit", edit, "", false, []string{"files /tmp/proj/retry.go", "edit /tmp/proj/retry.go\n" + oldLoop, "wrote"}},
	}
	sql := ""
	var sqlSb159 strings.Builder
	for i, c := range cases {
		sqlSb159.WriteString("insert into sessions values ('" + c.id + "',null,'retry',2,0,0,0.0," + itoa(1784282405+i*10) + "," + itoa(1784282400+i*10) + ",null,null);\n")
		sqlSb159.WriteString(crushInsert(t, "a_"+c.id, c.id, "assistant", int64(1784282400+i*10), []any{
			map[string]any{"type": "tool_call", "data": map[string]any{"id": "c_" + c.id, "name": c.tool, "input": c.input, "finished": true}}}))
		if c.result != "" {
			sqlSb159.WriteString(crushInsert(t, "t_"+c.id, c.id, "tool", int64(1784282401+i*10), []any{
				map[string]any{"type": "tool_result", "data": map[string]any{"tool_call_id": "c_" + c.id, "name": c.tool, "content": c.result, "is_error": c.isErr}}}))
		}
	}
	sql += sqlSb159.String()
	ss, err := ParseCrushDB(crushStore(t, "proj", sql))
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range cases {
		var got []string
		for _, s := range ss {
			if s.ID != c.id {
				continue
			}
			for _, m := range s.Messages {
				switch m.Role {
				case RoleToolOutput:
				case RoleWrote:
					got = append(got, "wrote")
				default:
					got = append(got, m.Role+" "+m.Text)
				}
			}
		}
		if strings.Join(got, "|") != strings.Join(c.want, "|") {
			t.Errorf("%s: records = %q, want %q", c.id, got, c.want)
		}
	}
}

// ZCode writes a failed Bash Claude's way, "Exit code N" as the first line of
// the output, and leaves metadata.exit unset, which is all the reader looked
// at (#4536). opencode's own parts, with metadata.exit, are read as before.
func TestZCodeFailedBashKeepsItsExitCode(t *testing.T) {
	part := func(id, output, meta string) string {
		return "insert into part values ('" + id + "','m_" + id + "','" + strings.ReplaceAll(`{"type":"tool","tool":"Bash","callID":"`+id+`","state":{"status":"completed","input":{"command":"go vet ./retry"},"output":`+vocabJSON(output)+meta+`,"time":{"start":1790000002000}}}`, "'", "''") + "');\n" +
			"insert into message values ('m_" + id + "','" + id + "',1790000001000,'{\"role\":\"assistant\",\"time\":{\"created\":1790000001000}}');\n" +
			"insert into session values ('" + id + "','/tmp/proj',1790000000000,1790000100000);\n"
	}
	db := vocabSQL(t, `create table session(id text primary key, directory text, time_created integer, time_updated integer);
create table message(id text, session_id text, time_created integer, data text);
create table part(id text, message_id text, data text);
`+part("failed", "Exit code 1\n./retry.go:12:5: undefined: backoffJitter", "")+
		part("clean", "ok  retry 0.01s", "")+
		part("quoting", "ok\nExit code 1", "")+
		part("meta", "Exit code 1\n./retry.go:12:5: undefined: backoffJitter", `,"metadata":{"exit":2}`))
	want := map[string][2]string{
		"failed":  {"$ go vet ./retry  → exit 1", "./retry.go:12:5: undefined: backoffJitter"},
		"clean":   {"$ go vet ./retry", "ok  retry 0.01s"},
		"quoting": {"$ go vet ./retry", "ok\nExit code 1"},
		"meta":    {"$ go vet ./retry  → exit 2", "Exit code 1\n./retry.go:12:5: undefined: backoffJitter"},
	}
	ss := parseKindForTest(t, "zcode-db", db)
	if len(ss) != len(want) {
		t.Fatalf("%d sessions, want %d", len(ss), len(want))
	}
	for _, s := range ss {
		got := [2]string{strings.Join(rolesOf(s, RoleCommand), "|"), strings.Join(rolesOf(s, RoleToolOutput), "|")}
		if got != want[s.ID] {
			t.Errorf("%s: command, output = %q, want %q", s.ID, got, want[s.ID])
		}
	}
}

// codeWhaleSaved is a saved CodeWhale session whose one bash call came back
// with result.
func codeWhaleSaved(result string) func(*testing.T, string) string {
	return func(t *testing.T, dir string) string {
		t.Helper()
		return writeExitFixture(t, filepath.Join(dir, "cw-1.json"), `{"schema_version":3,
"metadata":{"id":"cw-1","title":"fix the retry loop","created_at":"2026-10-01T10:00:00Z","updated_at":"2026-10-01T10:05:00Z","workspace":"/tmp/proj"},
"messages":[
 {"role":"user","content":[{"type":"text","text":"fix the retry loop"}]},
 {"role":"assistant","content":[{"type":"tool_use","id":"c1","name":"bash","input":{"command":"go test ./..."}}]},
 {"role":"user","content":[{"type":"tool_result","tool_use_id":"c1",`+result+`}]}]}`)
	}
}

// CodeWhale ends a failed bash result with "Command exited with code N" and
// marks it is_error (tools/shell.rs contract_bash_error_status); the reader
// never joined a result to its call (#4537).
func TestCodeWhaleFailedBashKeepsItsExitCode(t *testing.T) {
	runExitRows(t, []exitRow{
		{"failed", "codewhale", codeWhaleSaved(`"is_error":true,"content":"Error: --- FAIL: TestRetry (0.00s)\n    retry_test.go:12: retried 0 times\nFAIL\n\nCommand exited with code 1"`), "$ go test ./...  → exit 1"},
		{"clean", "codewhale", codeWhaleSaved(`"content":"ok  \tproj\t0.01s"`), "$ go test ./..."},
		{"timed out", "codewhale", codeWhaleSaved(`"is_error":true,"content":"Error: Command timed out after 120 seconds"`), "$ go test ./..."},
		{"not an error", "codewhale", codeWhaleSaved(`"content":"printed\nCommand exited with code 1"`), "$ go test ./..."},
	})
}

const commandCodeCall = `{"type":"message","id":"a1","timestamp":"2026-10-01T20:09:15.324Z","message":{"role":"assistant","content":[{"type":"tool_use","id":"call_1","name":"shell_command","input":{"command":"go test ./..."}}],"meta":{"source":"model"}}}` + "\n"

func commandCodeResult(text string) string {
	return `{"type":"message","id":"r1","timestamp":"2026-10-01T20:09:16.324Z","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"call_1","content":[{"type":"text","text":` + vocabJSON(text) + `}]}],"meta":{"source":"tool"}}}` + "\n"
}

func commandCodeExitStore(result string) func(*testing.T, string) string {
	return func(t *testing.T, dir string) string {
		t.Helper()
		return writeExitFixture(t, filepath.Join(dir, "-tmp-proj", "s1.jsonl"),
			`{"type":"session","version":3,"id":"s1","timestamp":"2026-10-01T20:09:14.839Z","cwd":"/tmp/proj"}`+"\n"+commandCodeCall+commandCodeResult(result))
	}
}

// Claw Code mirrors Claude Code's bash tool_result shapes, including
// returnCodeInterpretation strings inside JSON payloads.
func TestClawCodeFailedCommandKeepsItsExitCode(t *testing.T) {
	runExitRows(t, []exitRow{
		{"failed", "clawcode", clawCodeExitStore(`{"stdout":"--- FAIL: TestRetry (0.00s)\nFAIL","stderr":"","interrupted":false,"returnCodeInterpretation":"exit_code:1"}`, true), "$ go test ./...  → exit 1"},
		{"clean", "clawcode", clawCodeExitStore(`{"stdout":"ok  \tproj\t0.01s","stderr":"","interrupted":false}`, false), "$ go test ./...  → exit 0"},
		{"quoting", "clawcode", clawCodeExitStore(`{"stdout":"ok\nExit code: 1","stderr":"","interrupted":false}`, false), "$ go test ./...  → exit 0"},
	})
}

// Command Code opens a failed command's result with "Exit code: N", or "Exit
// code: N (<what it means>)" for a code it knows (formatShellCommandResult),
// and sets no is_error; the reader never joined a result to its call (#4539).
func TestCommandCodeFailedCommandKeepsItsExitCode(t *testing.T) {
	runExitRows(t, []exitRow{
		{"failed", "commandcode", commandCodeExitStore("Exit code: 1\n--- FAIL: TestRetry (0.00s)\nFAIL"), "$ go test ./...  → exit 1"},
		{"failed, explained", "commandcode", commandCodeExitStore("Exit code: 1 (No matches found)"), "$ go test ./...  → exit 1"},
		{"clean", "commandcode", commandCodeExitStore("ok  \tproj\t0.01s"), "$ go test ./..."},
		{"quoting", "commandcode", commandCodeExitStore("ok\nExit code: 1"), "$ go test ./..."},
	})
}

// A result answers the call before it, once. Command Code's ids read like
// call_1, and an id handed out again must not carry a later failure back to
// an earlier run that came back clean.
func TestAReusedCallIDStampsOnlyItsOwnRun(t *testing.T) {
	dir := t.TempDir()
	second := strings.ReplaceAll(strings.ReplaceAll(commandCodeCall, `"go test ./..."`, `"go vet ./..."`), `"id":"a1"`, `"id":"a2"`)
	p := writeExitFixture(t, filepath.Join(dir, "-tmp-proj", "s1.jsonl"),
		`{"type":"session","version":3,"id":"s1","timestamp":"2026-10-01T20:09:14.839Z","cwd":"/tmp/proj"}`+"\n"+
			commandCodeCall+commandCodeResult("ok  \tproj\t0.01s")+second+commandCodeResult("Exit code: 1\nvet: retry.go:3: unreachable code"))
	got := commandsOf(parseKindForTest(t, "commandcode", p))
	want := []string{"$ go test ./...", "$ go vet ./...  → exit 1"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("commands = %q, want %q", got, want)
	}
}

var failedClaudeTranscript = strings.Join([]string{
	`{"type":"user","sessionId":"c1","timestamp":"2026-10-01T10:00:00Z","cwd":"/tmp/proj","message":{"role":"user","content":"fix the retry loop"}}`,
	`{"type":"assistant","sessionId":"c1","timestamp":"2026-10-01T10:00:01Z","cwd":"/tmp/proj","message":{"role":"assistant","content":[{"type":"tool_use","id":"t1","name":"Bash","input":{"command":"go test ./..."}}]}}`,
	`{"type":"user","sessionId":"c1","timestamp":"2026-10-01T10:00:02Z","cwd":"/tmp/proj","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"t1","is_error":true,"content":"Exit code 1\n./retry.go:12:5: undefined: backoffJitter\nFAIL"}]}}`,
}, "\n") + "\n"

var failedPiTranscript = strings.Join([]string{
	`{"type":"session","version":3,"id":"pi-s1","timestamp":"2026-09-20T10:00:00.000Z","cwd":"/tmp/proj"}`,
	`{"type":"message","id":"u1","timestamp":"2026-09-20T10:00:00.000Z","message":{"role":"user","content":[{"type":"text","text":"fix the retry loop"}]}}`,
	`{"type":"message","id":"a1","timestamp":"2026-09-20T10:00:01.000Z","message":{"role":"assistant","content":[{"type":"toolCall","id":"c1","name":"bash","arguments":{"command":"go test ./..."}}]}}`,
	`{"type":"message","id":"r1","timestamp":"2026-09-20T10:00:02.000Z","message":{"role":"toolResult","toolCallId":"c1","toolName":"bash","content":[{"type":"text","text":"./retry.go:12:5: undefined: backoffJitter\n\nCommand exited with code 1"}],"details":{},"isError":true}}`,
}, "\n") + "\n"

// A command that failed carries its exit code in every harness that writes it
// down, each in its own place: a status line before or after the output, a
// field beside it, or a result filed apart from the call. Each row is one call
// to `go test ./...` and the failing result that harness writes for it
// (#4487, #4501, #4496, #4502, #4505, #4507, #4493).
func TestAFailedCommandKeepsItsExitCode(t *testing.T) {
	const want = "$ go test ./...  → exit 1"
	write := func(t *testing.T, p, body string) string {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	file := func(name, body string) func(*testing.T, string) string {
		return func(t *testing.T, dir string) string {
			t.Helper()
			return write(t, filepath.Join(dir, name), body)
		}
	}

	rows := []struct {
		name, kind string
		fixture    func(*testing.T, string) string
		parse      func(string) ([]model.Session, error)
	}{
		{name: "claude", kind: "claude", fixture: file("c1.jsonl", failedClaudeTranscript)},
		{name: "claude reference parser", fixture: file("c1.jsonl", failedClaudeTranscript), parse: func(p string) ([]model.Session, error) { return parseClaudeGenericFromOffset(p, 0) }},
		{name: "cherrystudio", kind: "cherrystudio", fixture: file("c1.jsonl", failedClaudeTranscript)},
		{name: "pi", kind: "pi", fixture: file("2026-09-20T10-00-00-000Z_s1.jsonl", failedPiTranscript)},
		{name: "omp", kind: "omp", fixture: file("2026-09-20T10-00-00-000Z_s1.jsonl", failedPiTranscript)},
		{name: "gjc", kind: "gjc", fixture: file("2026-09-20T10-00-00-000Z_s1.jsonl", failedPiTranscript)},
		{name: "prime", kind: "prime", fixture: file("2026-09-20T10-00-00-000Z_s1.jsonl", failedPiTranscript)},
		{name: "senpi", kind: "senpi", fixture: file("2026-09-20T10-00-00-000Z_s1.jsonl", failedPiTranscript)},
		{name: "kimchi", kind: "kimchi", fixture: file("2026-09-20T10-00-00-000Z_s1.jsonl", failedPiTranscript)},
		{name: "cherrystudio-pi", fixture: file("2026-09-20T10-00-00-000Z_s1.jsonl", failedPiTranscript), parse: func(p string) ([]model.Session, error) { return ParseCherryStudioPiFileFromOffset(p, 0) }},
		{name: "goose", kind: "goose-db", fixture: func(t *testing.T, _ string) string {
			t.Helper()
			return seedDB(t, "sessions.db", `create table sessions (id text primary key, name text, description text, working_dir text, created_at text, updated_at text);
create table messages (id integer primary key autoincrement, session_id text, role text, content_json text, created_timestamp integer);
insert into sessions values ('20260920_1','n','fix the retry loop','/tmp/proj','2026-09-20T10:00:00Z','2026-09-20T10:05:00Z');
insert into messages(session_id,role,content_json,created_timestamp) values ('20260920_1','user','[{"type":"text","text":"fix the retry loop"}]',1790874400);
insert into messages(session_id,role,content_json,created_timestamp) values ('20260920_1','assistant','[{"type":"toolRequest","id":"c1","toolCall":{"status":"success","value":{"name":"shell","arguments":{"command":"go test ./..."}}}}]',1790874401);
insert into messages(session_id,role,content_json,created_timestamp) values ('20260920_1','user','[{"type":"toolResponse","id":"c1","toolResult":{"status":"success","value":{"content":[{"type":"text","text":"./retry.go:12:5: undefined: backoffJitter\n\nCommand exited with code 1"}],"isError":true,"structuredContent":{"stdout":"","stderr":"./retry.go:12:5: undefined: backoffJitter","exit_code":1,"timed_out":false}}}}]',1790874402);`)
		}},
		{name: "cline cli", kind: "cline-sdk", fixture: func(t *testing.T, dir string) string {
			t.Helper()
			id := "1790877871094"
			write(t, filepath.Join(dir, id, id+".json"), `{"cwd":"/tmp/proj","started_at":"2026-10-01T18:00:00Z"}`)
			return write(t, filepath.Join(dir, id, id+".messages.json"), `{"version":1,"agent":"lead","sessionId":"`+id+`","messages":[
 {"role":"user","ts":1790877871100,"content":[{"type":"text","text":"fix the retry loop"}]},
 {"role":"assistant","ts":1790877871200,"content":[{"type":"tool_use","id":"call_1","name":"run_commands","input":{"commands":["go vet ./...","go test ./..."]}}]},
 {"role":"user","ts":1790877871300,"content":[{"type":"tool_result","tool_use_id":"call_1","name":"run_commands","content":[
  {"query":"go vet ./...","result":"","success":true},
  {"query":"go test ./...","result":"[Command exited with code 1]\n\n[stderr]\n./retry.go:12:5: undefined: backoffJitter\n","error":"Command exited with code 1","success":false}]}]}]}`)
		}},
		{name: "cline vscode", kind: "cline-vscode", fixture: func(t *testing.T, dir string) string {
			t.Helper()
			write(t, filepath.Join(dir, "state", "taskHistory.json"), `[{"id":"1790000000000","ts":1790000000000,"task":"fix the retry loop","cwdOnTaskInitialization":"/tmp/proj"}]`)
			return write(t, filepath.Join(dir, "tasks", "1790000000000", "api_conversation_history.json"), `[
 {"role":"user","content":[{"type":"text","text":"<task>fix the retry loop</task>"}]},
 {"role":"assistant","content":[{"type":"tool_use","id":"toolu_1","name":"execute_command","input":{"command":"go test ./...","requires_approval":false}}]},
 {"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_1","content":"Command failed with exit code 1.\nOutput:\n./retry.go:12:5: undefined: backoffJitter"}]}]`)
		}},
		{name: "kiro-cli", kind: "kiro-cli", fixture: func(t *testing.T, dir string) string {
			t.Helper()
			return write(t, filepath.Join(dir, "cli", "11111111-2222-4333-8444-555555555555.jsonl"), strings.Join([]string{
				`{"version":"v1","kind":"Prompt","data":{"message_id":"m0","content":[{"kind":"text","data":"fix the retry loop"}],"meta":{"timestamp":1790848800}}}`,
				`{"version":"v1","kind":"AssistantMessage","data":{"message_id":"m1","content":[{"kind":"toolUse","data":{"toolUseId":"t1","name":"shell","input":{"command":"go test ./..."}}}],"meta":{"timestamp":1790848801}}}`,
				`{"version":"v1","kind":"ToolResults","data":{"message_id":"m2","content":[{"kind":"toolResult","data":{"toolUseId":"t1","content":[{"kind":"json","data":{"exit_status":"exit status: 1","stdout":"","stderr":"./retry.go:12:5: undefined: backoffJitter"}}],"status":"success"}}],"meta":{"timestamp":1790848802}}}`,
			}, "\n")+"\n")
		}},
		{name: "kiro-cli no-interactive", kind: "kiro-db", fixture: func(t *testing.T, _ string) string {
			t.Helper()
			return kiroTestDB(t, kiroDBRow("/tmp/proj", "bf73e1f5-0000-4000-8000-000000000003", `{"conversation_id":"bf73e1f5-0000-4000-8000-000000000003","history":[
 {"user":{"content":{"Prompt":{"prompt":"fix the retry loop"}},"timestamp":"2026-10-01T10:00:00+00:00"},
  "assistant":{"ToolUse":{"message_id":"m1","content":"Running the tests.","tool_uses":[{"id":"t1","name":"execute_bash","args":{"command":"go test ./..."}}]}}},
 {"user":{"content":{"ToolUseResults":{"tool_use_results":[{"tool_use_id":"t1","content":[{"Json":{"exit_status":"1","stdout":"","stderr":"./retry.go:12:5: undefined: backoffJitter"}}],"status":"Success"}]}},"timestamp":null},
  "assistant":{"Response":{"message_id":"m2","content":"The build is broken."}}}]}`, 1790848800000))
		}},
		{name: "copilot-chat", kind: "copilot-chat", fixture: func(t *testing.T, dir string) string {
			t.Helper()
			return write(t, filepath.Join(dir, "workspaceStorage", "a1b2", "chatSessions", "5c0ffee0-0000-4000-8000-000000000000.jsonl"), strings.Join([]string{
				`{"kind":0,"v":{"version":3,"sessionId":"5c0ffee0-0000-4000-8000-000000000000","creationDate":1790000000000,"customTitle":"fix the retry loop","requests":[]}}`,
				`{"kind":2,"k":["requests"],"v":[{"requestId":"request_1","timestamp":1790000001000,"message":{"text":"fix the retry loop"},"response":[{"kind":"toolInvocationSerialized","isComplete":true,"toolCallId":"c2","toolId":"run_in_terminal","toolSpecificData":{"kind":"terminal","commandLine":{"original":"go test ./..."},"language":"sh","terminalCommandState":{"exitCode":1,"timestamp":1790000003000,"duration":1200},"terminalCommandOutput":{"text":"./retry.go:12:5: undefined: backoffJitter\nFAIL"}}}]}]}`,
			}, "\n")+"\n")
		}},
	}
	for _, r := range rows {
		t.Run(r.name, func(t *testing.T) {
			p := r.fixture(t, t.TempDir())
			var ss []model.Session
			if r.parse != nil {
				var err error
				if ss, err = r.parse(p); err != nil {
					t.Fatal(err)
				}
			} else {
				ss = parseKindForTest(t, r.kind, p)
			}
			assertCommand(t, ss, want)
		})
	}

	// Zed keeps the call and its result on one agent message.
	t.Run("zed", func(t *testing.T) {
		msg, _ := json.Marshal(map[string]any{"Agent": map[string]any{
			"content": []any{map[string]any{"ToolUse": map[string]any{"id": "c1", "name": "terminal", "input": map[string]any{"command": "go test ./...", "cd": "/tmp/proj"}}}},
			"tool_results": map[string]any{"c1": map[string]any{"tool_use_id": "c1", "tool_name": "terminal", "is_error": false,
				"content": map[string]any{"Text": "Command \"go test ./...\" failed with exit code 1.\n\n```\n./retry.go:12:5: undefined: backoffJitter\n```"}}},
		}})
		assertCommand(t, []model.Session{{Messages: zedWork(msg, time.Unix(1790000000, 0))}}, want)
	})
}

func assertCommand(t *testing.T, ss []model.Session, want string) {
	t.Helper()
	var got []string
	for _, s := range ss {
		for _, m := range s.Messages {
			if m.Role == RoleCommand {
				if m.Text == want {
					return
				}
				got = append(got, m.Text)
			}
		}
	}
	t.Errorf("commands = %q, want %q among them", got, want)
}

// A kiro-cli result that lands a pass after its call is read whole, or the
// stored call never gets its exit: a failure's code (#4505) or a clean run's
// exit 0 (#5527).
func TestKiroCLIReadsAFailedResultWithItsCall(t *testing.T) {
	call := `{"version":"v1","kind":"AssistantMessage","data":{"message_id":"m1","content":[{"kind":"toolUse","data":{"toolUseId":"t1","name":"shell","input":{"command":"go test ./..."}}}],"meta":{"timestamp":1790848801}}}` + "\n"
	result := func(status string) string {
		return `{"version":"v1","kind":"ToolResults","data":{"message_id":"m2","content":[{"kind":"toolResult","data":{"toolUseId":"t1","content":[{"kind":"json","data":{"exit_status":"` + status + `","stdout":"","stderr":""}}],"status":"success"}}],"meta":{"timestamp":1790848802}}}` + "\n"
	}
	for status, want := range map[string]bool{"exit status: 1": false, "exit status: 0": false} {
		p := filepath.Join(t.TempDir(), "s.jsonl")
		if err := os.WriteFile(p, []byte(call+result(status)), 0o644); err != nil {
			t.Fatal(err)
		}
		if got := KiroCLIResumes(p, int64(len(call))); got != want {
			t.Errorf("%s: resumes = %v, want %v", status, got, want)
		}
	}
}

// A Cline CLI batch can run the same command twice. Each entry's status
// belongs to one of the two, in order: the first clean entry stamped both and
// the failure that followed was dropped as already stamped.
func TestClineBatchStampsARepeatedCommandOncePerEntry(t *testing.T) {
	dir := t.TempDir()
	id := "1790877871094"
	if err := os.MkdirAll(filepath.Join(dir, id), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, id, id+".json"), []byte(`{"cwd":"/tmp/proj","started_at":"2026-10-01T18:00:00Z"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, id, id+".messages.json")
	if err := os.WriteFile(p, []byte(`{"version":1,"agent":"lead","sessionId":"`+id+`","messages":[
 {"role":"user","ts":1790877871100,"content":[{"type":"text","text":"fix the retry loop"}]},
 {"role":"assistant","ts":1790877871200,"content":[{"type":"tool_use","id":"call_1","name":"run_commands","input":{"commands":["go test ./...","go test ./..."]}}]},
 {"role":"user","ts":1790877871300,"content":[{"type":"tool_result","tool_use_id":"call_1","name":"run_commands","content":[
  {"query":"go test ./...","result":"ok","success":true},
  {"query":"go test ./...","result":"FAIL","error":"Command exited with code 1","success":false}]}]}]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, s := range parseKindForTest(t, "cline-sdk", p) {
		for _, m := range s.Messages {
			if m.Role == RoleCommand {
				got = append(got, m.Text)
			}
		}
	}
	want := []string{"$ go test ./...  → exit 0", "$ go test ./...  → exit 1"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("commands = %q, want %q", got, want)
	}
}

// A failed result that lands a pass after its call sends the file back for a
// whole read in every appendable format whose reader now stamps the failure,
// as kiro-cli's does above; a clean one, or a call and failure both in the
// tail, does not (#4443). Claude's, goose's, kiro-cli's and the pi family's
// readers stamp exit 0 on a clean result too, so their clean answer to a
// stored command is read whole as well (#5527).
func TestAFailedResultForAStoredCallIsReadWithItsCall(t *testing.T) {
	claudeCall := `{"type":"assistant","sessionId":"c1","timestamp":"2026-10-01T10:00:01Z","cwd":"/tmp/proj","message":{"role":"assistant","content":[{"type":"tool_use","id":"t1","name":"Bash","input":{"command":"go test ./..."}}]}}` + "\n"
	claudeOut := func(failed bool) string {
		if failed {
			return `{"type":"user","sessionId":"c1","timestamp":"2026-10-01T10:00:02Z","cwd":"/tmp/proj","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"t1","is_error":true,"content":"Exit code 1\nFAIL"}]}}` + "\n"
		}
		return `{"type":"user","sessionId":"c1","timestamp":"2026-10-01T10:00:02Z","cwd":"/tmp/proj","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"t1","is_error":false,"content":"ok"}]}}` + "\n"
	}
	gooseCall := `{"role":"assistant","created":1790874401,"content":[{"type":"toolRequest","id":"c1","toolCall":{"status":"success","value":{"name":"developer__shell","arguments":{"command":"go test ./..."}}}}]}` + "\n"
	gooseOut := func(failed bool) string {
		if failed {
			return `{"role":"user","created":1790874402,"content":[{"type":"toolResponse","id":"c1","toolResult":{"status":"success","value":{"content":[{"type":"text","text":"FAIL\n\nCommand exited with code 1"}],"isError":true}}}]}` + "\n"
		}
		return `{"role":"user","created":1790874402,"content":[{"type":"toolResponse","id":"c1","toolResult":{"status":"success","value":{"content":[{"type":"text","text":"ok"}],"structuredContent":{"exit_code":0}}}}]}` + "\n"
	}
	resumes := func(kind string) func(string, int64) bool {
		for _, h := range Registry() {
			for _, k := range h.Kinds {
				if k.Name == kind {
					return k.Resumes
				}
			}
		}
		t.Fatalf("no kind %s", kind)
		return nil
	}
	for _, c := range []struct {
		kind, head, tail string
		want             bool
	}{
		{"claude", claudeCall, claudeOut(true), false},
		{"claude", claudeCall, claudeOut(false), false},
		{"claude", "", claudeCall + claudeOut(true), true},
		{"cherrystudio", claudeCall, claudeOut(true), false},
		{"goose-jsonl", gooseCall, gooseOut(true), false},
		{"goose-jsonl", gooseCall, gooseOut(false), false},
		{"goose-jsonl", gooseCall, `{"role":"user","created":1790874402,"content":[{"type":"toolResponse","id":"c1","toolResult":{"status":"success","value":{"content":[{"type":"text","text":"FAIL"}],"structuredContent":{"exit_code":2}}}}]}` + "\n", false},
		{"goose-jsonl", "", gooseCall + gooseOut(true), true},
		{"clawcode", clawCodeCall, clawCodeResult(`{"stdout":"FAIL","stderr":"","interrupted":false,"returnCodeInterpretation":"exit_code:1"}`, true), false},
		{"clawcode", clawCodeCall, clawCodeResult(`{"stdout":"ok","stderr":"","interrupted":false}`, false), false},
		{"clawcode", clawCodeCall, clawCodeResult(`{"stdout":"partial","stderr":"","interrupted":true}`, false), true},
		{"clawcode", "", clawCodeCall + clawCodeResult(`{"stdout":"FAIL","stderr":"","interrupted":false,"returnCodeInterpretation":"exit_code:1"}`, true), true},
		// Command Code's failure is the "Exit code: N" line, with no is_error
		// (#4539).
		{"commandcode", commandCodeCall, commandCodeResult("Exit code: 1\nFAIL"), false},
		{"commandcode", commandCodeCall, commandCodeResult("ok"), true},
		{"commandcode", "", commandCodeCall + commandCodeResult("Exit code: 1\nFAIL"), true},
		// A PowerShell run is the shell since #4523.
		{"kimchi", piCall("c1", "powershell", `{"command":"go vet ./retry"}`) + "\n", piResult("c1", "powershell", "vet: unreachable\n\nCommand exited with code 1", `{}`, true) + "\n", false},
		{"kimchi", piCall("c1", "powershell", `{"command":"go vet ./retry"}`) + "\n", piResult("c1", "powershell", "ok", `{}`, false) + "\n", false},
		{"kimchi", piCall("c1", "read", `{"path":"retry.go"}`) + "\n", piResult("c1", "powershell", "ok", `{}`, false) + "\n", true},
		{"goose-jsonl", "", `{"role":"user","created":1790874402,"content":[{"type":"toolResponse","id":"c9","toolResult":{"status":"success","value":{"content":[{"type":"text","text":"ok"}],"structuredContent":{"exit_code":0}}}}]}` + "\n", true},
	} {
		p := filepath.Join(t.TempDir(), "s.jsonl")
		body := `{"type":"user","sessionId":"c1","timestamp":"2026-10-01T10:00:00Z","message":{"role":"user","content":"fix the retry loop"}}` + "\n" + c.head
		if err := os.WriteFile(p, []byte(body+c.tail), 0o644); err != nil {
			t.Fatal(err)
		}
		// A kind with no Resumes appends every tail.
		got := true
		if r := resumes(c.kind); r != nil {
			got = r(p, int64(len(body)))
		}
		if got != c.want {
			t.Errorf("%s head=%t tail %q: resumes = %v, want %v", c.kind, c.head != "", c.tail, got, c.want)
		}
	}
}

// Command Code's transcript has a sibling with the same extension — the
// checkpoint stream — and reading it as a conversation adds a session with no
// words in it, which then competes for a recall slot (#3647).
func TestCommandCodeSkipsTheCheckpointStream(t *testing.T) {
	root := fixtureRoot(t, "commandcode", "DEJA_COMMANDCODE_ROOT")
	files := CommandCodeSessionFiles()
	if len(files) != 1 {
		t.Fatalf("files = %v, want the transcript alone", files)
	}
	if strings.Contains(files[0], ".checkpoints.") {
		t.Fatalf("the checkpoint stream was listed as a transcript: %s", files[0])
	}

	ss, err := ParseCommandCodeFile(files[0])
	if err != nil {
		t.Fatal(err)
	}
	if len(ss) != 1 {
		t.Fatalf("sessions = %d, want 1", len(ss))
	}
	s := ss[0]
	if s.ID != "reg-commandcode-001" {
		t.Errorf("id = %q, want the sessionId the lines carry", s.ID)
	}
	if s.Project != "commandcode/demo" {
		t.Errorf("project = %q, want it decoded from the directory", s.Project)
	}
	if len(s.Messages) != 3 {
		t.Fatalf("messages = %d, want prompt, tool output and answer: %+v", len(s.Messages), s.Messages)
	}
	if s.Messages[1].Role != RoleToolOutput {
		t.Errorf("the tool line came back as %q; an error a user hit has to be searchable", s.Messages[1].Role)
	}
	if !strings.Contains(s.Messages[1].Text, "connection refused") {
		t.Errorf("the tool output lost its text: %+v", s.Messages[1])
	}
	if s.Messages[2].Role != "assistant" || !strings.Contains(s.Messages[2].Text, "host index") {
		t.Errorf("the answer is wrong: %+v", s.Messages[2])
	}

	// The skip has to be nameable, or doctor counts the checkpoint stream as a
	// file deja failed to understand and the row reports drift on a store it is
	// reading correctly.
	cps := CommandCodeCheckpointFiles()
	if len(cps) != 1 || !strings.HasSuffix(cps[0], ".checkpoints.jsonl") {
		t.Errorf("checkpoint files = %v, want the one beside the transcript", cps)
	}
	_ = root
}

// ZCode writes the same lines, and content can be a plain string there rather
// than the block array.
func TestZCodeReadsAStringContent(t *testing.T) {
	fixtureRoot(t, "zcode", "DEJA_ZCODE_ROOT")
	files := ZCodeSessionFiles()
	if len(files) != 1 {
		t.Fatalf("files = %v", files)
	}
	ss, err := ParseZCodeFile(files[0])
	if err != nil {
		t.Fatal(err)
	}
	if len(ss) != 1 || len(ss[0].Messages) != 2 {
		t.Fatalf("sessions = %+v", ss)
	}
	if !strings.Contains(ss[0].Messages[1].Text, "empty frame") {
		t.Errorf("the answer is missing: %+v", ss[0].Messages)
	}
	if ss[0].Messages[1].Time.IsZero() {
		t.Error("the answer has no time, so recency cannot rank it")
	}
}

// Senpi and Kimchi are pi's envelope, and what has to hold is that the harness
// name and the project come back as theirs rather than pi's.
func TestSenpiAndKimchiReadThePiEnvelope(t *testing.T) {
	root := fixtureRoot(t, "senpi", "DEJA_SENPI_ROOT")
	ss, err := ParseSenpiFile(filepath.Join(root, "-workspace-senpi-demo", "session.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if len(ss) != 1 {
		t.Fatalf("senpi sessions = %d", len(ss))
	}
	if ss[0].Harness != "senpi" {
		t.Errorf("harness = %q, want senpi", ss[0].Harness)
	}
	if ss[0].Project != "workspace/senpi-demo" {
		t.Errorf("project = %q, want it from the header's cwd", ss[0].Project)
	}
	// The third is the compaction summary the fixture ends with.
	if len(ss[0].Messages) != 3 || !strings.Contains(ss[0].Messages[1].Text, "acknowledged") || ss[0].Messages[2].Role != RoleSummary {
		t.Errorf("senpi messages = %+v", ss[0].Messages)
	}

	// Kimchi keeps a directory per project, the way pi does: its own binary
	// builds `<agent>/sessions/--<encoded cwd>--` in getDefaultSessionDirPath.
	// The fixture is laid out that way, and the walk has to find it — a flat
	// listing would read nothing on a real machine (#3678).
	kroot := fixtureRoot(t, "kimchi", "DEJA_KIMCHI_ROOT")
	nested := filepath.Join(kroot, "--workspace-kimchi-demo--", "session.jsonl")
	files := KimchiSessionFiles()
	found := false
	for _, f := range files {
		if f == nested {
			found = true
		}
	}
	if !found {
		t.Fatalf("the per-project transcript is not in the file list: %v", files)
	}
	ks, err := ParseKimchiFile(nested)
	if err != nil {
		t.Fatal(err)
	}
	if len(ks) != 1 {
		t.Fatalf("kimchi sessions = %d", len(ks))
	}
	if ks[0].Harness != "kimchi" {
		t.Errorf("harness = %q, want kimchi", ks[0].Harness)
	}
	// The header's cwd is what names the project — authoritative, and on a real
	// machine the encoded directory is built from the same value, so the layout
	// must not change the attribution. The flat case below asserts that pair.
	if ks[0].Project != "workspace/kimchi-demo" {
		t.Errorf("project = %q, want it from the header's cwd", ks[0].Project)
	}
	if ks[0].ID != "reg-kimchi-001" {
		t.Errorf("id = %q, want the header's id", ks[0].ID)
	}

	// And a session file sitting directly under the root is still read, with
	// the header's cwd naming the project.
	flat := filepath.Join(t.TempDir(), "session.jsonl")
	body, err := os.ReadFile(nested)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(flat, body, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_KIMCHI_ROOT", filepath.Dir(flat))
	fs, err := ParseKimchiFile(flat)
	if err != nil {
		t.Fatal(err)
	}
	if len(fs) != 1 || fs[0].Project != ks[0].Project {
		t.Errorf("flat file: %+v, want the same project the nested one got (%q)", fs, ks[0].Project)
	}
}

// gjc keeps a sub-agent's passes one directory deeper than the session they
// belong to. Indexed as sessions of their own they repeat the parent's work and
// compete with it for the same recall slot, so they are skipped unless asked
// for — the switch Claude Code's and Cursor's sub-agents already use (#3647).
func TestGjcSkipsSubagentPassesUnlessAskedFor(t *testing.T) {
	root := fixtureRoot(t, "gjc", "DEJA_GJC_ROOT")
	files := GjcSessionFiles()
	if len(files) != 1 {
		t.Fatalf("files = %v, want the session alone", files)
	}
	if strings.Contains(filepath.ToSlash(files[0]), "/session/") {
		t.Fatalf("a sub-agent pass was listed as a session: %s", files[0])
	}

	ss, err := ParseGjcFile(files[0])
	if err != nil {
		t.Fatal(err)
	}
	if len(ss) != 1 || ss[0].Harness != "gjc" {
		t.Fatalf("sessions = %+v", ss)
	}
	if ss[0].ID != "reg-gjc-001" {
		t.Errorf("id = %q, want the header's id", ss[0].ID)
	}
	if len(ss[0].Messages) != 3 || !strings.Contains(ss[0].Messages[1].Text, "twice") || ss[0].Messages[2].Role != RoleSummary {
		t.Errorf("messages = %+v", ss[0].Messages)
	}
	// The service_tier_change line is not a turn, and read as one it is an
	// empty message in the middle of the session.
	for _, m := range ss[0].Messages {
		if m.Text == "" {
			t.Errorf("a bookkeeping line became a message: %+v", ss[0].Messages)
		}
	}

	t.Setenv("DEJA_INCLUDE_SUBAGENTS", "1")
	if got := GjcSessionFiles(); len(got) != 2 {
		t.Errorf("with sub-agents asked for, files = %v, want both", got)
	}
	_ = root
}

// fixtureRoot points one reader at its committed fixture store.
func fixtureRoot(t *testing.T, name, env string) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", "..", "fixtures", "registry", name))
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv(env, root)
	return root
}

// fuzzEnv points every source root at a throwaway temp dir so parsers that
// resolve project names against the filesystem (claude/cursor encoded dirs,
// grok cwd markers, gemini registry) never touch the real home directory.
func fuzzEnv(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	home := filepath.Join(root, "home")
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(root, "claude"))
	t.Setenv("DEJA_CODEX_ROOT", filepath.Join(root, "codex"))
	t.Setenv("DEJA_GEMINI_ROOT", filepath.Join(root, "gemini"))
	t.Setenv("DEJA_GROK_ROOT", filepath.Join(root, "grok"))
	t.Setenv("DEJA_QWEN_ROOT", filepath.Join(root, "qwen"))
	t.Setenv("GROK_HOME", filepath.Join(root, "grok"))
	t.Setenv("DEJA_ANTIGRAVITY_ROOT", filepath.Join(root, "antigravity"))
	return root
}

// checkSessions asserts the invariants every parser must uphold: it only
// returns sessions with at least one message and a non-empty ID (each format
// derives an ID from the filename or transcript, never leaving it blank).
func checkSessions(t *testing.T, ss []model.Session) {
	t.Helper()
	for i := range ss {
		if ss[i].Messages == nil {
			t.Fatalf("session %d has nil Messages: %#v", i, ss[i])
		}
		if ss[i].ID == "" {
			t.Fatalf("session %d has empty ID: %#v", i, ss[i])
		}
	}
}

func writeFuzzFile(t *testing.T, dir, name string, data []byte) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, data, 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

const claudeSeed = `{"type":"user","sessionId":"claude-abc","cwd":"/tmp/demo","timestamp":"2026-01-02T03:04:05Z","message":{"role":"user","content":"Find frobnicator bug"}}
{"type":"assistant","sessionId":"claude-abc","cwd":"/tmp/demo","timestamp":"2026-01-02T03:04:06Z","message":{"role":"assistant","content":[{"type":"text","text":"in parser.go"}]}}
`

func FuzzParseClaudeFile(f *testing.F) {
	f.Add([]byte(claudeSeed))
	f.Add([]byte(`{"type":"user","sessionId":"c","timestamp":"2026-01-02T03:04:05Z","message":{"role":"user","content":"hi"}}` + "\n"))
	f.Add([]byte(`{"type":"summary","summary":"x"}` + "\n"))
	f.Add([]byte(claudeSeed[:len(claudeSeed)/2])) // torn tail
	f.Add([]byte("{\x00not json\n"))
	f.Add([]byte(""))
	f.Fuzz(func(t *testing.T, data []byte) {
		fuzzEnv(t)
		p := writeFuzzFile(t, t.TempDir(), "s.jsonl", data)
		ss, _ := ParseClaudeFile(p)
		checkSessions(t, ss)
	})
}

const codexRolloutSeed = `{"timestamp":"2026-01-02T03:04:05Z","type":"session_meta","payload":{"session_id":"codex-abc","cwd":"/tmp/codemo"}}
{"timestamp":"2026-01-02T03:04:06Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"hello codex"}]}}
{"timestamp":"2026-01-02T03:04:07Z","type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"fixed"}]}}
`

func FuzzParseCodexRollout(f *testing.F) {
	f.Add([]byte(codexRolloutSeed))
	f.Add([]byte(`{"timestamp":"2026-01-02T03:04:06Z","payload":{"role":"assistant","content":"x"}}` + "\n"))
	f.Add([]byte(`{"payload":{"message":"bare user text"}}` + "\n"))
	f.Add([]byte(codexRolloutSeed[:len(codexRolloutSeed)/2]))
	f.Add([]byte(`{"payload":null}` + "\n"))
	f.Add([]byte(""))
	f.Fuzz(func(t *testing.T, data []byte) {
		fuzzEnv(t)
		p := writeFuzzFile(t, t.TempDir(), "rollout-fuzz.jsonl", data)
		ss, _ := ParseCodexRollout(p)
		checkSessions(t, ss)
	})
}

func FuzzParseCodexHistory(f *testing.F) {
	f.Add([]byte(`{"session_id":"hist-abc","ts":1767323045,"text":"history needle"}` + "\n"))
	f.Add([]byte(`{"session_id":"","text":"no id"}` + "\n"))
	f.Add([]byte(`{"session_id":"h","text":""}` + "\n"))
	f.Add([]byte(`{"session_id":"h","ts":"2026-01-02T03:04:05Z","text":"a"}` + "\n{broken"))
	f.Add([]byte(""))
	f.Fuzz(func(t *testing.T, data []byte) {
		fuzzEnv(t)
		p := writeFuzzFile(t, t.TempDir(), "history.jsonl", data)
		ss, _ := ParseCodexHistory(p)
		checkSessions(t, ss)
	})
}

const geminiJSONSeed = `{"sessionId":"session-old-1","startTime":"2026-01-16T18:34:00.000Z","lastUpdated":"2026-01-16T18:35:00.000Z","messages":[{"id":"m1","timestamp":"2026-01-16T18:34:01.000Z","type":"user","content":"Hello"},{"id":"m2","timestamp":"2026-01-16T18:34:02.000Z","type":"gemini","content":"Hi!"}]}`

const geminiJSONLSeed = `{"sessionId":"sess-new-1","startTime":"2026-07-01T10:00:00.000Z","lastUpdated":"2026-07-01T10:00:00.000Z","kind":"main"}
{"id":"u1","timestamp":"2026-07-01T10:00:01.000Z","type":"user","content":[{"text":"first question"}]}
{"id":"a1","timestamp":"2026-07-01T10:00:02.000Z","type":"gemini","content":"wrong answer"}
{"$rewindTo":"a1"}
{"id":"a2","timestamp":"2026-07-01T10:00:05.000Z","type":"gemini","content":"right answer"}
{"$set":{"lastUpdated":"2026-07-01T10:00:06.000Z"}}
`

// ParseGeminiFile dispatches on extension, so each input is parsed as both a
// whole-session .json and a streaming .jsonl.
func FuzzParseGeminiFile(f *testing.F) {
	f.Add([]byte(geminiJSONSeed))
	f.Add([]byte(geminiJSONLSeed))
	f.Add([]byte(`{"$rewindTo":"missing"}` + "\n"))
	f.Add([]byte(geminiJSONLSeed[:len(geminiJSONLSeed)/2]))
	f.Add([]byte(`{"sessionId":"s","messages":[{"type":"user","content":[{"text":"x"}]}]}`))
	f.Add([]byte(""))
	f.Fuzz(func(t *testing.T, data []byte) {
		fuzzEnv(t)
		chats := filepath.Join(t.TempDir(), "tmp", "proj", "chats")
		for _, ext := range []string{".json", ".jsonl"} {
			p := writeFuzzFile(t, chats, "session-fuzz"+ext, data)
			ss, _ := ParseGeminiFile(p)
			checkSessions(t, ss)
		}
	})
}

const aiderSeed = "# aider chat started at 2026-07-16 10:32:01\n\n> aider v0.86.1\n\n#### fix the off-by-one\n#### keep tests green\n\nI'll fix the loop:\n\n```go\nfor i := 0; i < n; i++ {\n#### not a user line\n```\n\n> Applied edit\n\n# aider chat started at 2026-07-16 11:00:00\n\n#### second question\n\nSecond answer.\n"

func FuzzParseAiderFile(f *testing.F) {
	f.Add([]byte(aiderSeed))
	f.Add([]byte("# aider chat started at 2026-01-01 00:00:00\n#### q\nans\n"))
	f.Add([]byte("#### orphan user line with no session header\n"))
	f.Add([]byte("# aider chat started at not-a-timestamp\n#### q\n```\nunclosed fence"))
	f.Add([]byte(aiderSeed[:len(aiderSeed)/2]))
	f.Add([]byte(""))
	f.Fuzz(func(t *testing.T, data []byte) {
		fuzzEnv(t)
		p := writeFuzzFile(t, t.TempDir(), ".aider.chat.history.md", data)
		ss, _ := ParseAiderFile(p)
		checkSessions(t, ss)
	})
}

const antigravitySeed = `{"step_index":1,"source":"USER_EXPLICIT","type":"USER_INPUT","created_at":"2026-07-08T14:18:27Z","content":"<USER_REQUEST>\nBuild this\n</USER_REQUEST>"}
{"step_index":2,"source":"SYSTEM","type":"SYSTEM","created_at":"2026-07-08T14:18:28Z","content":"noise"}
{"step_index":3,"source":"MODEL","type":"PLANNER_RESPONSE","created_at":"2026-07-08T14:18:29Z","content":"I can help."}
`

func FuzzParseAntigravityFile(f *testing.F) {
	f.Add([]byte(antigravitySeed))
	f.Add([]byte(`{"source":"USER_EXPLICIT","created_at":"2026-07-08T14:18:27Z","content":"hi"}` + "\n"))
	f.Add([]byte(`{"source":"MODEL","content":"<USER_REQUEST></USER_REQUEST>"}` + "\n"))
	f.Add([]byte(antigravitySeed[:len(antigravitySeed)/2]))
	f.Add([]byte(""))
	f.Fuzz(func(t *testing.T, data []byte) {
		root := fuzzEnv(t)
		dir := filepath.Join(root, "antigravity", "brain", "traj-1", ".system_generated", "logs")
		p := writeFuzzFile(t, dir, "transcript.jsonl", data)
		ss, _ := ParseAntigravityFile(p)
		checkSessions(t, ss)
	})
}

const grokSeed = `{"timestamp":1782900001,"method":"session/update","params":{"update":{"sessionUpdate":"user_message_chunk","content":{"type":"text","text":"find grokneedle"},"_meta":{"promptIndex":0}}}}
{"timestamp":1782900002,"method":"session/update","params":{"update":{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"first "}},"_meta":{"promptId":"p1"}}}
{"timestamp":1782900005,"method":"session/update","params":{"update":{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"answer"}},"_meta":{"promptId":"p1"}}}
`

func FuzzParseGrokFile(f *testing.F) {
	f.Add([]byte(grokSeed))
	f.Add([]byte(`{"timestamp":1,"params":{"update":{"sessionUpdate":"user_message_chunk","content":{"type":"text","text":"x"}}}}` + "\n"))
	f.Add([]byte(`{"params":{"update":{"sessionUpdate":"user_message_chunk","content":"bare string"}}}` + "\n"))
	f.Add([]byte(grokSeed + "{malformed\n"))
	f.Add([]byte(grokSeed[:len(grokSeed)/2]))
	f.Add([]byte(""))
	f.Fuzz(func(t *testing.T, data []byte) {
		root := fuzzEnv(t)
		group := url.PathEscape("/work/cool-app")
		dir := filepath.Join(root, "grok", "sessions", group, "sess-1")
		summary := `{"info":{"id":"sess-1","cwd":"/work/cool-app"},"generated_title":"t","created_at":"2026-07-01T10:00:00Z","updated_at":"2026-07-01T10:00:06Z"}`
		writeFuzzFile(t, dir, "summary.json", []byte(summary))
		p := writeFuzzFile(t, dir, "updates.jsonl", data)
		ss, _ := ParseGrokFile(p)
		checkSessions(t, ss)
	})
}

const cursorTranscriptSeed = `{"role":"user","message":{"content":[{"type":"text","text":"transcriptneedle question"}]}}
{"role":"assistant","message":{"content":[{"type":"text","text":"Running ls."},{"type":"tool_use","name":"Bash","input":{"command":"ls"}}]}}
{"type":"turn_ended","status":"success"}
`

func FuzzParseCursorTranscript(f *testing.F) {
	f.Add([]byte(cursorTranscriptSeed))
	f.Add([]byte(`{"role":"user","message":{"content":"hi"}}` + "\n"))
	f.Add([]byte(`{"role":"assistant","message":null}` + "\n"))
	f.Add([]byte(cursorTranscriptSeed[:len(cursorTranscriptSeed)/2]))
	f.Add([]byte(""))
	f.Fuzz(func(t *testing.T, data []byte) {
		fuzzEnv(t)
		dir := filepath.Join(t.TempDir(), "projects", "Users-x-app", "agent-transcripts", "sess-1")
		p := writeFuzzFile(t, dir, "sess-1.jsonl", data)
		ss, _ := ParseCursorTranscript(p)
		checkSessions(t, ss)
	})
}

func geminiTree(t *testing.T) (root, chats string) {
	t.Helper()
	root = t.TempDir()
	t.Setenv("DEJA_GEMINI_ROOT", root)
	chats = filepath.Join(root, "tmp", "my-proj", "chats")
	if err := os.MkdirAll(chats, 0o755); err != nil {
		t.Fatal(err)
	}
	return root, chats
}

func TestParseGeminiOldJSON(t *testing.T) {
	_, chats := geminiTree(t)
	doc := `{"sessionId":"session-old-1","projectHash":"abc","startTime":"2026-01-16T18:34:00.000Z","lastUpdated":"2026-01-16T18:35:00.000Z","messages":[
	 {"id":"m1","timestamp":"2026-01-16T18:34:01.000Z","type":"user","content":"Hello gemneedle"},
	 {"id":"m2","timestamp":"2026-01-16T18:34:02.000Z","type":"gemini","model":"gemini-3-flash","content":"Hi there!"},
	 {"id":"m3","timestamp":"2026-01-16T18:34:03.000Z","type":"info","content":"noise"}]}`
	p := filepath.Join(chats, "session-old-1.json")
	if err := os.WriteFile(p, []byte(doc), 0o644); err != nil {
		t.Fatal(err)
	}
	ss, err := ParseGeminiFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if len(ss) != 1 || ss[0].ID != "session-old-1" || ss[0].Harness != "gemini" {
		t.Fatalf("bad session: %#v", ss)
	}
	if len(ss[0].Messages) != 2 {
		t.Fatalf("messages = %d, want 2 (info skipped): %#v", len(ss[0].Messages), ss[0].Messages)
	}
	if ss[0].Messages[1].Role != "assistant" || ss[0].Messages[1].Text != "Hi there!" {
		t.Fatalf("assistant wrong: %#v", ss[0].Messages[1])
	}
	if ss[0].Project != "my-proj" {
		t.Fatalf("project = %q", ss[0].Project)
	}
}

func TestParseGeminiJSONLWithRewind(t *testing.T) {
	_, chats := geminiTree(t)
	lines := `{"sessionId":"sess-new-1","projectHash":"abc","startTime":"2026-07-01T10:00:00.000Z","lastUpdated":"2026-07-01T10:00:00.000Z","kind":"main"}
{"id":"u1","timestamp":"2026-07-01T10:00:01.000Z","type":"user","content":[{"text":"first question"}]}
{"id":"a1","timestamp":"2026-07-01T10:00:02.000Z","type":"gemini","content":"wrong answer","model":"gemini-3"}
{"$rewindTo":"a1"}
{"id":"a2","timestamp":"2026-07-01T10:00:05.000Z","type":"gemini","content":"right answer","model":"gemini-3"}
{"$set":{"lastUpdated":"2026-07-01T10:00:06.000Z"}}
`
	p := filepath.Join(chats, "session-2026-07-01T10-00-sess-new-1.jsonl")
	if err := os.WriteFile(p, []byte(lines), 0o644); err != nil {
		t.Fatal(err)
	}
	ss, err := ParseGeminiFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if len(ss) != 1 || ss[0].ID != "sess-new-1" {
		t.Fatalf("bad session: %#v", ss)
	}
	m := ss[0].Messages
	if len(m) != 2 {
		t.Fatalf("messages = %d, want 2 (rewind dropped wrong answer): %#v", len(m), m)
	}
	if m[0].Text != "first question" || m[1].Text != "right answer" {
		t.Fatalf("rewind replay wrong: %#v", m)
	}
	if ss[0].Updated.Second() != 6 {
		t.Fatalf("$set lastUpdated not applied: %v", ss[0].Updated)
	}
}

// Gemini 0.64 repeats earlier function responses under fresh message ids when
// a session is resumed. The repeated record must not duplicate tool output.
func TestGeminiResumeDedupesFunctionResponses(t *testing.T) {
	_, chats := geminiTree(t)
	lines := `{"sessionId":"sess-resume-output","projectHash":"abc","startTime":"2026-10-18T05:25:29.000Z","lastUpdated":"2026-10-18T05:25:39.066Z","kind":"main"}
{"id":"g1","timestamp":"2026-10-18T05:25:30.000Z","type":"gemini","content":"","toolCalls":[{"id":"write_file_1","name":"write_file","args":{"file_path":"parent.txt","content":"PARENT_LINE"}}]}
{"id":"u1","timestamp":"2026-10-18T05:25:30.384Z","type":"user","content":[{"functionResponse":{"id":"write_file_1","name":"write_file","response":{"output":"Successfully wrote parent.txt"}}}]}
{"id":"g2","timestamp":"2026-10-18T05:25:31.000Z","type":"gemini","content":"Created parent.txt."}
{"id":"u1_response","timestamp":"2026-10-18T05:25:39.066Z","type":"user","content":[{"functionResponse":{"id":"write_file_1","name":"write_file","response":{"output":"Successfully wrote parent.txt"}}}]}
`
	p := filepath.Join(chats, "session-2026-10-18T05-25-sess-resume-output.jsonl")
	if err := os.WriteFile(p, []byte(lines), 0o644); err != nil {
		t.Fatal(err)
	}
	ss, err := ParseGeminiFile(p)
	if err != nil || len(ss) != 1 {
		t.Fatalf("parse: %v, %d sessions", err, len(ss))
	}
	outputs := 0
	for _, m := range ss[0].Messages {
		if m.Role == RoleToolOutput {
			outputs++
		}
	}
	if outputs != 1 {
		t.Fatalf("tool outputs = %d, want 1: %#v", outputs, ss[0].Messages)
	}
}

func TestDedupeGeminiFunctionResponsesKeepsDistinctAndMixedParts(t *testing.T) {
	tests := []struct {
		name     string
		messages []geminiMessage
		wantLen  int
		wantText string
	}{
		{
			name: "same tool name with a distinct call id",
			messages: []geminiMessage{
				{Content: json.RawMessage(`[{"functionResponse":{"id":"write_file_1","name":"write_file","response":{"output":"first"}}}]`)},
				{Content: json.RawMessage(`[{"functionResponse":{"id":"write_file_2","name":"write_file","response":{"output":"second"}}}]`)},
			},
			wantLen: 2,
		},
		{
			name: "text beside a repeated response",
			messages: []geminiMessage{
				{Content: json.RawMessage(`[{"functionResponse":{"id":"write_file_1","name":"write_file","response":{"output":"first"}}}]`)},
				{Content: json.RawMessage(`[{"functionResponse":{"id":"write_file_1","name":"write_file","response":{"output":"first"}}},{"text":"keep this note"}]`)},
			},
			wantLen:  2,
			wantText: "keep this note",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := dedupeGeminiFunctionResponses(tt.messages)
			if len(got) != tt.wantLen {
				t.Fatalf("messages = %d, want %d: %#v", len(got), tt.wantLen, got)
			}
			if text := geminiContentText(got[len(got)-1].Content); text != tt.wantText {
				t.Fatalf("last message text = %q, want %q", text, tt.wantText)
			}
		})
	}
}

func TestGeminiProjectFromRegistry(t *testing.T) {
	root, chats := geminiTree(t)
	if err := os.WriteFile(filepath.Join(root, "projects.json"), []byte(`{"projects":{"/Users/x/work/cool-app":"my-proj"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	doc := `{"sessionId":"s1","startTime":"2026-01-16T18:34:00.000Z","lastUpdated":"2026-01-16T18:34:00.000Z","messages":[{"id":"m1","timestamp":"2026-01-16T18:34:01.000Z","type":"user","content":"q"}]}`
	p := filepath.Join(chats, "session-s1.json")
	if err := os.WriteFile(p, []byte(doc), 0o644); err != nil {
		t.Fatal(err)
	}
	ss, err := ParseGeminiFile(p)
	if err != nil || len(ss) != 1 {
		t.Fatalf("ss=%v err=%v", ss, err)
	}
	if ss[0].Project != "work/cool-app" {
		t.Fatalf("project = %q, want work/cool-app", ss[0].Project)
	}
}

func TestGeminiDedupePrefersJSONL(t *testing.T) {
	in := []model.Session{
		{Harness: "gemini", ID: "dup1", Path: "/a/session-dup1.json"},
		{Harness: "gemini", ID: "dup1", Path: "/a/session-dup1.jsonl"},
		{Harness: "gemini", ID: "solo", Path: "/a/session-solo.json"},
	}
	out := dedupeGeminiSessions(in)
	if len(out) != 2 {
		t.Fatalf("deduped to %d, want 2", len(out))
	}
	if out[0].ID != "dup1" || !strings.HasSuffix(out[0].Path, ".jsonl") {
		t.Fatalf("jsonl not preferred: %#v", out[0])
	}
}

// Resuming a .jsonl session leaves a second .jsonl under its id holding only
// the preamble. Neither replaces the other here, and a .jsonl with fewer
// messages than the .json it shares an id with does not replace it (#4213).
func TestGeminiDedupeKeepsAResumedTranscript(t *testing.T) {
	msgs := func(n int) []model.Message { return make([]model.Message, n) }
	out := dedupeGeminiSessions([]model.Session{
		{Harness: "gemini", ID: "r", Path: "/a/session-13-04-r.jsonl", Messages: msgs(6)},
		{Harness: "gemini", ID: "r", Path: "/a/session-13-07-r.jsonl", Messages: msgs(1)},
	})
	if len(out) != 2 {
		t.Fatalf("kept %d of two .jsonl transcripts sharing an id, want both", len(out))
	}
	out = dedupeGeminiSessions([]model.Session{
		{Harness: "gemini", ID: "o", Path: "/a/session-o.json", Messages: msgs(6)},
		{Harness: "gemini", ID: "o", Path: "/a/session-o-stub.jsonl", Messages: msgs(1)},
	})
	if len(out) != 1 || !strings.HasSuffix(out[0].Path, ".json") {
		t.Fatalf("a preamble-only .jsonl replaced the .json holding the conversation: %#v", out)
	}
	out = dedupeGeminiSessions([]model.Session{
		{Harness: "gemini", ID: "o", Path: "/a/session-o.jsonl", Messages: msgs(6)},
		{Harness: "gemini", ID: "o", Path: "/a/session-o.json", Messages: msgs(6)},
	})
	if len(out) != 1 || !strings.HasSuffix(out[0].Path, ".jsonl") {
		t.Fatalf("the .json replaced its .jsonl rewrite: %#v", out)
	}
	// A rewind after the rewrite leaves the .jsonl shorter than the .json it
	// came from; it is still the current one, in either order.
	for _, in := range [][]model.Session{
		{{Harness: "gemini", ID: "w", Path: "/a/session-w.json", Messages: msgs(6)}, {Harness: "gemini", ID: "w", Path: "/a/session-w.jsonl", Messages: msgs(3)}},
		{{Harness: "gemini", ID: "w", Path: "/a/session-w.jsonl", Messages: msgs(3)}, {Harness: "gemini", ID: "w", Path: "/a/session-w.json", Messages: msgs(6)}},
	} {
		out = dedupeGeminiSessions(in)
		if len(out) != 1 || out[0].Path != "/a/session-w.jsonl" {
			t.Fatalf("the stale .json won over its rewound .jsonl rewrite: %#v", out)
		}
	}
	// And with the resume stub beside them, the rewrite still pairs with the
	// file it was named after.
	out = dedupeGeminiSessions([]model.Session{
		{Harness: "gemini", ID: "w", Path: "/a/session-a-stub.jsonl", Messages: msgs(1)},
		{Harness: "gemini", ID: "w", Path: "/a/session-w.json", Messages: msgs(6)},
		{Harness: "gemini", ID: "w", Path: "/a/session-w.jsonl", Messages: msgs(3)},
	})
	paths := make([]string, 0, len(out))
	for _, s := range out {
		paths = append(paths, s.Path)
	}
	if got := strings.Join(paths, " "); !strings.Contains(got, "/a/session-w.jsonl") || strings.Contains(got, "/a/session-w.json ") || strings.HasSuffix(got, ".json") {
		t.Fatalf("stub, .json and its rewrite kept %s, want the rewrite and not the .json", got)
	}
}

// With a resume stub and the real .jsonl both held, an older .json named after
// neither is weighed against the conversation, not against whichever .jsonl
// happened to be read first: read stub-first it replaced the stub and sat
// beside the transcript it is an older copy of.
func TestGeminiDedupeIgnoresReadOrder(t *testing.T) {
	msgs := func(n int) []model.Message { return make([]model.Message, n) }
	files := []model.Session{
		{Harness: "gemini", ID: "x", Path: "/a/a-stub.jsonl", Messages: msgs(1)},
		{Harness: "gemini", ID: "x", Path: "/a/b-real.jsonl", Messages: msgs(12)},
		{Harness: "gemini", ID: "x", Path: "/a/c-old.json", Messages: msgs(5)},
	}
	for _, order := range [][]int{{0, 1, 2}, {0, 2, 1}, {1, 0, 2}, {1, 2, 0}, {2, 0, 1}, {2, 1, 0}} {
		var in []model.Session
		for _, i := range order {
			in = append(in, files[i])
		}
		kept := dedupeGeminiSessions(in)
		paths := make([]string, 0, len(kept))
		for _, s := range kept {
			paths = append(paths, s.Path)
		}
		sort.Strings(paths)
		if got := strings.Join(paths, " "); got != "/a/a-stub.jsonl /a/b-real.jsonl" {
			t.Errorf("read in order %v kept %s, want the stub and the real .jsonl", order, got)
		}
	}
}

func TestGeminiMessageTimeFallback(t *testing.T) {
	_, chats := geminiTree(t)
	doc := `{"sessionId":"s1","startTime":"2026-07-15T10:00:00.000Z","lastUpdated":"2026-07-15T10:00:00.000Z","messages":[{"id":"m1","type":"user","content":"no timestamp here"}]}`
	p := filepath.Join(chats, "session-s1.json")
	if err := os.WriteFile(p, []byte(doc), 0o644); err != nil {
		t.Fatal(err)
	}
	ss, err := ParseGeminiFile(p)
	if err != nil || len(ss) != 1 || len(ss[0].Messages) != 1 {
		t.Fatalf("ss=%#v err=%v", ss, err)
	}
	if ss[0].Messages[0].Time.IsZero() || ss[0].Messages[0].Time != ss[0].Started {
		t.Fatalf("message time not defaulted to start: %v vs %v", ss[0].Messages[0].Time, ss[0].Started)
	}
}

// On --resume Gemini writes its rebuilt history back as a $set snapshot, and
// that history leaves out user turns starting with <hook_context> — the
// prompts deja's per-prompt recall was attached to. The turn stays indexed
// (#4214). Shape from Gemini CLI 0.60.0.
func TestGeminiResumeSnapshotKeepsTheHookedPrompt(t *testing.T) {
	_, chats := geminiTree(t)
	lines := `{"sessionId":"sess-resume-1","projectHash":"abc","startTime":"2026-10-01T13:04:39.847Z","lastUpdated":"2026-10-01T13:04:39.847Z","kind":"main"}
{"$set":{"messages":[{"id":"ctx","timestamp":"2026-10-01T13:04:39.850Z","type":"user","content":[{"text":"<session_context>\nThis is the Gemini CLI.</session_context>"}]}],"lastUpdated":"2026-10-01T13:04:39.850Z"}}
{"id":"u1","timestamp":"2026-10-01T13:04:40.000Z","type":"user","content":[{"text":"<hook_context>&lt;deja-recall&gt;old&lt;/deja-recall&gt;</hook_context>\n\nthe tidewren migration fails on null defaults"}]}
{"id":"g1","timestamp":"2026-10-01T13:04:41.000Z","type":"gemini","content":"add NOT NULL DEFAULT ''"}
{"$set":{"sessionId":"sess-resume-1"}}
{"$set":{"messages":[{"id":"ctx","timestamp":"2026-10-01T13:04:39.850Z","type":"user","content":[{"text":"<session_context>\nThis is the Gemini CLI.</session_context>"}]},{"id":"g1","timestamp":"2026-10-01T13:04:41.000Z","type":"gemini","content":[{"text":"add NOT NULL DEFAULT ''"}]}],"lastUpdated":"2026-10-01T13:07:52.000Z"}}
{"id":"u2","timestamp":"2026-10-01T13:07:52.500Z","type":"user","content":[{"text":"what was the fix?"}]}
{"id":"g2","timestamp":"2026-10-01T13:07:55.000Z","type":"gemini","content":"NOT NULL DEFAULT ''"}
`
	p := filepath.Join(chats, "session-2026-10-01T13-04-sess-resume-1.jsonl")
	if err := os.WriteFile(p, []byte(lines), 0o644); err != nil {
		t.Fatal(err)
	}
	ss, err := ParseGeminiFile(p)
	if err != nil || len(ss) != 1 {
		t.Fatalf("parse: %v, %d sessions", err, len(ss))
	}
	all := make([]string, 0, len(ss[0].Messages))
	for _, m := range ss[0].Messages {
		all = append(all, m.Role+": "+m.Text)
	}
	got := strings.Join(all, " | ")
	if !strings.Contains(got, "tidewren migration fails") {
		t.Fatalf("the prompt before the resume was lost: %s", got)
	}
	if strings.Count(got, "add NOT NULL DEFAULT ''") != 1 || !strings.Contains(got, "what was the fix?") {
		t.Fatalf("turns doubled or missing after the snapshot: %s", got)
	}
	if i, j := strings.Index(got, "tidewren"), strings.Index(got, "what was the fix?"); i > j {
		t.Fatalf("turns out of order: %s", got)
	}
}

// Compression, a split tool turn and a turn rewritten under its id all reach
// the reader as Gemini's own history; the snapshot is taken in its order and
// nothing is doubled (#4214 review).
func TestGeminiSnapshotsFollowGeminisHistory(t *testing.T) {
	_, chats := geminiTree(t)
	parse := func(name, lines string) string {
		t.Helper()
		p := filepath.Join(chats, name)
		if err := os.WriteFile(p, []byte(lines), 0o644); err != nil {
			t.Fatal(err)
		}
		ss, err := ParseGeminiFile(p)
		if err != nil || len(ss) != 1 {
			t.Fatalf("parse %s: %v, %d sessions", name, err, len(ss))
		}
		all := make([]string, 0, len(ss[0].Messages))
		for _, m := range ss[0].Messages {
			all = append(all, m.Text)
		}
		return strings.Join(all, " | ")
	}
	head := `{"sessionId":"s-%s","projectHash":"abc","startTime":"2026-10-01T13:00:00.000Z","lastUpdated":"2026-10-01T13:00:00.000Z","kind":"main"}` + "\n"
	msg := func(id, typ, text string) string {
		return `{"id":"` + id + `","timestamp":"2026-10-01T13:00:01.000Z","type":"` + typ + `","content":[{"text":"` + text + `"}]}`
	}
	set := func(ms ...string) string { return `{"$set":{"messages":[` + strings.Join(ms, ",") + `]}}` + "\n" }

	// Compression: the kept tail comes back under new ids.
	got := parse("session-2026-10-01T13-00-s-compress.jsonl", strings.Replace(head, "%s", "compress", 1)+
		msg("u1", "user", "first question alpha")+"\n"+msg("g1", "gemini", "first answer alpha")+"\n"+
		msg("u2", "user", "second question beta")+"\n"+msg("g2", "gemini", "second answer beta")+"\n"+
		set(msg("n1", "user", "summary of earlier"), msg("n2", "user", "second question beta"), msg("n3", "gemini", "second answer beta"))+
		msg("u3", "user", "third")+"\n")
	if strings.Count(got, "second question beta") != 1 || !strings.HasSuffix(got, "third") {
		t.Errorf("compression doubled the tail: %s", got)
	}

	// A turn new to the snapshot lands where the snapshot puts it.
	got = parse("session-2026-10-01T13-00-s-split.jsonl", strings.Replace(head, "%s", "split", 1)+
		msg("q1", "user", "q one")+"\n"+msg("a1", "gemini", "a one")+"\n"+msg("q2", "user", "q two")+"\n"+
		set(msg("q1", "user", "q one"), msg("mid", "gemini", "the middle"), msg("a1", "gemini", "a one"), msg("q2", "user", "q two")))
	if got != "q one | the middle | a one | q two" {
		t.Errorf("snapshot order lost: %s", got)
	}

	// The same id written twice is one turn.
	got = parse("session-2026-10-01T13-00-s-rewrite.jsonl", strings.Replace(head, "%s", "rewrite", 1)+
		msg("q1", "user", "q one")+"\n"+msg("a1", "gemini", "thinking")+"\n"+msg("a1", "gemini", "the answer")+"\n")
	if got != "q one | the answer" {
		t.Errorf("a rewritten turn was kept twice: %s", got)
	}
}

// Gemini CLI 0.64 writes a sub-agent's run under chats/<parent id>/ and names
// it in the header; read without its parent, recall from the live parent
// answered with its own sub-agents (#5134).
func TestGeminiSubagentNamesItsParent(t *testing.T) {
	_, chats := geminiTree(t)
	parent := "f7b7a064-d884-4582-a228-e95a8c1ac4f3"
	dir := filepath.Join(chats, parent)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	child := filepath.Join(dir, "24b39ab6-b48a-48e4-80f9-0ba7bb24acc6.jsonl")
	lines := `{"sessionId":"24b39ab6-b48a-48e4-80f9-0ba7bb24acc6","projectHash":"abc","startTime":"2026-10-17T21:48:40.460Z","lastUpdated":"2026-10-17T21:48:40.460Z","kind":"subagent","directories":["/w/p"]}
{"id":"u1","timestamp":"2026-10-17T21:48:41.000Z","type":"user","content":[{"text":"check the release train history"}]}
{"id":"a1","timestamp":"2026-10-17T21:48:42.000Z","type":"gemini","content":"nothing found","model":"gemini-3"}
`
	if err := os.WriteFile(child, []byte(lines), 0o644); err != nil {
		t.Fatal(err)
	}
	main := filepath.Join(chats, "session-2026-10-17T21-47-f7b7a064.jsonl")
	if err := os.WriteFile(main, []byte(strings.Replace(strings.Replace(lines, "24b39ab6-b48a-48e4-80f9-0ba7bb24acc6", parent, 1), `"subagent"`, `"main"`, 1)), 0o644); err != nil {
		t.Fatal(err)
	}
	got := map[string]model.Session{}
	for _, p := range []string{child, main} {
		ss, err := ParseGeminiFile(p)
		if err != nil || len(ss) != 1 {
			t.Fatalf("%s: %v %#v", p, err, ss)
		}
		got[ss[0].ID] = ss[0]
	}
	if c := got["24b39ab6-b48a-48e4-80f9-0ba7bb24acc6"]; c.Kind != "subagent" || c.Parent != parent {
		t.Fatalf("sub-agent kind/parent = %q/%q, want subagent/%s", c.Kind, c.Parent, parent)
	}
	if m := got[parent]; m.Kind != "" || m.Parent != "" {
		t.Fatalf("the main session got kind/parent %q/%q", m.Kind, m.Parent)
	}
}

// Gemini keeps what a tool ran on the gemini record's toolCalls and what came
// back on the next user record's functionResponse; the reader read neither, so
// a Gemini store yielded no command, no tool output and no fix pair (#3293).
// Shapes from a real 2026-07 store.
func TestGeminiToolCallsBecomeCommandAndToolOutput(t *testing.T) {
	_, chats := geminiTree(t)
	lines := `{"sessionId":"sess-tools-1","projectHash":"abc","startTime":"2026-07-20T21:07:00.000Z","lastUpdated":"2026-07-20T21:08:00.000Z","kind":"main"}
{"id":"u1","timestamp":"2026-07-20T21:07:01.000Z","type":"user","content":[{"text":"run the tests"}]}
{"id":"g1","timestamp":"2026-07-20T21:07:10.000Z","type":"gemini","content":"","model":"gemini-3","toolCalls":[{"id":"run_shell_command__1","name":"run_shell_command","args":{"command":"go test ./..."},"result":[{"functionResponse":{"id":"run_shell_command__1","name":"run_shell_command","response":{"error":"Command: go test ./...\nExit Code: 1\nOutput: pkg/parser.go:42:9: undefined: frobnicateWidget\nFAIL"}}}]}]}
{"id":"u2","timestamp":"2026-07-20T21:07:12.000Z","type":"user","content":[{"functionResponse":{"id":"run_shell_command__1","name":"run_shell_command","response":{"error":"Command: go test ./...\nExit Code: 1\nOutput: pkg/parser.go:42:9: undefined: frobnicateWidget\nFAIL"}}}]}
{"id":"g2","timestamp":"2026-07-20T21:07:20.000Z","type":"gemini","content":"frobnicateWidget was renamed; fixing the call site.","model":"gemini-3"}
`
	p := filepath.Join(chats, "session-2026-07-20T21-07-sess-tools-1.jsonl")
	if err := os.WriteFile(p, []byte(lines), 0o644); err != nil {
		t.Fatal(err)
	}
	ss, err := ParseGeminiFile(p)
	if err != nil || len(ss) != 1 {
		t.Fatalf("parse: %v, %d sessions", err, len(ss))
	}
	var cmds, tool, users []string
	for _, m := range ss[0].Messages {
		switch m.Role {
		case RoleCommand:
			cmds = append(cmds, m.Text)
		case RoleToolOutput:
			tool = append(tool, m.Text)
		case "user":
			users = append(users, m.Text)
		}
	}
	if len(cmds) != 1 || !strings.Contains(cmds[0], "go test ./...") {
		t.Errorf("commands = %q, want the run_shell_command", cmds)
	}
	if len(tool) != 1 || !strings.Contains(tool[0], "undefined: frobnicateWidget") {
		t.Errorf("tool output = %q, want the failing command's error, once", tool)
	}
	if len(users) != 1 || users[0] != "run the tests" {
		t.Errorf("user turns = %q, want the typed prompt alone", users)
	}
}

// A failed run_shell_command carries its status the way a Codex or opencode
// command does (#4208). Shapes from Gemini CLI 0.60.0: the call on the gemini
// record with its result inline, and the result again on the next user record.
func TestGeminiFailedCommandCarriesItsExitCode(t *testing.T) {
	_, chats := geminiTree(t)
	lines := `{"sessionId":"sess-exit-1","projectHash":"abc","startTime":"2026-10-01T12:51:00.000Z","lastUpdated":"2026-10-01T12:51:10.000Z","kind":"main"}
{"id":"u1","timestamp":"2026-10-01T12:51:01.000Z","type":"user","content":[{"text":"show the last commit"}]}
{"id":"g1","timestamp":"2026-10-01T12:51:02.000Z","type":"gemini","content":"","model":"luna","toolCalls":[{"id":"run_shell_command__1","name":"run_shell_command","args":{"command":"git log --oneline -1","description":"log"},"result":[{"functionResponse":{"id":"run_shell_command__1","name":"run_shell_command","response":{"output":"<untrusted_context>\nOutput: fatal: your current branch 'master' does not have any commits yet\nExit Code: 128\nProcess Group PGID: 98713\n</untrusted_context>"}}}],"status":"success"},{"id":"run_shell_command__2","name":"run_shell_command","args":{"command":"git status --short"},"result":[{"functionResponse":{"id":"run_shell_command__2","name":"run_shell_command","response":{"output":"Output: (empty)\nProcess Group PGID: 98714"}}}],"status":"success"}]}
{"id":"u2","timestamp":"2026-10-01T12:51:03.000Z","type":"user","content":[{"functionResponse":{"id":"run_shell_command__1","name":"run_shell_command","response":{"output":"<untrusted_context>\nOutput: fatal: your current branch 'master' does not have any commits yet\nExit Code: 128\nProcess Group PGID: 98713\n</untrusted_context>"}}},{"functionResponse":{"id":"run_shell_command__2","name":"run_shell_command","response":{"output":"Output: (empty)\nProcess Group PGID: 98714"}}}]}
{"id":"g2","timestamp":"2026-10-01T12:51:04.000Z","type":"gemini","content":"No commits yet.","model":"luna"}
`
	p := filepath.Join(chats, "session-2026-10-01T12-51-sess-exit-1.jsonl")
	if err := os.WriteFile(p, []byte(lines), 0o644); err != nil {
		t.Fatal(err)
	}
	ss, err := ParseGeminiFile(p)
	if err != nil || len(ss) != 1 {
		t.Fatalf("parse: %v, %d sessions", err, len(ss))
	}
	var cmds []string
	for _, m := range ss[0].Messages {
		if m.Role == RoleCommand {
			cmds = append(cmds, m.Text)
		}
	}
	want := []string{"$ git log --oneline -1  → exit 128", "$ git status --short"}
	if strings.Join(cmds, "|") != strings.Join(want, "|") {
		t.Fatalf("commands = %q, want %q", cmds, want)
	}
}

// The status is the footer's, after the output: a command that prints an
// "Exit Code:" line of its own is not read by it, and an id Gemini reuses
// later does not carry an old command's status to a new one.
func TestGeminiExitCodeIsTheFootersOwn(t *testing.T) {
	cases := map[string]int{
		"Output: Exit Code: 3\nExit Code: 3\nok":                                                                                             0,
		"Output: building\nExit Code: 0\nfailed\nExit Code: 1\nProcess Group PGID: 9":                                                        1,
		"<untrusted_context>\nOutput: x\nExit Code: 2\nSignal: (none)\nBackground PIDs: (none)\nProcess Group PGID: 7\n</untrusted_context>": 2,
		"Output: (empty)\nProcess Group PGID: 98714":                                                                                         0,
	}
	for out, want := range cases {
		if got := geminiExitCode(out); got != want {
			t.Errorf("geminiExitCode(%q) = %d, want %d", out, got, want)
		}
	}

	_, chats := geminiTree(t)
	lines := `{"sessionId":"sess-exit-2","projectHash":"abc","startTime":"2026-10-01T12:51:00.000Z","lastUpdated":"2026-10-01T12:51:10.000Z","kind":"main"}
{"id":"g1","timestamp":"2026-10-01T12:51:02.000Z","type":"gemini","content":"","toolCalls":[{"id":"run_shell_command__1","name":"run_shell_command","args":{"command":"go test ./..."},"result":[{"functionResponse":{"id":"run_shell_command__1","name":"run_shell_command","response":{"output":"Output: ok\nProcess Group PGID: 1"}}}]}]}
{"id":"g2","timestamp":"2026-10-01T12:51:04.000Z","type":"gemini","content":"","toolCalls":[{"id":"run_shell_command__1","name":"run_shell_command","args":{"command":"ls nope"},"result":[{"functionResponse":{"id":"run_shell_command__1","name":"run_shell_command","response":{"output":"Output: ls: nope: No such file\nExit Code: 1\nProcess Group PGID: 2"}}}]}]}
`
	p := filepath.Join(chats, "session-2026-10-01T12-51-sess-exit-2.jsonl")
	if err := os.WriteFile(p, []byte(lines), 0o644); err != nil {
		t.Fatal(err)
	}
	ss, err := ParseGeminiFile(p)
	if err != nil || len(ss) != 1 {
		t.Fatalf("parse: %v, %d sessions", err, len(ss))
	}
	for _, m := range ss[0].Messages {
		if m.Role == RoleCommand && strings.Contains(m.Text, "go test") && strings.Contains(m.Text, "exit") {
			t.Fatalf("a reused id carried another call's status: %q", m.Text)
		}
	}
}

// Grok records what a tool ran in the tool_call event's rawInput; the reader
// kept the title and the output and dropped the input, so no Grok session ever
// yielded a command or a file record (#3285). Shape from the real store.
func TestGrokToolCallInputBecomesCommandAndFileRecords(t *testing.T) {
	root := t.TempDir()
	t.Setenv("DEJA_GROK_ROOT", root)
	dir := filepath.Join(root, "sessions", "%2Fw%2Fapp", "01a03432-4d92-7243-ab6c-8cdd1c697738")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	rows := []string{
		`{"timestamp":1787582100,"method":"session/update","params":{"sessionId":"01a03432-4d92-7243-ab6c-8cdd1c697738","update":{"sessionUpdate":"user_message_chunk","content":{"type":"text","text":"run the grok tests"}},"_meta":{"promptId":"p1","agentTimestampMs":1787582100000}}}`,
		`{"timestamp":1787582112,"method":"session/update","params":{"sessionId":"01a03432-4d92-7243-ab6c-8cdd1c697738","update":{"sessionUpdate":"tool_call","toolCallId":"call-1","title":"run_terminal_command","rawInput":{"command":"go test ./internal/sources/ -run Grok"},"_meta":{"x.ai/tool":{"version":1,"name":"run_terminal_command","kind":"run_terminal_command","namespace":"grok_build","label":"Run Terminal Command","read_only":false}}},"_meta":{"promptId":"p1","agentTimestampMs":1787582112336}}}`,
		`{"timestamp":1787582115,"method":"session/update","params":{"sessionId":"01a03432-4d92-7243-ab6c-8cdd1c697738","update":{"sessionUpdate":"tool_call_update","toolCallId":"call-1","status":"completed","content":[{"type":"content","content":{"type":"text","text":"ok  \tgithub.com/x/y\t0.4s"}}]},"_meta":{"promptId":"p1","agentTimestampMs":1787582115000}}}`,
		`{"timestamp":1787582120,"method":"session/update","params":{"sessionId":"01a03432-4d92-7243-ab6c-8cdd1c697738","update":{"sessionUpdate":"tool_call","toolCallId":"call-2","title":"read_file","rawInput":{"target_file":"/w/app/internal/sources/grok.go"},"_meta":{"x.ai/tool":{"version":1,"name":"read_file","kind":"read_file","namespace":"grok_build","label":"Read File","read_only":true}}},"_meta":{"promptId":"p1","agentTimestampMs":1787582120000}}}`,
	}
	if err := os.WriteFile(filepath.Join(dir, "updates.jsonl"), []byte(strings.Join(rows, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ss, err := ParseGrokFile(filepath.Join(dir, "updates.jsonl"))
	if err != nil || len(ss) != 1 {
		t.Fatalf("parse: %v, %d sessions", err, len(ss))
	}
	var cmds, files []string
	for _, m := range ss[0].Messages {
		switch m.Role {
		case RoleCommand:
			cmds = append(cmds, m.Text)
		case RoleFiles:
			files = append(files, m.Text)
		}
	}
	if len(cmds) != 1 || cmds[0] != "$ go test ./internal/sources/ -run Grok" {
		t.Errorf("commands = %q, want the run_terminal_command input", cmds)
	}
	if len(files) != 1 || !strings.Contains(files[0], "/w/app/internal/sources/grok.go") {
		t.Errorf("files = %q, want the read_file target", files)
	}
}

// bigGrokUpdates writes a session shaped like the one in #1321: a few thousand
// ACP lines, most of them tool events carrying large payloads.
func bigGrokUpdates(t *testing.T, lines int) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "sessions", "proj", "01a00feb")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	summary := `{"info":{"id":"01a00feb","cwd":"/work/app"},"generated_title":"Fix the parser","created_at":"2026-07-01T10:00:00Z","updated_at":"2026-07-01T12:00:00Z"}`
	if err := os.WriteFile(filepath.Join(dir, "summary.json"), []byte(summary), 0o644); err != nil {
		t.Fatal(err)
	}
	payload := strings.Repeat("build output line with paths and errors ", 100)
	var b strings.Builder
	for i := range lines {
		switch i % 20 {
		case 0:
			fmt.Fprintf(&b, `{"timestamp":%d,"method":"session/update","params":{"update":{"sessionUpdate":"user_message_chunk","content":{"type":"text","text":"question %d about the build"},"_meta":{"promptIndex":%d}}}}`+"\n", 1782900000+i, i, i)
		case 1:
			fmt.Fprintf(&b, `{"timestamp":%d,"method":"session/update","params":{"update":{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"answer %d"}},"_meta":{"promptId":"p%d"}}}`+"\n", 1782900000+i, i, i)
		default:
			fmt.Fprintf(&b, `{"timestamp":%d,"method":"session/update","params":{"update":{"sessionUpdate":"tool_call","toolCallId":"call_%d","title":"bash","kind":"execute","status":"completed","content":[{"type":"content","content":{"type":"text","text":%q}}]}}}`+"\n", 1782900000+i, i, payload)
		}
	}
	p := filepath.Join(dir, "updates.jsonl")
	if err := os.WriteFile(p, []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func parseGrokTiming(t *testing.T, lines int) (time.Duration, map[string]int) {
	t.Helper()
	p := bigGrokUpdates(t, lines)
	started := time.Now()
	ss, err := ParseGrokFile(p)
	took := time.Since(started)
	if err != nil {
		t.Fatal(err)
	}
	roles := map[string]int{}
	for _, s := range ss {
		for _, m := range s.Messages {
			roles[m.Role]++
		}
	}
	return took, roles
}

// The session shape from #1321 is mostly tool events, and the parser used to
// skip those lines before decoding them — which is why it was cheap and why it
// found nothing. Indexing the run costs reading the run.
//
// The bar is linearity rather than a stopwatch reading: this suite runs under
// `-race` on CI, where the same parse takes 6 s instead of 0.17 s, so an
// absolute bound measures the machine. Four times the lines should cost about
// four times as much; sixteen would mean something has gone quadratic.
func TestGrokIndexesTheRunWithoutGoingQuadratic(t *testing.T) {
	// The best of three pairs, not one. A ratio of two short timings on a
	// shared runner measures the runner: the same parse is a steady 4.0x here
	// and came back 8.6x once on the windows leg, where the 500-line baseline
	// read 62ms against 370ms on this machine — the small number is the one
	// noise distorts, and it distorts the ratio upward. Taking the smallest
	// ratio cannot hide a parse that has gone quadratic, because every pair
	// would read about 16x.
	best := math.Inf(1)
	var roles map[string]int
	for range 3 {
		small, _ := parseGrokTiming(t, 500)
		large, r := parseGrokTiming(t, 2000)
		roles = r
		t.Logf("500 lines in %v, 2000 lines in %v; roles=%v", small, large, roles)
		if small <= 0 {
			continue
		}
		if ratio := float64(large) / float64(small); ratio < best {
			best = ratio
		}
	}
	if roles[RoleToolOutput] == 0 {
		t.Fatal("the run is not indexed: no tool records from a session that is mostly tool events")
	}
	if math.IsInf(best, 1) {
		t.Skip("timer resolution too coarse to compare")
	}
	if best > 8 {
		t.Errorf("4x the lines cost %.1fx the time at best of three, which is not linear", best)
	}
}

// Grok Build 1.0.41 writes "session_kind": "headless" for every `grok -p` run:
// a top-level session started from a shell, not a spawned one. Read as a
// kind, every scripted session showed up as a subagent whose parent was lost
// (#4585). A spawned kind is still kept.
func TestGrokHeadlessSessionIsNotASpawn(t *testing.T) {
	for _, tc := range []struct{ kind, want string }{
		{"headless", ""},
		{"subagent_fork", "subagent_fork"},
	} {
		_, updates := grokTree(t)
		summary := `{"info":{"id":"019f-grok-session","cwd":"/work/cool-app"},"session_kind":"` + tc.kind + `","created_at":"2026-07-01T10:00:00Z"}`
		if err := os.WriteFile(filepath.Join(filepath.Dir(updates), "summary.json"), []byte(summary), 0o644); err != nil {
			t.Fatal(err)
		}
		line := `{"timestamp":1782900001,"method":"session/update","params":{"update":{"sessionUpdate":"user_message_chunk","content":{"type":"text","text":"run the tests"}}}}` + "\n"
		if err := os.WriteFile(updates, []byte(line), 0o644); err != nil {
			t.Fatal(err)
		}
		ss, err := ParseGrokFile(updates)
		if err != nil || len(ss) != 1 {
			t.Fatalf("%s: %v %#v", tc.kind, err, ss)
		}
		if ss[0].Kind != tc.want {
			t.Errorf("session_kind %q read as kind %q, want %q", tc.kind, ss[0].Kind, tc.want)
		}
	}
}

// grokLineageSession writes summary.json and updates.jsonl for one session
// under root and returns the updates path.
func grokLineageSession(t *testing.T, root, id, summary, updates string) string {
	t.Helper()
	dir := filepath.Join(root, "sessions", url.PathEscape("/work/ledger"), id)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "summary.json"), []byte(summary), 0o644); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, "updates.jsonl")
	if err := os.WriteFile(p, []byte(updates), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// Grok Build 1.0.41: a --fork-session copy names its source and forked_at, a
// subagent names no parent and its parent's subagent_spawned record names
// both ends.
func TestGrokForkAndSubagentLineage(t *testing.T) {
	root := t.TempDir()
	t.Setenv("DEJA_GROK_ROOT", root)
	now := time.Now().UTC()
	at := func(d time.Duration) string { return now.Add(d).Format(time.RFC3339Nano) }
	turn := `{"timestamp":1,"method":"session/update","params":{"update":{"sessionUpdate":"user_message_chunk","content":{"type":"text","text":"hi"}}}}` + "\n"
	spawn := `{"timestamp":2,"method":"_x.ai/session/update","params":{"update":{"sessionUpdate":"subagent_spawned","parent_session_id":"p1","child_session_id":"c1"}}}` + "\n"

	grokLineageSession(t, root, "p1", `{"info":{"id":"p1","cwd":"/work/ledger"},"session_kind":"headless","created_at":"`+at(-time.Hour)+`"}`, turn+spawn)
	// A session created after the child cannot hold its spawn record.
	grokLineageSession(t, root, "late", `{"info":{"id":"late","cwd":"/work/ledger"},"created_at":"`+at(time.Hour)+`"}`,
		strings.ReplaceAll(spawn, `"p1"`, `"late"`))
	child := grokLineageSession(t, root, "c1", `{"info":{"id":"c1","cwd":"/work/ledger"},"session_kind":"subagent","created_at":"`+at(-time.Minute)+`"}`, turn)
	orphan := grokLineageSession(t, root, "c2", `{"info":{"id":"c2","cwd":"/work/ledger"},"session_kind":"subagent_fork","created_at":"`+at(-time.Minute)+`"}`, turn)
	fork := grokLineageSession(t, root, "f1", `{"info":{"id":"f1","cwd":"/work/ledger"},"session_kind":"headless","parent_session_id":"p1","forked_at":"`+at(-time.Minute)+`"}`, turn)

	for _, tc := range []struct{ path, kind, parent string }{
		{child, "subagent", "p1"},
		{orphan, "subagent_fork", ""},
		{fork, "fork", "p1"},
	} {
		ss, err := ParseGrokFile(tc.path)
		if err != nil || len(ss) != 1 {
			t.Fatalf("%s: %v %#v", tc.path, err, ss)
		}
		if ss[0].Kind != tc.kind || ss[0].Parent != tc.parent {
			t.Errorf("%s read as kind %q parent %q, want %q %q", filepath.Base(filepath.Dir(tc.path)), ss[0].Kind, ss[0].Parent, tc.kind, tc.parent)
		}
	}
	// Found once, the parent is served from memory.
	if p := grokSpawner(child, "c1", ""); p != "p1" {
		t.Errorf("grokSpawner(c1) = %q, want p1", p)
	}
	if p := grokSpawner(child, "", ""); p != "" {
		t.Errorf("grokSpawner with no child id = %q", p)
	}

	// By id alone, as a prompt hook or MCP call names the asker.
	if s, ok := GrokSessionHead("c1", ""); !ok || s.Parent != "p1" || s.Kind != "subagent" {
		t.Errorf("GrokSessionHead(c1) = %+v %v", s, ok)
	}
	if s, ok := GrokSessionHead("f1", fork); !ok || s.Parent != "p1" || s.Kind != "fork" {
		t.Errorf("GrokSessionHead(f1, transcript) = %+v %v", s, ok)
	}
	for _, id := range []string{"", " ", "nope", "..", "../c1", "p1"} {
		transcript := ""
		if id == "p1" {
			transcript = child // names another session's file
		}
		if s, ok := GrokSessionHead(id, transcript); ok && id != "p1" {
			t.Errorf("GrokSessionHead(%q) = %+v, want none", id, s)
		} else if id == "p1" && ok {
			t.Errorf("GrokSessionHead(p1) took c1's summary: %+v", s)
		}
	}
	if IsGrokTranscript(filepath.Join(filepath.Dir(child), "chat_history.jsonl")) {
		t.Error("chat_history.jsonl read as a grok transcript")
	}
}

// chat_history.jsonl is what Grok sends the model; text a message carried is
// in it JSON-encoded once, and a tool output quoting it is encoded twice.
func TestGrokChatHolds(t *testing.T) {
	root := t.TempDir()
	t.Setenv("DEJA_GROK_ROOT", root)
	updates := grokLineageSession(t, root, "s1", `{"info":{"id":"s1","cwd":"/work/ledger"}}`, "")
	chat := filepath.Join(filepath.Dir(updates), "chat_history.jsonl")
	marker := "<deja-recall>\nThe sessions below are "
	if GrokChatHolds(updates, marker) {
		t.Error("no chat_history.jsonl, yet it holds the marker")
	}
	// Past the first read chunk, so a match across the boundary is found.
	pad := bytes.Repeat([]byte("x"), 256<<10-10)
	line := `{"type":"user","content":[{"type":"text","text":"` + string(pad) + `<deja-recall>\nThe sessions below are here"}]}` + "\n"
	if err := os.WriteFile(chat, []byte(line), 0o644); err != nil {
		t.Fatal(err)
	}
	if !GrokChatHolds(updates, marker) {
		t.Error("the marker across a read boundary was missed")
	}
	quoted := `{"type":"tool_result","content":"{\"text\":\"<deja-recall>\\nThe sessions below are \"}"}` + "\n"
	if err := os.WriteFile(chat, []byte(quoted), 0o644); err != nil {
		t.Fatal(err)
	}
	if GrokChatHolds(updates, marker) {
		t.Error("a tool output quoting the marker counted")
	}
	if GrokChatHolds(chat, marker) {
		t.Error("a path that is not a grok transcript was read")
	}
}

// grokShapeDB writes the same two messages with whichever stamp shape the
// caller names — grokDBTime reads all of them, so the filter has to as well.
func grokShapeDB(t *testing.T, early, late string) string {
	t.Helper()
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skip("sqlite3 not installed")
	}
	db := filepath.Join(t.TempDir(), "grok.db")
	schema := `
CREATE TABLE workspaces (id TEXT PRIMARY KEY, canonical_path TEXT);
CREATE TABLE sessions (id TEXT PRIMARY KEY, workspace_id TEXT, title TEXT, cwd_last TEXT, created_at TEXT);
CREATE TABLE messages (session_id TEXT, seq INTEGER, role TEXT, message_json TEXT, created_at TEXT);
INSERT INTO sessions VALUES ('s1','w1','Pool timeouts','/work/api','` + early + `');
INSERT INTO messages VALUES ('s1',0,'user','{"role":"user","content":"connection pool exhausted again"}','` + early + `');
INSERT INTO messages VALUES ('s1',1,'assistant','{"role":"assistant","content":[{"type":"text","text":"raise max_conns"}]}','` + late + `');
`
	cmd := exec.CommandContext(t.Context(), "sqlite3", db)
	cmd.Stdin = stringsReader(schema)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("seed: %v: %s", err, out)
	}
	return db
}

// The filter compared strings while the reader parsed three shapes, so a store
// writing sqlite's own "2026-07-27 15:29:47" had every same-day message read
// as older than the watermark: a space sorts below the T of an RFC3339 string.
// Nothing reached the index until the date rolled over (#2150, the shape #2030
// fixed for goose).
func TestGrokSinceHandlesEveryStampShapeItReads(t *testing.T) {
	for _, shape := range []struct {
		name, early, late string
	}{
		{"rfc3339", "2026-07-27T10:00:00.000Z", "2026-07-27T15:29:47.000Z"},
		{"sqlite", "2026-07-27 10:00:00", "2026-07-27 15:29:47"},
		// A stamp carrying an offset rather than Z: normalising both sides
		// converts it, where a text comparison reads the digits as written.
		{"offset", "2026-07-27T13:00:00+03:00", "2026-07-27T18:29:47+03:00"},
	} {
		t.Run(shape.name, func(t *testing.T) {
			db := grokShapeDB(t, shape.early, shape.late)
			count := func(cut string) int {
				at, err := time.Parse(time.RFC3339, cut)
				if err != nil {
					t.Fatal(err)
				}
				ss, err := ParseGrokDBSince(db, at)
				if err != nil {
					t.Fatal(err)
				}
				n := 0
				for _, s := range ss {
					n += len(s.Messages)
				}
				return n
			}
			// The premise: both messages are there when nothing is filtered.
			ss, err := ParseGrokDBSince(db, time.Time{})
			if err != nil {
				t.Fatal(err)
			}
			if len(ss) != 1 || len(ss[0].Messages) != 2 {
				t.Fatalf("unfiltered read: %d session(s), want one with two messages", len(ss))
			}
			if n := count("2026-07-27T09:00:00Z"); n != 2 {
				t.Errorf("a watermark earlier the same day returned %d messages, want both", n)
			}
			// Whole, not just the later message: the cursor selects sessions,
			// because what comes back replaces what the index holds for that
			// session (#2075).
			if n := count("2026-07-27T12:00:00Z"); n != 2 {
				t.Errorf("a watermark between the two returned %d messages, want the session whole", n)
			}
			if n := count("2026-07-27T16:00:00Z"); n != 0 {
				t.Errorf("a watermark after both returned %d messages, want none", n)
			}
			// The boundary: a message stamped at the watermark comes back
			// once more rather than being skipped. The clause compares
			// through strftime's %f, which is milliseconds, so the watermark
			// is backed off by one — offering a message twice is the side of
			// that error worth being on (#2155).
			if n := count("2026-07-27T15:29:47Z"); n < 1 {
				t.Errorf("a watermark exactly at the later message returned %d, want it offered once more", n)
			}
		})
	}
}

// A stamp sqlite cannot read normalises to null. Such a row comes back on
// every incremental pass rather than disappearing from all of them: an
// unreadable stamp is a reason to look at a message, not to hide it — the rule
// zed's reader already follows. It brings its session with it, which is how
// every selected session travels.
func TestGrokSinceKeepsAMessageWithAnUnreadableStamp(t *testing.T) {
	db := grokShapeDB(t, "2026-07-27 10:00:00", "not a timestamp at all")
	at, err := time.Parse(time.RFC3339, "2026-07-27T16:00:00Z")
	if err != nil {
		t.Fatal(err)
	}
	ss, err := ParseGrokDBSince(db, at)
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, s := range ss {
		n += len(s.Messages)
	}
	if n != 2 {
		t.Errorf("%d message(s) came back, want the session holding the one whose stamp cannot be read", n)
	}
}

func grokTree(t *testing.T) (root, updates string) {
	t.Helper()
	root = t.TempDir()
	t.Setenv("DEJA_GROK_ROOT", root)
	group := url.PathEscape("/work/cool-app")
	dir := filepath.Join(root, "sessions", group, "019f-grok-session")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	return root, filepath.Join(dir, "updates.jsonl")
}

func TestParseGrokFile(t *testing.T) {
	_, updates := grokTree(t)
	summary := `{"info":{"id":"019f-grok-session","cwd":"/work/cool-app"},"session_summary":"Fallback title","generated_title":"Fix the parser","created_at":"2026-07-01T10:00:00Z","updated_at":"2026-07-01T10:00:06Z"}`
	if err := os.WriteFile(filepath.Join(filepath.Dir(updates), "summary.json"), []byte(summary), 0o644); err != nil {
		t.Fatal(err)
	}
	lines := `{"timestamp":1782900001,"method":"session/update","params":{"update":{"sessionUpdate":"user_message_chunk","content":{"type":"text","text":"find grokneedle"},"_meta":{"promptIndex":0}}}}
{"timestamp":1782900002,"method":"session/update","params":{"update":{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"first "}},"_meta":{"promptId":"p1"}}}
{"timestamp":1782900003,"method":"session/update","params":{"update":{"sessionUpdate":"agent_thought_chunk","content":{"type":"text","text":"private reasoning"}}}}
{"timestamp":1782900004,"method":"session/update","params":{"update":{"sessionUpdate":"tool_call","content":{"type":"text","text":"tool noise"}}}}
{"timestamp":1782900005,"method":"session/update","params":{"update":{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"answer"}},"_meta":{"promptId":"p1"}}}
{malformed
`
	if err := os.WriteFile(updates, []byte(lines), 0o644); err != nil {
		t.Fatal(err)
	}
	ss, err := ParseGrokFile(updates)
	if err != nil {
		t.Fatal(err)
	}
	if len(ss) != 1 || ss[0].ID != "019f-grok-session" || ss[0].Harness != "grok" {
		t.Fatalf("bad session: %#v", ss)
	}
	if ss[0].Project != "work/cool-app" || ss[0].Title != "Fix the parser" {
		t.Fatalf("bad metadata: %#v", ss[0])
	}
	// Speech and the tool call between the two spoken chunks: the run is what
	// `--role tool` searches, and dropping it left Grok users with a transcript
	// that read as complete (#1321).
	if len(ss[0].Messages) != 3 {
		t.Fatalf("messages = %d, want 3: %#v", len(ss[0].Messages), ss[0].Messages)
	}
	if ss[0].Messages[0].Role != "user" || ss[0].Messages[0].Text != "find grokneedle" {
		t.Fatalf("bad user message: %#v", ss[0].Messages[0])
	}
	if ss[0].Messages[1].Role != "assistant" || ss[0].Messages[1].Text != "first answer" {
		t.Fatalf("assistant chunks not merged: %#v", ss[0].Messages[1])
	}
	if ss[0].Messages[2].Role != RoleToolOutput || ss[0].Messages[2].Text != "tool noise" {
		t.Fatalf("tool call not indexed: %#v", ss[0].Messages[2])
	}
}

func TestGrokDiscoveryAndRootOverrides(t *testing.T) {
	root, updates := grokTree(t)
	if err := os.WriteFile(updates, []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(filepath.Dir(updates), "chat_history.jsonl"), []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	files := GrokSessionFiles()
	if len(files) != 1 || files[0] != updates {
		t.Fatalf("files = %v, want [%s]", files, updates)
	}

	home := t.TempDir()
	t.Setenv("GROK_HOME", home)
	if GrokHome() != home {
		t.Fatalf("GROK_HOME ignored: %q", GrokHome())
	}
	if GrokRoot() != root {
		t.Fatalf("DEJA_GROK_ROOT ignored: %q", GrokRoot())
	}
	t.Setenv("DEJA_GROK_ROOT", "")
	if GrokRoot() != home {
		t.Fatalf("GrokRoot fallback = %q, want %q", GrokRoot(), home)
	}
}

func TestGrokCWDFromEncodedGroupAndMarker(t *testing.T) {
	_, updates := grokTree(t)
	if got := GrokCWDForSession(updates); got != "/work/cool-app" {
		t.Fatalf("encoded cwd = %q", got)
	}
	group := filepath.Dir(filepath.Dir(updates))
	if err := os.WriteFile(filepath.Join(group, ".cwd"), []byte("/a/very/long/project\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := GrokCWDForSession(updates); got != "/a/very/long/project" {
		t.Fatalf("marker cwd = %q", got)
	}
}

func grokSessionWith(t *testing.T, lines string) []string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "sessions", "proj", "01a00feb")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	summary := `{"info":{"id":"01a00feb","cwd":"/work/app"},"generated_title":"Fix the build","created_at":"2026-07-01T10:00:00Z","updated_at":"2026-07-01T12:00:00Z"}`
	if err := os.WriteFile(filepath.Join(dir, "summary.json"), []byte(summary), 0o644); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, "updates.jsonl")
	if err := os.WriteFile(p, []byte(lines), 0o644); err != nil {
		t.Fatal(err)
	}
	ss, err := ParseGrokFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if len(ss) != 1 {
		t.Fatalf("got %d sessions, want 1", len(ss))
	}
	out := make([]string, 0, len(ss[0].Messages))
	for _, m := range ss[0].Messages {
		out = append(out, m.Role+": "+m.Text)
	}
	return out
}

// Grok writes the whole run into updates.jsonl and the parser took only the
// talk, so `--role tool` found nothing and `deja show` returned a conversation
// that looked complete (#1321). The real events carry their output in an ACP
// content array.
func TestGrokToolCallsAreIndexed(t *testing.T) {
	lines := `{"timestamp":1782900001,"method":"session/update","params":{"update":{"sessionUpdate":"user_message_chunk","content":{"type":"text","text":"why does the build fail"},"_meta":{"promptIndex":0}}}}
{"timestamp":1782900002,"method":"session/update","params":{"update":{"sessionUpdate":"tool_call","toolCallId":"call_1","title":"go build ./...","kind":"execute","status":"pending"}}}
{"timestamp":1782900003,"method":"session/update","params":{"update":{"sessionUpdate":"tool_call_update","toolCallId":"call_1","status":"completed","content":[{"type":"content","content":{"type":"text","text":"undefined: parseThing"}}]}}}
{"timestamp":1782900004,"method":"session/update","params":{"update":{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"the helper was renamed"}},"_meta":{"promptId":"p1"}}}
`
	got := grokSessionWith(t, lines)
	want := []string{
		"user: why does the build fail",
		"tool-output: execute: go build ./...",
		"tool-output: undefined: parseThing",
		"assistant: the helper was renamed",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("got:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

// The private reasoning stream stays out, as it always has.
func TestGrokThoughtsStayOut(t *testing.T) {
	lines := `{"timestamp":1782900001,"method":"session/update","params":{"update":{"sessionUpdate":"agent_thought_chunk","content":{"type":"text","text":"private reasoning"}}}}
{"timestamp":1782900002,"method":"session/update","params":{"update":{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"said out loud"}},"_meta":{"promptId":"p1"}}}
`
	for _, line := range grokSessionWith(t, lines) {
		if strings.Contains(line, "private reasoning") {
			t.Errorf("thought chunk was indexed: %q", line)
		}
	}
}

// A streamed reply is joined into one message, and a tool call landing between
// its chunks must not split it.
func TestGrokChunksStillJoinAcrossAToolCall(t *testing.T) {
	lines := `{"timestamp":1782900001,"method":"session/update","params":{"update":{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"first "}},"_meta":{"promptId":"p1"}}}
{"timestamp":1782900002,"method":"session/update","params":{"update":{"sessionUpdate":"tool_call","toolCallId":"call_1","title":"ls","kind":"execute"}}}
{"timestamp":1782900003,"method":"session/update","params":{"update":{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"second"}},"_meta":{"promptId":"p1"}}}
`
	got := grokSessionWith(t, lines)
	want := []string{"assistant: first second", "tool-output: execute: ls"}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("got:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

// Two replies streaming at once: the join keys on the prompt id, so chunks of
// different replies stay separate. What must not happen is a chunk landing in
// the wrong message — the join now writes into a remembered index rather than
// into whatever was appended last.
func TestGrokInterleavedRepliesDoNotMix(t *testing.T) {
	lines := `{"timestamp":1782900001,"method":"session/update","params":{"update":{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"one "}},"_meta":{"promptId":"p1"}}}
{"timestamp":1782900002,"method":"session/update","params":{"update":{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"two "}},"_meta":{"promptId":"p2"}}}
{"timestamp":1782900003,"method":"session/update","params":{"update":{"sessionUpdate":"tool_call","toolCallId":"c1","title":"ls","kind":"execute"}}}
{"timestamp":1782900004,"method":"session/update","params":{"update":{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"two more"}},"_meta":{"promptId":"p2"}}}
`
	got := grokSessionWith(t, lines)
	want := []string{
		"assistant: one ",
		"assistant: two two more",
		"tool-output: execute: ls",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("got:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

func grokTestDB(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skip("sqlite3 not installed")
	}
	db := filepath.Join(t.TempDir(), "grok.db")
	schema := `
CREATE TABLE workspaces (id TEXT PRIMARY KEY, canonical_path TEXT);
CREATE TABLE sessions (id TEXT PRIMARY KEY, workspace_id TEXT, title TEXT, cwd_last TEXT, created_at TEXT);
CREATE TABLE messages (session_id TEXT, seq INTEGER, role TEXT, message_json TEXT, created_at TEXT);
INSERT INTO sessions VALUES ('s1','w1','Pool timeouts','/work/api','2026-07-27T10:00:00.000Z');
INSERT INTO messages VALUES ('s1',0,'user','{"role":"user","content":"connection pool exhausted again"}','2026-07-27T10:00:00.000Z');
INSERT INTO messages VALUES ('s1',1,'assistant','{"role":"assistant","content":[{"type":"text","text":"raise max_conns"},{"type":"tool_use","id":"t1"}]}','2026-07-27T10:00:05.000Z');
INSERT INTO messages VALUES ('s1',2,'tool','{"role":"tool","content":"ignored"}','2026-07-27T10:00:06.000Z');
INSERT INTO messages VALUES ('s1',3,'assistant','{"role":"assistant","content":[{"type":"tool_use","id":"t2"}]}','2026-07-27T10:00:07.000Z');
INSERT INTO sessions VALUES ('s0','w1','Yesterday','/work/api','2026-07-26T09:00:00.000Z');
INSERT INTO messages VALUES ('s0',0,'user','{"role":"user","content":"nothing touched this one again"}','2026-07-26T09:00:00.000Z');
`
	cmd := exec.CommandContext(t.Context(), "sqlite3", db)
	cmd.Stdin = stringsReader(schema)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("seed: %v: %s", err, out)
	}
	return db
}

func stringsReader(s string) *os.File {
	r, w, _ := os.Pipe()
	go func() {
		_, _ = w.WriteString(s)
		_ = w.Close()
	}()
	return r
}

func TestParseGrokDB(t *testing.T) {
	ss, err := ParseGrokDBSince(grokTestDB(t), time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	// Two: the fixture also holds the session the since filter has to leave
	// out, which is the one TestParseGrokDBSinceFilters is about.
	if len(ss) != 2 {
		t.Fatalf("sessions = %d", len(ss))
	}
	var s model.Session
	for _, got := range ss {
		if got.ID == "s1" {
			s = got
		}
	}
	// Project is the cwd's last two segments, not the whole path: trust policy
	// and per-project scoping key on the same name the file-based grok parser
	// produces (projectName), so /work/api scopes as "work/api".
	if s.Harness != "grok" || s.Project != "work/api" || s.Title != "Pool timeouts" {
		t.Fatalf("session metadata wrong: %+v", s)
	}
	// A user message carries a plain string, an assistant message an array of
	// blocks; tool traffic and block-less turns are not history worth indexing.
	if len(s.Messages) != 2 {
		t.Fatalf("messages = %d, want the user turn and the one with text: %+v", len(s.Messages), s.Messages)
	}
	if s.Messages[0].Text != "connection pool exhausted again" {
		t.Fatalf("user text = %q", s.Messages[0].Text)
	}
	if s.Messages[1].Text != "raise max_conns" {
		t.Fatalf("assistant text = %q, block extraction is wrong", s.Messages[1].Text)
	}
	if s.Started.IsZero() || s.Updated.Before(s.Started) {
		t.Fatalf("times wrong: started=%v updated=%v", s.Started, s.Updated)
	}
}

// The incremental path reindexes only what changed; a since filter that takes
// everything would rebuild the whole store on every write.
func TestParseGrokDBSinceFilters(t *testing.T) {
	db := grokTestDB(t)
	cut, err := time.Parse(time.RFC3339, "2026-07-27T10:00:02Z")
	if err != nil {
		t.Fatal(err)
	}
	ss, err := ParseGrokDBSince(db, cut)
	if err != nil {
		t.Fatal(err)
	}
	// The session it picks comes back whole — a partial return replaces what
	// the index holds for that session and would take the earlier turns with
	// it (#2075) — so what this asserts is that the new message is there.
	// Asserting nothing about it passed for months while the filter returned
	// nothing at all, because a missing quote made sqlite reject the query and
	// the error was swallowed as "no sessions".
	var sawNew bool
	for _, s := range ss {
		// The exclusion the cursor is for: a session with nothing newer than
		// the watermark stays out, or an incremental pass would rebuild the
		// whole store on every write.
		if s.ID == "s0" {
			t.Errorf("a session untouched since the watermark came back: %+v", s.Messages)
		}
		for _, m := range s.Messages {
			if m.Text == "raise max_conns" {
				sawNew = true
			}
		}
	}
	if !sawNew {
		t.Fatal("since filter returned nothing; incremental indexing would never pick up new messages")
	}
}

func TestParseGrokDBIgnoresMissingStore(t *testing.T) {
	ss, err := ParseGrokDBSince(filepath.Join(t.TempDir(), "absent.db"), time.Time{})
	if err != nil || ss != nil {
		t.Fatalf("missing store: sessions=%v err=%v", ss, err)
	}
	// sqlite3 creates a database on open; a parser that let it would leave a
	// file behind and then index it forever.
	if _, err := os.Stat(filepath.Join(t.TempDir(), "absent.db")); !os.IsNotExist(err) {
		t.Fatal("parser created the store it was asked to read")
	}
}

// `deja doctor` names a package — `needs-sqlite3`, `needs-zstd` — for a store it
// found and could not read. For a harness the reader does not use that is
// advice for a problem they do not have, and the exclude file had no way to say
// so: it took project patterns only (#3499).
func TestAHarnessCanBeExcluded(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	t.Setenv("DEJA_EXCLUDE_PROJECTS", "")
	t.Setenv("DEJA_EXCLUDE_HARNESSES", "")
	path := filepath.Join(dir, "deja", "exclude")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(body string) {
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	write("")
	if HarnessExcluded("opencode") {
		t.Error("an empty exclude file excluded a store")
	}

	write("# stores this reader will never want read\nharness:opencode\nharness:Zed\n")
	for _, name := range []string{"opencode", "zed", "ZED"} {
		if !HarnessExcluded(name) {
			t.Errorf("%q is in the exclude file and was not excluded", name)
		}
	}
	if HarnessExcluded("claude") {
		t.Error("a store nobody named was excluded")
	}

	// The prefix keeps the two namespaces apart in both directions. A harness
	// line carries its prefix into the project matcher, where it matches no
	// project anyone has:
	write("harness:continue\n")
	for _, project := range []string{"continue", "continue-deploy", "work/continue-api"} {
		if ExcludedProject(project) {
			t.Errorf("a harness line excluded the project %q", project)
		}
	}
	// and a project pattern does not exclude the store of the same name, which
	// is the direction that would otherwise silently stop reading a harness.
	write("opencode\n")
	if HarnessExcluded("opencode") {
		t.Error("a project pattern excluded the store of that name")
	}
	if !ExcludedProject("work/opencode") {
		t.Error("the project pattern stopped working")
	}

	// The environment says the same thing, for a machine where the file is not
	// the place to put it — CI, a container, a one-off run.
	write("")
	t.Setenv("DEJA_EXCLUDE_HARNESSES", " Crush , goose ")
	for _, name := range []string{"crush", "goose"} {
		if !HarnessExcluded(name) {
			t.Errorf("DEJA_EXCLUDE_HARNESSES did not exclude %q", name)
		}
	}
	if HarnessExcluded("cline") {
		t.Error("the environment excluded a store it does not name")
	}
}

// hostileDir is what somebody else's machine looks like: a space, a percent
// octet that is not an escape, parentheses, a dot, and a non-ASCII name. Every
// parser fixture is read from under one.
//
// This is the class the Copilot Chat reader was wrong about — its resource
// paths were never percent-decoded, which nobody here could see because every
// path on this laptop is ASCII with no spaces, and a contributor with
// `C:\Users\me\my app` hit it immediately (#3498, #3505).
const hostileDir = "проект (v2.1) %20 name"

// Every registry fixture parses the same under a path nobody on this machine
// would have written.
func TestEveryRegistryFixtureParsesUnderAHostilePath(t *testing.T) {
	root := filepath.Clean(filepath.Join("..", ".."))
	hermeticSourcesEnv(t)
	registry := readFormatRegistry(t, filepath.Join(root, "docs", "registry", "registry.json"))
	for _, entry := range registry.Harnesses {

		t.Run(entry.ID, func(t *testing.T) {
			// The whole fixture tree moves, not the one file: several parsers
			// read a sibling — kimi's state.json, goose's session header — and
			// copying the file alone would test the copy rather than the path.
			work := filepath.Join(t.TempDir(), hostileDir)
			copyTree(t, filepath.Join(root, "fixtures"), filepath.Join(work, "fixtures"))
			for _, fixture := range entry.FixturePaths {
				copied := filepath.Join(work, filepath.FromSlash(fixture))
				if _, err := os.Stat(copied); err != nil {
					t.Fatalf("fixture did not survive the copy: %v", err)
				}
				sessions := parseRegistryFixtureIn(t, entry.ID, copied, work)
				validateRegistrySessions(t, entry.ID, sessions)
			}
		})
	}
}

func copyTree(t *testing.T, src, dst string) {
	t.Helper()
	err := filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, p)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		b, err := os.ReadFile(p) //nolint:gosec // the test copies its own fixture tree
		if err != nil {
			return err
		}
		return os.WriteFile(target, b, 0o644)
	})
	if err != nil {
		t.Fatal(err)
	}
}

// The incremental path re-reads a file from where it stopped. Resuming from
// the wrong offset either loses the first message after the boundary or
// duplicates one, and both are silent.
func TestQwenParsesFromAnOffset(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "session.jsonl")
	first := `{"type":"user","sessionId":"q1","timestamp":"2026-01-01T00:00:00Z","message":{"role":"user","parts":[{"text":"FIRSTLINE"}]}}` + "\n"
	second := `{"type":"user","sessionId":"q1","timestamp":"2026-01-01T00:01:00Z","message":{"role":"user","parts":[{"text":"SECONDLINE"}]}}` + "\n"
	if err := os.WriteFile(path, []byte(first+second), 0o644); err != nil {
		t.Fatal(err)
	}
	all, err := ParseQwenFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 1 || len(all[0].Messages) != 2 {
		t.Fatalf("full parse returned %+v", all)
	}
	// Resuming past the first line must yield only what came after it.
	tail, err := ParseQwenFileFromOffset(path, int64(len(first)))
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range tail {
		for _, m := range s.Messages {
			if strings.Contains(m.Text, "FIRSTLINE") {
				t.Fatalf("offset parse re-read an indexed line: %+v", s.Messages)
			}
		}
	}
	found := false
	for _, s := range tail {
		for _, m := range s.Messages {
			if strings.Contains(m.Text, "SECONDLINE") {
				found = true
			}
		}
	}
	if !found {
		t.Fatal("offset parse lost the message it was supposed to pick up")
	}
	// An offset past the end is what a truncated file looks like: nothing to
	// add, and no error for the caller to handle.
	if ss, err := ParseQwenFileFromOffset(path, 1<<20); err != nil || len(ss) != 0 {
		t.Fatalf("offset past the end: %+v %v", ss, err)
	}
}

func TestNotesParseFromOffsetSkipsWhatWasRead(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "notes.jsonl")
	first := `{"kind":"note","project":"p","text":"FIRSTNOTE","t":"2026-01-01T00:00:00Z"}` + "\n"
	second := `{"kind":"note","project":"p","text":"SECONDNOTE","t":"2026-01-01T00:01:00Z"}` + "\n"
	if err := os.WriteFile(path, []byte(first+second), 0o644); err != nil {
		t.Fatal(err)
	}
	tail, err := ParseNotesFileFromOffset(path, int64(len(first)))
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range tail {
		for _, m := range s.Messages {
			if strings.Contains(m.Text, "FIRSTNOTE") {
				t.Fatalf("offset parse re-read a note: %+v", s.Messages)
			}
		}
	}
}
