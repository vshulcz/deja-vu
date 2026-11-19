package sources

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/vshulcz/deja-vu/internal/model"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

const museTestID = "01a0ac0d-5355-7c41-bc77-3b55f0e77ea1"

// museRec is one record line as the CLI writes it.
func museRec(stream, payloadType string, payload map[string]any, secs int64) map[string]any {
	return map[string]any{
		"schema_version": 1,
		"id":             "rec",
		"stream":         map[string]any{"kind": "session", "id": stream},
		"sequence":       secs,
		"recorded_at":    int64(1_789_790_400_000_000) + secs*1_000_000,
		"record_type":    "event",
		"payload_type":   payloadType,
		"payload":        payload,
	}
}

func museRun(stream string, event map[string]any, secs int64) map[string]any {
	return museRec(stream, "runtime.session", map[string]any{"kind": "run", "run_id": "run-1", "event": event}, secs)
}

func museFrame(t *testing.T, children ...map[string]any) map[string]any {
	t.Helper()
	cs := make([]any, 0, len(children))
	for i, c := range children {
		b, err := json.Marshal(c)
		if err != nil {
			t.Fatal(err)
		}
		cs = append(cs, map[string]any{"child_index": i, "record_json": string(b)})
	}
	return map[string]any{"retained_frame": "session_permission_transaction", "frame_schema_version": 1, "children": cs}
}

func writeMuseLog(t *testing.T, dir string, lines ...map[string]any) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	for _, l := range lines {
		j, err := json.Marshal(l)
		if err != nil {
			t.Fatal(err)
		}
		b.Write(j)
		b.WriteByte('\n')
	}
	p := filepath.Join(dir, "session.jsonl")
	if err := os.WriteFile(p, []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func museConversation(t *testing.T) []map[string]any {
	t.Helper()
	return []map[string]any{
		museRec(museTestID, "runtime.session.metadata", map[string]any{
			"kind": "metadata", "record": map[string]any{"workspace_root": "/w/poollab", "model_id": "muse-spark-1.3"},
		}, 0),
		museFrame(t, museRec(museTestID, "runtime.session.permission_format_declared", map[string]any{"format": "profile_v1"}, 1)),
		museRec(museTestID, "session.name.changed", map[string]any{"session_id": museTestID, "new_name": "springtime-acrux"}, 2),
		museRun(museTestID, map[string]any{"kind": "started", "prompt": "the pool runs out under load, find the leak"}, 10),
		museRun(museTestID, map[string]any{"kind": "started", "task_id": "01a0ac0d-5905-7f72-94ab-1dd08a3f0e7f"}, 11),
		museRun(museTestID, map[string]any{"kind": "assistant_tool_calls_committed", "tool_calls": []any{
			map[string]any{"id": "fc_1", "call_id": "call_1", "name": "bash", "args": `{"command":"go test ./internal/pool","workdir":"/w/poollab"}`},
			map[string]any{"id": "fc_2", "call_id": "call_2", "name": "read_file", "args": `{"path":"/w/poollab/internal/pool/acquire.go","offset":1}`},
		}}, 12),
		museRun(museTestID, map[string]any{"kind": "tool_result_batch_committed", "results": []any{
			map[string]any{"tool_call_index": 0, "tool_call_id": "call_1", "text": `{"command":"go test ./internal/pool","exit_code":1,"terminal_status":"exited","output":"pool_test.go:88: leaked 4 connections","truncated":false}`},
			map[string]any{"tool_call_index": 1, "tool_call_id": "call_2", "text": "1|package pool"},
		}}, 13),
		museRun(museTestID, map[string]any{"kind": "assistant_tool_calls_committed", "tool_calls": []any{
			map[string]any{"id": "fc_3", "call_id": "call_3", "name": "edit_file", "args": `{"path":"/w/poollab/internal/pool/acquire.go","find":"defer conn.Close() // released on every path","replace":"defer pool.Put(conn) // returned to the pool on every path"}`},
		}}, 14),
		museRun(museTestID, map[string]any{"kind": "model_completed", "usage": map[string]any{"input_tokens": 3173}, "model": "muse-spark-1.3"}, 19),
		museRun(museTestID, map[string]any{"kind": "assistant_message_committed", "message_id": "m1", "text": "acquire closed the connection instead of returning it."}, 20),
		museRec(museTestID, "session.end", map[string]any{"kind": "session_end", "record": map[string]any{"exit_reason": "clean"}}, 30),
	}
}

func parseMuseOne(t *testing.T, path string) (s struct {
	ID, Project, Title, Kind, Parent string
	Started, Updated                 time.Time
	Roles, Texts                     []string
}) {
	t.Helper()
	ss, err := ParseMuseFile(path)
	if err != nil || len(ss) != 1 {
		t.Fatalf("parse: %v, %d sessions", err, len(ss))
	}
	got := ss[0]
	if got.Harness != "muse" {
		t.Fatalf("harness = %q", got.Harness)
	}
	s.ID, s.Project, s.Title, s.Kind, s.Parent = got.ID, got.Project, got.Title, got.Kind, got.Parent
	s.Started, s.Updated = got.Started, got.Updated
	for _, m := range got.Messages {
		s.Roles = append(s.Roles, m.Role)
		s.Texts = append(s.Texts, m.Text)
	}
	return s
}

// The person's turn is a run start with a prompt; a task start has the same
// kind and none. The id is the session stream's, the workspace the metadata's,
// the title the session's name, and recorded_at is microseconds.
func TestParseMuseSession(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "2026", "09", "18", museTestID)
	s := parseMuseOne(t, writeMuseLog(t, dir, museConversation(t)...))
	if s.ID != museTestID || s.Project != "w/poollab" || s.Title != "springtime-acrux" {
		t.Errorf("id/project/title = %q/%q/%q", s.ID, s.Project, s.Title)
	}
	if want := time.Unix(1_789_790_410, 0); !s.Started.Equal(want) {
		t.Errorf("started = %v, want %v (recorded_at is microseconds)", s.Started, want)
	}
	if want := time.Unix(1_789_790_420, 0); !s.Updated.Equal(want) {
		t.Errorf("updated = %v, want %v", s.Updated, want)
	}
	var users []string
	for i, r := range s.Roles {
		if r == "user" {
			users = append(users, s.Texts[i])
		}
	}
	if len(users) != 1 || users[0] != "the pool runs out under load, find the leak" {
		t.Errorf("user turns = %q, want the one prompt", users)
	}
	if last := s.Texts[len(s.Texts)-1]; s.Roles[len(s.Roles)-1] != "assistant" || last != "acquire closed the connection instead of returning it." {
		t.Errorf("last = %s %q", s.Roles[len(s.Roles)-1], last)
	}
}

// Tool calls come through as work records: the command with the exit code
// bash reported, the files named, the span edit_file replaced, and what the
// command printed rather than bash's JSON wrapper.
func TestParseMuseToolCalls(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "2026", "09", "18", museTestID)
	s := parseMuseOne(t, writeMuseLog(t, dir, museConversation(t)...))
	has := func(role, text string) bool {
		for i, r := range s.Roles {
			if r == role && s.Texts[i] == text {
				return true
			}
		}
		return false
	}
	for _, w := range []struct{ role, text string }{
		{RoleCommand, "$ go test ./internal/pool  → exit 1"},
		{RoleFiles, "/w/poollab/internal/pool/acquire.go"},
		{RoleToolOutput, "pool_test.go:88: leaked 4 connections"},
		{RoleToolOutput, "1|package pool"},
		{RoleEdit, "/w/poollab/internal/pool/acquire.go\ndefer conn.Close() // released on every path"},
	} {
		if !has(w.role, w.text) {
			t.Errorf("missing %s %q in %q", w.role, w.text, s.Texts)
		}
	}
	for _, text := range s.Texts {
		if strings.Contains(text, `"exit_code"`) {
			t.Errorf("bash's JSON wrapper indexed as output: %q", text)
		}
	}
}

// A retained frame holds whole records as strings; a prompt inside one is
// still the person's turn.
func TestParseMuseRetainedFrame(t *testing.T) {
	lines := museConversation(t)
	lines[3] = museFrame(t, lines[3])
	dir := filepath.Join(t.TempDir(), "2026", "09", "18", museTestID)
	s := parseMuseOne(t, writeMuseLog(t, dir, lines...))
	if len(s.Roles) == 0 || s.Roles[0] != "user" || s.Texts[0] != "the pool runs out under load, find the leak" {
		t.Errorf("first = %q %q, want the framed prompt", s.Roles, s.Texts)
	}
}

// A child's task run is logged in the parent under its own stream id, and its
// start carries the child's objective as a prompt. That is not the person.
func TestParseMuseSkipsOtherStreams(t *testing.T) {
	lines := append(museConversation(t),
		museRun("018f0000-0000-0000-0000-000000000001", map[string]any{"kind": "started", "prompt": "Role: demo-worker Objective: list the pool tests"}, 21),
		museRun("018f0000-0000-0000-0000-000000000001", map[string]any{"kind": "assistant_message_committed", "text": "child reply"}, 22))
	dir := filepath.Join(t.TempDir(), "2026", "09", "18", museTestID)
	s := parseMuseOne(t, writeMuseLog(t, dir, lines...))
	for _, text := range s.Texts {
		if strings.Contains(text, "demo-worker") || text == "child reply" {
			t.Errorf("another stream's record read into the session: %q", text)
		}
	}
}

// With no metadata record the route facts' cwd names the workspace.
func TestParseMuseRouteFactsCwd(t *testing.T) {
	lines := museConversation(t)[1:]
	lines = append([]map[string]any{museRec(museTestID, "runtime.session.route_facts", map[string]any{
		"kind": "route_facts", "record": map[string]any{"cwd": "/w/other"},
	}, 0)}, lines...)
	dir := filepath.Join(t.TempDir(), "2026", "09", "18", museTestID)
	if s := parseMuseOne(t, writeMuseLog(t, dir, lines...)); s.Project != "w/other" {
		t.Errorf("project = %q, want the route facts' cwd", s.Project)
	}
}

// Store discovery: XDG_DATA_HOME moves the store, DEJA_MUSE_ROOTS replaces
// it, and a child's log is listed unless DEJA_INCLUDE_SUBAGENTS=0. Nothing
// else in a session directory is a transcript.
func TestMuseSessionFiles(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("DEJA_MUSE_ROOTS", "")
	t.Setenv("XDG_DATA_HOME", "")
	t.Setenv("DEJA_INCLUDE_SUBAGENTS", "")
	if got, want := MuseRoots(), []string{filepath.Join(home, ".local", "share", "muse", "sessions")}; strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("default roots = %q, want %q", got, want)
	}
	xdg := filepath.Join(home, "xdg")
	t.Setenv("XDG_DATA_HOME", xdg)
	root := filepath.Join(xdg, "muse", "sessions")
	if got := MuseRoots(); len(got) != 1 || got[0] != root {
		t.Errorf("XDG roots = %q, want %q", got, root)
	}

	sess := filepath.Join(root, "2026", "09", "18", museTestID)
	parent := writeMuseLog(t, sess, museConversation(t)...)
	child := writeMuseLog(t, filepath.Join(sess, "subagent", "a8234fe2-23ee-4cc5-8e81-8908fe89b18d"), museConversation(t)...)
	for _, side := range []string{"cron.db", "goals.db", filepath.Join("tool-outputs", "call_1.txt")} {
		p := filepath.Join(sess, side)
		_ = os.MkdirAll(filepath.Dir(p), 0o755)
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if got := MuseSessionFiles(); len(got) != 2 {
		t.Errorf("files = %q, want both logs", got)
	}
	if !MuseSubagentFile(child) || MuseSubagentFile(parent) {
		t.Errorf("subagent file: child %v, parent %v", MuseSubagentFile(child), MuseSubagentFile(parent))
	}
	if got := MuseSidecarFiles(); len(got) != 3 {
		t.Errorf("sidecars = %q, want cron.db, goals.db and the spilled output", got)
	}
	if !isMuseSession(parent) || isMuseSession(filepath.Join(sess, "cron.db")) {
		t.Error("isMuseSession matched the wrong file")
	}

	t.Setenv("DEJA_INCLUDE_SUBAGENTS", "0")
	if got := MuseSessionFiles(); len(got) != 1 || got[0] != parent {
		t.Errorf("files with subagents off = %q, want the parent log only", got)
	}

	other := t.TempDir()
	t.Setenv("DEJA_MUSE_ROOTS", other+string(os.PathListSeparator)+root)
	if got := MuseRoots(); len(got) != 2 || got[0] != other {
		t.Errorf("DEJA_MUSE_ROOTS roots = %q", got)
	}
}

// A child's log is a session of its own, naming the one that spawned it.
func TestParseMuseSubagent(t *testing.T) {
	childID := "a8234fe2-23ee-4cc5-8e81-8908fe89b18d"
	conv := museConversation(t)
	lines := make([]map[string]any, 0, len(conv))
	for _, l := range conv {
		b := mustJSON(t, l)
		var c map[string]any
		_ = json.Unmarshal([]byte(strings.ReplaceAll(string(b), museTestID, childID)), &c)
		lines = append(lines, c)
	}
	dir := filepath.Join(t.TempDir(), "2026", "09", "18", museTestID, "subagent", childID)
	s := parseMuseOne(t, writeMuseLog(t, dir, lines...))
	if s.Kind != "subagent" || s.Parent != museTestID || s.ID != childID {
		t.Errorf("kind/parent/id = %q/%q/%q", s.Kind, s.Parent, s.ID)
	}
}

// museChildLog is a child's log as Muse 1.4.2 writes one: the permission
// records, then the run. A workflow child also records its task kind and the
// workspace it runs in; a reminder or verification observer records neither.
func museChildLog(t *testing.T, childID string, workflow bool, prompt string) []map[string]any {
	t.Helper()
	lines := []map[string]any{
		museFrame(t, museRec(childID, "runtime.session.permission_format_declared", map[string]any{"format": "profile_v1"}, 1)),
		museRec(childID, "runtime.session.metadata", map[string]any{"kind": "metadata", "record": map[string]any{"provider_id": "meta", "model_id": "muse-spark-1.3"}}, 2),
	}
	if workflow {
		lines = append(lines,
			museRec(childID, "runtime.session", map[string]any{"kind": "task", "task_id": "t1", "event": map[string]any{"kind": "proposed", "task_id": "t1", "task_kind": "workflow.agent.native"}}, 3),
			museRec(childID, "session.workspace_branch.observed", map[string]any{"kind": "workspace_branch", "record": map[string]any{
				"workspace_root": "/w/poollab", "reference": map[string]any{"kind": "branch", "name": "main"}, "vcs": "git", "dirty": false,
			}}, 4))
	}
	return append(lines,
		museRun(childID, map[string]any{"kind": "started", "prompt": prompt}, 10),
		museRun(childID, map[string]any{"kind": "assistant_message_committed", "text": "done"}, 11))
}

// Muse runs a reminder observer beside every session, and a verification one
// after tool use, as children of it. Their prompt is Muse's own instruction
// text, not something anyone asked for, so they are not sessions (#4711).
func TestParseMuseSkipsObserverChild(t *testing.T) {
	childID := "16c7cc3b-bcf0-4882-b9cc-53230679a4f3"
	dir := filepath.Join(t.TempDir(), "2026", "10", "05", museTestID, "subagent", childID)
	p := writeMuseLog(t, dir, museChildLog(t, childID, false, "You are a reminder observer for the main agent.\nDo not answer the user.")...)
	ss, err := ParseMuseFile(p)
	if err != nil || len(ss) != 0 {
		t.Fatalf("observer child parsed as %d sessions (err %v), want none", len(ss), err)
	}
}

// A workflow child names its workspace only in a workspace_branch record, and
// it is the child's project (#4712). It is delegated work, so it is kept.
func TestParseMuseWorkflowChildProject(t *testing.T) {
	childID := "4c579dbf-4e3a-796d-8bcb-de78af441dd5"
	dir := filepath.Join(t.TempDir(), "2026", "10", "05", museTestID, "subagent", childID)
	s := parseMuseOne(t, writeMuseLog(t, dir, museChildLog(t, childID, true, "Run make lint and report.")...))
	if s.Kind != "subagent" || s.Parent != museTestID || s.ID != childID {
		t.Errorf("kind/parent/id = %q/%q/%q", s.Kind, s.Parent, s.ID)
	}
	if s.Project != "w/poollab" {
		t.Errorf("project = %q, want the workspace_branch root", s.Project)
	}
}

// The workspace resume runs in is the session's own: a child's
// workspace_branch record logged in the parent under the child's stream is
// not it.
func TestMuseWorkspaceIgnoresOtherStreams(t *testing.T) {
	lines := museConversation(t)[1:]
	lines = append(lines, museRec("018f0000-0000-0000-0000-000000000001", "session.workspace_branch.observed",
		map[string]any{"kind": "workspace_branch", "record": map[string]any{"workspace_root": "/w/child-tree"}}, 21))
	dir := filepath.Join(t.TempDir(), "2026", "09", "18", museTestID)
	p := writeMuseLog(t, dir, lines...)
	if ws := MuseWorkspace(p); ws != "" {
		t.Errorf("workspace = %q, want none: the only one named is another stream's", ws)
	}
	full := writeMuseLog(t, filepath.Join(t.TempDir(), museTestID), museConversation(t)...)
	if ws := MuseWorkspace(full); ws != "/w/poollab" {
		t.Errorf("workspace = %q, want the metadata's", ws)
	}
}

func TestOmnigentParseAndSince(t *testing.T) {
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skip("sqlite3 not installed")
	}
	dir := t.TempDir()
	db := filepath.Join(dir, "omnigent.db")
	buildSQLiteFixture(t, filepath.Join("..", "..", "fixtures", "registry", "omnigent", "omnigent.sql"), db)
	t.Setenv("DEJA_OMNIGENT_DB", db)

	if got := OmnigentDB(); got != db {
		t.Fatalf("OmnigentDB() = %q, want %q", got, db)
	}
	ss := LoadOmnigent()
	if len(ss) != 1 {
		t.Fatalf("LoadOmnigent() got %d sessions, want 1", len(ss))
	}
	s := ss[0]
	if s.Harness != "omnigent" || s.ID != "0195a8c0-7b20-7000-8000-112233445566" {
		t.Fatalf("got harness=%q id=%q", s.Harness, s.ID)
	}
	if s.Title != "Pin queue consumer prefetch count" {
		t.Fatalf("Title = %q", s.Title)
	}
	if s.Project != "workspace/omnigent-worker" {
		t.Fatalf("Project = %q, want workspace/omnigent-worker", s.Project)
	}
	if len(s.Messages) < 3 {
		t.Fatalf("Messages = %d, want >= 3", len(s.Messages))
	}

	since, err := ParseOmnigentDBSince(db, time.Unix(1752915000, 0).UTC())
	if err != nil || len(since) != 1 {
		t.Fatalf("ParseOmnigentDBSince: len=%d err=%v", len(since), err)
	}
	zero, err := ParseOmnigentDBSince(db, time.Time{})
	if err != nil || len(zero) != 1 {
		t.Fatalf("ParseOmnigentDBSince zero: len=%d err=%v", len(zero), err)
	}
	after, err := ParseOmnigentDBSince(db, time.Unix(1752916000, 0).UTC())
	if err != nil || len(after) != 0 {
		t.Fatalf("ParseOmnigentDBSince future: len=%d err=%v", len(after), err)
	}
	if got := OmnigentSessionDir(db, "0195a8c0-7b20-7000-8000-112233445566"); got != "/workspace/omnigent-worker" {
		t.Fatalf("OmnigentSessionDir() = %q", got)
	}
	if got := OmnigentSessionDir(db, "missing"); got != "" {
		t.Fatalf("OmnigentSessionDir(missing) = %q", got)
	}
}

func TestOmnigentPathsAndHelpers(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("DEJA_OMNIGENT_ROOT", "")
	t.Setenv("OMNIGENT_DATA_DIR", "")
	t.Setenv("DEJA_OMNIGENT_DB", "")

	if got, want := OmnigentRoot(), filepath.Join(home, ".omnigent"); got != want {
		t.Fatalf("OmnigentRoot() default = %q, want %q", got, want)
	}
	t.Setenv("OMNIGENT_DATA_DIR", "~/omni-data")
	if got, want := OmnigentRoot(), filepath.Join(home, "omni-data"); got != want {
		t.Fatalf("OmnigentRoot() OMNIGENT_DATA_DIR = %q, want %q", got, want)
	}
	t.Setenv("DEJA_OMNIGENT_ROOT", "~/deja-omni")
	if got, want := OmnigentRoot(), filepath.Join(home, "deja-omni"); got != want {
		t.Fatalf("OmnigentRoot() DEJA_OMNIGENT_ROOT = %q, want %q", got, want)
	}

	missingDB := filepath.Join(t.TempDir(), "missing.db")
	t.Setenv("DEJA_OMNIGENT_DB", missingDB)
	if got := OmnigentFiles(); got != nil {
		t.Fatalf("OmnigentFiles() missing = %v, want nil", got)
	}
	emptyDB := filepath.Join(t.TempDir(), "empty.db")
	if err := os.WriteFile(emptyDB, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_OMNIGENT_DB", emptyDB)
	if got := OmnigentFiles(); got != nil {
		t.Fatalf("OmnigentFiles() empty = %v, want nil", got)
	}
	okDB := filepath.Join(t.TempDir(), "ok.db")
	if err := os.WriteFile(okDB, []byte("sqlite"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_OMNIGENT_DB", okDB)
	if got := OmnigentFiles(); len(got) != 1 || got[0] != okDB {
		t.Fatalf("OmnigentFiles() non-empty = %v, want [%q]", got, okDB)
	}

	if got := omnigentUUID("0195a8c07b2070008000112233445566"); got != "0195a8c0-7b20-7000-8000-112233445566" {
		t.Fatalf("omnigentUUID(32-hex) = %q", got)
	}
	if got := omnigentUUID("0195a8c07b207000800011223344556z"); got != "0195a8c07b207000800011223344556z" {
		t.Fatalf("omnigentUUID(non-hex) = %q", got)
	}
	if got := omnigentUUID(""); got != "" {
		t.Fatalf("omnigentUUID(empty) = %q", got)
	}

	for _, tc := range []struct {
		in   any
		want int
	}{
		{float64(1), 1},
		{json.Number("6"), 6},
		{"message", 1},
		{"function_call", 2},
		{"function_call_output", 3},
		{"compaction", 6},
		{"terminal_command", 11},
		{"other", 0},
		{nil, 0},
	} {
		if got := omnigentItemType(tc.in); got != tc.want {
			t.Errorf("omnigentItemType(%v) = %d, want %d", tc.in, got, tc.want)
		}
	}

	if got := omnigentContentText("str"); got != "str" {
		t.Fatalf("omnigentContentText(str) = %q", got)
	}
	if got := omnigentContentText([]any{"a", map[string]any{"input_text": "b"}, map[string]any{"output_text": "c"}}); got != "a\nb\nc" {
		t.Fatalf("omnigentContentText(slice) = %q", got)
	}
	if got := omnigentContentText(map[string]any{"text": "d"}); got != "d" {
		t.Fatalf("omnigentContentText(map) = %q", got)
	}
	if got := omnigentContentText(42); got != "" {
		t.Fatalf("omnigentContentText(42) = %q", got)
	}
}

func TestOmnigentMinimalSchemaAndItemTypes(t *testing.T) {
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skip("sqlite3 not installed")
	}
	dir := t.TempDir()
	missingDB := filepath.Join(dir, "missing.db")
	if ss, err := ParseOmnigentDB(missingDB); err != nil || ss != nil {
		t.Fatalf("ParseOmnigentDB(missing) = %v, %v", ss, err)
	}
	if got := OmnigentSessionDir(missingDB, "x"); got != "" {
		t.Fatalf("OmnigentSessionDir(missing) = %q", got)
	}

	unrelatedDB := filepath.Join(dir, "unrelated.db")
	cmd := exec.CommandContext(t.Context(), "sqlite3", unrelatedDB)
	cmd.Stdin = strings.NewReader("CREATE TABLE misc(id INT); INSERT INTO misc VALUES (1);")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("create unrelated db: %v: %s", err, out)
	}
	if omnigentDBHasTable(unrelatedDB) {
		t.Fatal("omnigentDBHasTable(unrelated) = true, want false")
	}
	if ss, err := ParseOmnigentDB(unrelatedDB); err != nil || ss != nil {
		t.Fatalf("ParseOmnigentDB(unrelated) = %v, %v", ss, err)
	}
	if got := OmnigentSessionDir(unrelatedDB, "x"); got != "" {
		t.Fatalf("OmnigentSessionDir(unrelated) = %q", got)
	}

	db := filepath.Join(dir, "minimal.db")
	sql := `
CREATE TABLE conversations(workspace_id TEXT, id TEXT PRIMARY KEY, created_at INTEGER, updated_at INTEGER, title TEXT, parent_conversation_id TEXT, fork_from_item_id TEXT);
CREATE TABLE conversation_items(workspace_id TEXT, conversation_id TEXT, position INTEGER, id TEXT, created_at INTEGER, type INTEGER, data TEXT);
INSERT INTO conversations VALUES ('ws1', '0195a8c07b2070008000000000000002', 1752915001, 1752915010, '', '0195a8c07b2070008000000000000001', '');
INSERT INTO conversation_items VALUES ('ws1', '0195a8c07b2070008000000000000002', 1, 'i1', 1752915001, 1, '{"role":"user","is_meta":true,"content":"ignored"}');
INSERT INTO conversation_items VALUES ('ws1', '0195a8c07b2070008000000000000002', 2, 'i2', 1752915002, 1, '{"role":"system","content":"ignored"}');
INSERT INTO conversation_items VALUES ('ws1', '0195a8c07b2070008000000000000002', 3, 'i3', 1752915003, 1, '{"role":"user","content":"Retry consumer loop\ndetails"}');
INSERT INTO conversation_items VALUES ('ws1', '0195a8c07b2070008000000000000002', 4, 'i4', 1752915004, 1, '{"role":"assistant","content":"Updated backoff"}');
INSERT INTO conversation_items VALUES ('ws1', '0195a8c07b2070008000000000000002', 5, 'i5', 1752915005, 2, '{"name":"edit_file","call_id":"c1","arguments":"{\"file_path\":\"/Users/alex/app/worker.py\",\"old_string\":\"1\",\"new_string\":\"2\"}"}');
INSERT INTO conversation_items VALUES ('ws1', '0195a8c07b2070008000000000000002', 6, 'i6', 1752915006, 3, '{"output":"patched worker.py"}');
INSERT INTO conversation_items VALUES ('ws1', '0195a8c07b2070008000000000000002', 7, 'i7', 1752915007, 6, '{"summary":"Compacted retry context"}');
INSERT INTO conversation_items VALUES ('ws1', '0195a8c07b2070008000000000000002', 8, 'i8', 1752915008, 11, '{"kind":"input","input":"pytest tests/test_worker.py"}');
INSERT INTO conversation_items VALUES ('ws1', '0195a8c07b2070008000000000000002', 9, 'i9', 1752915009, 11, '{"kind":"output","stdout":"1 passed","stderr":""}');
`
	cmd2 := exec.CommandContext(t.Context(), "sqlite3", db)
	cmd2.Stdin = strings.NewReader(sql)
	if out, err := cmd2.CombinedOutput(); err != nil {
		t.Fatalf("create minimal db: %v: %s", err, out)
	}

	t.Setenv("DEJA_SUBAGENTS", "1")
	ss, err := ParseOmnigentDB(db)
	if err != nil {
		t.Fatalf("ParseOmnigentDB: %v", err)
	}
	if len(ss) != 1 {
		t.Fatalf("ParseOmnigentDB got %d sessions, want 1", len(ss))
	}
	if ss[0].Kind != model.KindSubagent {
		t.Fatalf("ss[0].Kind = %q, want %q", ss[0].Kind, model.KindSubagent)
	}
	if ss[0].Title != "Retry consumer loop" {
		t.Fatalf("ss[0].Title = %q, want %q", ss[0].Title, "Retry consumer loop")
	}
}

func TestParseOmpFile(t *testing.T) {
	root := t.TempDir()
	t.Setenv("HOME", filepath.Join(root, "home"))
	t.Setenv("USERPROFILE", os.Getenv("HOME"))
	t.Setenv("DEJA_OMP_ROOT", filepath.Join(root, "omp-sessions"))
	project := filepath.Join(root, "omp-sessions", "-Code-pleasure-course")
	if err := os.MkdirAll(project, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(project, "2026-08-17T10-56-36-424Z_01a00f5d-bc48-7000-b56f-33a99ffec433.jsonl")
	data := `{"type":"session","version":3,"id":"01a00f5d-bc48-7000-b56f-33a99ffec433","timestamp":"2026-08-17T10:56:36.424Z","cwd":"/Users/halo/Code/pleasure-course","title":"Download YouTube videos"}
{"type":"model_change","id":"m1","parentId":null,"timestamp":"2026-08-17T10:56:36.516Z","model":"ollama-cloud/deepseek-v4-pro:0813"}
{"type":"message","id":"u1","parentId":"m1","timestamp":"2026-08-17T10:57:05.912Z","message":{"role":"user","content":[{"type":"text","text":"download the videos"}],"timestamp":1786964225859}}
{"type":"message","id":"a1","parentId":"u1","timestamp":"2026-08-17T10:57:12.306Z","message":{"role":"assistant","content":[{"type":"thinking","thinking":"read the file first"},{"type":"toolCall","id":"ollama:1:read","name":"read","arguments":{"path":"anastasia.md"}},{"type":"text","text":"reading the list"}],"timestamp":1786964232306}}
{"type":"message","id":"t1","parentId":"a1","timestamp":"2026-08-17T10:57:12.500Z","message":{"role":"toolResult","toolCallId":"ollama:1:read","toolName":"read","content":[{"type":"text","text":"some file output"}]}}
{"type":"message","id":"a2","parentId":"t1","timestamp":"2026-08-17T10:57:20.000Z","message":{"role":"assistant","content":[{"type":"text","text":"done"}],"timestamp":1786964240000}}
`
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
	ss, err := ParseOmpFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(ss) != 1 {
		t.Fatalf("want 1 session, got %d", len(ss))
	}
	s := ss[0]
	if s.Harness != "omp" {
		t.Fatalf("harness = %q, want omp", s.Harness)
	}
	if s.ID != "01a00f5d-bc48-7000-b56f-33a99ffec433" {
		t.Fatalf("id = %q", s.ID)
	}
	// The header cwd is promoted to the project key, not the lossy directory
	// name (-Code-pleasure-course would decode to pleasure/course).
	// Encoding the cwd and decoding it back was that guess again (#4427).
	if s.Project != "Code/pleasure-course" {
		t.Fatalf("project = %q, want Code/pleasure-course", s.Project)
	}
	// user + assistant(text only, thinking skipped) + the read's file row +
	// tool output + assistant. The toolCall was skipped too until #4113.
	if len(s.Messages) != 5 {
		t.Fatalf("want 5 messages, got %d: %#v", len(s.Messages), s.Messages)
	}
	if s.Messages[0].Role != "user" || s.Messages[0].Text != "download the videos" {
		t.Fatalf("message[0] = %#v", s.Messages[0])
	}
	if s.Messages[1].Role != "assistant" || s.Messages[1].Text != "reading the list" {
		t.Fatalf("message[1] = %#v (thinking must be skipped)", s.Messages[1])
	}
	if want := "/Users/halo/Code/pleasure-course/anastasia.md"; s.Messages[2].Role != RoleFiles || s.Messages[2].Text != want {
		t.Fatalf("message[2] = %#v, want the file the read named, resolved against the cwd", s.Messages[2])
	}
	if s.Messages[3].Role != RoleToolOutput || s.Messages[3].Text != "some file output" {
		t.Fatalf("message[3] = %#v, want tool output", s.Messages[3])
	}
	if s.Messages[4].Role != "assistant" || s.Messages[4].Text != "done" {
		t.Fatalf("message[4] = %#v", s.Messages[4])
	}
}

func TestParseOmpFileFromOffset(t *testing.T) {
	root := t.TempDir()
	t.Setenv("HOME", filepath.Join(root, "home"))
	t.Setenv("USERPROFILE", os.Getenv("HOME"))
	t.Setenv("DEJA_OMP_ROOT", filepath.Join(root, "omp-sessions"))
	project := filepath.Join(root, "omp-sessions", "-tmp")
	if err := os.MkdirAll(project, 0o755); err != nil {
		t.Fatal(err)
	}
	line1 := `{"type":"session","version":3,"id":"off-1","timestamp":"2026-08-17T10:56:36.424Z","cwd":"/tmp/demo"}` + "\n"
	line2 := `{"type":"message","id":"u1","parentId":null,"timestamp":"2026-08-17T10:57:05.912Z","message":{"role":"user","content":[{"type":"text","text":"first"}]}}` + "\n"
	line3 := `{"type":"message","id":"a1","parentId":"u1","timestamp":"2026-08-17T10:57:12.306Z","message":{"role":"assistant","content":[{"type":"text","text":"reply"}]}}` + "\n"
	path := filepath.Join(project, "session.jsonl")
	if err := os.WriteFile(path, []byte(line1+line2+line3), 0o644); err != nil {
		t.Fatal(err)
	}
	offset := int64(len(line1) + len(line2))
	ss, err := ParseOmpFileFromOffset(path, offset)
	if err != nil {
		t.Fatal(err)
	}
	if len(ss) != 1 || len(ss[0].Messages) != 1 {
		t.Fatalf("want 1 session with 1 message, got %#v", ss)
	}
	if ss[0].Messages[0].Text != "reply" {
		t.Fatalf("message text = %q, want reply", ss[0].Messages[0].Text)
	}
}

func TestParseOmpFileEmpty(t *testing.T) {
	root := t.TempDir()
	t.Setenv("HOME", filepath.Join(root, "home"))
	t.Setenv("USERPROFILE", os.Getenv("HOME"))
	t.Setenv("DEJA_OMP_ROOT", filepath.Join(root, "omp-sessions"))
	project := filepath.Join(root, "omp-sessions", "-tmp-empty")
	if err := os.MkdirAll(project, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(project, "session.jsonl")
	data := `{"type":"session","version":3,"id":"empty-1","timestamp":"2026-08-17T10:56:36.424Z","cwd":"/tmp/empty"}
{"type":"model_change","id":"m1","timestamp":"2026-08-17T10:56:36.516Z"}
`
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
	ss, err := ParseOmpFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(ss) != 0 {
		t.Fatalf("expected no sessions from metadata-only file, got %d", len(ss))
	}
}

func TestOmpDiscoveryAndProjectBase(t *testing.T) {
	root := t.TempDir()
	t.Setenv("HOME", root)
	t.Setenv("USERPROFILE", root)
	t.Setenv("DEJA_OMP_ROOT", "")
	if got := OmpRoot(); got != filepath.Join(root, ".omp", "agent", "sessions") {
		t.Fatalf("default OmpRoot = %q", got)
	}
	ompRoot := filepath.Join(root, "omp-sessions")
	t.Setenv("DEJA_OMP_ROOT", ompRoot)
	proj := filepath.Join(ompRoot, "-Code-adaptivefrontier-legacy")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(proj, "s.jsonl")
	if err := os.WriteFile(path, []byte(`{"type":"session","id":"s1","timestamp":"2026-08-17T10:56:36.424Z","cwd":"/Users/halo/Code/adaptivefrontier/legacy"}
{"type":"message","timestamp":"2026-08-17T10:57:05.912Z","message":{"role":"user","content":"omp discovery fact"}}
`), 0o644); err != nil {
		t.Fatal(err)
	}
	files := OmpSessionFiles()
	if len(files) != 1 || files[0] != path {
		t.Fatalf("OmpSessionFiles = %v", files)
	}
	ss := LoadOmp()
	if len(ss) != 1 || ss[0].ID != "s1" || ss[0].Harness != "omp" {
		t.Fatalf("LoadOmp = %#v", ss)
	}
	// Header cwd wins over the encoded directory name.
	if !strings.HasSuffix(ss[0].Project, "legacy") {
		t.Fatalf("omp project = %q, want …legacy", ss[0].Project)
	}
}

func TestOrcaParseAndSince(t *testing.T) {
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skip("sqlite3 not installed")
	}
	dir := t.TempDir()
	db := filepath.Join(dir, "orca.db")
	buildSQLiteFixture(t, filepath.Join("..", "..", "fixtures", "registry", "orca", "orca.sql"), db)
	t.Setenv("DEJA_ORCA_DB", db)

	if got := OrcaDB(); got != db {
		t.Fatalf("OrcaDB() = %q, want %q", got, db)
	}
	ss := LoadOrca()
	if len(ss) != 1 {
		t.Fatalf("LoadOrca() got %d sessions, want 1", len(ss))
	}
	s := ss[0]
	if s.Harness != "orca" || s.ID != "orca-sess-01" {
		t.Fatalf("got harness=%q id=%q", s.Harness, s.ID)
	}
	if s.Title != "Fix webhook signature verification" {
		t.Fatalf("Title = %q", s.Title)
	}
	if s.Project != "workspace/orca-payments" {
		t.Fatalf("Project = %q, want workspace/orca-payments", s.Project)
	}
	if len(s.Messages) < 3 {
		t.Fatalf("Messages = %d, want >= 3", len(s.Messages))
	}

	since, err := ParseOrcaDBSince(db, time.UnixMilli(1752915000000).UTC())
	if err != nil || len(since) != 1 {
		t.Fatalf("ParseOrcaDBSince: len=%d err=%v", len(since), err)
	}
	zero, err := ParseOrcaDBSince(db, time.Time{})
	if err != nil || len(zero) != 1 {
		t.Fatalf("ParseOrcaDBSince zero: len=%d err=%v", len(zero), err)
	}
	after, err := ParseOrcaDBSince(db, time.UnixMilli(1752916000000).UTC())
	if err != nil || len(after) != 0 {
		t.Fatalf("ParseOrcaDBSince future: len=%d err=%v", len(after), err)
	}
}

func TestOrcaPathsAndFiles(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("DEJA_ORCA_ROOT", "")
	t.Setenv("DEJA_ORCA_DB", "")
	t.Setenv("APPDATA", "")
	t.Setenv("XDG_CONFIG_HOME", "")

	if got := OrcaRoot(); got == "" {
		t.Fatal("OrcaRoot() returned empty")
	}
	t.Setenv("DEJA_ORCA_ROOT", "~/orca-custom")
	if got, want := OrcaRoot(), filepath.Join(home, "orca-custom"); got != want {
		t.Fatalf("OrcaRoot() override = %q, want %q", got, want)
	}

	if got, want := orcaDataDir("darwin"), filepath.Join(home, "Library", "Application Support", "orca"); got != want {
		t.Fatalf("orcaDataDir(darwin) = %q, want %q", got, want)
	}
	if got, want := orcaDataDir("windows"), filepath.Join(home, "AppData", "Roaming", "orca"); got != want {
		t.Fatalf("orcaDataDir(windows) default = %q, want %q", got, want)
	}
	winAppData := filepath.Join(home, "win-appdata")
	t.Setenv("APPDATA", winAppData)
	if got, want := orcaDataDir("windows"), filepath.Join(winAppData, "orca"); got != want {
		t.Fatalf("orcaDataDir(windows) APPDATA = %q, want %q", got, want)
	}

	if got, want := orcaDataDir("linux"), filepath.Join(home, ".config", "orca"); got != want {
		t.Fatalf("orcaDataDir(linux) default = %q, want %q", got, want)
	}
	xdgLower := filepath.Join(home, "xdg-lower")
	if err := os.MkdirAll(filepath.Join(xdgLower, "orca"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("XDG_CONFIG_HOME", xdgLower)
	if got, want := orcaDataDir("linux"), filepath.Join(xdgLower, "orca"); got != want {
		t.Fatalf("orcaDataDir(linux) lower = %q, want %q", got, want)
	}
	xdgUpper := filepath.Join(home, "xdg-upper")
	if err := os.MkdirAll(filepath.Join(xdgUpper, "Orca"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("XDG_CONFIG_HOME", xdgUpper)
	if got, want := orcaDataDir("linux"), filepath.Join(xdgUpper, "Orca"); !strings.EqualFold(got, want) {
		t.Fatalf("orcaDataDir(linux) upper = %q, want %q", got, want)
	}

	missingDB := filepath.Join(t.TempDir(), "missing.db")
	t.Setenv("DEJA_ORCA_DB", missingDB)
	if got := OrcaFiles(); got != nil {
		t.Fatalf("OrcaFiles() missing = %v, want nil", got)
	}
	emptyDB := filepath.Join(t.TempDir(), "empty.db")
	if err := os.WriteFile(emptyDB, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_ORCA_DB", emptyDB)
	if got := OrcaFiles(); got != nil {
		t.Fatalf("OrcaFiles() empty = %v, want nil", got)
	}
	okDB := filepath.Join(t.TempDir(), "ok.db")
	if err := os.WriteFile(okDB, []byte("sqlite"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_ORCA_DB", okDB)
	if got := OrcaFiles(); len(got) != 1 || got[0] != okDB {
		t.Fatalf("OrcaFiles() non-empty = %v, want [%q]", got, okDB)
	}
}

func TestOrcaJournalBranchesAndSessionDir(t *testing.T) {
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skip("sqlite3 not installed")
	}
	dir := t.TempDir()
	missingDB := filepath.Join(dir, "missing.db")
	if ss, err := ParseOrcaDB(missingDB); err != nil || ss != nil {
		t.Fatalf("ParseOrcaDB(missing) = %v, %v", ss, err)
	}
	if got := OrcaSessionDir(missingDB, "x"); got != "" {
		t.Fatalf("OrcaSessionDir(missing) = %q", got)
	}

	unrelatedDB := filepath.Join(dir, "unrelated.db")
	cmd := exec.CommandContext(t.Context(), "sqlite3", unrelatedDB)
	cmd.Stdin = strings.NewReader("CREATE TABLE misc(id INT); INSERT INTO misc VALUES (1);")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("create unrelated db: %v: %s", err, out)
	}
	if orcaDBHasTable(unrelatedDB) {
		t.Fatal("orcaDBHasTable(unrelatedDB) = true, want false")
	}
	if ss, err := ParseOrcaDB(unrelatedDB); err != nil || ss != nil {
		t.Fatalf("ParseOrcaDB(unrelated) = %v, %v", ss, err)
	}
	if got := OrcaSessionDir(unrelatedDB, "x"); got != "" {
		t.Fatalf("OrcaSessionDir(unrelated) = %q", got)
	}

	db := filepath.Join(dir, "journal.db")
	sql := `
CREATE TABLE agent_session_records(session_id TEXT PRIMARY KEY, record_json TEXT NOT NULL);
CREATE TABLE journal_rows(session_id TEXT NOT NULL, epoch TEXT NOT NULL, seq INTEGER NOT NULL, ts INTEGER NOT NULL, row_json TEXT NOT NULL, PRIMARY KEY (session_id, epoch, seq));
INSERT INTO agent_session_records VALUES ('s-ws', '{"id":"s-ws","workspaceId":"/Users/alex/work/orca-ws","cwd":"","title":"","createdAt":1752915001000,"updatedAt":1752915005000}');
INSERT INTO agent_session_records VALUES ('s-cwd', '{"id":"s-cwd","workspaceId":"/Users/alex/work/ws","cwd":"/Users/alex/work/orca-cwd","title":"Explicit"}');
INSERT INTO journal_rows VALUES ('s-ws', '', 1, 1752915001000, '{"itemId":"m1","revision":1,"observedAt":1752915001000,"sequence":1,"body":{"kind":"message","role":"user","blocks":[{"kind":"text","text":"Old draft"}]}}');
INSERT INTO journal_rows VALUES ('s-ws', '', 2, 1752915002000, '{"itemId":"m1","revision":2,"observedAt":1752915002000,"sequence":1,"body":{"kind":"message","role":"user","blocks":[{"kind":"text","text":"Settled title line\nmore context"}]}}');
INSERT INTO journal_rows VALUES ('s-ws', '', 3, 1752915002500, '{"itemId":"","revision":1,"observedAt":0,"sequence":0,"body":{"kind":"message","role":"assistant","blocks":[{"kind":"text","text":"Inspecting handler"},{"kind":"tool-call","toolCallId":"tc1","name":"read_file","input":{"file_path":"/Users/alex/work/orca-ws/handler.go"}},{"kind":"tool-result","output":"package handler"}]}}');
INSERT INTO journal_rows VALUES ('s-ws', '', 4, 1752915003000, '{"itemId":"tc-top","revision":1,"observedAt":1752915003000,"sequence":4,"body":{"kind":"tool-call","name":"bash","input":{"command":"go test ./..."},"output":{"head":"ok  orca-ws 0.1s"}}}');
INSERT INTO journal_rows VALUES ('s-ws', '', 5, 1752915004000, '{"itemId":"diff-1","revision":1,"observedAt":1752915004000,"sequence":5,"body":{"kind":"diff","path":"/Users/alex/work/orca-ws/handler.go","patch":{"head":"@@ -1 +1,2 @@\n package handler\n+// ok"}}}');
INSERT INTO journal_rows VALUES ('s-ws', '', 6, 1752915004500, 'not-json');
INSERT INTO journal_rows VALUES ('s-ws', '', 7, 1752915004600, '{"itemId":"sys","revision":1,"body":{"kind":"message","role":"system","blocks":[{"kind":"text","text":"ignored"}]}}');
INSERT INTO journal_rows VALUES ('s-cwd', '', 1, 1752915001000, '{"itemId":"u1","revision":1,"observedAt":1752915001000,"sequence":1,"body":{"kind":"message","role":"user","blocks":[{"kind":"text","text":"Check cwd"}]}}');
`
	cmd2 := exec.CommandContext(t.Context(), "sqlite3", db)
	cmd2.Stdin = strings.NewReader(sql)
	if out, err := cmd2.CombinedOutput(); err != nil {
		t.Fatalf("create journal db: %v: %s", err, out)
	}

	ss, err := ParseOrcaDB(db)
	if err != nil {
		t.Fatalf("ParseOrcaDB: %v", err)
	}
	if len(ss) != 2 {
		t.Fatalf("ParseOrcaDB got %d sessions, want 2", len(ss))
	}
	if ss[0].Title != "Settled title line" {
		t.Fatalf("ss[0].Title = %q, want %q", ss[0].Title, "Settled title line")
	}
	var joined strings.Builder
	for _, m := range ss[0].Messages {
		joined.WriteString(m.Text)
		joined.WriteByte('\n')
	}
	if !strings.Contains(joined.String(), "Inspecting handler") {
		t.Fatalf("expected Inspecting handler in messages: %q", joined.String())
	}
	if got := OrcaSessionDir(db, "s-ws"); got != "/Users/alex/work/orca-ws" {
		t.Fatalf("OrcaSessionDir(s-ws) = %q", got)
	}
	if got := OrcaSessionDir(db, "s-cwd"); got != "/Users/alex/work/orca-cwd" {
		t.Fatalf("OrcaSessionDir(s-cwd) = %q", got)
	}
	if got := OrcaSessionDir(db, "unknown"); got != "" {
		t.Fatalf("OrcaSessionDir(unknown) = %q", got)
	}
}

func TestParityInlineBlockUnpack(t *testing.T) {
	blk, ok := parseInlineBlock("  [thinking] verify retry cap first  ")
	if !ok || blk.typ != "thinking" || blk.text != "verify retry cap first" {
		t.Fatalf("parseInlineBlock(thinking) = (%+v, %v)", blk, ok)
	}

	blk, ok = parseInlineBlock(`[tool_use Bash] {"command":"go test ./..."}`)
	if !ok || blk.typ != "tool_use" || blk.name != "Bash" {
		t.Fatalf("parseInlineBlock(tool_use) = (%+v, %v)", blk, ok)
	}

	blk, ok = parseInlineBlock("[tool_result] Exit code 2\nFAIL pkg/foo")
	if !ok || blk.typ != "tool_result" || !blk.isErr || !strings.Contains(blk.text, "FAIL pkg/foo") {
		t.Fatalf("parseInlineBlock(tool_result) = (%+v, %v)", blk, ok)
	}

	if _, ok := parseInlineBlock("plain user speech"); ok {
		t.Fatal("parseInlineBlock accepted plain speech")
	}

	blocks, results, speech, onlyTool := unpackContentBlocks([]any{
		"[thinking] internal thought",
		"visible explanation",
		`[tool_use Edit] {"file_path":"/Users/alex/repo/main.go","old_string":"a","new_string":"b"}`,
		"[tool_result] Exit code 1\nbad syntax",
	})
	if speech != "visible explanation" {
		t.Fatalf("speech = %q, want visible explanation", speech)
	}
	if onlyTool {
		t.Fatal("onlyTool should be false when human/assistant speech is present")
	}
	if len(blocks) != 2 || len(results) != 1 || !strings.Contains(results[0], "bad syntax") {
		t.Fatalf("unpackContentBlocks got blocks=%d results=%v", len(blocks), results)
	}
}

func TestParityFlatRoleAndZCodeWorkRecords(t *testing.T) {
	root := t.TempDir()
	t.Setenv("DEJA_ZCODE_ROOT", root)

	projDir := filepath.Join(root, "-Users-alex-repo")
	if err := os.MkdirAll(projDir, 0o755); err != nil {
		t.Fatal(err)
	}
	huge := strings.Repeat("x", maxParsedMessage+512) + "TAIL_END"
	path := filepath.Join(projDir, "zc-parity-1.jsonl")
	lines := strings.Join([]string{
		`{"role":"user","sessionId":"zc-parity-1","timestamp":"2026-10-01T10:00:00Z","content":"update retry backoff"}`,
		fmt.Sprintf(`{"role":"assistant","sessionId":"zc-parity-1","timestamp":"2026-10-01T10:00:01Z","content":["[thinking] inspect retry.go",%q,%q,%q,{"type":"tool_use","id":"call-sh-1","name":"Bash","input":{"command":"go test ./..."}}]}`,
			`[tool_use Read] {"file_path":"/Users/alex/repo/retry.go"}`,
			`[tool_use Write] {"file_path":"/Users/alex/repo/backoff.go","content":"func ComputeBackoffDelay(attempt int, baseMillis int64) int64 {\n\treturn baseMillis * (1 << attempt)\n}\n"}`,
			`[tool_use Edit] {"file_path":"/Users/alex/repo/retry.go","old_string":"max := 3","new_string":"max := 5"}`),
		fmt.Sprintf(`{"role":"tool","sessionId":"zc-parity-1","tool_call_id":"call-sh-1","exit_code":2,"timestamp":"2026-10-01T10:00:02Z","content":%q}`, huge),
	}, "\n") + "\n"
	if err := os.WriteFile(path, []byte(lines), 0o644); err != nil {
		t.Fatal(err)
	}

	ss, err := ParseZCodeFile(path)
	if err != nil || len(ss) != 1 {
		t.Fatalf("ParseZCodeFile = (%d, %v), want 1 session", len(ss), err)
	}
	byRole := map[string][]string{}
	for _, m := range ss[0].Messages {
		byRole[m.Role] = append(byRole[m.Role], m.Text)
	}
	for _, role := range []string{RoleFiles, RoleWrote, RoleEdit, RoleCommand, RoleToolOutput} {
		if len(byRole[role]) == 0 {
			t.Fatalf("missing role %q in ZCode messages: %#v", role, byRole)
		}
	}
	if !strings.Contains(byRole[RoleCommand][0], "go test ./...") || !strings.Contains(byRole[RoleCommand][0], "2") {
		t.Fatalf("RoleCommand did not join exit code: %q", byRole[RoleCommand][0])
	}
	if len(byRole[RoleToolOutput][0]) > maxParsedMessage {
		t.Fatalf("RoleToolOutput exceeded cap: %d > %d", len(byRole[RoleToolOutput][0]), maxParsedMessage)
	}
}

func TestParityDeepSeekToolOutputSwitchAndCap(t *testing.T) {
	root := t.TempDir()
	t.Setenv("DEJA_DEEPSEEK_ROOT", root)

	sessDir := filepath.Join(root, "--work-pgbouncer-lab--", "session-ds-cap")
	if err := os.MkdirAll(sessDir, 0o755); err != nil {
		t.Fatal(err)
	}
	huge := strings.Repeat("y", maxParsedMessage+512)
	body := strings.Join([]string{
		`{"type":"session","version":0,"id":"session-ds-cap","createdAt":1787320263519,"cwd":"/work/pgbouncer-lab"}`,
		`{"type":"user/message","seq":1,"time":1787320263520,"data":{"content":[{"type":"text","text":"check pool"}],"source":{"kind":"user"},"role":"user"}}`,
		`{"type":"tool/call","seq":2,"time":1787320263530,"data":{"callId":"c1","name":"bash","arguments":"{\"command\":\"cat notes.txt\"}"}}`,
		fmt.Sprintf(`{"type":"tool/result","seq":3,"time":1787320263540,"data":{"message":{"source":{"kind":"tool","callId":"c1"},"content":[{"type":"tool-result","toolCallId":"c1","content":[{"type":"text","text":%q}]}]}}}`, huge),
		`{"type":"assistant/message","seq":4,"time":1787320263550,"data":{"turn":1,"step":2,"message":{"role":"assistant","content":[{"type":"text","text":"pool checked"}],"source":{"kind":"model"}}}}`,
	}, "\n") + "\n"
	path := filepath.Join(sessDir, "session.jsonl")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	ss, err := ParseDeepSeekFile(path)
	if err != nil || len(ss) != 1 {
		t.Fatalf("ParseDeepSeekFile = (%d, %v), want 1", len(ss), err)
	}
	var toolOutputs []string
	for _, m := range ss[0].Messages {
		if m.Role == RoleToolOutput {
			toolOutputs = append(toolOutputs, m.Text)
		}
	}
	if len(toolOutputs) != 1 {
		t.Fatalf("expected 1 RoleToolOutput message by default, got %d", len(toolOutputs))
	}
	if len(toolOutputs[0]) > maxParsedMessage {
		t.Fatalf("DeepSeek RoleToolOutput length %d > maxParsedMessage %d", len(toolOutputs[0]), maxParsedMessage)
	}

	t.Setenv("DEJA_INDEX_TOOL_OUTPUT", "0")
	offSS, err := ParseDeepSeekFile(path)
	if err != nil || len(offSS) != 1 {
		t.Fatalf("ParseDeepSeekFile(off) = (%d, %v)", len(offSS), err)
	}
	for _, m := range offSS[0].Messages {
		if m.Role == RoleToolOutput {
			t.Fatalf("DEJA_INDEX_TOOL_OUTPUT=0 leaked RoleToolOutput: %q", m.Text)
		}
	}
}

func TestParityResolveFallbackProjectAcrossHarnesses(t *testing.T) {
	tmp := t.TempDir()
	mkRepo := func(parent, name string) string {
		repo := filepath.Join(tmp, parent, name)
		if err := os.MkdirAll(filepath.Join(repo, ".git"), 0o755); err != nil {
			t.Fatal(err)
		}
		return repo
	}
	repo1 := mkRepo("services", "billing-api")
	repo2 := mkRepo("src", "payments-core")
	repo3 := mkRepo("work", "cache-proxy")

	// 1. >= 3 RoleFiles resolves project.
	sFiles := model.Session{
		ID:      "p-files",
		Harness: "qwen",
		Project: "",
		Messages: []model.Message{
			{Role: RoleFiles, Text: strings.Join([]string{
				filepath.Join(repo1, "handler.go"),
				filepath.Join(repo1, "store.go"),
				filepath.Join(repo1, "retry.go"),
			}, "\n")},
		},
	}
	resolveFallbackProject(&sFiles)
	if sFiles.Project != "services/billing-api" {
		t.Fatalf("sFiles.Project = %q, want services/billing-api", sFiles.Project)
	}

	// 2. RoleEdit / RoleWrote resolves home-derived project even with < 3 RoleFiles.
	sEdit := model.Session{
		ID:      "p-edit",
		Harness: "deepseek",
		Project: "Users/alex",
		Messages: []model.Message{
			{Role: RoleEdit, Text: filepath.Join(repo2, "main.go") + "\nfunc main() {}"},
		},
	}
	resolveFallbackProject(&sEdit)
	if sEdit.Project != "src/payments-core" {
		t.Fatalf("sEdit.Project = %q, want src/payments-core", sEdit.Project)
	}

	// 3. `cd <dir>` in RoleCommand resolves empty/- project.
	sCmd := model.Session{
		ID:      "p-cmd",
		Harness: "codearts",
		Project: "-",
		Messages: []model.Message{
			{Role: RoleCommand, Text: "cd " + repo3 + " && go test ./..."},
		},
	}
	resolveFallbackProject(&sCmd)
	if sCmd.Project != "work/cache-proxy" {
		t.Fatalf("sCmd.Project = %q, want work/cache-proxy", sCmd.Project)
	}
}

func TestParityWorktreeAndScratchAllHarnesses(t *testing.T) {
	collapseCases := []struct {
		in   string
		want string
	}{
		{"/Users/alex/repo/.codex/worktrees/wt-1/pkg/a.go", "/Users/alex/repo/pkg/a.go"},
		{"/Users/alex/repo/.qoder/worktrees/wt-2/pkg/b.go", "/Users/alex/repo/pkg/b.go"},
		// Home-root Codex Desktop worktrees must not collapse away the checkout root.
		{"/Users/alex/.codex/worktrees/ab12/repo/a.go", "/Users/alex/.codex/worktrees/ab12/repo/a.go"},
		{"/home/alex/.codex/worktrees/ab12/repo/a.go", "/home/alex/.codex/worktrees/ab12/repo/a.go"},
		{"/root/.codex/worktrees/ab12/repo/a.go", "/root/.codex/worktrees/ab12/repo/a.go"},
		{`C:\Users\alex\.codex\worktrees\ab12\repo\a.go`, `C:\Users\alex\.codex\worktrees\ab12\repo\a.go`},
		// Git internal worktree metadata must not collapse into a repo file.
		{"/Users/alex/repo/.git/worktrees/wt-1/HEAD", "/Users/alex/repo/.git/worktrees/wt-1/HEAD"},
	}
	for _, tc := range collapseCases {
		if got := CollapseWorktreePath(tc.in); got != tc.want {
			t.Errorf("CollapseWorktreePath(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}

	if IsAgentScratchPath("/Users/alex/.codex/worktrees/ab12/repo/a.go") {
		t.Fatal("IsAgentScratchPath flagged real file inside ~/.codex/worktrees/<id>/repo as scratch")
	}
	if IsAgentScratchPath("/Users/alex/repo/.qoder/worktrees/wt-2/pkg/b.go") {
		t.Fatal("IsAgentScratchPath flagged file inside .qoder/worktrees checkout as scratch")
	}
	for _, scratch := range []string{
		"/Users/alex/.codex/sessions/2026/11/rollout.jsonl",
		"/Users/alex/.qoder/projects/-Users-alex-repo/s.jsonl",
		"/Users/alex/.cursor/plans/plan.md",
	} {
		if !IsAgentScratchPath(scratch) {
			t.Errorf("IsAgentScratchPath(%q) = false, want true", scratch)
		}
	}
}

func TestParityQoderAndCherryStudioCompactionAndAutoMemory(t *testing.T) {
	// Qoder compaction, head, and auto-memory discovery.
	qoderRoot := t.TempDir()
	t.Setenv("DEJA_QODER_ROOT", qoderRoot)
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(t.TempDir(), "no-claude"))

	qProj := filepath.Join(qoderRoot, "projects", "-Users-alex-qproj")
	qMem := filepath.Join(qProj, "memory")
	if err := os.MkdirAll(qMem, 0o755); err != nil {
		t.Fatal(err)
	}
	qTranscript := filepath.Join(qProj, "q-sess-1.jsonl")
	qBody := strings.Join([]string{
		`{"type":"user","sessionId":"q-sess-1","cwd":"/Users/alex/qproj","timestamp":"2026-10-02T10:00:00Z","message":{"role":"user","content":"add retry jitter"}}`,
		`{"type":"assistant","sessionId":"q-sess-1","cwd":"/Users/alex/qproj","timestamp":"2026-10-02T10:00:05Z","message":{"role":"assistant","content":[{"type":"tool_use","id":"q-edit-1","name":"Edit","input":{"file_path":"/Users/alex/qproj/retry.go"}}]}}`,
	}, "\n") + "\n"
	if err := os.WriteFile(qTranscript, []byte(qBody), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(qMem, "feedback_rules.md"), []byte("---\nname: Retry jitter\ntype: feedback\n---\nAlways add jitter to retry loops.\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	qt, err := ReadCompactionTranscript(qTranscript, "q-sess-1")
	if err != nil {
		t.Fatalf("ReadCompactionTranscript(qoder): %v", err)
	}
	if qt.Harness != "qoder" || qt.Session.Harness != "qoder" || qt.Workspace != "/Users/alex/qproj" || len(qt.ToolCalls) != 1 {
		t.Fatalf("qoder compaction = %#v", qt)
	}
	qh, err := TranscriptHead(qTranscript, "q-sess-1")
	if err != nil || qh.Harness != "qoder" {
		t.Fatalf("TranscriptHead(qoder) = (%q, %v)", qh.Harness, err)
	}
	if entries := LoadClaudeAutoMemory([]string{"/Users/alex/qproj"}); len(entries) != 1 || !strings.Contains(entries[0].Body, "Always add jitter") {
		t.Fatalf("LoadClaudeAutoMemory(qoder) = %#v", entries)
	}

	// Cherry Studio compaction, snapshot collapse, head, and auto-memory discovery.
	cherryProjects := filepath.Join(t.TempDir(), "CherryStudio", "Data", "Agents", ".claude", "projects")
	t.Setenv("DEJA_CHERRYSTUDIO_ROOTS", cherryProjects)

	cProj := filepath.Join(cherryProjects, "-Users-alex-cproj")
	cMem := filepath.Join(cProj, "memory")
	if err = os.MkdirAll(cMem, 0o755); err != nil {
		t.Fatal(err)
	}
	cTranscript := filepath.Join(cProj, "cs-sess-1.jsonl")
	cBody := strings.Join([]string{
		`{"type":"user","sessionId":"cs-sess-1","cwd":"/Users/alex/cproj","timestamp":"2026-10-03T10:00:00Z","message":{"role":"user","content":"check lock timeout"}}`,
		`{"type":"assistant","uuid":"u1","requestId":"req-1","sessionId":"cs-sess-1","cwd":"/Users/alex/cproj","timestamp":"2026-10-03T10:00:01Z","message":{"id":"msg-1","role":"assistant","content":[{"type":"text","text":"advisory"}]}}`,
		`{"type":"assistant","uuid":"u2","requestId":"req-1","sessionId":"cs-sess-1","cwd":"/Users/alex/cproj","timestamp":"2026-10-03T10:00:02Z","message":{"id":"msg-1","role":"assistant","content":[{"type":"text","text":"advisory lock timeout"},{"type":"tool_use","id":"cs-read-1","name":"Read","input":{"file_path":"/Users/alex/cproj/lock.go"}}]}}`,
	}, "\n") + "\n"
	if err = os.WriteFile(cTranscript, []byte(cBody), 0o600); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(cMem, "project_conventions.md"), []byte("---\nname: Lock timeout\ntype: project\n---\nUse explicit lock timeout on migrations.\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	ct, err := ReadCompactionTranscript(cTranscript, "cs-sess-1")
	if err != nil {
		t.Fatalf("ReadCompactionTranscript(cherrystudio): %v", err)
	}
	if ct.Harness != "cherrystudio" || ct.Session.Harness != "cherrystudio" || ct.Workspace != "/Users/alex/cproj" || len(ct.ToolCalls) != 1 {
		t.Fatalf("cherrystudio compaction = %#v", ct)
	}
	assistantSpeech := 0
	for _, m := range ct.Session.Messages {
		if m.Role == "assistant" {
			assistantSpeech++
		}
	}
	if assistantSpeech != 1 {
		t.Fatalf("cherrystudio compaction did not collapse streaming snapshots: got %d assistant turns (%#v)", assistantSpeech, ct.Session.Messages)
	}
	ch, err := TranscriptHead(cTranscript, "cs-sess-1")
	if err != nil || ch.Harness != "cherrystudio" {
		t.Fatalf("TranscriptHead(cherrystudio) = (%q, %v)", ch.Harness, err)
	}
	if entries := LoadClaudeAutoMemory([]string{"/Users/alex/cproj"}); len(entries) != 1 || !strings.Contains(entries[0].Body, "explicit lock timeout") {
		t.Fatalf("LoadClaudeAutoMemory(cherrystudio) = %#v", entries)
	}
}

func TestParityRegistryResumesForCodeArtsQoderZCode(t *testing.T) {
	checked := map[string]bool{}
	for _, h := range Registry() {
		switch h.Name {
		case "codearts", "qoder":
			checked[h.Name] = true
			for _, k := range h.Kinds {
				if k.Resumes == nil {
					t.Errorf("%s kind %q missing Resumes callback", h.Name, k.Name)
				}
			}
		case "zcode":
			checked[h.Name] = true
			for _, k := range h.Kinds {
				if k.Name == "zcode" && k.Resumes == nil {
					t.Errorf("zcode kind %q missing Resumes callback", k.Name)
				}
			}
		}
	}
	for _, want := range []string{"codearts", "qoder", "zcode"} {
		if !checked[want] {
			t.Fatalf("harness %q missing from Registry()", want)
		}
	}
	_ = slices.Contains([]string{"codearts", "qoder", "zcode"}, "qoder")
}

func TestParityHelperBranchesAndQwenHead(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want bool
	}{
		{"", true},
		{"-", true},
		{"Users/alex", true},
		{"home/alex", true},
		{"Users/", false},
		{"services/api", false},
	} {
		if got := IsHomeOrEmptyProject(tc.in); got != tc.want {
			t.Errorf("IsHomeOrEmptyProject(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}

	for _, tc := range []struct {
		res  map[string]any
		code int
		ok   bool
	}{
		{map[string]any{"content": "Exit code 3\nFAIL"}, 3, true},
		{map[string]any{"content": "ok\nCommand failed with exit code 4."}, 4, true},
		{map[string]any{"is_error": true}, 1, true},
		{map[string]any{"content": "no status"}, 0, false},
	} {
		code, ok := flatRoleResultExitCode(tc.res)
		if code != tc.code || ok != tc.ok {
			t.Errorf("flatRoleResultExitCode(%#v) = (%d, %v), want (%d, %v)", tc.res, code, ok, tc.code, tc.ok)
		}
	}

	if got := CollapseWorktreePath(""); got != "" {
		t.Errorf("CollapseWorktreePath(\"\") = %q", got)
	}
	if got := CollapseWorktreePath("/Users/alex/repo/.git/worktrees/feat/main.go"); got != "/Users/alex/repo/.git/worktrees/feat/main.go" {
		t.Errorf("CollapseWorktreePath(.git/worktrees) modified path: %q", got)
	}
	if got := CollapseWorktreePath("relative/.codex/worktrees/wt-1/a.go"); got != "relative/a.go" {
		t.Errorf("CollapseWorktreePath(relative) = %q", got)
	}

	for _, tc := range []struct {
		in   string
		want string
		ok   bool
	}{
		{"", "", false},
		{"[thinking] step 1", "", false},
		{"[tool_use Read] {}", "", false},
		{"[tool_result] FAIL unit test", "FAIL unit test", true},
		{"final reply", "final reply", true},
	} {
		got, ok := filterInlineSpeechText(tc.in)
		if got != tc.want || ok != tc.ok {
			t.Errorf("filterInlineSpeechText(%q) = (%q, %v), want (%q, %v)", tc.in, got, ok, tc.want, tc.ok)
		}
	}

	qwenDir := filepath.Join(t.TempDir(), "chats")
	if err := os.MkdirAll(qwenDir, 0o755); err != nil {
		t.Fatal(err)
	}
	qwenPath := filepath.Join(qwenDir, "qw-1.jsonl")
	qwenLines := strings.Join([]string{
		`{"type":"user","sessionId":"qw-1","cwd":"/Users/alex/qproj","timestamp":"2026-10-01T10:00:00Z","message":{"role":"user","parts":[{"text":"check timeout"}]}}`,
		`{"type":"assistant","sessionId":"qw-1","cwd":"/Users/alex/qproj","timestamp":"2026-10-01T10:00:01Z","message":{"role":"model","parts":[{"text":"increased timeout to 5s"}]}}`,
	}, "\n") + "\n"
	if err := os.WriteFile(qwenPath, []byte(qwenLines), 0o644); err != nil {
		t.Fatal(err)
	}
	qh, err := TranscriptHead(qwenPath, "qw-1")
	if err != nil || qh.Harness != "qwen" || qh.ID != "qw-1" {
		t.Fatalf("TranscriptHead(qwen) = (%+v, %v)", qh, err)
	}
}

func TestParsePrimitivesInt64AndExitCode(t *testing.T) {
	cases := []struct {
		in       any
		wantI64  int64
		wantExit int
	}{
		{nil, 0, 0},
		{float64(42), 42, 42},
		{int64(127), 127, 127},
		{int(-9), -9, -9},
		{json.Number("137"), 137, 137},
		{json.Number("15.0"), 15, 15},
		{json.Number("not-a-number"), 0, 0},
		{" 255 ", 255, 255},
		{"not-an-int", 0, 0},
		{int64(9999), 9999, 1},
		{int64(-9999), -9999, 1},
	}
	for _, tc := range cases {
		if got := int64Of(tc.in); got != tc.wantI64 {
			t.Errorf("int64Of(%#v) = %d, want %d", tc.in, got, tc.wantI64)
		}
		if got := exitCode(tc.in); got != tc.wantExit {
			t.Errorf("exitCode(%#v) = %d, want %d", tc.in, got, tc.wantExit)
		}
	}
}

func TestWithStderrBoundsAndPreservesWrappedError(t *testing.T) {
	baseErr := errors.New("exit status 1")
	if got := withStderr(baseErr, nil); got.Error() != baseErr.Error() {
		t.Fatalf("nil buffer changed error: %v", got)
	}
	if got := withStderr(baseErr, bytes.NewBufferString("   \n  ")); got.Error() != baseErr.Error() {
		t.Fatalf("blank buffer changed error: %v", got)
	}
	longLine := strings.Repeat("x", sqliteStderrMax+50) + "\nsecond line ignored"
	wrapped := withStderr(baseErr, bytes.NewBufferString(longLine))
	if !errors.Is(wrapped, baseErr) {
		t.Fatalf("wrapped error lost base cause: %v", wrapped)
	}
	if strings.Contains(wrapped.Error(), "second line") {
		t.Fatalf("second line leaked into error: %v", wrapped)
	}
	if !strings.Contains(wrapped.Error(), "…") {
		t.Fatalf("expected ellipsis truncation in %q", wrapped.Error())
	}
	for pad := sqliteStderrMax - 5; pad <= sqliteStderrMax+5; pad++ {
		multi := strings.Repeat("a", pad) + "ошибка чтения таблицы"
		got := withStderr(baseErr, bytes.NewBufferString(multi)).Error()
		if !utf8.ValidString(got) {
			t.Fatalf("withStderr produced invalid UTF-8 at pad=%d: %q", pad, got)
		}
	}
}

// switchRow is one reader's session holding a command and what it printed,
// read through the registry kind. out is a word only the tool output holds;
// empty for a reader whose store keeps no output beside the call.
type switchRow struct {
	name, kind string
	fixture    func(t *testing.T, dir string) string
	cmd, out   string
	// said is speech the row holds beside the tool output, which the
	// tool-output switch must keep.
	said string
}

func parseSwitchRows() []switchRow {
	file := func(name, body string) func(*testing.T, string) string {
		return func(t *testing.T, dir string) string {
			t.Helper()
			return writeExitFixture(t, filepath.Join(dir, name), body)
		}
	}
	// Speech and a tool result in one user message: the result rode along
	// with the speech and was kept under it.
	claudeMixed := strings.Replace(failedClaudeTranscript,
		`"content":[{"type":"tool_result"`, `"content":[{"type":"text","text":"also check the logs"},{"type":"tool_result"`, 1)
	return []switchRow{
		{name: "claude", kind: "claude", fixture: file("c1.jsonl", failedClaudeTranscript), cmd: "go test ./...", out: "backoffJitter"},
		{name: "claude mixed message", kind: "claude", fixture: file("c1.jsonl", claudeMixed), cmd: "go test ./...", out: "backoffJitter", said: "also check the logs"},
		{name: "cherrystudio", kind: "cherrystudio", fixture: file("c1.jsonl", claudeMixed), cmd: "go test ./...", out: "backoffJitter", said: "also check the logs"},
		{name: "pi", kind: "pi", fixture: file("2026-09-20T10-00-00-000Z_s1.jsonl", failedPiTranscript), cmd: "go test ./...", out: "backoffJitter"},
		{name: "clawcode", kind: "clawcode", fixture: clawCodeExitStore(`{"stdout":"./retry.go:12:5: undefined: backoffJitter","stderr":"","interrupted":false,"returnCodeInterpretation":"exit_code:1"}`, true), cmd: "go test ./...", out: "backoffJitter"},
		{name: "commandcode", kind: "commandcode", fixture: commandCodeExitStore("Exit code: 1\n./retry.go:12:5: undefined: backoffJitter"), cmd: "go test ./...", out: "backoffJitter"},
		{name: "goose jsonl", kind: "goose-jsonl", fixture: file("20260920_1.jsonl", strings.Join([]string{
			`{"role":"user","created":1790874400,"content":[{"type":"text","text":"fix the retry loop"}]}`,
			`{"role":"assistant","created":1790874401,"content":[{"type":"toolRequest","id":"c1","toolCall":{"status":"success","value":{"name":"developer__shell","arguments":{"command":"go test ./..."}}}}]}`,
			`{"role":"user","created":1790874402,"content":[{"type":"toolResponse","id":"c1","toolResult":{"status":"success","value":{"content":[{"type":"text","text":"./retry.go:12:5: undefined: backoffJitter\n\nCommand exited with code 1"}],"isError":true}}}]}`,
		}, "\n")+"\n"), cmd: "go test ./...", out: "backoffJitter"},
		{name: "goose db", kind: "goose-db", fixture: func(t *testing.T, _ string) string {
			t.Helper()
			return seedDB(t, "sessions.db", `create table sessions (id text primary key, name text, description text, working_dir text, created_at text, updated_at text);
create table messages (id integer primary key autoincrement, session_id text, role text, content_json text, created_timestamp integer);
insert into sessions values ('20260920_1','n','fix the retry loop','/tmp/proj','2026-09-20T10:00:00Z','2026-09-20T10:05:00Z');
insert into messages(session_id,role,content_json,created_timestamp) values ('20260920_1','user','[{"type":"text","text":"fix the retry loop"}]',1790874400);
insert into messages(session_id,role,content_json,created_timestamp) values ('20260920_1','assistant','[{"type":"toolRequest","id":"c1","toolCall":{"status":"success","value":{"name":"shell","arguments":{"command":"go test ./..."}}}}]',1790874401);
insert into messages(session_id,role,content_json,created_timestamp) values ('20260920_1','user','[{"type":"toolResponse","id":"c1","toolResult":{"status":"success","value":{"content":[{"type":"text","text":"./retry.go:12:5: undefined: backoffJitter"}],"isError":true}}}]',1790874402);`)
		}, cmd: "go test ./...", out: "backoffJitter"},
		{name: "grok jsonl", kind: "grok", fixture: func(t *testing.T, dir string) string {
			t.Helper()
			t.Setenv("DEJA_GROK_ROOT", dir)
			const id = "01a03432-4d92-7243-ab6c-8cdd1c697738"
			return writeExitFixture(t, filepath.Join(dir, "sessions", "%2Fw%2Fapp", id, "updates.jsonl"), strings.Join([]string{
				`{"timestamp":1787582100,"method":"session/update","params":{"sessionId":"` + id + `","update":{"sessionUpdate":"user_message_chunk","content":{"type":"text","text":"run the tests"}},"_meta":{"promptId":"p1","agentTimestampMs":1787582100000}}}`,
				`{"timestamp":1787582112,"method":"session/update","params":{"sessionId":"` + id + `","update":{"sessionUpdate":"tool_call","toolCallId":"call-1","title":"run_terminal_command","rawInput":{"command":"go test ./..."},"_meta":{"x.ai/tool":{"version":1,"name":"run_terminal_command","kind":"run_terminal_command"}}},"_meta":{"promptId":"p1","agentTimestampMs":1787582112336}}}`,
				`{"timestamp":1787582115,"method":"session/update","params":{"sessionId":"` + id + `","update":{"sessionUpdate":"tool_call_update","toolCallId":"call-1","status":"completed","content":[{"type":"content","content":{"type":"text","text":"./retry.go:12:5: undefined: backoffJitter"}}]},"_meta":{"promptId":"p1","agentTimestampMs":1787582115000}}}`,
			}, "\n")+"\n")
		}, cmd: "go test ./...", out: "backoffJitter"},
		{name: "crush", kind: "crush", fixture: func(t *testing.T, _ string) string {
			t.Helper()
			sql := "insert into sessions values ('s1',null,'retry',2,0,0,0.0,1784282405,1784282400,null,null);\n"
			sql += crushInsert(t, "m1", "s1", "assistant", 1784282400, []any{
				map[string]any{"type": "tool_call", "data": map[string]any{"id": "c1", "name": "bash", "input": `{"command":"go test ./..."}`, "finished": true}}})
			sql += crushInsert(t, "m2", "s1", "tool", 1784282401, []any{
				map[string]any{"type": "tool_result", "data": map[string]any{"tool_call_id": "c1", "name": "bash", "content": "./retry.go:12:5: undefined: backoffJitter\nExit code 1", "is_error": false}}})
			return crushStore(t, "proj", sql)
		}, cmd: "go test ./...", out: "backoffJitter"},
		{name: "copilot agent transcript", kind: "copilot-chat", fixture: func(t *testing.T, _ string) string {
			t.Helper()
			return writeCopilotAgentTranscript(t, copilotChatRoot(t), "h1", "s-agent", `{"folder":"file:///work/projA"}`, []string{
				`{"type":"session.start","data":{"sessionId":"s-agent","version":1,"producer":"copilot-agent","startTime":"2026-09-14T05:46:01.086Z"},"id":"e1","timestamp":"2026-09-14T05:46:01.086Z","parentId":null}`,
				`{"type":"user.message","data":{"content":"why does the build fail"},"id":"e2","timestamp":"2026-09-14T05:46:10.000Z","parentId":"e1"}`,
				`{"type":"tool.execution_start","data":{"toolCallId":"c2","toolName":"run_in_terminal","arguments":{"command":"go test ./..."}},"id":"e5","timestamp":"2026-09-14T05:46:13.000Z"}`,
				`{"type":"tool.execution_complete","data":{"toolCallId":"c2","success":true},"id":"e6","timestamp":"2026-09-14T05:46:14.000Z"}`,
			})
		}, cmd: "go test ./..."},
	}
}

func switchRecords(ss []model.Session) (cmds, outs []string, all string) {
	var b strings.Builder
	for _, s := range ss {
		for _, m := range s.Messages {
			switch m.Role {
			case RoleCommand:
				cmds = append(cmds, m.Text)
			case RoleToolOutput:
				outs = append(outs, m.Text)
			}
			b.WriteString(m.Text)
			b.WriteByte('\n')
		}
	}
	return cmds, outs, b.String()
}

// DEJA_INDEX_TOOL_OUTPUT=0 and DEJA_INDEX_COMMANDS=0 leave tool output and
// commands out of every reader. Six readers kept tool output under the first
// and two kept commands under the second (#5413). Each row is checked with
// both switches on first, so a row whose fixture yields neither proves
// nothing and fails.
func TestEveryReaderHonoursTheToolOutputAndCommandSwitches(t *testing.T) {
	for _, r := range parseSwitchRows() {
		t.Run(r.name, func(t *testing.T) {
			parse := func(env, val string) []model.Session {
				t.Setenv("DEJA_INDEX_TOOL_OUTPUT", "")
				t.Setenv("DEJA_INDEX_COMMANDS", "")
				if env != "" {
					t.Setenv(env, val)
				}
				return parseKindForTest(t, r.kind, r.fixture(t, t.TempDir()))
			}
			cmds, _, all := switchRecords(parse("", ""))
			if !strings.Contains(strings.Join(cmds, "\n"), r.cmd) {
				t.Fatalf("control: commands = %q, want %q among them", cmds, r.cmd)
			}
			if r.out != "" && !strings.Contains(all, r.out) {
				t.Fatalf("control: %q is not in the session:\n%s", r.out, all)
			}

			cmds, outs, all := switchRecords(parse("DEJA_INDEX_TOOL_OUTPUT", "0"))
			if len(outs) > 0 || (r.out != "" && strings.Contains(all, r.out)) {
				t.Errorf("DEJA_INDEX_TOOL_OUTPUT=0 kept tool output:\n%s", all)
			}
			if !strings.Contains(strings.Join(cmds, "\n"), r.cmd) {
				t.Errorf("DEJA_INDEX_TOOL_OUTPUT=0 lost the command: %q", cmds)
			}
			if !strings.Contains(all, r.said) {
				t.Errorf("DEJA_INDEX_TOOL_OUTPUT=0 lost what was said, %q:\n%s", r.said, all)
			}

			cmds, _, all = switchRecords(parse("DEJA_INDEX_COMMANDS", "0"))
			if len(cmds) > 0 {
				t.Errorf("DEJA_INDEX_COMMANDS=0 kept commands %q", cmds)
			}
			if r.out != "" && !strings.Contains(all, r.out) {
				t.Errorf("DEJA_INDEX_COMMANDS=0 lost the tool output:\n%s", all)
			}
		})
	}
}

// Every parse the registry hands out, for every reader there is and every
// reader added later, goes through the one wrapper that applies the switches,
// so a reader that never tests them still has them applied.
func TestEveryRegisteredParseAppliesTheSwitches(t *testing.T) {
	for _, h := range AllHarnesses() {
		if !h.switched {
			t.Errorf("%s: Load bypasses the switches", h.Name)
		}
		for _, k := range h.Kinds {
			if !k.switched || k.Parse == nil {
				t.Errorf("%s: Parse bypasses the switches", k.Name)
			}
		}
	}
}

// Each parse switch drops its own role and nothing else.
func TestApplyParseSwitchesDropsOnlyTheSwitchedRoles(t *testing.T) {
	roles := []string{"user", "assistant", RoleToolOutput, RoleCommand, RoleFiles, RoleEdit, RoleWrote}
	session := func() []model.Session {
		msgs := make([]model.Message, 0, len(roles))
		for _, r := range roles {
			msgs = append(msgs, model.Message{Role: r, Text: r})
		}
		return []model.Session{{ID: "s", Messages: msgs}}
	}
	for env, role := range map[string]string{
		"DEJA_INDEX_TOOL_OUTPUT": RoleToolOutput,
		"DEJA_INDEX_COMMANDS":    RoleCommand,
		"DEJA_INDEX_PATHS":       RoleFiles,
		"DEJA_INDEX_EDITS":       RoleEdit,
		"DEJA_INDEX_WRITES":      RoleWrote,
	} {
		t.Run(env, func(t *testing.T) {
			t.Setenv(env, "0")
			parsed := ApplyParseSwitches(session())[0].Messages
			got := make([]string, 0, len(parsed))
			for _, m := range parsed {
				got = append(got, m.Role)
			}
			var want []string
			for _, r := range roles {
				if r != role {
					want = append(want, r)
				}
			}
			if strings.Join(got, ",") != strings.Join(want, ",") {
				t.Errorf("roles = %v, want %v", got, want)
			}
		})
	}
	if got := len(ApplyParseSwitches(session())[0].Messages); got != len(roles) {
		t.Errorf("every switch on: %d records, want %d", got, len(roles))
	}
	t.Setenv("DEJA_INDEX_COMMANDS", "0")
	for _, m := range ParseSwitched(session()[0]).Messages {
		if m.Role == RoleCommand {
			t.Error("a session read outside the registry kept its commands")
		}
	}
}

// A sub-agent run cut to its last turns is cut after the switches, so the
// turns it keeps are ones the index keeps: cut first, a child whose tail was
// commands and their output kept less under =0 than one whose reader dropped
// them itself.
func TestSubagentTailIsCutAfterTheParseSwitches(t *testing.T) {
	t.Setenv("DEJA_INCLUDE_SUBAGENTS", "")
	t.Setenv("DEJA_INDEX_TOOL_OUTPUT", "0")
	msgs := make([]model.Message, 0, 12)
	for range 6 {
		msgs = append(msgs, model.Message{Role: "assistant", Text: "turn"}, model.Message{Role: RoleToolOutput, Text: "out"})
	}
	ss := applyReadSwitches([]model.Session{{ID: "kid", Kind: "subagent", Messages: append([]model.Message{{Role: "user", Text: "the task"}}, msgs...)}})
	want := applyReadSwitches([]model.Session{{ID: "kid", Kind: "subagent", Messages: append([]model.Message{{Role: "user", Text: "the task"}}, dropRoles(msgs, map[string]bool{RoleToolOutput: true})...)}})
	if len(ss[0].Messages) != len(want[0].Messages) {
		t.Errorf("kept %d records, want %d as from a reader that drops the output itself", len(ss[0].Messages), len(want[0].Messages))
	}
}

// opencode sessions whose diffs changed are read beside the registry, and
// get the same switches its kinds apply.
func TestOpencodeDiffSessionsGetTheSwitches(t *testing.T) {
	db := seedDB(t, "opencode.db", `create table session(id text, parent_id text, directory text, title text, time_created any, time_updated any);
create table message(id text, session_id text, time_created any, data text);
create table part(id text, message_id text, data text);
insert into session values('ses_parent',NULL,'/w','fix it','2026-01-02T03:00:00Z','2026-01-03T03:00:00Z');
insert into session values('ses_child','ses_parent','/w','review (@general subagent)','2026-01-02T03:10:00Z','2026-01-02T03:20:00Z');
insert into message values('m1','ses_parent',1767409200000,'{"role":"user","agent":"build"}');
insert into part values('p1','m1','{"type":"text","text":"fix the retry loop","time":{"start":"2026-01-02T03:00:00Z"}}');
insert into message values('m2','ses_child',1767409800000,'{"role":"user","agent":"general"}');
insert into part values('p2','m2','{"type":"text","text":"review the retry loop","time":{"start":"2026-01-02T03:10:00Z"}}');`)
	t.Setenv("DEJA_OPENCODE_DB", db)
	diffs := t.TempDir()
	t.Setenv("DEJA_OPENCODE_DIFFS", diffs)
	paths := make([]string, 0, 2)
	for _, id := range []string{"ses_parent", "ses_child"} {
		p := filepath.Join(diffs, id+".json")
		writeExitFixture(t, p, opencodeDiffFixture)
		paths = append(paths, p)
	}
	t.Setenv("DEJA_INCLUDE_SUBAGENTS", "0")
	t.Setenv("DEJA_INDEX_EDITS", "0")
	ss, err := ParseOpencodeDiffSessions(paths)
	if err != nil {
		t.Fatal(err)
	}
	if len(ss) != 1 || ss[0].ID != "ses_parent" {
		t.Fatalf("sessions = %d, want the parent alone under DEJA_INCLUDE_SUBAGENTS=0", len(ss))
	}
	for _, m := range ss[0].Messages {
		if m.Role == RoleEdit {
			t.Errorf("DEJA_INDEX_EDITS=0 kept an edit: %.60q", m.Text)
		}
	}
}

// Every harness writes timestamps its own way, and the ones deja gets wrong do
// not error — they land in 1970 or in year one, which sorts a session out of
// every window and makes it unreachable by any time query.
func TestHermesTimeAcceptsTheShapesItSees(t *testing.T) {
	want := time.Unix(1785238966, 0).UTC()
	for name, in := range map[string]any{
		"float seconds":  float64(1785238966),
		"json number":    json.Number("1785238966"),
		"rfc3339 string": want.Format(time.RFC3339),
	} {
		got := hermesTime(in)
		if got.IsZero() {
			t.Fatalf("%s: parsed to zero", name)
		}
		if d := got.Sub(want); d > time.Second || d < -time.Second {
			t.Fatalf("%s: got %v, want about %v", name, got.UTC(), want)
		}
	}
	for name, in := range map[string]any{
		"nil":     nil,
		"garbage": "not a time",
		"bool":    true,
	} {
		if got := hermesTime(in); !got.IsZero() {
			t.Fatalf("%s parsed to %v; a wrong time is worse than none", name, got)
		}
	}
}

// Kimi writes a message either as a plain string or as typed parts. Reading
// only one shape loses half the conversation, and reading tool parts as text
// fills recall with plumbing.
func TestKimiTextHandlesBothShapes(t *testing.T) {
	for name, tc := range map[string]struct {
		in   any
		want string
	}{
		"plain string": {"  hello there  ", "hello there"},
		"text parts": {[]any{
			map[string]any{"type": "text", "text": "first"},
			map[string]any{"type": "text", "text": "second"},
		}, "first\nsecond"},
		"tool parts only": {[]any{
			map[string]any{"type": "tool_use", "id": "t1"},
		}, ""},
		"mixed": {[]any{
			map[string]any{"type": "tool_use", "id": "t1"},
			map[string]any{"type": "text", "text": "kept"},
		}, "kept"},
		"nil":    {nil, ""},
		"number": {float64(42), ""},
	} {
		if got := kimiText(tc.in); got != tc.want {
			t.Fatalf("%s: got %q, want %q", name, got, tc.want)
		}
	}
}

// The offset parser resumes an append. Skipping too little duplicates the
// message, too much loses it, and both are silent.
func TestNotesOffsetParserResumesExactly(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "notes.jsonl")
	first := `{"kind":"note","project":"p","text":"FIRSTNOTE","ts":"2026-01-01T00:00:00Z"}` + "\n"
	second := `{"kind":"note","project":"p","text":"SECONDNOTE","ts":"2026-01-01T00:01:00Z"}` + "\n"
	if err := os.WriteFile(path, []byte(first+second), 0o644); err != nil {
		t.Fatal(err)
	}
	all, err := ParseNotesFileFromOffset(path, 0)
	if err != nil {
		t.Fatal(err)
	}
	if countMessages(all) != 2 {
		t.Fatalf("full parse produced %d messages", countMessages(all))
	}
	tail, err := ParseNotesFileFromOffset(path, int64(len(first)))
	if err != nil {
		t.Fatal(err)
	}
	if countMessages(tail) != 1 {
		t.Fatalf("resumed parse produced %d messages, want the one new note", countMessages(tail))
	}
	// Past the end there is nothing to add, and that is not an error the
	// indexer should have to special-case.
	if ss, err := ParseNotesFileFromOffset(path, 1<<20); err != nil || countMessages(ss) != 0 {
		t.Fatalf("offset past the end: %d messages, err=%v", countMessages(ss), err)
	}
	// A missing file behaves the same way.
	if _, err := ParseNotesFileFromOffset(filepath.Join(dir, "absent.jsonl"), 0); err == nil {
		t.Log("absent notes file reads as empty rather than erroring; either is fine")
	}
}

// Promoted notes carry the state and tags that decide whether recall prefers
// them, so what lands on disk has to survive a round trip.
func TestAppendPromotedTaggedWritesEveryField(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("USERPROFILE", dir)
	t.Setenv("DEJA_NOTES_FILE", filepath.Join(dir, "notes.jsonl"))
	when := time.Date(2026, 3, 4, 5, 6, 7, 0, time.UTC)
	if err := AppendPromotedTagged("proj", "Title here", "the body", "claude:s1", "superseded", []string{"Retry", "retry", "#backoff"}, when); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(dir, "notes.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(strings.TrimSpace(string(b))), &got); err != nil {
		t.Fatalf("note is not json: %v\n%s", err, b)
	}
	for key, want := range map[string]string{
		"kind": "promoted", "project": "proj", "title": "Title here",
		"text": "the body", "session": "claude:s1", "state": "superseded",
	} {
		if s, _ := got[key].(string); s != want {
			t.Fatalf("%s = %q, want %q", key, s, want)
		}
	}
	tags, _ := got["tags"].([]any)
	if len(tags) != 2 {
		t.Fatalf("tags = %v, want the duplicate dropped and the hash stripped", tags)
	}
}

func countMessages(ss []model.Session) int {
	n := 0
	for _, s := range ss {
		n += len(s.Messages)
	}
	return n
}

func TestUnwrapPastedContent(t *testing.T) {
	for _, tc := range []struct{ name, in, want string }{
		{
			name: "whole prompt is a paste",
			in:   "<pasted_content id=\"dd73\">\nSet up and test ~/.claude/hooks/x.sh\n</pasted_content id=\"dd73\">",
			want: "Set up and test ~/.claude/hooks/x.sh",
		},
		{
			name: "paste then the person's question",
			in:   "<pasted_content id=\"543b\">\nhttps://example.com/pull/1\n</pasted_content id=\"543b\">\n\n\nWhat's next?",
			want: "https://example.com/pull/1\n\n\n\n\nWhat's next?",
		},
		{
			name: "closed mid-line does not glue words",
			in:   "see <pasted_content id=\"cc08\">a failed session?</pasted_content id=\"cc08\">Can you check",
			want: "see \na failed session?\nCan you check",
		},
		{
			name: "two pastes",
			in:   "<pasted_content id=\"a\">one</pasted_content id=\"a\"> and <pasted_content id=\"b\">two</pasted_content id=\"b\">",
			want: "one\n and \ntwo",
		},
		{
			name: "a paste quoting a wrapper keeps the quote's unmatched tag",
			in:   "<pasted_content id=\"9ab0\">it showed '<pasted_content id=\"dd73\"> Set up'</pasted_content id=\"9ab0\">",
			want: "it showed '<pasted_content id=\"dd73\"> Set up'",
		},
		{
			name: "a person naming the tag keeps the sentence",
			in:   "strip the <pasted_content> wrapper from titles",
			want: "strip the <pasted_content> wrapper from titles",
		},
		{
			name: "an open with no matching close is left",
			in:   "<pasted_content id=\"a\">text</pasted_content id=\"b\">",
			want: "<pasted_content id=\"a\">text</pasted_content id=\"b\">",
		},
	} {
		if got := UnwrapPastedContent(tc.in); got != tc.want {
			t.Errorf("%s:\n got %q\nwant %q", tc.name, got, tc.want)
		}
	}
}

// Claude Code wraps pasted text in <pasted_content id="…">…</pasted_content
// id="…">, and both readers stored the tags as the person's words: sessions
// were titled `<pasted_content id="dd73"> Set up and test…` and the install's
// demo question asked "what did we decide about pasted_content set?".
func TestClaudeReadersUnwrapPastedContent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "s1.jsonl")
	body := `{"type":"user","sessionId":"s1","timestamp":"2026-08-10T10:00:00Z","message":{"role":"user","content":"<pasted_content id=\"dd73\">\nSet up and test the reminder hook\n</pasted_content id=\"dd73\">\n\nwhy does it not fire"}}` + "\n" +
		`{"type":"assistant","sessionId":"s1","timestamp":"2026-08-10T10:00:02Z","message":{"role":"assistant","content":[{"type":"text","text":"the <pasted_content id=\"x\">quoted</pasted_content id=\"x\"> stays as written"}]}}` + "\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	for name, parse := range map[string]func(string, int64) ([]model.Session, error){
		"typed":   parseClaudeTypedFromOffset,
		"generic": parseClaudeGenericFromOffset,
	} {
		ss, err := parse(path, 0)
		if err != nil || len(ss) != 1 || len(ss[0].Messages) != 2 {
			t.Fatalf("%s: %v, %+v", name, err, ss)
		}
		if got, want := ss[0].Messages[0].Text, "Set up and test the reminder hook\n\n\n\nwhy does it not fire"; got != want {
			t.Errorf("%s: user text = %q, want %q", name, got, want)
		}
		if got, want := ss[0].Messages[1].Text, `the <pasted_content id="x">quoted</pasted_content id="x"> stays as written`; got != want {
			t.Errorf("%s: assistant text = %q, want %q", name, got, want)
		}
	}
}

// Every one of these decides where deja looks for a harness. A wrong path is
// not a crash — it is a harness that silently reports nothing, which is the
// failure mode this project keeps hitting.
func TestHarnessPathsFollowTheirOverrides(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	for _, v := range []string{
		"CLINE_DIR", "CLINE_DATA_DIR", "CLINE_SESSION_DATA_DIR",
		"CLINE_MCP_SETTINGS_PATH", "XDG_CONFIG_HOME", "XDG_DATA_HOME",
	} {
		t.Setenv(v, "")
	}

	for name, got := range map[string]string{
		"cline config":   ClineConfigDir(),
		"cline sessions": ClineSessionsDir(),
		"cline mcp":      ClineMCPSettingsPath(),
		"cline plugins":  ClinePluginsDir(),
	} {
		if !strings.HasPrefix(got, home) {
			t.Fatalf("%s = %q, outside the home directory", name, got)
		}
	}
	// Plugins sit beside the data directory, not inside it: a plugin under
	// <data>/plugins is never discovered.
	if strings.HasPrefix(ClinePluginsDir(), ClineConfigDir()) {
		t.Fatalf("plugins dir %q is inside the data dir %q", ClinePluginsDir(), ClineConfigDir())
	}

	// CLINE_DIR moves the whole tree.
	moved := filepath.Join(home, "moved")
	t.Setenv("CLINE_DIR", moved)
	for name, got := range map[string]string{"config": ClineConfigDir(), "plugins": ClinePluginsDir()} {
		if !strings.HasPrefix(got, moved) {
			t.Fatalf("CLINE_DIR ignored by %s: %q", name, got)
		}
	}
	custom := filepath.Join(home, "custom.json")
	t.Setenv("CLINE_MCP_SETTINGS_PATH", custom)
	if ClineMCPSettingsPath() != custom {
		t.Fatalf("CLINE_MCP_SETTINGS_PATH ignored: %q", ClineMCPSettingsPath())
	}
}

func TestGoosePathRootRelocatesEverything(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	for _, v := range []string{"DEJA_GOOSE_ROOT", "DEJA_GOOSE_DB", "XDG_DATA_HOME"} {
		t.Setenv(v, "")
	}
	root := filepath.Join(home, "elsewhere")
	t.Setenv("GOOSE_PATH_ROOT", root)
	if got := GooseDataDir(); got != filepath.Join(root, "data") {
		t.Fatalf("GooseDataDir() = %q", got)
	}
	if got := GooseDB(); !strings.HasPrefix(got, root) {
		t.Fatalf("GooseDB() = %q, ignores GOOSE_PATH_ROOT", got)
	}
	// Legacy .jsonl transcripts live beside the SQLite store; an absent store
	// yields nothing rather than an error the caller must special-case.
	if files := GooseJSONLFiles(); len(files) != 0 {
		t.Fatalf("empty store returned files: %v", files)
	}
	if ss := LoadGoose(); len(ss) != 0 {
		t.Fatalf("empty store returned sessions: %d", len(ss))
	}
}

func TestLoadersTolerateAbsentStores(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	for _, v := range []string{
		"DEJA_GROK_DB", "DEJA_GROK_ROOT", "DEJA_HERMES_HOME",
		"DEJA_HERMES_PROFILES_ROOT", "DEJA_KIMI_ROOT", "DEJA_QWEN_ROOT",
		"DEJA_OPENCLAW_ROOT", "HERMES_HOME",
	} {
		t.Setenv(v, "")
	}
	if ss := LoadGrokDB(); len(ss) != 0 {
		t.Fatalf("grok db: %d sessions from nothing", len(ss))
	}
	if files := HermesSessionFiles(); len(files) != 0 {
		t.Fatalf("hermes: %v", files)
	}
	if ss := LoadKimi(); len(ss) != 0 {
		t.Fatalf("kimi: %d", len(ss))
	}
	if ss := LoadQwen(); len(ss) != 0 {
		t.Fatalf("qwen: %d", len(ss))
	}
	if files := QwenSessionFiles(); len(files) != 0 {
		t.Fatalf("qwen files: %v", files)
	}
	if ss := LoadOpenClaw(); len(ss) != 0 {
		t.Fatalf("openclaw: %d", len(ss))
	}
	if runtime.GOOS == "windows" {
		return
	}
	// Reading a store deja does not have must not create one.
	if _, err := os.Stat(filepath.Join(home, ".grok")); err == nil {
		t.Fatal("reading an absent store created it")
	}
}

// piSession is a pi-shaped transcript in /tmp/proj holding lines after the
// header and the first prompt.
func piSession(lines ...string) string {
	return `{"type":"session","version":3,"id":"s1","timestamp":"2026-10-01T10:00:00.000Z","cwd":"/tmp/proj"}` + "\n" +
		`{"type":"message","id":"u1","timestamp":"2026-10-01T10:00:00.000Z","message":{"role":"user","content":[{"type":"text","text":"fix the retry loop"}]}}` + "\n" +
		strings.Join(lines, "\n") + "\n"
}

func piCall(id, name, args string) string {
	return `{"type":"message","id":"a-` + id + `","timestamp":"2026-10-01T10:00:01.000Z","message":{"role":"assistant","content":[{"type":"toolCall","id":"` + id + `","name":"` + name + `","arguments":` + args + `}]}}`
}

func piResult(id, name, text, details string, failed bool) string {
	isErr := "false"
	if failed {
		isErr = "true"
	}
	return `{"type":"message","id":"r-` + id + `","timestamp":"2026-10-01T10:00:02.000Z","message":{"role":"toolResult","toolCallId":"` + id + `","toolName":"` + name + `","content":[{"type":"text","text":` + vocabJSON(text) + `}],"details":` + details + `,"isError":` + isErr + `}}`
}

// powershell is a built-in tool of the pi-coding-agent Kimchi and Senpi ship,
// beside bash and with the same {command, timeout}; the reader took only bash
// and exec for the shell, so a PowerShell run kept its output and lost the
// command, in a Senpi eval cell too (#4523).
func TestPowerShellRunIsACommand(t *testing.T) {
	for _, kind := range []string{"kimchi", "senpi", "pi"} {
		p := writePiFixture(t, piSession(
			piCall("c0", "powershell", `{"command":"go build ./..."}`),
			piResult("c0", "powershell", "package retry", `{}`, false),
			piCall("c1", "powershell", `{"command":"go vet ./retry"}`),
			piResult("c1", "powershell", "vet: unreachable\n\nCommand exited with code 1", `{}`, true),
		))
		got := commandsOf(parseKindForTest(t, kind, p))
		want := []string{"$ go build ./...  → exit 0", "$ go vet ./retry  → exit 1"}
		if strings.Join(got, "|") != strings.Join(want, "|") {
			t.Errorf("%s: commands = %q, want %q", kind, got, want)
		}
	}
	// Senpi's codemode runs powershell only inside an eval cell.
	p := writePiFixture(t, piSession(
		piCall("e0", "eval", `{"code":"await tool.powershell({command: \"go test ./retry\"})"}`),
		piResult("e0", "eval", `{"text":"ok"}`, `{"toolCalls":[{"name":"powershell","ok":true,"args":{"command":"go test ./retry"}}]}`, false),
	))
	if got := commandsOf(parseKindForTest(t, "senpi", p)); len(got) != 1 || got[0] != "$ go test ./retry" {
		t.Errorf("senpi eval: commands = %q, want the cell's powershell run", got)
	}
}

// changesOf is the files, edit and wrote records of a session, in order.
func changesOf(ss []model.Session) []string {
	var out []string
	for _, s := range ss {
		for _, m := range s.Messages {
			switch m.Role {
			case RoleFiles, RoleEdit, RoleWrote:
				out = append(out, m.Role+" "+m.Text)
			}
		}
	}
	return out
}

// omp and gjc edit in modes besides hashline and apply_patch, and the reader
// took none of their keys: omp's replace is {path, old_string, new_string},
// gjc's {path, edits:[{old_text, new_text}]}, and both patch modes are {path,
// edits:[{op, diff}]}. gjc picks replace for Claude, DeepSeek and Qwen, so
// those users' edits were a file name with nothing behind it (#4524).
func TestOmpAndGjcReplaceAndPatchEdits(t *testing.T) {
	const oldDelay, newDelay = "\tdelay := base", "\tdelay := base * time.Duration(attempt)"
	const newJitter = "\treturn time.Duration(rand.Int63n(int64(base)))"
	const created = "const maxAttempts = 5 // retries before the loop gives up"
	backoff, jitter, limits := "/tmp/proj/backoff.go", "/tmp/proj/jitter.go", "/tmp/proj/limits.go"
	rows := []struct {
		name, kind, args string
		failed           bool
		want             []string
	}{
		{"gjc replace", "gjc", `{"path":"backoff.go","edits":[{"old_text":"\tdelay := base","new_text":"\tdelay := base * time.Duration(attempt)","all":false}]}`, false,
			[]string{"files " + backoff, "edit " + backoff + "\n" + oldDelay, "wrote " + WroteRecord(backoff, newDelay)}},
		{"omp replace", "omp", `{"path":"backoff.go","old_string":"\tdelay := base","new_string":"\tdelay := base * time.Duration(attempt)"}`, false,
			[]string{"files " + backoff, "edit " + backoff + "\n" + oldDelay, "wrote " + WroteRecord(backoff, newDelay)}},
		{"omp replace, several", "omp", `{"path":"backoff.go","edits":[{"old_string":"\tdelay := base","new_string":"\tdelay := base * time.Duration(attempt)"}]}`, false,
			[]string{"files " + backoff, "edit " + backoff + "\n" + oldDelay, "wrote " + WroteRecord(backoff, newDelay)}},
		{"gjc patch", "gjc", `{"path":"jitter.go","edits":[{"op":"update","diff":"@@\n-\treturn 0\n+\treturn time.Duration(rand.Int63n(int64(base)))\n"}]}`, false,
			[]string{"files " + jitter, "edit " + jitter + "\n\treturn 0", "wrote " + WroteRecord(jitter, newJitter)}},
		{"omp patch, two hunks", "omp", `{"path":"jitter.go","edits":[{"op":"update","diff":"@@ func a\n context\n-\treturn 0\n+\treturn time.Duration(rand.Int63n(int64(base)))\n@@ func b\n-\treturn 1\n"}]}`, false,
			[]string{"files " + jitter, "edit " + jitter + "\n\treturn 0", "edit " + jitter + "\n\treturn 1", "wrote " + WroteRecord(jitter, newJitter)}},
		{"omp patch, create", "omp", `{"path":"limits.go","edits":[{"op":"create","diff":"package retry\n\n` + created + `\n"}]}`, false,
			[]string{"files " + limits, "wrote " + WroteRecord(limits, created)}},
		{"omp patch, delete", "omp", `{"path":"limits.go","edits":[{"op":"delete"}]}`, false,
			[]string{"files " + limits}},
		// A rename writes the new path: the removed lines were the old
		// file's, the added ones are the new file's (#4576).
		{"omp patch, rename", "omp", `{"path":"jitter.go","edits":[{"op":"update","rename":"backoff/jitter.go","diff":"@@\n-\treturn 0\n+\treturn time.Duration(rand.Int63n(int64(base)))\n"}]}`, false,
			[]string{"files " + jitter + "\n/tmp/proj/backoff/jitter.go", "edit " + jitter + "\n\treturn 0", "wrote " + WroteRecord("/tmp/proj/backoff/jitter.go", newJitter)}},
		// op is optional and omp reads a missing one as update
		// (pi-edit Operation::parse), with or without a rename.
		{"omp patch, no op", "omp", `{"path":"jitter.go","edits":[{"diff":"@@\n-\treturn 0\n+\treturn time.Duration(rand.Int63n(int64(base)))\n"}]}`, false,
			[]string{"files " + jitter, "edit " + jitter + "\n\treturn 0", "wrote " + WroteRecord(jitter, newJitter)}},
		{"omp patch, rename, no op", "omp", `{"path":"jitter.go","edits":[{"rename":"backoff/jitter.go","diff":"@@\n-\treturn 0\n+\treturn time.Duration(rand.Int63n(int64(base)))\n"}]}`, false,
			[]string{"files " + jitter + "\n/tmp/proj/backoff/jitter.go", "edit " + jitter + "\n\treturn 0", "wrote " + WroteRecord("/tmp/proj/backoff/jitter.go", newJitter)}},
		{"gjc replace refused", "gjc", `{"path":"backoff.go","edits":[{"old_text":"\tdelay := base","new_text":"\tdelay := base * time.Duration(attempt)"}]}`, true,
			[]string{"files " + backoff}},
	}
	for _, r := range rows {
		p := writePiFixture(t, piSession(piCall("c0", "edit", r.args), piResult("c0", "edit", "Updated", `{}`, r.failed)))
		got := changesOf(parseKindForTest(t, r.kind, p))
		if strings.Join(got, "|") != strings.Join(r.want, "|") {
			t.Errorf("%s: records = %q, want %q", r.name, got, r.want)
		}
	}
}

// omp's default edit mode is hashline in its own syntax: `[path#TAG]`
// sections, `PUT N.=M:` ops whose body rows start with "+", the whole wrapped
// in `*** Begin Patch`. The reader knew only gjc's `§path`, so every omp edit
// left nothing (#4525). The replaced lines come off the result's diff.
func TestOmpHashlineEdit(t *testing.T) {
	const newLoop = "\tfor attempt := 0; attempt < maxAttempts; attempt++ {"
	retry, jitter := "/tmp/proj/retry.go", "/tmp/proj/jitter.go"
	rows := []struct {
		name, input, details string
		failed               bool
		want                 []string
	}{
		{"put", "*** Begin Patch\n[retry.go#1a2b]\nPUT 3.=3:\n+" + newLoop + "\n*** End Patch", `{"path":"retry.go","diff":"-3|\tfor {\n+3|` + strings.ReplaceAll(newLoop, "\t", `\t`) + `"}`, false,
			[]string{"files " + retry, "wrote " + WroteRecord(retry, newLoop), "edit " + retry + "\n\tfor {"}},
		{"bare, two sections", "[retry.go#1A2B]\nPUT >3:\n+" + newLoop + "\n[jitter.go#3c4d]\nCUT 5.=5\n", `{}`, false,
			[]string{"files " + retry + "\n" + jitter, "wrote " + WroteRecord(retry, newLoop)}},
		{"refused", "[retry.go#1a2b]\nPUT 3.=3:\n+" + newLoop, `{}`, true,
			[]string{"files " + retry}},
		// omp takes the path as everything between the brackets less the
		// tag, so a path with "#" in it or one quoted for its space is a
		// file too (qxo/IOa in omp's cli).
		{"hash and quotes", "[src/C#/Retry.cs#1a2b]\nPUT >3:\n+" + newLoop + "\n[\"my proj/jitter.go\"#3c4d]\nCUT 5.=5", `{}`, false,
			[]string{"files /tmp/proj/src/C#/Retry.cs\n/tmp/proj/my proj/jitter.go", "wrote " + WroteRecord("/tmp/proj/src/C#/Retry.cs", newLoop)}},
		// gjc's own form is read as before, a TOML section line in its body
		// included.
		{"gjc", "§retry.toml\n≔1rq\n[server]\nretry_attempts_before_giving_up = 5", `{}`, false,
			[]string{"files /tmp/proj/retry.toml", "wrote " + WroteRecord("/tmp/proj/retry.toml", "retry_attempts_before_giving_up = 5")}},
	}
	for _, r := range rows {
		p := writePiFixture(t, piSession(piCall("c0", "edit", `{"input":`+vocabJSON(r.input)+`}`), piResult("c0", "edit", "Updated retry.go", r.details, r.failed)))
		got := changesOf(parseKindForTest(t, "omp", p))
		if strings.Join(got, "|") != strings.Join(r.want, "|") {
			t.Errorf("%s: records = %q, want %q", r.name, got, r.want)
		}
	}
}

// prime's one tool is ipython, and its edit skill changes a file from inside a
// cell; each change lands on the result as details.diffs[{path, oldStr,
// newStr}], which prime itself reads back to list the files a session edited.
// The reader kept the cell's output only (#4526).
func TestPrimeEditInACellIsRecorded(t *testing.T) {
	const newLoop = "\tfor attempt := 0; attempt < maxAttempts; attempt++ {"
	const jitter = "func backoffJitter(attempt int) time.Duration { return 0 }"
	diffs := `{"status":"ok","stdout":"Edited /tmp/proj/retry.go","diffs":[` +
		`{"path":"/tmp/proj/retry.go","oldStr":"\tfor {","newStr":` + vocabJSON(newLoop) + `,"startLine":3},` +
		`{"path":"jitter.go","oldStr":"","newStr":` + vocabJSON(jitter) + `,"startLine":1}]}`
	p := writePiFixture(t, piSession(
		piCall("c0", "ipython", `{"code":"await edit(path=\"retry.go\", old_str=\"\\tfor {\", new_str=\"…\")"}`),
		piResult("c0", "ipython", "Edited /tmp/proj/retry.go", diffs, false),
		piCall("c1", "ipython", `{"code":"print(1)"}`),
		piResult("c1", "ipython", "1", `{"status":"ok","stdout":"1","diffs":[]}`, false),
	))
	got := changesOf(parseKindForTest(t, "prime", p))
	want := []string{
		"files /tmp/proj/retry.go\n/tmp/proj/jitter.go",
		"edit /tmp/proj/retry.go\n\tfor {",
		"wrote " + WroteRecord("/tmp/proj/retry.go", newLoop),
		"wrote " + WroteRecord("/tmp/proj/jitter.go", jitter),
	}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("records = %q, want %q", got, want)
	}
}

// A resumed parse must produce the same session identity as a whole read.
//
// pi is on the incremental-append list, and pi filenames are
// `<ISO-timestamp>_<uuid>.jsonl` while the header's id is the bare uuid — so a
// tail read past the header filed itself under an id the whole read never
// produces, and the appended turn landed beside the session instead of in it.
// The existing offset test asserted only the message text, which is why this
// went unnoticed (#2870).
func TestPiKeepsItsIdentityWhenResumedPastTheHeader(t *testing.T) {
	root := t.TempDir()
	t.Setenv("HOME", filepath.Join(root, "home"))
	t.Setenv("USERPROFILE", os.Getenv("HOME"))
	t.Setenv("DEJA_PI_ROOT", filepath.Join(root, "pi-sessions"))
	project := filepath.Join(root, "pi-sessions", "--tmp-demo--")
	if err := os.MkdirAll(project, 0o755); err != nil {
		t.Fatal(err)
	}
	head := `{"type":"session","version":3,"id":"9f1c-uuid","timestamp":"2026-01-02T03:04:05Z","cwd":"/tmp/demo"}` + "\n"
	first := `{"type":"message","id":"u1","timestamp":"2026-01-02T03:04:10Z","message":{"role":"user","content":[{"type":"text","text":"first"}]}}` + "\n"
	tail := `{"type":"message","id":"a1","timestamp":"2026-01-02T03:04:15Z","message":{"role":"assistant","content":[{"type":"text","text":"reply"}]}}` + "\n"
	path := filepath.Join(project, "2026-01-02T03-04-05_9f1c-uuid.jsonl")
	if err := os.WriteFile(path, []byte(head+first+tail), 0o644); err != nil {
		t.Fatal(err)
	}

	whole, err := ParsePiFile(path)
	if err != nil {
		t.Fatal(err)
	}
	resumed, err := ParsePiFileFromOffset(path, int64(len(head)+len(first)))
	if err != nil {
		t.Fatal(err)
	}
	if len(whole) != 1 || len(resumed) != 1 {
		t.Fatalf("want one session from each read, got %d and %d", len(whole), len(resumed))
	}
	// Both against the header's own value, not merely against each other: an
	// equivalence assertion is blind to a change that breaks both reads alike.
	for _, got := range []struct {
		read string
		id   string
	}{{"whole", whole[0].ID}, {"resumed", resumed[0].ID}} {
		if got.id != "9f1c-uuid" {
			t.Errorf("%s read id = %q, want the header's 9f1c-uuid", got.read, got.id)
		}
	}
	if resumed[0].Project != whole[0].Project {
		t.Errorf("resumed project = %q, whole-read project = %q", resumed[0].Project, whole[0].Project)
	}
}

// A file whose first line is not a header must not have that line read as one:
// the resumed read fetches line 1 blind, and without the type check a plain
// message would hand the session its own message id.
func TestPiIgnoresAFirstLineThatIsNotAHeader(t *testing.T) {
	root := t.TempDir()
	t.Setenv("HOME", filepath.Join(root, "home"))
	t.Setenv("USERPROFILE", os.Getenv("HOME"))
	t.Setenv("DEJA_PI_ROOT", filepath.Join(root, "pi-sessions"))
	project := filepath.Join(root, "pi-sessions", "--tmp-headless--")
	if err := os.MkdirAll(project, 0o755); err != nil {
		t.Fatal(err)
	}
	first := `{"type":"message","id":"u1","timestamp":"2026-01-02T03:04:10Z","message":{"role":"user","content":[{"type":"text","text":"first"}]}}` + "\n"
	tail := `{"type":"message","id":"a1","timestamp":"2026-01-02T03:04:15Z","message":{"role":"assistant","content":[{"type":"text","text":"reply"}]}}` + "\n"
	path := filepath.Join(project, "headless.jsonl")
	if err := os.WriteFile(path, []byte(first+tail), 0o644); err != nil {
		t.Fatal(err)
	}

	resumed, err := ParsePiFileFromOffset(path, int64(len(first)))
	if err != nil {
		t.Fatal(err)
	}
	if len(resumed) != 1 {
		t.Fatalf("want one session, got %d", len(resumed))
	}
	if resumed[0].ID != "headless" {
		t.Errorf("id = %q, want the filename headless — a message line was read as the header", resumed[0].ID)
	}
}

// omp and OpenClaw share the reader and promote the header's cwd to the
// project, so losing the header renamed the project as well as the id. Both
// are off the append list today; this is what has to hold before either joins
// it.
func TestOmpKeepsHeaderProjectWhenResumedPastTheHeader(t *testing.T) {
	root := t.TempDir()
	t.Setenv("HOME", filepath.Join(root, "home"))
	t.Setenv("USERPROFILE", os.Getenv("HOME"))
	t.Setenv("DEJA_OMP_ROOT", filepath.Join(root, "omp-sessions"))
	project := filepath.Join(root, "omp-sessions", "-Code-demo-course")
	if err := os.MkdirAll(project, 0o755); err != nil {
		t.Fatal(err)
	}
	head := `{"type":"session","version":3,"id":"omp-uuid","timestamp":"2026-01-02T03:04:05Z","cwd":"/Users/x/work/authsvc"}` + "\n"
	first := `{"type":"message","id":"u1","timestamp":"2026-01-02T03:04:10Z","message":{"role":"user","content":[{"type":"text","text":"first"}]}}` + "\n"
	tail := `{"type":"message","id":"a1","timestamp":"2026-01-02T03:04:15Z","message":{"role":"assistant","content":[{"type":"text","text":"reply"}]}}` + "\n"
	path := filepath.Join(project, "2026-01-02T03-04-05_omp-uuid.jsonl")
	if err := os.WriteFile(path, []byte(head+first+tail), 0o644); err != nil {
		t.Fatal(err)
	}

	whole, err := ParseOmpFile(path)
	if err != nil {
		t.Fatal(err)
	}
	resumed, err := ParseOmpFileFromOffset(path, int64(len(head)+len(first)))
	if err != nil {
		t.Fatal(err)
	}
	if len(whole) != 1 || len(resumed) != 1 {
		t.Fatalf("want one session from each read, got %d and %d", len(whole), len(resumed))
	}
	if resumed[0].ID != whole[0].ID {
		t.Errorf("resumed id = %q, whole-read id = %q", resumed[0].ID, whole[0].ID)
	}
	// Absolute, for the same reason: the header names /Users/x/work/authsvc,
	// and the directory the file sits in says Code-demo-course.
	for _, got := range []struct {
		read    string
		project string
	}{{"whole", whole[0].Project}, {"resumed", resumed[0].Project}} {
		if !strings.Contains(got.project, "authsvc") {
			t.Errorf("%s read project = %q, want the header cwd's authsvc", got.read, got.project)
		}
	}
}

// omp writes a fixed-size title slot before the session header, so line 1 is
// not the header. A resumed read took line 1, found no session record, and the
// appended turns landed in a second session keyed by the file name (#4406).
func TestOmpKeepsItsIdentityWhenTheTitleSlotComesFirst(t *testing.T) {
	root := t.TempDir()
	t.Setenv("HOME", filepath.Join(root, "home"))
	t.Setenv("USERPROFILE", os.Getenv("HOME"))
	t.Setenv("DEJA_OMP_ROOT", filepath.Join(root, "omp-sessions"))
	project := filepath.Join(root, "omp-sessions", "--tmp-proj--")
	if err := os.MkdirAll(project, 0o755); err != nil {
		t.Fatal(err)
	}
	title := `{"type":"title","v":1,"title":"","updatedAt":"2026-10-01T09:00:00.000Z","pad":"    "}` + "\n"
	head := `{"type":"session","version":3,"id":"0199aaaa-0000-7000-8000-000000000001","timestamp":"2026-10-01T09:00:00.000Z","cwd":"/tmp/proj"}` + "\n"
	first := `{"type":"message","id":"u1","timestamp":"2026-10-01T09:00:10Z","message":{"role":"user","content":[{"type":"text","text":"fix the retry loop"}]}}` + "\n"
	tail := `{"type":"message","id":"u2","timestamp":"2026-10-01T09:01:00Z","message":{"role":"user","content":[{"type":"text","text":"check the backoff too"}]}}` + "\n"
	path := filepath.Join(project, "2026-10-01T09-00-00-000Z_0199aaaa-0000-7000-8000-000000000001.jsonl")
	if err := os.WriteFile(path, []byte(title+head+first+tail), 0o644); err != nil {
		t.Fatal(err)
	}

	resumed, err := ParseOmpFileFromOffset(path, int64(len(title)+len(head)+len(first)))
	if err != nil {
		t.Fatal(err)
	}
	if len(resumed) != 1 {
		t.Fatalf("want one session from the resumed read, got %d", len(resumed))
	}
	if resumed[0].ID != "0199aaaa-0000-7000-8000-000000000001" {
		t.Errorf("resumed read id = %q, want the header's 0199aaaa-…", resumed[0].ID)
	}
	if want := claudeProjectName(pathToProjectKey("/tmp/proj")); resumed[0].Project != want {
		t.Errorf("resumed project = %q, want the header cwd's %q", resumed[0].Project, want)
	}
	if len(resumed[0].Messages) != 1 || resumed[0].Messages[0].Text != "check the backoff too" {
		t.Errorf("resumed messages = %#v, want only the appended turn", resumed[0].Messages)
	}
}

func TestParsePiFile(t *testing.T) {
	root := t.TempDir()
	t.Setenv("HOME", filepath.Join(root, "home"))
	t.Setenv("USERPROFILE", os.Getenv("HOME"))
	t.Setenv("DEJA_PI_ROOT", filepath.Join(root, "pi-sessions"))
	// The encoded name carries a suffix nobody can have on disk. The decoder
	// resolves an encoded project against the filesystem, so a fixture naming
	// `/tmp/deja-vu` asserts the dash fallback only on a machine that happens
	// not to have that directory — and a contributor who did failed this test
	// twice on unmodified main (#3512).
	unique := encodedFixtureSuffix(t)
	project := filepath.Join(root, "pi-sessions", "--tmp-deja-vu-"+unique+"--")
	if err := os.MkdirAll(project, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(project, "2026-01-02T03-04-05-000Z_abc-123.jsonl")
	data := `{"type":"session","version":3,"id":"abc-123","timestamp":"2026-01-02T03:04:05Z","cwd":"/tmp/deja-vu-fixture"}
{"type":"model_change","id":"m1","parentId":null,"timestamp":"2026-01-02T03:04:06Z","provider":"anthropic","modelId":"claude-sonnet-4"}
{"type":"message","id":"u1","parentId":"m1","timestamp":"2026-01-02T03:04:10Z","message":{"role":"user","content":[{"type":"text","text":"what is pi?"}],"timestamp":1767337450000}}
{"type":"message","id":"a1","parentId":"u1","timestamp":"2026-01-02T03:04:15Z","message":{"role":"assistant","content":[{"type":"thinking","thinking":"Let me think..."},{"type":"text","text":"pi is a coding agent"}],"api":"anthropic","model":"claude-sonnet-4","usage":{"input":10,"output":20},"stopReason":"endTurn","timestamp":1767337455000}}
{"type":"message","id":"t1","parentId":"a1","timestamp":"2026-01-02T03:04:16Z","message":{"role":"toolResult","toolCallId":"call_1","toolName":"bash","content":[{"type":"text","text":"some output"}]}}
{"type":"message","id":"a2","parentId":"t1","timestamp":"2026-01-02T03:04:20Z","message":{"role":"assistant","content":[{"type":"text","text":"here is the result"}],"timestamp":1767337460000}}
`
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
	ss, err := ParsePiFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(ss) != 1 {
		t.Fatalf("want 1 session, got %d", len(ss))
	}
	s := ss[0]
	if s.Harness != "pi" {
		t.Fatalf("harness = %q, want pi", s.Harness)
	}
	if s.ID != "abc-123" {
		t.Fatalf("id = %q, want abc-123", s.ID)
	}
	// The header's cwd names it; the folder is the fallback for a header
	// without one (#4427).
	if want := "tmp/deja-vu-fixture"; s.Project != want {
		t.Fatalf("project = %q, want %q", s.Project, want)
	}
	if len(s.Messages) != 4 {
		t.Fatalf("want 4 messages (user + 2 assistant + tool output), got %d: %#v", len(s.Messages), s.Messages)
	}
	if s.Messages[0].Role != "user" || s.Messages[0].Text != "what is pi?" {
		t.Fatalf("message[0] = %#v", s.Messages[0])
	}
	if s.Messages[1].Role != "assistant" || s.Messages[1].Text != "pi is a coding agent" {
		t.Fatalf("message[1] = %#v", s.Messages[1])
	}
	// Tool output is kept as roleToolOutput so pi feeds friction and `--role tool`
	// like the other harnesses that carry it.
	if s.Messages[2].Role != RoleToolOutput || s.Messages[2].Text != "some output" {
		t.Fatalf("message[2] = %#v, want tool output", s.Messages[2])
	}
	if s.Messages[3].Role != "assistant" || s.Messages[3].Text != "here is the result" {
		t.Fatalf("message[3] = %#v", s.Messages[3])
	}
}

func TestParsePiFileFromOffset(t *testing.T) {
	root := t.TempDir()
	t.Setenv("HOME", filepath.Join(root, "home"))
	t.Setenv("USERPROFILE", os.Getenv("HOME"))
	t.Setenv("DEJA_PI_ROOT", filepath.Join(root, "pi-sessions"))
	project := filepath.Join(root, "pi-sessions", "--tmp-demo--")
	if err := os.MkdirAll(project, 0o755); err != nil {
		t.Fatal(err)
	}
	line1 := `{"type":"session","version":3,"id":"off-1","timestamp":"2026-01-02T03:04:05Z","cwd":"/tmp/demo"}` + "\n"
	line2 := `{"type":"message","id":"u1","parentId":null,"timestamp":"2026-01-02T03:04:10Z","message":{"role":"user","content":[{"type":"text","text":"first"}]}}` + "\n"
	line3 := `{"type":"message","id":"a1","parentId":"u1","timestamp":"2026-01-02T03:04:15Z","message":{"role":"assistant","content":[{"type":"text","text":"reply"}]}}` + "\n"
	path := filepath.Join(project, "session.jsonl")
	if err := os.WriteFile(path, []byte(line1+line2+line3), 0o644); err != nil {
		t.Fatal(err)
	}
	// Parse from offset past the header + first message
	offset := int64(len(line1) + len(line2))
	ss, err := ParsePiFileFromOffset(path, offset)
	if err != nil {
		t.Fatal(err)
	}
	if len(ss) != 1 || len(ss[0].Messages) != 1 {
		t.Fatalf("want 1 session with 1 message, got %#v", ss)
	}
	if ss[0].Messages[0].Text != "reply" {
		t.Fatalf("message text = %q, want reply", ss[0].Messages[0].Text)
	}
}

func TestParsePiFileEmpty(t *testing.T) {
	root := t.TempDir()
	t.Setenv("HOME", filepath.Join(root, "home"))
	t.Setenv("USERPROFILE", os.Getenv("HOME"))
	t.Setenv("DEJA_PI_ROOT", filepath.Join(root, "pi-sessions"))
	project := filepath.Join(root, "pi-sessions", "--tmp-empty--")
	if err := os.MkdirAll(project, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(project, "session.jsonl")
	data := `{"type":"session","version":3,"id":"empty-1","timestamp":"2026-01-02T03:04:05Z","cwd":"/tmp/empty"}
{"type":"model_change","id":"m1","timestamp":"2026-01-02T03:04:06Z"}
`
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
	ss, err := ParsePiFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(ss) != 0 {
		t.Fatalf("expected no sessions from metadata-only file, got %d", len(ss))
	}
}

func TestPiDiscoveryAndProjectBase(t *testing.T) {
	root := t.TempDir()
	t.Setenv("HOME", root)
	t.Setenv("USERPROFILE", root)
	t.Setenv("DEJA_PI_ROOT", "")
	if got := PiRoot(); got != filepath.Join(root, ".pi", "agent", "sessions") {
		t.Fatalf("default PiRoot = %q", got)
	}
	piRoot := filepath.Join(root, "pi-sessions")
	t.Setenv("DEJA_PI_ROOT", piRoot)
	proj := filepath.Join(piRoot, "--Users-max-app--")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(proj, "s.jsonl")
	if err := os.WriteFile(path, []byte(`{"type":"session","id":"s1","timestamp":"2026-01-02T03:04:05Z"}
{"type":"message","timestamp":"2026-01-02T03:04:06Z","message":{"role":"user","content":"pi discovery fact"}}
`), 0o644); err != nil {
		t.Fatal(err)
	}
	files := PiSessionFiles()
	if len(files) != 1 || files[0] != path {
		t.Fatalf("PiSessionFiles = %v", files)
	}
	ss := LoadPi()
	if len(ss) != 1 || ss[0].ID != "s1" || ss[0].Harness != "pi" {
		t.Fatalf("LoadPi = %#v", ss)
	}
	if !strings.HasSuffix(ss[0].Project, "app") {
		t.Fatalf("pi project decode = %q, want …app", ss[0].Project)
	}
	if base := PiProjectDirBase(path); base != "--Users-max-app--" {
		t.Fatalf("PiProjectDirBase = %q", base)
	}
	// A bare filename resolves to an empty encoded base.
	if base := PiProjectDirBase("plain.jsonl"); base != "plain.jsonl" && base != "" {
		t.Fatalf("bare PiProjectDirBase = %q", base)
	}
}

// The toolCall blocks pi writes, one of each kind, with the argument names a
// real pi, omp, OpenClaw, gjc and Kimchi session carries: `path` for the file
// tools, `command` for the shell, `edits[].oldText/newText` for an edit and
// `content` for a whole-file write. The reader kept the text and skipped the
// calls, so `deja files`, `how`, `restore` and `blame` had nothing for any of
// the six harnesses (#4113).
const piToolFixture = `{"type":"session","version":3,"id":"pi-tools","timestamp":"2026-10-01T20:50:00.000Z","cwd":"/work/app"}
{"type":"message","id":"u1","timestamp":"2026-10-01T20:50:01.000Z","message":{"role":"user","content":[{"type":"text","text":"raise the retry count"}]}}
{"type":"message","id":"a1","timestamp":"2026-10-01T20:50:02.000Z","message":{"role":"assistant","content":[{"type":"text","text":"Reading the config."},{"type":"toolCall","id":"c-read","name":"read","arguments":{"path":"retry.cfg"}}]}}
{"type":"message","id":"r1","timestamp":"2026-10-01T20:50:03.000Z","message":{"role":"toolResult","toolCallId":"c-read","toolName":"read","content":[{"type":"text","text":"retries = 3"}],"isError":false}}
{"type":"message","id":"a2","timestamp":"2026-10-01T20:50:04.000Z","message":{"role":"assistant","content":[{"type":"toolCall","id":"c-edit","name":"edit","arguments":{"path":"retry.cfg","edits":[{"oldText":"max_retry_attempts_before_giving_up = 3","newText":"max_retry_attempts_before_giving_up = 5"}]}}]}}
{"type":"message","id":"r2","timestamp":"2026-10-01T20:50:05.000Z","message":{"role":"toolResult","toolCallId":"c-edit","toolName":"edit","content":[{"type":"text","text":"edited"}],"isError":false}}
{"type":"message","id":"a2b","timestamp":"2026-10-01T20:50:05.500Z","message":{"role":"assistant","content":[{"type":"toolCall","id":"c-edit-bad","name":"edit","arguments":{"path":"retry.cfg","edits":[{"oldText":"a line the file never held at any point","newText":"what the agent hoped to write there instead"}]}}]}}
{"type":"message","id":"r2b","timestamp":"2026-10-01T20:50:05.600Z","message":{"role":"toolResult","toolCallId":"c-edit-bad","toolName":"edit","content":[{"type":"text","text":"Could not find the exact text"}],"isError":true}}
{"type":"message","id":"a3","timestamp":"2026-10-01T20:50:06.000Z","message":{"role":"assistant","content":[{"type":"toolCall","id":"c-write","name":"write","arguments":{"path":"/work/app/NOTES.md","content":"backoff doubles on every failed attempt\n"}}]}}
{"type":"message","id":"r3","timestamp":"2026-10-01T20:50:07.000Z","message":{"role":"toolResult","toolCallId":"c-write","toolName":"write","content":[{"type":"text","text":"wrote"}],"isError":false}}
{"type":"message","id":"a4","timestamp":"2026-10-01T20:50:08.000Z","message":{"role":"assistant","content":[{"type":"toolCall","id":"c-bash","name":"bash","arguments":{"command":"git status --short"}},{"type":"toolCall","id":"c-exec","name":"exec","arguments":{"command":"go test ./..."}}]}}
{"type":"message","id":"r4","timestamp":"2026-10-01T20:50:09.000Z","message":{"role":"toolResult","toolCallId":"c-bash","toolName":"bash","content":[{"type":"text","text":" M retry.cfg"}],"isError":false}}
{"type":"message","id":"r5","timestamp":"2026-10-01T20:50:10.000Z","message":{"role":"toolResult","toolCallId":"c-exec","toolName":"exec","content":[{"type":"text","text":"FAIL"}],"isError":true,"details":{"exitCode":1}}}
`

// gjc's edit takes one string in its hashline form: `§path`, then ops
// (`≔A..B` replace, `«A` insert before, `»A` insert after) each followed by the
// lines they write. The replaced text is not in the call; gjc's result carries
// it in details.diff.
const gjcHashlineFixture = `{"type":"session","version":3,"id":"gjc-tools","timestamp":"2026-10-01T20:55:00.000Z","cwd":"/work/app"}
{"type":"message","id":"u1","timestamp":"2026-10-01T20:55:01.000Z","message":{"role":"user","content":[{"type":"text","text":"raise the retry count"}]}}
{"type":"message","id":"a1","timestamp":"2026-10-01T20:55:02.000Z","message":{"role":"assistant","content":[{"type":"toolCall","id":"c-bad","name":"edit","arguments":{"input":"*** Begin Patch\n*** Update File: retry.cfg\n*** End Patch\n"}}]}}
{"type":"message","id":"r1","timestamp":"2026-10-01T20:55:03.000Z","message":{"role":"toolResult","toolCallId":"c-bad","toolName":"edit","content":[{"type":"text","text":"input must begin with \"§PATH\""}],"isError":true}}
{"type":"message","id":"a2","timestamp":"2026-10-01T20:55:04.000Z","message":{"role":"assistant","content":[{"type":"toolCall","id":"c-hash","name":"edit","arguments":{"input":"§retry.cfg\n≔1rq\nmax_retry_attempts_before_giving_up = 5\n"}}]}}
{"type":"message","id":"r2","timestamp":"2026-10-01T20:55:05.000Z","message":{"role":"toolResult","toolCallId":"c-hash","toolName":"edit","content":[{"type":"text","text":"retry.cfg:\n-1--|max_retry_attempts_before_giving_up = 3\n+1sb|max_retry_attempts_before_giving_up = 5"}],"details":{"diff":"-1|max_retry_attempts_before_giving_up = 3\n+1|max_retry_attempts_before_giving_up = 5","firstChangedLine":1,"op":"update"},"isError":false}}
`

func writePiFixture(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "s.jsonl")
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func rolesOf(s model.Session, role string) []string {
	var out []string
	for _, m := range s.Messages {
		if m.Role == role {
			out = append(out, m.Text)
		}
	}
	return out
}

func wroteHas(t *testing.T, records []string, path, line string) bool {
	t.Helper()
	h, ok := HashWrittenLine(line)
	if !ok {
		t.Fatalf("%q is too short to be evidence, so this measures nothing", line)
	}
	for _, r := range records {
		if p, has := WroteRecordHas(r, h); has && p == path {
			return true
		}
	}
	return false
}

func TestPiShapedToolCallsAreIndexed(t *testing.T) {
	ss, err := ParsePiFile(writePiFixture(t, piToolFixture))
	if err != nil || len(ss) != 1 {
		t.Fatalf("parse: %v, %d sessions", err, len(ss))
	}
	s := ss[0]
	files := strings.Join(rolesOf(s, RoleFiles), "\n")
	cfg := "/work/app/retry.cfg"
	for _, want := range []string{cfg, "/work/app/NOTES.md"} {
		if !strings.Contains(files, want) {
			t.Errorf("files rows %q do not name %s", files, want)
		}
	}
	cmds := rolesOf(s, RoleCommand)
	if len(cmds) != 2 || cmds[0] != "$ git status --short  → exit 0" || cmds[1] != "$ go test ./...  → exit 1" {
		t.Errorf("commands = %q", cmds)
	}
	edits := rolesOf(s, RoleEdit)
	if len(edits) != 1 || edits[0] != cfg+"\nmax_retry_attempts_before_giving_up = 3" {
		t.Errorf("edit spans = %q", edits)
	}
	wrote := rolesOf(s, RoleWrote)
	// An edit the harness refused changed nothing, and restore must not hand
	// its text back as something that stopped existing.
	if wroteHas(t, wrote, cfg, "what the agent hoped to write there instead") {
		t.Errorf("a refused edit is recorded as written: %q", wrote)
	}
	if !wroteHas(t, wrote, cfg, "max_retry_attempts_before_giving_up = 5") {
		t.Errorf("the edit's written side is not recorded: %q", wrote)
	}
	if !wroteHas(t, wrote, "/work/app/NOTES.md", "backoff doubles on every failed attempt") {
		t.Errorf("the write is not recorded: %q", wrote)
	}

	// The switches every other harness honours turn these off here too.
	for _, env := range []string{"DEJA_INDEX_PATHS", "DEJA_INDEX_COMMANDS", "DEJA_INDEX_EDITS", "DEJA_INDEX_WRITES"} {
		t.Setenv(env, "0")
	}
	ss, _ = ParsePiFile(writePiFixture(t, piToolFixture))
	for _, role := range []string{RoleFiles, RoleCommand, RoleEdit, RoleWrote} {
		if got := rolesOf(ss[0], role); len(got) != 0 {
			t.Errorf("%s rows with indexing switched off: %q", role, got)
		}
	}
}

// The incremental read starts past the header, and the paths still resolve
// against the session's directory.
func TestPiShapedToolCallsFromOffset(t *testing.T) {
	p := writePiFixture(t, piToolFixture)
	off := int64(strings.Index(piToolFixture, `{"type":"message","id":"a2"`))
	ss, err := ParsePiFileFromOffset(p, off)
	if err != nil || len(ss) != 1 {
		t.Fatalf("parse: %v, %d sessions", err, len(ss))
	}
	if edits := rolesOf(ss[0], RoleEdit); len(edits) != 1 || !strings.HasPrefix(edits[0], "/work/app/retry.cfg"+"\n") {
		t.Errorf("edit spans from offset = %q", edits)
	}
}

func TestGjcHashlineEditsAreIndexed(t *testing.T) {
	root := t.TempDir()
	t.Setenv("DEJA_GJC_ROOT", root)
	p := filepath.Join(root, "v2-x", "s.jsonl")
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(gjcHashlineFixture), 0o644); err != nil {
		t.Fatal(err)
	}
	ss, err := ParseGjcFile(p)
	if err != nil || len(ss) != 1 {
		t.Fatalf("parse: %v, %d sessions", err, len(ss))
	}
	s := ss[0]
	cfg := "/work/app/retry.cfg"
	if files := rolesOf(s, RoleFiles); len(files) != 1 || files[0] != cfg {
		t.Errorf("files rows = %q, want the one file the hashline edit named", files)
	}
	edits := rolesOf(s, RoleEdit)
	if len(edits) != 1 || edits[0] != cfg+"\nmax_retry_attempts_before_giving_up = 3" {
		t.Errorf("edit spans = %q, want the replaced line from gjc's diff", edits)
	}
	if !wroteHas(t, rolesOf(s, RoleWrote), cfg, "max_retry_attempts_before_giving_up = 5") {
		t.Errorf("the hashline payload is not recorded as written: %q", rolesOf(s, RoleWrote))
	}
}

// A shell call whose result lands a pass later sends the file back for a whole
// read: a failure's code and a clean run's exit 0 are both marked on the
// stored command (#4443, #5527). A call that ran no command, as an empty one,
// lets the clean result append.
func TestPiResumesPastACleanCommandResult(t *testing.T) {
	head := `{"type":"session","version":3,"id":"s","timestamp":"2026-09-01T09:00:00Z","cwd":"/tmp/proj"}
{"type":"message","id":"a1","timestamp":"2026-09-01T09:02:00Z","message":{"role":"assistant","content":[{"type":"toolCall","id":"c1","name":"bash","arguments":{"command":"deja search retry"}}]}}
`
	for _, tc := range []struct {
		name, res string
		resumes   bool
	}{
		{"clean", `"details":{"exitCode":0},"isError":false`, false},
		{"clean without a code", `"isError":false`, false},
		{"failed", `"details":{"exitCode":2},"isError":true`, false},
		{"nonzero code", `"details":{"exitCode":1},"isError":false`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := filepath.Join(t.TempDir(), "s.jsonl")
			body := head + `{"type":"message","id":"r1","timestamp":"2026-09-01T09:02:01Z","message":{"role":"toolResult","toolCallId":"c1","toolName":"bash","content":[{"type":"text","text":"ok"}],` + tc.res + "}}\n"
			if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
				t.Fatal(err)
			}
			if got := piResumes(p, int64(len(head))); got != tc.resumes {
				t.Errorf("piResumes = %v, want %v", got, tc.resumes)
			}
		})
	}
	// The control: a clean answer to a call the file does not hold.
	p := filepath.Join(t.TempDir(), "s.jsonl")
	body := head + `{"type":"message","id":"r1","timestamp":"2026-09-01T09:02:01Z","message":{"role":"toolResult","toolCallId":"c9","toolName":"bash","content":[{"type":"text","text":"ok"}],"isError":false}}` + "\n"
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if !piResumes(p, int64(len(head))) {
		t.Error("a clean answer to a call that ran no command sent the file back")
	}
}

// An edit result answers a call stored already: its refusal is what drops the
// edit, so the file goes back for a whole read. A read's result changes
// nothing, and a call made in the tail is known when its answer arrives.
func TestPiResumesOnAnEditResultForAStoredCall(t *testing.T) {
	call := func(id, name string) string {
		return `{"type":"message","id":"a-` + id + `","timestamp":"2026-09-01T09:02:00Z","message":{"role":"assistant","content":[{"type":"text","text":"on it"},{"type":"toolCall","id":"` + id + `","name":"` + name + `","arguments":{"path":"retry.cfg"}}]}}` + "\n"
	}
	result := func(id, name string) string {
		return `{"type":"message","id":"r-` + id + `","timestamp":"2026-09-01T09:02:01Z","message":{"role":"toolResult","toolCallId":"` + id + `","toolName":"` + name + `","content":[{"type":"text","text":"Could not find the exact text"}],"isError":true}}` + "\n"
	}
	head := `{"type":"session","version":3,"id":"s","timestamp":"2026-09-01T09:00:00Z","cwd":"/tmp/proj"}` + "\n"
	for _, c := range []struct {
		name, before, after string
		want                bool
	}{
		{"edit refused a pass later", call("e1", "edit"), result("e1", "edit"), false},
		{"result without a tool name", call("e1", "edit"), result("e1", ""), false},
		{"read result", call("r1", "read"), result("r1", "read"), true},
		{"edit and refusal in the tail", "", call("e2", "edit") + result("e2", "edit"), true},
		{"a user turn", "", `{"type":"message","id":"u","timestamp":"2026-09-01T09:03:00Z","message":{"role":"user","content":[{"type":"text","text":"toolCall"}]}}` + "\n", true},
	} {
		t.Run(c.name, func(t *testing.T) {
			p, off := tailFile(t, "s.jsonl", head+c.before, c.after)
			if got := piResumes(p, off); got != c.want {
				t.Errorf("piResumes = %v, want %v", got, c.want)
			}
		})
	}
}

// A pi-family session read on the other OS keeps its paths in the convention
// it was written in. filepath.Join put a relative path under /work/app as
// \work\app\retry.cfg on Windows, and joined a drive-letter path onto the cwd
// everywhere else (#4438).
func TestPiPathsResolveTheSameOnEveryHost(t *testing.T) {
	for _, c := range []struct{ p, cwd, want string }{
		{"retry.cfg", "/work/app", "/work/app/retry.cfg"},
		{"./sub/retry.cfg", "/work/app", "/work/app/sub/retry.cfg"},
		{"/work/app/retry.cfg", "/work/app", "/work/app/retry.cfg"},
		{`C:\app\retry.cfg`, "/work/app", `C:\app\retry.cfg`},
		{`\app\retry.cfg`, "/work/app", `\app\retry.cfg`},
		{"retry.cfg", "", "retry.cfg"},
	} {
		r := &piReader{cwd: c.cwd}
		if got := r.abs(c.p); got != c.want {
			t.Errorf("abs(%q) under %q = %q, want %q", c.p, c.cwd, got, c.want)
		}
	}
}

func writePrimeSession(t *testing.T, root, id, cwd string) {
	t.Helper()
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	lines := `{"type":"session","version":3,"id":"` + id + `","timestamp":"2026-08-30T09:00:00.000Z","cwd":"` + cwd + `","parentSession":null,"rlmDepth":0}
{"type":"model_change","id":"m1","parentId":null,"timestamp":"2026-08-30T09:00:01.000Z","model":"prime/rlm-1"}
{"type":"message","id":"u1","parentId":"m1","timestamp":"2026-08-30T09:01:00.000Z","message":{"role":"user","content":[{"type":"text","text":"why does the telemetry parser drop frames"}]}}
{"type":"message","id":"a1","parentId":"u1","timestamp":"2026-08-30T09:02:00.000Z","message":{"role":"assistant","content":[{"type":"text","text":"the ring buffer is capped at 4096 frames"}]}}
`
	path := filepath.Join(root, id+".jsonl")
	if err := os.WriteFile(path, []byte(lines), 0o600); err != nil {
		t.Fatal(err)
	}
}

// The root is flat — one file per session, named by uuid7 — so there is no
// encoded project directory to read a name out of. The header's cwd is what
// names the project, which is the whole reason this parser passes
// useHeaderCwd.
func TestPrimeTakesItsProjectFromTheHeader(t *testing.T) {
	root := filepath.Join(t.TempDir(), "sessions")
	t.Setenv("DEJA_PRIME_ROOT", root)
	writePrimeSession(t, root, "0192f3a1-7c31-7000-8000-abcdef012345", "/Users/x/code/rocketapp")

	got := LoadPrime()
	if len(got) != 1 {
		t.Fatalf("sessions = %d, want 1", len(got))
	}
	s := got[0]
	if s.Harness != "prime" {
		t.Errorf("harness = %q", s.Harness)
	}
	if s.Project != "code/rocketapp" {
		t.Errorf("project = %q, want it read from the header cwd", s.Project)
	}
	if len(s.Messages) != 2 {
		t.Fatalf("messages = %d, want the user turn and the answer", len(s.Messages))
	}
	if s.Messages[1].Text != "the ring buffer is capped at 4096 frames" {
		t.Errorf("answer text = %q", s.Messages[1].Text)
	}
}

// prime-agent relocates its own store with two variables, and a machine that
// has moved its sessions has moved them for deja too. DEJA_PRIME_ROOT still
// wins, for tests and for a store that is neither.
func TestPrimeReadsTheAgentsOwnRootVariables(t *testing.T) {
	moved := filepath.Join(t.TempDir(), "moved")
	t.Setenv("DEJA_PRIME_ROOT", "")
	t.Setenv("PRIME_AGENT_SESSION_DIR", moved)
	writePrimeSession(t, moved, "0192f3a1-7c31-7000-8000-000000000001", "/Users/x/code/moved")
	if got := PrimeRoot(); got != moved {
		t.Fatalf("root = %q, want the agent's own variable honoured", got)
	}
	if len(LoadPrime()) != 1 {
		t.Error("a relocated store came back empty")
	}

	own := filepath.Join(t.TempDir(), "own")
	t.Setenv("DEJA_PRIME_ROOT", own)
	if got := PrimeRoot(); got != own {
		t.Errorf("root = %q, want DEJA_PRIME_ROOT to win", got)
	}
}

// rlm.spawn writes each child as its own transcript, but beside the session
// root under session-artifacts/<parent>/sub-<n>/, not in sessions/. Walking the
// root alone left every child unindexed (#4407).
func TestPrimeIndexesSpawnedChildrenAsSubagents(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "sessions")
	t.Setenv("DEJA_PRIME_ROOT", root)
	parent := "01a0f994-dbc6-75ac-9517-efe6eac2e13e"
	writePrimeSession(t, root, parent, "/tmp/proj-prime-g")
	sub := filepath.Join(base, "session-artifacts", parent, "sub-820d576b")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	child := filepath.Join(sub, "01a0f994-e172-71c9-b9fd-834f9bd5836e.jsonl")
	lines := `{"type":"session","version":3,"id":"01a0f994-e172-71c9-b9fd-834f9bd5836e","timestamp":"2026-10-01T22:27:58.000Z","cwd":"/private/tmp/proj-prime-g","parentSession":"~/.prime/agent/sessions/` + parent + `.jsonl","rlmDepth":1}
{"type":"message","id":"u1","timestamp":"2026-10-01T22:27:59.000Z","message":{"role":"user","content":[{"type":"text","text":"check the config"}]}}
{"type":"message","id":"t1","timestamp":"2026-10-01T22:28:01.000Z","message":{"role":"toolResult","content":[{"type":"text","text":"BashResult(exit_code=0, output='1:retries = 5\\n', duration=0.25296541582793)\n"}]}}
`
	if err := os.WriteFile(child, []byte(lines), 0o600); err != nil {
		t.Fatal(err)
	}
	// prime's event log sits beside the child and is not a transcript.
	edges := `{"type":"session_registered","session_id":"01a0f994-e172-71c9-b9fd-834f9bd5836e","parent_session_id":"` + parent + `"}` + "\n"
	for _, dir := range []string{sub, filepath.Dir(sub)} {
		if err := os.WriteFile(filepath.Join(dir, "semantic-edges.jsonl"), []byte(edges), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	files := PrimeSessionFiles()
	if len(files) != 2 {
		t.Fatalf("files = %v, want the parent and the child", files)
	}
	if KindForPath(child) != "prime" {
		t.Errorf("KindForPath(child) = %q, want prime", KindForPath(child))
	}
	var got []string
	for _, s := range LoadPrime() {
		got = append(got, s.ID)
		if s.ID != "01a0f994-e172-71c9-b9fd-834f9bd5836e" {
			continue
		}
		if s.Kind != "subagent" || s.Parent != parent {
			t.Errorf("child kind/parent = %q/%q, want subagent/%s", s.Kind, s.Parent, parent)
		}
	}
	if len(got) != 2 {
		t.Fatalf("sessions = %v, want the parent and the child", got)
	}
}

// A spawned child is a subagent like Claude Code's, and comes in the same way:
// the task, what it changed and how it ended, the reading in between left out,
// unless DEJA_INCLUDE_SUBAGENTS=1 asks for the whole run; =0 skips children.
// Indexed whole, a child's exploring competed with its parent for recall
// (#3009) and DEJA_INCLUDE_SUBAGENTS did nothing for prime (#4407).
func TestPrimeChildrenFollowTheSubagentSwitch(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "sessions")
	t.Setenv("DEJA_PRIME_ROOT", root)
	parent := "01a0f994-dbc6-75ac-9517-efe6eac2e13e"
	writePrimeSession(t, root, parent, "/tmp/proj-prime-g")
	sub := filepath.Join(base, "session-artifacts", parent, "sub-820d576b")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	child := filepath.Join(sub, "01a0f994-e172-71c9-b9fd-834f9bd5836e.jsonl")
	lines := `{"type":"session","version":3,"id":"01a0f994-e172-71c9-b9fd-834f9bd5836e","cwd":"/tmp/proj-prime-g","parentSession":"~/.prime/agent/sessions/` + parent + `.jsonl","rlmDepth":1}` + "\n" +
		`{"type":"message","id":"u1","timestamp":"2026-10-01T22:27:59.000Z","message":{"role":"user","content":[{"type":"text","text":"check the config"}]}}` + "\n"
	var linesSb146 strings.Builder
	for i := range 8 {
		linesSb146.WriteString(fmt.Sprintf(`{"type":"message","id":"a%d","timestamp":"2026-10-01T22:28:%02d.000Z","message":{"role":"assistant","content":[{"type":"text","text":"looking at part %d"}]}}`, i, i, i) + "\n")
	}
	lines += linesSb146.String()
	lines += `{"type":"message","id":"z","timestamp":"2026-10-01T22:29:00.000Z","message":{"role":"assistant","content":[{"type":"text","text":"retries is 5"}]}}` + "\n"
	if err := os.WriteFile(child, []byte(lines), 0o600); err != nil {
		t.Fatal(err)
	}
	texts := func() []string {
		ss, err := ParsePrimeFile(child)
		if err != nil || len(ss) != 1 {
			t.Fatalf("parse child: %v, %d sessions", err, len(ss))
		}
		out := make([]string, 0, len(ss[0].Messages))
		for _, m := range ss[0].Messages {
			out = append(out, m.Text)
		}
		return out
	}

	t.Setenv("DEJA_INCLUDE_SUBAGENTS", "")
	got := texts()
	if len(got) != SubagentTailKept+1 || got[0] != "check the config" || got[len(got)-1] != "retries is 5" {
		t.Errorf("default: %q, want the task and the last %d turns", got, SubagentTailKept)
	}
	if !IsPrimeChildPath(child) || IsPrimeChildPath(filepath.Join(root, parent+".jsonl")) {
		t.Error("IsPrimeChildPath does not tell the child from the parent")
	}

	t.Setenv("DEJA_INCLUDE_SUBAGENTS", "1")
	if got := texts(); len(got) != 10 {
		t.Errorf("DEJA_INCLUDE_SUBAGENTS=1: %d messages, want all 10", len(got))
	}

	t.Setenv("DEJA_INCLUDE_SUBAGENTS", "0")
	if files := PrimeSessionFiles(); len(files) != 1 || IsPrimeChildPath(files[0]) {
		t.Errorf("DEJA_INCLUDE_SUBAGENTS=0: files = %v, want the parent alone", files)
	}
	if KindForPath(child) == "prime" {
		t.Error("DEJA_INCLUDE_SUBAGENTS=0: the child still matches for incremental ingest")
	}
}

func TestPrivacyExclusions(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "config"))
	t.Setenv("DEJA_EXCLUDE_PROJECTS", "*secret*, API")
	if err := os.MkdirAll(filepath.Dir(ExcludePath()), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(ExcludePath(), []byte("# comment\n  private  \n"), 0o600); err != nil {
		t.Fatal(err)
	}
	patterns := ExclusionPatterns()
	if len(patterns) != 3 || !ExcludedProject("my-private-app") || !ExcludedProject("secret-service") || !ExcludedProject("api-client") || ExcludedProject("public") {
		t.Fatalf("patterns=%v", patterns)
	}
	ss := []model.Session{{Project: "private"}, {Project: "public"}}
	filtered := FilterSessions(ss)
	if len(filtered) != 1 || filtered[0].Project != "public" {
		t.Fatalf("filtered=%#v", filtered)
	}
}

func writeProjectAliases(t *testing.T, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(ProjectAliasPath()), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(ProjectAliasPath(), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestProjectAliasNamesEveryDirectoryUnderAPath(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", "")
	mono := filepath.Join(home, "mono")
	writeProjectAliases(t, "# one project for the whole tree\n"+
		mono+" = work\n"+
		filepath.Join(mono, "tools")+" = tools\n"+
		"~/trees = work\n"+
		"relative/path = nope\n"+
		filepath.Join(home, "noname")+" =\n"+
		"no separator here\n")

	for _, c := range []struct {
		dir, want string
	}{
		{mono, "work"},
		{filepath.Join(mono, "svc", "api"), "work"},
		{filepath.Join(mono, "tools", "lint"), "tools"},
		{filepath.Join(home, "trees", "task-1", "svc"), "work"},
		// A sibling sharing the prefix as text is another directory.
		{filepath.Join(home, "mono-other", "app"), "mono-other/app"},
		{filepath.Join(home, "noname", "app"), "noname/app"},
		{filepath.Join(home, "relative", "path"), "relative/path"},
	} {
		if got := cwdProjectName(c.dir); got != c.want {
			t.Errorf("cwdProjectName(%q) = %q, want %q", c.dir, got, c.want)
		}
	}
	// A note filed from a directory under the alias lands under its name.
	if got := ClaudeProjectName(filepath.Join(mono, "svc")); got != "work" {
		t.Errorf("ClaudeProjectName under the alias = %q, want work", got)
	}
}

func TestProjectAliasFileEditIsReadWithoutRestart(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", "")
	dir := filepath.Join(home, "mono", "svc")
	if _, ok := ProjectAliasFor(dir); ok {
		t.Fatal("an alias with no file")
	}
	writeProjectAliases(t, filepath.Join(home, "mono")+" = work\n")
	if got, _ := ProjectAliasFor(dir); got != "work" {
		t.Fatalf("after writing the file: %q", got)
	}
	writeProjectAliases(t, filepath.Join(home, "mono")+" = jobs\n")
	// The same size, so only the time tells the two apart.
	later := time.Now().Add(time.Hour)
	if err := os.Chtimes(ProjectAliasPath(), later, later); err != nil {
		t.Fatal(err)
	}
	if got, _ := ProjectAliasFor(dir); got != "jobs" {
		t.Fatalf("after editing the file: %q", got)
	}
	if err := os.Remove(ProjectAliasPath()); err != nil {
		t.Fatal(err)
	}
	if got, ok := ProjectAliasFor(dir); ok {
		t.Fatalf("after removing the file: %q", got)
	}
}

func aliasHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("DEJA_EXCLUDE_PROJECTS", "")
	return home
}

// A repository excluded by its own name stays out when an alias covering it
// is added later: renamed to the tree's alias it no longer matched the pattern
// and went into the index (#5373).
func TestExcludeFileBeatsTheAlias(t *testing.T) {
	home := aliasHome(t)
	work := filepath.Join(home, "work")
	ledgerd := filepath.Join(work, "svc", "ledgerd")
	api := filepath.Join(work, "svc", "api")
	writeProjectAliases(t, work+" = work\n")
	if err := os.WriteFile(ExcludePath(), []byte("svc/ledgerd\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := cwdProjectName(ledgerd); got != "svc/ledgerd" {
		t.Errorf("cwdProjectName(excluded repo) = %q, want svc/ledgerd", got)
	}
	if got := ClaudeProjectName(ledgerd); got == "work" {
		t.Errorf("a note from the excluded repo is filed under the alias")
	}
	if got := cwdProjectName(api); got != "work" {
		t.Errorf("cwdProjectName(api) = %q, want work", got)
	}
	kept := FilterSessions([]model.Session{
		{ID: "l", Project: cwdProjectName(ledgerd)},
		{ID: "a", Project: cwdProjectName(api)},
	})
	if len(kept) != 1 || kept[0].ID != "a" {
		t.Errorf("FilterSessions kept %+v, want only the api session", kept)
	}

	// A pattern for the alias name takes out the whole tree.
	if err := os.WriteFile(ExcludePath(), []byte("# private\nwork\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := cwdProjectName(api); got != "work" {
		t.Errorf("cwdProjectName(api) = %q, want work", got)
	}
	if kept := FilterSessions([]model.Session{{ID: "a", Project: cwdProjectName(api)}}); len(kept) != 0 {
		t.Errorf("an exclude pattern for the alias left %+v", kept)
	}

	// The variable counts like the file.
	if err := os.Remove(ExcludePath()); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_EXCLUDE_PROJECTS", "ledgerd")
	if got := cwdProjectName(ledgerd); got != "svc/ledgerd" {
		t.Errorf("with DEJA_EXCLUDE_PROJECTS, cwdProjectName = %q, want svc/ledgerd", got)
	}
}

// A long-lived process names a Claude folder once and keeps the name. Editing
// the alias file has to drop those names, or the folder stays filed under an
// alias that is gone.
func TestAliasEditForgetsCachedFolderNames(t *testing.T) {
	home := aliasHome(t)
	repo := filepath.Join(home, "work", "svc", "api")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	folder := strings.Map(func(r rune) rune {
		if r == '/' || r == '\\' || r == ':' || r == '.' || r == '_' {
			return '-'
		}
		return r
	}, repo)
	if resolveEncodedPath(folder) != repo {
		t.Skipf("%s does not resolve back here", folder)
	}
	writeProjectAliases(t, filepath.Join(home, "work")+" = work\n")
	if got := claudeProjectName(folder); got != "work" {
		t.Fatalf("with the alias: %q", got)
	}
	// An exclude edit drops them too: the name decided before it would carry
	// an excluded repository into the index under the alias.
	if err := os.WriteFile(ExcludePath(), []byte("svc/api\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := claudeProjectName(folder); got != "svc/api" {
		t.Errorf("after excluding the repository: %q, want svc/api", got)
	}
	if err := os.Remove(ExcludePath()); err != nil {
		t.Fatal(err)
	}
	if got := claudeProjectName(folder); got != "work" {
		t.Fatalf("after dropping the pattern: %q", got)
	}
	if err := os.Remove(ProjectAliasPath()); err != nil {
		t.Fatal(err)
	}
	if got := claudeProjectName(folder); got != "svc/api" {
		t.Errorf("after removing the alias: %q, want svc/api", got)
	}
}

func TestProjectAliasFingerprint(t *testing.T) {
	home := aliasHome(t)
	if got := ProjectAliasFingerprint(); got != "none" {
		t.Fatalf("no file: %q", got)
	}
	writeProjectAliases(t, "# only a comment\n")
	if got := ProjectAliasFingerprint(); got != "none" {
		t.Fatalf("no valid line: %q", got)
	}
	a, b := filepath.Join(home, "a"), filepath.Join(home, "b")
	writeProjectAliases(t, a+" = one\n"+b+" = two\n")
	first := ProjectAliasFingerprint()
	writeProjectAliases(t, "# reordered\n"+b+" = two\n\n"+a+" = one\n")
	later := time.Now().Add(time.Hour)
	_ = os.Chtimes(ProjectAliasPath(), later, later)
	if got := ProjectAliasFingerprint(); got != first {
		t.Errorf("order and comments changed the fingerprint: %q vs %q", got, first)
	}
	writeProjectAliases(t, a+" = one\n"+b+" = three\n")
	_ = os.Chtimes(ProjectAliasPath(), later.Add(time.Hour), later.Add(time.Hour))
	if got := ProjectAliasFingerprint(); got == first || got == "none" {
		t.Errorf("a renamed tree kept the fingerprint %q", got)
	}
}

// The write-back looks for the checkout a codex session ran in by the nearest
// directory carrying its project name. Under an alias every directory carries
// it, so the file's own folder won; it now finds nothing and leaves it to the
// thread's recorded cwd.
func TestSessionDirFromPathsIgnoresTheAlias(t *testing.T) {
	home := aliasHome(t)
	work := filepath.Join(home, "work")
	writeProjectAliases(t, work+" = work\n")
	deep := filepath.Join(work, "svc", "api", "internal", "store")
	s := model.Session{Project: "work", Messages: []model.Message{
		{Role: RoleFiles, Text: filepath.Join(deep, "a.go") + "\n" + filepath.Join(deep, "b.go")},
	}}
	if got := sessionDirFromPaths(s); got == deep {
		t.Errorf("sessionDirFromPaths = %q, the file's folder rather than a checkout", got)
	}
	s.Project = "svc/api"
	s.Messages[0].Text = filepath.Join(work, "svc", "api", "a.go")
	if got := sessionDirFromPaths(s); got != filepath.Join(work, "svc", "api") {
		t.Errorf("a session indexed under its own name: %q", got)
	}
}

// Every reader that knows the directory a session ran in labels its project
// the way Claude Code's does: the last two segments of that directory. Codex
// and others took the basename, so one directory was two projects and
// `--project w/my-app` dropped half of it (#4457); Cline and others folded the
// path into a folder key and decoded it back, which filed my-app under my/app
// whenever my/app existed (#4458); a file:// URI went in undecoded (#4461).
func TestRecordedCwdLabelsTheProjectLikeClaude(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range []string{"w/my-app", "w/my/app", "w/with space"} {
		if err := os.MkdirAll(filepath.Join(root, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	myApp := filepath.Join(root, "w", "my-app")
	spaced := filepath.Join(root, "w", "with space")
	spacedURI := "file://" + strings.ReplaceAll(filepath.ToSlash(spaced), " ", "%20")
	js := func(s string) string { b := mustJSON(t, s); return string(b) }
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
	fixture := func(t *testing.T, rel, from, to string) string {
		t.Helper()
		b, err := os.ReadFile(filepath.Join("..", "..", "fixtures", "registry", rel))
		if err != nil {
			t.Fatal(err)
		}
		return strings.ReplaceAll(string(b), from, to)
	}
	sqlite := func(t *testing.T, db, stmts string) string {
		t.Helper()
		if !SQLite3Available() {
			t.Skip("sqlite3 not installed")
		}
		if err := os.MkdirAll(filepath.Dir(db), 0o755); err != nil {
			t.Fatal(err)
		}
		cmd := exec.CommandContext(t.Context(), "sqlite3", db)
		cmd.Stdin = strings.NewReader(stmts)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("sqlite3: %v\n%s", err, out)
		}
		return db
	}
	user := `fix the retry loop`

	cases := []struct {
		name, kind, cwd, want string
		store                 func(t *testing.T, home, cwd string) string
	}{
		{"codex", "codex", myApp, "w/my-app", func(t *testing.T, home, cwd string) string {
			t.Helper()
			body := fixture(t, "codex/rollout-2026-07-17T09-00-00-registry-codex.jsonl", "/workspace/registry-demo", strings.Trim(js(cwd), `"`))
			return write(t, filepath.Join(home, ".codex", "sessions", "2026", "07", "17", "rollout-2026-07-17T09-00-00-0199a000-1111-7222-8333-444455556666.jsonl"), body)
		}},
		{"goose", "goose-jsonl", myApp, "w/my-app", func(t *testing.T, home, cwd string) string {
			t.Helper()
			body := fixture(t, "goose/sessions/20250724_1.jsonl", "/workspace/demo", strings.Trim(js(cwd), `"`))
			return write(t, filepath.Join(home, ".local", "share", "goose", "sessions", "20250724_1.jsonl"), body)
		}},
		{"goose db", "goose-db", myApp, "w/my-app", func(t *testing.T, home, cwd string) string {
			t.Helper()
			stmts := fixture(t, "goose/sessions/goose.sql", "/workspace/demo", strings.ReplaceAll(cwd, "'", "''"))
			return sqlite(t, filepath.Join(home, ".local", "share", "goose", "sessions", "sessions.db"), stmts)
		}},
		{"opencode", "opencode", myApp, "w/my-app", func(t *testing.T, home, cwd string) string {
			t.Helper()
			db := filepath.Join(home, "opencode.db")
			t.Setenv("DEJA_OPENCODE_DB", db)
			return sqlite(t, db, fixture(t, "opencode/opencode.sql", "/workspace/registry-demo", strings.ReplaceAll(cwd, "'", "''")))
		}},
		{"kimi", "kimi", myApp, "w/my-app", func(t *testing.T, home, cwd string) string {
			t.Helper()
			sd := filepath.Join(home, ".kimi-code", "sessions", "wd_0123456789ab", "session_0001")
			write(t, filepath.Join(sd, "state.json"), `{"createdAt":"2026-07-01T10:00:00.000Z","title":"retry","workDir":`+js(cwd)+`}`)
			wire := fixture(t, "kimi/sessions/wd_demo_0123456789ab/session_fixture01/agents/main/wire.jsonl", "", "")
			return write(t, filepath.Join(sd, "agents", "main", "wire.jsonl"), wire)
		}},
		{"grok", "grok", myApp, "w/my-app", func(t *testing.T, home, cwd string) string {
			t.Helper()
			sd := filepath.Join(home, ".grok", "sessions", "w", "00000001-1111-4222-8333-444455556666")
			write(t, filepath.Join(sd, "summary.json"), `{"info":{"id":"00000001-1111-4222-8333-444455556666","cwd":`+js(cwd)+`},"created_at":"2026-07-17T09:00:00Z"}`)
			return write(t, filepath.Join(sd, "updates.jsonl"), `{"timestamp":1784278801,"params":{"update":{"sessionUpdate":"user_message_chunk","content":{"type":"text","text":"`+user+`"},"_meta":{"promptIndex":0}}}}`+"\n")
		}},
		{"dsh", "deepseek", myApp, "w/my-app", func(t *testing.T, home, cwd string) string {
			t.Helper()
			return write(t, filepath.Join(home, ".dsh", "sessions", "--w-my-app--", "session-s1", "session.jsonl"),
				`{"type":"session","version":0,"id":"session-s1","createdAt":1787320263519,"cwd":`+js(cwd)+`}`+"\n"+
					`{"type":"user/message","seq":7,"time":1787320263580,"data":{"content":[{"type":"text","text":"`+user+`"}],"source":{"kind":"user"},"role":"user"}}`+"\n")
		}},
		{"amp", "amp", myApp, "w/my-app", func(t *testing.T, home, cwd string) string {
			t.Helper()
			return write(t, filepath.Join(home, ".local", "share", "amp", "threads", "T-1.json"),
				`{"id":"T-1","title":"retry","created":1767337445000,"env":{"initial":{"trees":[{"uri":`+js("file://"+filepath.ToSlash(cwd))+`}]}},"messages":[{"role":"user","content":[{"type":"text","text":"`+user+`"}]}]}`)
		}},
		{"aider", "aider", myApp, "w/my-app", func(t *testing.T, _, cwd string) string {
			t.Helper()
			return write(t, filepath.Join(cwd, ".aider.chat.history.md"), fixture(t, "aider/.aider.chat.history.md", "", ""))
		}},
		{"cline", "cline-sdk", myApp, "w/my-app", func(t *testing.T, home, cwd string) string {
			t.Helper()
			r := filepath.Join(home, "cline-sessions")
			t.Setenv("DEJA_CLINE_ROOT", r)
			write(t, filepath.Join(r, "session_1", "session_1.json"), `{"session_id":"session_1","created_at":"2026-01-01T00:00:00.000Z","cwd":`+js(cwd)+`,"workspace_root":`+js(cwd)+`,"prompt":"`+user+`"}`)
			return write(t, filepath.Join(r, "session_1", "session_1.messages.json"), `{"version":1,"agent":"lead","sessionId":"session_1","messages":[{"role":"user","content":[{"type":"text","text":"`+user+`"}],"ts":1767225600000}]}`)
		}},
		{"roo cli", "roo", myApp, "w/my-app", func(t *testing.T, home, cwd string) string {
			t.Helper()
			r := filepath.Join(home, "roo")
			t.Setenv("DEJA_ROO_CLI_ROOT", r)
			dir := filepath.Join(r, "tasks", "00000001-1111-4222-8333-444455556666")
			write(t, filepath.Join(dir, "history_item.json"), `{"id":"00000001-1111-4222-8333-444455556666","number":1,"ts":1767225700000,"task":"`+user+`","workspace":`+js(cwd)+`}`)
			return write(t, filepath.Join(dir, "api_conversation_history.json"), `[{"role":"user","content":[{"type":"text","text":"<task>\n`+user+`\n</task>"}]}]`)
		}},
		{"continue", "continue", myApp, "w/my-app", func(t *testing.T, home, cwd string) string {
			t.Helper()
			r := filepath.Join(home, ".continue")
			t.Setenv("DEJA_CONTINUE_ROOT", r)
			return write(t, filepath.Join(r, "sessions", "00000001.json"), `{"sessionId":"00000001","title":"retry","workspaceDirectory":`+js(cwd)+`,"history":[{"message":{"role":"user","content":"`+user+`"},"contextItems":[]}]}`)
		}},
		{"continue uri", "continue", spacedURI, "w/with space", func(t *testing.T, home, cwd string) string {
			t.Helper()
			r := filepath.Join(home, ".continue")
			t.Setenv("DEJA_CONTINUE_ROOT", r)
			return write(t, filepath.Join(r, "sessions", "00000002.json"), `{"sessionId":"00000002","title":"retry","workspaceDirectory":`+js(cwd)+`,"history":[{"message":{"role":"user","content":"`+user+`"},"contextItems":[]}]}`)
		}},
		{"codewhale", "codewhale", myApp, "w/my-app", func(t *testing.T, home, cwd string) string {
			t.Helper()
			r := filepath.Join(home, ".codewhale", "sessions")
			t.Setenv("DEJA_CODEWHALE_ROOT", r)
			return write(t, filepath.Join(r, "00000001.json"), `{"schema_version":3,"metadata":{"id":"00000001","title":"retry","created_at":"2026-09-19T08:00:00Z","workspace":`+js(cwd)+`},"messages":[{"role":"user","content":[{"type":"text","text":"`+user+`"}]}]}`)
		}},
		{"clawcode", "clawcode", myApp, "w/my-app", func(t *testing.T, home, cwd string) string {
			t.Helper()
			r := filepath.Join(home, ".claw", "sessions")
			t.Setenv("DEJA_CLAWCODE_ROOT", r)
			return write(t, filepath.Join(r, "sess-1.jsonl"), `{"type":"session_meta","session_id":"sess-1","workspace_root":`+js(cwd)+`,"created_at_ms":1759305600000}`+"\n"+`{"type":"message","message":{"role":"user","blocks":[{"type":"text","text":"`+user+`"}]}}`+"\n")
		}},
		{"kiro cli", "kiro-cli", myApp, "w/my-app", func(t *testing.T, home, cwd string) string {
			t.Helper()
			r := filepath.Join(home, ".kiro", "sessions")
			t.Setenv("DEJA_KIRO_ROOT", r)
			write(t, filepath.Join(r, "cli", "00000001.json"), `{"session_id":"00000001","cwd":`+js(cwd)+`,"created_at":"2026-08-01T10:00:00Z"}`)
			return write(t, filepath.Join(r, "cli", "00000001.jsonl"), `{"version":"v1","kind":"Prompt","data":{"message_id":"p1","content":[{"kind":"text","data":"`+user+`"}],"meta":{"timestamp":1785600005.42}}}`+"\n")
		}},
		{"reasonix", "reasonix", myApp, "w/my-app", func(t *testing.T, home, cwd string) string {
			t.Helper()
			r := filepath.Join(home, ".reasonix")
			t.Setenv("REASONIX_STATE_HOME", r)
			p := filepath.Join(r, "projects", pathToProjectKey(cwd), "sessions", "20260920-101000.000000000-deepseek-chat.jsonl")
			write(t, p+".meta", `{"id":"20260920-101000.000000000-deepseek-chat","created_at":"2026-09-20T10:00:00Z","scope":"project","workspace_root":`+js(cwd)+`}`)
			return write(t, p, `{"role":"user","content":"`+user+`","createdAt":1789898405000}`+"\n")
		}},
		{"hermes", "hermes", myApp, "w/my-app", func(t *testing.T, home, cwd string) string {
			t.Helper()
			return sqlite(t, filepath.Join(home, ".hermes", "state.db"), `create table messages (id integer primary key autoincrement, session_id text not null, role text not null, content text, tool_call_id text, tool_calls text, tool_name text, timestamp real not null, token_count integer, finish_reason text);
create table sessions (id text primary key, cwd text);
insert into messages(session_id,role,content,timestamp) values('h1','user','`+user+`',1785015600.25);
insert into sessions values('h1','`+strings.ReplaceAll(cwd, "'", "''")+`');`)
		}},
		{"copilot-chat", "copilot-chat", spacedURI, "w/with space", func(t *testing.T, home, cwd string) string {
			t.Helper()
			ws := filepath.Join(home, "Library", "Application Support", "Code", "User", "workspaceStorage", "a1b2c3d4e5f6")
			write(t, filepath.Join(ws, "workspace.json"), `{"folder":`+js(cwd)+`}`)
			rel := "copilot-chat/workspaceStorage/a1b2c3d4e5f6/chatSessions/3b7e1f1b-0000-4000-8000-000000000000.jsonl"
			return write(t, filepath.Join(ws, "chatSessions", "3b7e1f1b-0000-4000-8000-000000000000.jsonl"), fixture(t, rel, "file:///tmp/registry-demo", cwd))
		}},
		{"copilot-chat unc", "copilot-chat", "file://server/share/proj", "share/proj", func(t *testing.T, home, cwd string) string {
			t.Helper()
			ws := filepath.Join(home, "Library", "Application Support", "Code", "User", "workspaceStorage", "a1b2c3d4e5f6")
			write(t, filepath.Join(ws, "workspace.json"), `{"folder":`+js(cwd)+`}`)
			rel := "copilot-chat/workspaceStorage/a1b2c3d4e5f6/chatSessions/3b7e1f1b-0000-4000-8000-000000000000.jsonl"
			return write(t, filepath.Join(ws, "chatSessions", "3b7e1f1b-0000-4000-8000-000000000000.jsonl"), fixture(t, rel, "file:///tmp/registry-demo", cwd))
		}},
		{"copilot windows", "copilot", `C:\Users\x\my-app`, "x/my-app", func(t *testing.T, home, cwd string) string {
			t.Helper()
			id := "7cf44517-55ca-435c-893a-3fde1973a44e"
			body := fixture(t, "copilot/"+id+"/events.jsonl", "/Users/x/coding/gateway", strings.Trim(js(cwd), `"`))
			return write(t, filepath.Join(home, ".copilot", "session-state", id, "events.jsonl"), body)
		}},
	}
	for i, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			home := filepath.Join(root, fmt.Sprintf("home%02d", i))
			t.Setenv("HOME", home)
			t.Setenv("XDG_DATA_HOME", filepath.Join(home, ".local", "share"))
			t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
			p := c.store(t, home, c.cwd)
			ss := parseKindForTest(t, c.kind, p)
			if len(ss) == 0 {
				t.Fatalf("%s: no session from %s", c.kind, p)
			}
			for _, s := range ss {
				if s.Project != c.want {
					t.Errorf("%s labels %q as %q, want %q", c.kind, c.cwd, s.Project, c.want)
				}
			}
		})
	}
}

func parseKindForTest(t *testing.T, kind, p string) []model.Session {
	t.Helper()
	for _, h := range Registry() {
		for _, k := range h.Kinds {
			if k.Name == kind {
				ss, err := k.Parse(p, 0)
				if err != nil {
					t.Fatalf("parse %s: %v", p, err)
				}
				return ss
			}
		}
	}
	t.Fatalf("no kind %s", kind)
	return nil
}

// A workspace on a UNC share reopens on that share: the host is part of the
// path, and without it `code /share/proj` names a folder that is not there
// (#4462).
func TestCopilotChatWorkspaceDirKeepsTheUNCHost(t *testing.T) {
	ws := filepath.Join(t.TempDir(), "a1b2c3d4e5f6")
	for uri, want := range map[string]string{
		"file://server/share/proj":   filepath.FromSlash("//server/share/proj"),
		"file:///tmp/w/with%20space": filepath.FromSlash("/tmp/w/with space"),
	} {
		if err := os.MkdirAll(ws, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(ws, "workspace.json"), []byte(`{"folder":"`+uri+`"}`), 0o644); err != nil {
			t.Fatal(err)
		}
		if got := CopilotChatWorkspaceDir(filepath.Join(ws, "chatSessions", "x.json")); got != want {
			t.Errorf("%s: workspace dir %q, want %q", uri, got, want)
		}
	}
}

// A drive is not a parent on any of the ways a Claude Code project gets its
// name: the folder name decoded once the directory is gone says C:\proj is
// "proj", as the recorded cwd does, or one directory is filed under two names
// depending on whether its transcripts carry a cwd.
func TestADriveRootDecodesToTheNameItsCwdGives(t *testing.T) {
	for _, cwd := range []string{`C:\proj`, `C:\a\b`, `D:\app`} {
		enc := claudeEncodePath(cwd)
		if got, want := decodeProjectBase(enc), cwdProjectName(cwd); got != want {
			t.Errorf("%s: folder %s decodes to %q, the cwd names %q", cwd, enc, got, want)
		}
	}
}

func filesMsg(paths ...string) model.Message {
	t := ""
	var tSb14 strings.Builder
	for i, p := range paths {
		if i > 0 {
			tSb14.WriteString("\n")
		}
		tSb14.WriteString(p)
	}
	t += tSb14.String()
	return model.Message{Role: RoleFiles, Text: t}
}

// A repo is a directory with a .git in it; a folder is not. The fixture builds
// two so the majority rule has something to choose between.
func repoFixture(t *testing.T) (string, string) {
	t.Helper()
	tmp := t.TempDir()
	for _, name := range []string{"alpha", "beta"} {
		if err := os.MkdirAll(filepath.Join(tmp, name, ".git"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(filepath.Join(tmp, name, "internal"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return filepath.Join(tmp, "alpha"), filepath.Join(tmp, "beta")
}

func TestProjectFromPathsTakesTheMajorityRepo(t *testing.T) {
	a, b := repoFixture(t)
	ms := []model.Message{
		filesMsg(filepath.Join(a, "internal", "x.go"), filepath.Join(a, "internal", "y.go")),
		filesMsg(filepath.Join(a, "main.go")),
		filesMsg(filepath.Join(b, "main.go")),
	}
	got := projectFromPaths(ms)
	// "/" rather than filepath.Join: a project name is an identifier and is
	// spelled the same on every platform.
	want := filepath.Base(filepath.Dir(a)) + "/alpha"
	if got != want {
		t.Fatalf("project = %q, want %q", got, want)
	}
}

// A session genuinely split between two repositories should keep whatever name
// it already had rather than be filed under a coin flip.
func TestProjectFromPathsDeclinesWithoutAMajority(t *testing.T) {
	a, b := repoFixture(t)
	ms := []model.Message{
		filesMsg(filepath.Join(a, "one.go"), filepath.Join(a, "two.go")),
		filesMsg(filepath.Join(b, "one.go"), filepath.Join(b, "two.go")),
	}
	if got := projectFromPaths(ms); got != "" {
		t.Fatalf("project = %q, want no answer", got)
	}
}

func TestProjectFromPathsIgnoresTheAgentsOwnFiles(t *testing.T) {
	a, _ := repoFixture(t)
	ms := []model.Message{
		filesMsg("/Users/x/.claude/projects/-Users-x/scratchpad/probe.py"),
		filesMsg("/private/tmp/claude-501/-Users-x/tasks/run.output"),
		filesMsg(filepath.Join(a, "main.go")),
	}
	// One real path is not three, so there is not enough evidence to override.
	if got := projectFromPaths(ms); got != "" {
		t.Fatalf("project = %q, want no answer from one real path", got)
	}
	if !agentScratch("/Users/x/.claude/projects/p/scratchpad/probe.py") {
		t.Fatal("a scratch clone under .claude must not count as a project")
	}
}

func TestProjectFromPathsNeedsEvidence(t *testing.T) {
	a, _ := repoFixture(t)
	ms := []model.Message{filesMsg(filepath.Join(a, "main.go"))}
	if got := projectFromPaths(ms); got != "" {
		t.Fatalf("project = %q, want no answer from a single file", got)
	}
	if got := projectFromPaths(nil); got != "" {
		t.Fatalf("project = %q, want no answer with no files", got)
	}
}

func TestRepoRootTerminatesAtTheVolumeRoot(t *testing.T) {
	// The walk used to stop only at "/", so on Windows it spun forever at the
	// volume root. A timeout rather than an assertion: the failure mode is a
	// hang, and it only reproduces on the OS whose root is not "/".
	done := make(chan string, 1)
	go func() {
		done <- repoRoot(filepath.Join(filepath.VolumeName(os.TempDir())+string(filepath.Separator), "nowhere", "deep"))
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("repoRoot did not terminate at the volume root")
	}
}

func TestResolveHomeOrEmptyAndDirProjectACL(t *testing.T) {
	// A session started from a home directory (~ -> "Users/alice") that opens
	// with a deja handoff prompt resolves its project from the handoff prompt
	// even before touching 3 files.
	handoffMsg := model.Message{
		Role: "user",
		Text: "Continuing a task handed off from a claude session (project goprojects/deja-vu, 2026-10-01).",
	}
	s := model.Session{
		Harness:  "codex",
		ID:       "s1",
		Project:  "Users/alice",
		Messages: []model.Message{handoffMsg},
	}
	resolveHomeOrEmptyProject(&s)
	if s.Project != "goprojects/deja-vu" {
		t.Fatalf("resolveHomeOrEmptyProject(handoff) = %q; want goprojects/deja-vu", s.Project)
	}

	// Same for directory-derived sessions (claude, mcode) started from ~.
	s2 := model.Session{
		Harness:  "claude",
		ID:       "s2",
		Project:  "home/alice",
		Messages: []model.Message{handoffMsg},
	}
	resolveDirProject(&s2)
	if s2.Project != "goprojects/deja-vu" || s2.ProjectFromDir {
		t.Fatalf("resolveDirProject(handoff from home) = (%q, %v); want (goprojects/deja-vu, false)", s2.Project, s2.ProjectFromDir)
	}

	// parseCompactionSession preserves non-Claude parser project attribution
	// when workspace is empty (e.g. transcriptHead on Copilot/Qwen).
	copilotDir := filepath.Join(t.TempDir(), "1111-2222")
	if err := os.MkdirAll(copilotDir, 0o755); err != nil {
		t.Fatal(err)
	}
	copilotPath := filepath.Join(copilotDir, "events.jsonl")
	copilotData := strings.Join([]string{
		`{"type":"session.start","timestamp":"2026-10-01T10:00:00Z","data":{"sessionId":"1111-2222","startTime":"2026-10-01T10:00:00Z","context":{"cwd":"/work/org/app"}}}`,
		`{"type":"user.message","timestamp":"2026-10-01T10:00:01Z","data":{"content":"hello"}}`,
	}, "\n") + "\n"
	compSess, err := parseCompactionSession(copilotPath, "copilot", "", []byte(copilotData))
	if err != nil {
		t.Fatal(err)
	}
	if compSess.Project != "org/app" {
		t.Fatalf("parseCompactionSession(copilot, workspace=\"\").Project = %q; want org/app", compSess.Project)
	}
}

// A project name is an identifier, not a path on this disk. Assembled with the
// OS separator it reads goprojects\deja-vu on windows and goprojects/deja-vu
// everywhere else — two names for one project.
//
// Scoping then misses on windows, where eleven internal/index tests were
// failing on exactly this, and sync is worse: a project exported from a windows
// machine arrives as "tmp\touch" and never matches the "tmp/touch" the same
// repository is called on any other machine, so the peer's history is indexed
// and unreachable.
func TestAProjectNameIsSpelledTheSameOnEveryPlatform(t *testing.T) {
	if got := projectSegments("goprojects", "deja-vu"); got != "goprojects/deja-vu" {
		t.Errorf("project name = %q, want it separated by / on every platform", got)
	}
}

// promote writes a record every run, so re-promoting an unchanged decision
// used to append a duplicate — the note then carried the same line N times and
// each copy lifted its own weight in recall. An identical re-promote must be a
// no-op on disk; a genuine change (state, text, tags) still appends.
func TestIdenticalRepromoteDoesNotGrowTheNote(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "notes.jsonl")
	t.Setenv("DEJA_NOTES_FILE", path)
	src := time.Date(2026, 6, 1, 10, 0, 0, 0, time.UTC)

	appendOnce := func(state, text, title string, tags []string) {
		if err := AppendPromotedSourced("proj", title, text, "claude:c1", state, tags, src, time.Now()); err != nil {
			t.Fatalf("append: %v", err)
		}
	}
	noteMessages := func() int {
		ss, err := ParseNotesFile(path)
		if err != nil {
			t.Fatal(err)
		}
		for _, s := range ss {
			if s.ID == PromotedNoteID("claude:c1") {
				return len(s.Messages)
			}
		}
		return 0
	}

	// Five identical promotes collapse to one message.
	for range 5 {
		appendOnce("accepted", "pool exhausted", "davit", []string{"perf"})
	}
	if got := noteMessages(); got != 1 {
		t.Fatalf("identical re-promotes grew the note: got %d messages, want 1", got)
	}

	// A state change is a correction — it appends.
	appendOnce("rejected", "pool exhausted", "davit", []string{"perf"})
	if got := noteMessages(); got != 2 {
		t.Fatalf("state change did not append: got %d messages, want 2", got)
	}
	// Repeating the rejected state does not grow it again.
	appendOnce("rejected", "pool exhausted", "davit", []string{"perf"})
	if got := noteMessages(); got != 2 {
		t.Fatalf("identical rejected re-promote grew the note: got %d, want 2", got)
	}
	// A new tag is a change — it appends.
	appendOnce("rejected", "pool exhausted", "davit", []string{"perf", "urgent"})
	if got := noteMessages(); got != 3 {
		t.Fatalf("tag change did not append: got %d messages, want 3", got)
	}
}

// countNoteRecords is how many records a notes file holds.
func countNoteRecords(t *testing.T, path string) int {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	body := strings.TrimSpace(string(b))
	if body == "" {
		return 0
	}
	return strings.Count(body, "\n") + 1
}

// An identical re-promote writes nothing (#1319). Cleaning tags where they are
// written (#1811) put stored tags on one side of that comparison and freshly
// cleaned ones on the other, so a note written by an older binary — its tag
// carrying a control byte, or past the length cap — no longer compares equal.
// The result is one corrected record, once: the copy that lands holds the
// cleaned tags, and every promote after it is silent again (#1815).
func TestAnOldRecordIsRewrittenOnceAndThenSettles(t *testing.T) {
	path := filepath.Join(t.TempDir(), "notes.jsonl")
	t.Setenv("DEJA_NOTES_FILE", path)
	now := time.Now()

	// What a pre-#1811 binary wrote: the tags stored exactly as typed.
	old, err := json.Marshal(map[string]any{
		"ts": now.UTC().Format(time.RFC3339Nano), "project": "proj",
		"text": "we cap retries at three", "kind": "promoted", "session": "sess1",
		"state": "accepted", "title": "retries",
		"tags": []string{"red\x1b[31malert\x1b[0m", strings.Repeat("w", 400)},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(path, append(old, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}

	tags := []string{"red\x1b[31mALERT\x1b[0m", strings.Repeat("w", 400)}
	promote := func() {
		t.Helper()
		if perr := AppendPromotedSourced("proj", "retries", "we cap retries at three", "sess1", "accepted", tags, time.Time{}, now); perr != nil {
			t.Fatal(perr)
		}
	}
	promote()
	if got := countNoteRecords(t, path); got != 2 {
		t.Fatalf("the first re-promote wrote %d records, want the one correction", got)
	}
	promote()
	promote()
	if got := countNoteRecords(t, path); got != 2 {
		t.Errorf("re-promoting kept appending: %d records after three runs", got)
	}
	// The record that landed carries cleaned tags — that is what makes the
	// next comparison equal.
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(strings.Split(strings.TrimSpace(string(body)), "\n")[1], "\\u001b") {
		t.Errorf("the corrected record still carries an escape byte:\n%s", body)
	}

	// The control: a store this binary wrote stays at one record however often
	// the same decision is promoted, so the count above is the old data and
	// not the dedupe being broken.
	clean := filepath.Join(t.TempDir(), "clean.jsonl")
	t.Setenv("DEJA_NOTES_FILE", clean)
	promote()
	promote()
	promote()
	if got := countNoteRecords(t, clean); got != 1 {
		t.Errorf("an identical re-promote into a clean store wrote %d records", got)
	}
}

// Qwen Code 0.20 writes every sub-agent run, foreground, background or fork,
// to subagents/<session>/agent-<id>.jsonl. The reader took them only under
// DEJA_INCLUDE_SUBAGENTS=1, so by default nothing in the index had written the
// file a sub-agent wrote and blame said nobody did (#5223). They are read the
// way Claude's are now: the task, the edits and the last turns.
func TestQwenSubagentRunIsReadByDefault(t *testing.T) {
	root := t.TempDir()
	t.Setenv("HOME", filepath.Join(root, "home"))
	t.Setenv("USERPROFILE", filepath.Join(root, "home"))
	t.Setenv("DEJA_QWEN_ROOT", filepath.Join(root, "qwen"))
	project := filepath.Join(root, "qwen", "projects", "-w-my-app")
	put := func(p, body string) string {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	const parent = "8bde8fee-08e8-4950-b83b-2e23c486eb7d"
	put(filepath.Join(project, "chats", parent+".jsonl"),
		`{"type":"user","sessionId":"`+parent+`","timestamp":"2026-10-18T06:02:40Z","cwd":"/w/my-app","message":{"role":"user","parts":[{"text":"delegate the notes"}]}}`+"\n")
	rec := func(typ, at, msg string) string {
		return `{"sessionId":"` + parent + `","timestamp":"2026-10-18T06:10:` + at + `Z","type":"` + typ + `","cwd":"/w/my-app","agentId":"fork-call_f7a","agentName":"fork","isSidechain":true,` + msg + "}\n"
	}
	// A fork child: the parent's history it inherits sits in a system
	// agent_bootstrap record, then the directive, then its own work.
	body := `{"sessionId":"` + parent + `","timestamp":"2026-10-18T06:10:00Z","type":"system","subtype":"agent_bootstrap","agentId":"fork-call_f7a","agentName":"fork","isSidechain":true,"systemPayload":{"kind":"fork","history":[{"role":"user","parts":[{"text":"PARENTONLY delegate the notes"}]}]}}` + "\n" +
		rec("user", "01", `"message":{"role":"user","parts":[{"text":"write fork.txt with the NUMBAT5 line"}]}`) +
		rec("assistant", "02", `"message":{"role":"model","parts":[{"functionCall":{"id":"c1","name":"write_file","args":{"file_path":"/w/my-app/fork.txt","content":"NUMBAT5 fork child line written by the forked sub-agent"}}}]}`) +
		rec("tool_result", "03", `"message":{"role":"user","parts":[{"functionResponse":{"id":"c1","name":"write_file","response":{"output":"Successfully created and wrote to new file: /w/my-app/fork.txt."}}}]}`)
	var bodySb44 strings.Builder
	for i := range 8 {
		bodySb44.WriteString(rec("assistant", fmt.Sprintf("%02d", 10+i), `"message":{"role":"model","parts":[{"text":"MIDDLEPROSE step `+strconv.Itoa(i)+`"}]}`))
	}
	body += bodySb44.String()
	body += rec("assistant", "30", `"message":{"role":"model","parts":[{"text":"fork.txt written"}]}`)
	child := put(filepath.Join(project, "subagents", parent, "agent-fork-call_f7a.jsonl"), body)

	has := func(p string) bool {
		return slices.Contains(QwenSessionFiles(), p)
	}
	t.Setenv("DEJA_INCLUDE_SUBAGENTS", "")
	if !has(child) {
		t.Fatal("a sub-agent log is left out by default")
	}
	ss, err := ParseQwenFile(child)
	if err != nil || len(ss) != 1 {
		t.Fatalf("parsed %d sessions: %v", len(ss), err)
	}
	s := ss[0]
	if s.Kind != "subagent" || s.Parent != parent || s.Agent != "fork" {
		t.Errorf("child read as kind=%q parent=%q agent=%q, want a fork sub-agent of %s", s.Kind, s.Parent, s.Agent, parent)
	}
	text := make([]string, 0, len(s.Messages))
	wrote := false
	for _, m := range s.Messages {
		text = append(text, m.Text)
		if m.Role == RoleWrote && strings.HasPrefix(m.Text, "/w/my-app/fork.txt") {
			wrote = true
		}
	}
	all := strings.Join(text, "\n")
	if !strings.HasPrefix(s.Messages[0].Text, "write fork.txt") {
		t.Errorf("first turn is %q, want the task", s.Messages[0].Text)
	}
	if !wrote {
		t.Errorf("the child's write of fork.txt is not kept: %q", all)
	}
	if strings.Contains(all, "PARENTONLY") {
		t.Error("the parent's history copied into the fork is read as the child's")
	}
	if strings.Contains(all, "MIDDLEPROSE step 0") || !strings.Contains(all, "fork.txt written") {
		t.Errorf("default read is not task, edits and tail: %q", all)
	}

	t.Setenv("DEJA_INCLUDE_SUBAGENTS", "1")
	if ss, _ := ParseQwenFile(child); len(ss) != 1 || !strings.Contains(fmt.Sprint(ss[0].Messages), "MIDDLEPROSE step 0") {
		t.Error("DEJA_INCLUDE_SUBAGENTS=1 does not keep the whole run")
	}
	t.Setenv("DEJA_INCLUDE_SUBAGENTS", "0")
	if has(child) {
		t.Error("DEJA_INCLUDE_SUBAGENTS=0 still reads the sub-agent log")
	}
}
