package sources

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
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
