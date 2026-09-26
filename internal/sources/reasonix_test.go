package sources

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"
)

// clearReasonixEnv keeps the machine's own Reasonix settings out of a test.
func clearReasonixEnv(t *testing.T, home string) {
	t.Helper()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("APPDATA", filepath.Join(home, "AppData", "Roaming"))
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "xdg"))
	for _, k := range []string{"DEJA_REASONIX_ROOT", "REASONIX_HOME", "REASONIX_STATE_HOME"} {
		t.Setenv(k, "")
	}
}

func writeJSONLines(t *testing.T, path string, lines ...any) {
	t.Helper()
	var b strings.Builder
	for _, l := range lines {
		j, err := json.Marshal(l)
		if err != nil {
			t.Fatal(err)
		}
		b.Write(j)
		b.WriteByte('\n')
	}
	writeReasonixFile(t, path, b.String())
}

func writeJSONDoc(t *testing.T, path string, v any) {
	t.Helper()
	j, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	writeReasonixFile(t, path, string(j))
}

func writeReasonixFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func toolCall(id, name string, args map[string]any) map[string]any {
	a, _ := json.Marshal(args)
	return map[string]any{"id": id, "name": name, "arguments": string(a)}
}

// The current store: a transcript under projects/<slug>/sessions, its
// `.jsonl.meta` naming the workspace and the clock, createdAt on the turns the
// host stamps, and flat tool_calls whose arguments are a JSON string.
func TestParseReasonixSession(t *testing.T) {
	root := t.TempDir()
	clearReasonixEnv(t, root)
	t.Setenv("DEJA_REASONIX_ROOT", root)
	path := filepath.Join(root, "projects", "-w-relaylab", "sessions", "20260920-101000.000000000-deepseek-chat.jsonl")
	userAt := time.Date(2026, 9, 20, 10, 10, 5, 0, time.UTC)
	writeJSONLines(t, path,
		map[string]any{"role": "system", "content": "You are Reasonix."},
		map[string]any{"role": "user", "content": "<context>open files</context>\nthe relay drops frames under load", "raw_content": "the relay drops frames under load", "createdAt": userAt.UnixMilli()},
		map[string]any{"role": "user", "content": "[host] resuming after interrupt", "host_authored": true},
		map[string]any{"role": "assistant", "content": "Running the relay tests first.", "tool_calls": []any{
			toolCall("call_00", "bash", map[string]any{"command": "go test ./relay -run TestBackpressure"}),
		}},
		map[string]any{"role": "tool", "tool_call_id": "call_00", "name": "bash", "content": "relay_test.go:41: dropped 3 frames"},
		map[string]any{"role": "assistant", "content": "", "tool_calls": []any{
			toolCall("call_01", "edit_file", map[string]any{"path": "relay/queue.go", "old_string": "select {\ndefault:", "new_string": "q <- f"}),
		}},
	)
	writeJSONDoc(t, path+".meta", map[string]any{
		"id":             "20260920-101000.000000000-deepseek-chat",
		"created_at":     "2026-09-20T10:10:00Z",
		"updated_at":     "2026-09-20T10:14:00Z",
		"workspace_root": "/w/relaylab",
		"custom_title":   "relay backpressure",
	})

	ss, err := ParseReasonixFile(path)
	if err != nil || len(ss) != 1 {
		t.Fatalf("parse: %v, %d sessions", err, len(ss))
	}
	s := ss[0]
	if s.Harness != "reasonix" || s.ID != "20260920-101000.000000000-deepseek-chat" {
		t.Errorf("harness/id = %q/%q", s.Harness, s.ID)
	}
	if s.Project != "w/relaylab" {
		t.Errorf("project = %q, want the workspace from the meta", s.Project)
	}
	if s.Title != "relay backpressure" {
		t.Errorf("title = %q", s.Title)
	}
	if !s.Started.Equal(time.Date(2026, 9, 20, 10, 10, 0, 0, time.UTC)) || !s.Updated.Equal(time.Date(2026, 9, 20, 10, 14, 0, 0, time.UTC)) {
		t.Errorf("started/updated = %v/%v, want the meta's span", s.Started, s.Updated)
	}

	byRole := map[string][]string{}
	for _, m := range s.Messages {
		byRole[m.Role] = append(byRole[m.Role], m.Text)
	}
	if got := byRole["user"]; len(got) != 1 || got[0] != "the relay drops frames under load" {
		t.Errorf("user turns = %q, want the typed prompt alone", got)
	}
	if got := byRole["assistant"]; len(got) != 1 || got[0] != "Running the relay tests first." {
		t.Errorf("assistant turns = %q", got)
	}
	if got := byRole[RoleCommand]; len(got) != 1 || got[0] != "$ go test ./relay -run TestBackpressure" {
		t.Errorf("commands = %q, want the bash call's argument", got)
	}
	if got := byRole[RoleToolOutput]; len(got) != 1 || got[0] != "relay_test.go:41: dropped 3 frames" {
		t.Errorf("tool output = %q", got)
	}
	if got := byRole[RoleFiles]; len(got) != 1 || got[0] != "relay/queue.go" {
		t.Errorf("files = %q", got)
	}
	if got := byRole[RoleEdit]; len(got) != 1 || !strings.HasPrefix(got[0], "relay/queue.go\n") {
		t.Errorf("edits = %q, want the replaced span", got)
	}
	for _, texts := range byRole {
		for _, text := range texts {
			if strings.Contains(text, "You are Reasonix") || strings.Contains(text, "[host]") {
				t.Errorf("%q was indexed as something someone said", text)
			}
		}
	}
	if !s.Messages[0].Time.Equal(userAt) {
		t.Errorf("first turn at %v, want its own createdAt %v", s.Messages[0].Time, userAt)
	}
	for i := 1; i < len(s.Messages); i++ {
		if s.Messages[i].Time.Before(s.Messages[i-1].Time) {
			t.Fatalf("record %d goes back in time", i)
		}
	}
}

// v0.x wrote the same line shape with OpenAI's nested tool_calls, the
// workspace in <id>.meta.json and the clock only in <id>.events.jsonl.
func TestParseReasonixLegacySession(t *testing.T) {
	root := t.TempDir()
	clearReasonixEnv(t, root)
	dir := filepath.Join(root, "sessions")
	path := filepath.Join(dir, "code-scripts-202606200404.jsonl")
	args, _ := json.Marshal(map[string]any{"command": "npm run build"})
	writeJSONLines(t, path,
		map[string]any{"role": "user", "content": "the build script fails on windows"},
		map[string]any{"role": "assistant", "content": "", "tool_calls": []any{map[string]any{
			"id": "call_00_a", "type": "function",
			"function": map[string]any{"name": "bash", "arguments": string(args)},
		}}},
		map[string]any{"role": "tool", "tool_call_id": "call_00_a", "name": "bash", "content": "'rm' is not recognized"},
	)
	writeJSONDoc(t, filepath.Join(dir, "code-scripts-202606200404.meta.json"), map[string]any{
		"workspace": "/w/scripts", "summary": "windows build script",
	})
	writeJSONLines(t, filepath.Join(dir, "code-scripts-202606200404.events.jsonl"),
		map[string]any{"id": 1, "type": "user.message", "ts": "2026-06-20T04:04:00Z", "text": "the build script fails on windows"},
		map[string]any{"id": 2, "type": "model.final", "ts": "2026-06-20T04:06:30Z", "usage": map[string]any{"prompt_tokens": 900}, "costUsd": 0.001},
	)

	ss, err := ParseReasonixFile(path)
	if err != nil || len(ss) != 1 {
		t.Fatalf("parse: %v, %d sessions", err, len(ss))
	}
	s := ss[0]
	if s.Project != "w/scripts" || s.Title != "windows build script" {
		t.Errorf("project/title = %q/%q, want the v0.x meta's", s.Project, s.Title)
	}
	if !s.Started.Equal(time.Date(2026, 6, 20, 4, 4, 0, 0, time.UTC)) || !s.Updated.Equal(time.Date(2026, 6, 20, 4, 6, 30, 0, time.UTC)) {
		t.Errorf("started/updated = %v/%v, want the event log's span", s.Started, s.Updated)
	}
	var cmds []string
	for _, m := range s.Messages {
		if m.Role == RoleCommand {
			cmds = append(cmds, m.Text)
		}
	}
	if len(cmds) != 1 || cmds[0] != "$ npm run build" {
		t.Errorf("commands = %q, want the nested function's arguments", cmds)
	}
}

// With no sidecar at all the file's own mtime is the clock, so the session
// still sorts and falls inside a --since window.
func TestParseReasonixFallsBackToMtime(t *testing.T) {
	root := t.TempDir()
	clearReasonixEnv(t, root)
	path := filepath.Join(root, "sessions", "bare.jsonl")
	writeJSONLines(t, path,
		map[string]any{"role": "user", "content": "run it again"},
		map[string]any{"role": "user", "content": "run it again"},
	)
	mtime := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	if err := os.Chtimes(path, mtime, mtime); err != nil {
		t.Fatal(err)
	}
	ss, err := ParseReasonixFile(path)
	if err != nil || len(ss) != 1 {
		t.Fatalf("parse: %v, %d sessions", err, len(ss))
	}
	if ss[0].Started.IsZero() || !ss[0].Started.Equal(mtime) {
		t.Errorf("started = %v, want the file's mtime %v", ss[0].Started, mtime)
	}
	if n := len(ss[0].Messages); n != 2 || ss[0].Messages[0].Time.Equal(ss[0].Messages[1].Time) {
		t.Error("two identical turns share one stamp, so the index keeps one of them")
	}
}

// The session directories hold a dozen sidecars beside each transcript, and
// only the transcript is a conversation.
func TestReasonixSessionFilesSkipSidecars(t *testing.T) {
	root := t.TempDir()
	clearReasonixEnv(t, root)
	t.Setenv("DEJA_REASONIX_ROOT", root)
	global := filepath.Join(root, "sessions")
	project := filepath.Join(root, "projects", "-w-api", "sessions")
	for _, p := range []string{
		filepath.Join(global, "a.jsonl"),
		filepath.Join(global, "a.events.jsonl"),
		filepath.Join(global, "a.events.jsonl.damaged"),
		filepath.Join(global, "a.wire.jsonl"),
		filepath.Join(global, "a.jsonl.meta"),
		filepath.Join(global, "a.jsonl.lock"),
		filepath.Join(global, "a.meta.json"),
		filepath.Join(global, "a.context.json"),
		filepath.Join(global, "subagent-x.jsonl"),
		filepath.Join(global, "subagents", "a", "worker.jsonl"),
		filepath.Join(project, "b.jsonl"),
		filepath.Join(project, "b.guardian.jsonl"),
	} {
		writeReasonixFile(t, p, "{}\n")
	}
	var got []string
	for _, p := range ReasonixSessionFiles() {
		got = append(got, filepath.Base(p))
	}
	slices.Sort(got)
	if strings.Join(got, ",") != "a.jsonl,b.jsonl" {
		t.Fatalf("session files = %v, want the two transcripts", got)
	}
	if !IsReasonixSession(filepath.Join(project, "b.jsonl")) || IsReasonixSession(filepath.Join(project, "b.guardian.jsonl")) {
		t.Error("the registry kind disagrees with the file list")
	}
	if IsReasonixSession(filepath.Join(global, "subagents", "a", "worker.jsonl")) {
		t.Error("a subagent log matches as a transcript")
	}
}

// The state root follows Reasonix's own chain: REASONIX_STATE_HOME, then
// REASONIX_HOME, then a relocation in config.toml, then the OS default.
func TestReasonixStateRootChain(t *testing.T) {
	home := t.TempDir()
	clearReasonixEnv(t, home)
	want := filepath.Join(home, ".reasonix")
	if runtime.GOOS == "windows" {
		want = filepath.Join(home, "AppData", "Roaming", "reasonix")
	}
	if got := ReasonixStateRoot(); got != want {
		t.Errorf("default root = %q, want %q", got, want)
	}

	moved := filepath.Join(home, "big-disk", "reasonix-state")
	cfg, _ := json.Marshal(moved) // a TOML basic string is a JSON string
	writeReasonixFile(t, filepath.Join(want, "config.toml"),
		"[ui]\nstate = \"ignored\"\n\n[storage]\nstate = "+string(cfg)+"\n")
	if got := ReasonixStateRoot(); got != moved {
		t.Errorf("relocated root = %q, want the [storage] state entry %q", got, moved)
	}

	pinned := filepath.Join(home, "pinned")
	t.Setenv("REASONIX_HOME", pinned)
	if got := ReasonixStateRoot(); got != pinned {
		t.Errorf("REASONIX_HOME root = %q, want %q", got, pinned)
	}
	state := filepath.Join(home, "state")
	t.Setenv("REASONIX_STATE_HOME", state)
	if got := ReasonixStateRoot(); got != state {
		t.Errorf("REASONIX_STATE_HOME root = %q, want %q", got, state)
	}
}

// Reasonix copies legacy sessions into its current store and leaves the
// originals, so a legacy file is read only when the current store lacks its id.
func TestReasonixLegacyRootIsReadWithoutDuplicates(t *testing.T) {
	home := t.TempDir()
	clearReasonixEnv(t, home)
	legacy := filepath.Join(home, "xdg", "reasonix")
	if runtime.GOOS == "windows" {
		legacy = filepath.Join(home, ".reasonix")
	}
	current := ReasonixStateRoot()
	writeReasonixFile(t, filepath.Join(current, "sessions", "imported.jsonl"), "{}\n")
	writeReasonixFile(t, filepath.Join(legacy, "sessions", "imported.jsonl"), "{}\n")
	writeReasonixFile(t, filepath.Join(legacy, "sessions", "old-only.jsonl"), "{}\n")

	var got []string
	for _, p := range ReasonixSessionFiles() {
		rel, _ := filepath.Rel(home, p)
		got = append(got, filepath.ToSlash(rel))
	}
	slices.Sort(got)
	curRel, _ := filepath.Rel(home, filepath.Join(current, "sessions", "imported.jsonl"))
	oldRel, _ := filepath.Rel(home, filepath.Join(legacy, "sessions", "old-only.jsonl"))
	want := []string{filepath.ToSlash(curRel), filepath.ToSlash(oldRel)}
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Fatalf("session files = %v, want %v", got, want)
	}

	t.Setenv("REASONIX_HOME", current)
	if roots := ReasonixRoots(); len(roots) != 1 {
		t.Errorf("roots = %v, want only the pinned home", roots)
	}
}
