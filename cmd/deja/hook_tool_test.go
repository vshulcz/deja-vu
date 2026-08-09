package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	indexPkg "github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/usage"
)

func toolHookRun(t *testing.T, payload string) string {
	t.Helper()
	var out strings.Builder
	if err := runHookTool(os.Getenv("DEJA_INDEX_DIR"), strings.NewReader(payload), &out); err != nil {
		t.Fatal(err)
	}
	return out.String()
}

// The hook fires on every action an agent takes, so its default is silence and
// its ceiling is one short line. A command this machine has run before earns
// that line; anything else does not.
func TestToolHookSpeaksOnlyForACommandWithAHistory(t *testing.T) {
	tmp := hermeticEnv(t)
	t.Setenv("DEJA_INDEX_DIR", filepath.Join(tmp, "index.db"))
	root := os.Getenv("DEJA_CLAUDE_ROOT")
	for _, id := range []string{"a", "b"} {
		writeClaudeFixture(t, filepath.Join(root, "p", id+".jsonl"), id, []string{
			`{"type":"user","sessionId":"` + id + `","timestamp":"2026-01-02T03:04:05Z","message":{"role":"user","content":"run the suite"}}`,
			`{"type":"assistant","sessionId":"` + id + `","timestamp":"2026-01-02T03:04:06Z","message":{"role":"assistant","content":[{"type":"tool_use","id":"t1","name":"Bash","input":{"command":"go test ./... -count=1"}}]}}`,
		})
	}
	if _, err := captureRun(t, "index"); err != nil {
		t.Fatal(err)
	}

	out := toolHookRun(t, `{"hook_event_name":"PreToolUse","tool_name":"Bash","tool_input":{"command":"go test ./... -count=1"},"session_id":"now"}`)
	if out == "" {
		t.Fatal("a command run in two sessions produced no line")
	}
	var resp sessionStartHookResponse
	if err := json.Unmarshal([]byte(out), &resp); err != nil {
		t.Fatalf("hook output is not the PreToolUse shape: %v (%q)", err, out)
	}
	if resp.HookSpecificOutput.HookEventName != "PreToolUse" {
		t.Errorf("wrong event name: %q", resp.HookSpecificOutput.HookEventName)
	}
	if !strings.Contains(resp.HookSpecificOutput.AdditionalContext, "2 sessions") {
		t.Errorf("the line does not say how widely it ran: %q", resp.HookSpecificOutput.AdditionalContext)
	}
	if n := len(resp.HookSpecificOutput.AdditionalContext); n > toolHookMaxBytes+len(frameRecall("")) {
		t.Errorf("payload is %d bytes, past the per-action budget", n)
	}

	// A command with no history at all is silence, not a "nothing found" line:
	// this is paid once per action.
	if got := toolHookRun(t, `{"hook_event_name":"PreToolUse","tool_name":"Bash","tool_input":{"command":"terraform apply -auto-approve"},"session_id":"now"}`); got != "" {
		t.Errorf("spoke about a command it has never seen: %q", got)
	}
	// And an editor action on a file nothing has touched stays silent too.
	if got := toolHookRun(t, `{"hook_event_name":"PreToolUse","tool_name":"Edit","tool_input":{"file_path":"/srv/app/never.go"},"session_id":"now"}`); got != "" {
		t.Errorf("spoke about a file with no history: %q", got)
	}
	// Malformed input is a miss, never an error.
	if got := toolHookRun(t, "not json"); got != "" {
		t.Errorf("answered malformed input: %q", got)
	}
}

// FileSessions matches on basename, so a same-named file in an unrelated repo
// would inflate the count and point deja blame at the wrong project. The file
// line must count only the project being worked in.
func TestToolHookFileLineIsScopedToTheProject(t *testing.T) {
	tmp := hermeticEnv(t)
	t.Setenv("DEJA_INDEX_DIR", filepath.Join(tmp, "index.db"))
	root := os.Getenv("DEJA_CLAUDE_ROOT")
	touch := func(proj, cwd, id string) {
		writeClaudeFixture(t, filepath.Join(root, proj, id+".jsonl"), id, []string{
			`{"type":"user","sessionId":"` + id + `","cwd":"` + cwd + `","timestamp":"2026-01-02T03:04:05Z","message":{"role":"user","content":"edit config"}}`,
			`{"type":"assistant","sessionId":"` + id + `","cwd":"` + cwd + `","timestamp":"2026-01-02T03:04:06Z","message":{"role":"assistant","content":[{"type":"tool_use","id":"t1","name":"Edit","input":{"file_path":"` + cwd + `/config.go","old_string":"a","new_string":"b"}}]}}`,
		})
	}
	// Six sessions in each of two projects, both editing a file named config.go.
	for i := 0; i < 6; i++ {
		touch("alpha", "/work/alpha", "al"+string(rune('0'+i)))
		touch("beta", "/work/beta", "be"+string(rune('0'+i)))
	}
	if _, err := captureRun(t, "index"); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CLAUDE_PROJECT_DIR", "/work/alpha")
	out := toolHookRun(t, `{"hook_event_name":"PreToolUse","tool_name":"Edit","tool_input":{"file_path":"/work/alpha/config.go"},"session_id":"now","cwd":"/work/alpha"}`)
	if out == "" {
		t.Fatal("the file worked on in this project produced no line")
	}
	var resp sessionStartHookResponse
	if err := json.Unmarshal([]byte(out), &resp); err != nil {
		t.Fatal(err)
	}
	ctx := resp.HookSpecificOutput.AdditionalContext
	if strings.Contains(ctx, "12 sessions") {
		t.Errorf("the count included the other project's identically-named file:\n%s", ctx)
	}
	if !strings.Contains(ctx, "6 sessions") {
		t.Errorf("the current project's history was not counted correctly:\n%s", ctx)
	}
}

// The hook must not fire for tools that only read: Read/Glob/NotebookRead carry
// a file_path, and a wide matcher would otherwise pay the cost on every one.
func TestToolHookIgnoresNonEditingTools(t *testing.T) {
	tmp := hermeticEnv(t)
	t.Setenv("DEJA_INDEX_DIR", filepath.Join(tmp, "index.db"))
	root := os.Getenv("DEJA_CLAUDE_ROOT")
	for i := 0; i < 6; i++ {
		id := "s" + string(rune('0'+i))
		writeClaudeFixture(t, filepath.Join(root, "alpha", id+".jsonl"), id, []string{
			`{"type":"user","sessionId":"` + id + `","cwd":"/work/alpha","timestamp":"2026-01-02T03:04:05Z","message":{"role":"user","content":"edit"}}`,
			`{"type":"assistant","sessionId":"` + id + `","cwd":"/work/alpha","timestamp":"2026-01-02T03:04:06Z","message":{"role":"assistant","content":[{"type":"tool_use","id":"t1","name":"Edit","input":{"file_path":"/work/alpha/config.go","old_string":"a","new_string":"b"}}]}}`,
		})
	}
	if _, err := captureRun(t, "index"); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CLAUDE_PROJECT_DIR", "/work/alpha")
	// Read on a file with real history: still silent.
	if got := toolHookRun(t, `{"hook_event_name":"PreToolUse","tool_name":"Read","tool_input":{"file_path":"/work/alpha/config.go"},"session_id":"now","cwd":"/work/alpha"}`); got != "" {
		t.Errorf("the hook fired for a Read: %q", got)
	}
	// Edit on the same file: speaks.
	if got := toolHookRun(t, `{"hook_event_name":"PreToolUse","tool_name":"Edit","tool_input":{"file_path":"/work/alpha/config.go"},"session_id":"now","cwd":"/work/alpha"}`); got == "" {
		t.Error("the hook stayed silent for an Edit on a file with history")
	}
}

// Isolate the project-scoping branch: query a bare filename so the exact-path
// escape hatch cannot fire, leaving project-scoping as the only thing that can
// admit the session. And a file in no known project stays silent.
func TestToolHookFileScopingIsLoadBearing(t *testing.T) {
	tmp := hermeticEnv(t)
	t.Setenv("DEJA_INDEX_DIR", filepath.Join(tmp, "index.db"))
	root := os.Getenv("DEJA_CLAUDE_ROOT")
	for i := 0; i < 6; i++ {
		id := "s" + string(rune('0'+i))
		writeClaudeFixture(t, filepath.Join(root, "alpha", id+".jsonl"), id, []string{
			`{"type":"user","sessionId":"` + id + `","cwd":"/work/alpha","timestamp":"2026-01-02T03:04:05Z","message":{"role":"user","content":"edit"}}`,
			`{"type":"assistant","sessionId":"` + id + `","cwd":"/work/alpha","timestamp":"2026-01-02T03:04:06Z","message":{"role":"assistant","content":[{"type":"tool_use","id":"t1","name":"Edit","input":{"file_path":"/work/alpha/config.go","old_string":"a","new_string":"b"}}]}}`,
		})
	}
	if _, err := captureRun(t, "index"); err != nil {
		t.Fatal(err)
	}
	// Bare filename: t=="/work/alpha/config.go" != "config.go", so only project
	// scoping (cwd alpha) can admit these sessions.
	t.Setenv("CLAUDE_PROJECT_DIR", "/work/alpha")
	if got := toolHookRun(t, `{"hook_event_name":"PreToolUse","tool_name":"Edit","tool_input":{"file_path":"config.go"},"session_id":"now","cwd":"/work/alpha"}`); got == "" {
		t.Error("project scoping did not admit a same-project session on a bare-filename query")
	}
	// Same bare query from an unrelated project: no session matches → silent.
	t.Setenv("CLAUDE_PROJECT_DIR", "/work/gamma")
	if got := toolHookRun(t, `{"hook_event_name":"PreToolUse","tool_name":"Edit","tool_input":{"file_path":"config.go"},"session_id":"now","cwd":"/work/gamma"}`); got != "" {
		t.Errorf("a file in no known project produced a line: %q", got)
	}
}

// The command line honours the trust policy: a command that ran only in a
// withheld project stays silent, and the count excludes withheld sessions.
func TestToolHookCommandHonoursTrustPolicy(t *testing.T) {
	tmp := hermeticEnv(t)
	t.Setenv("DEJA_INDEX_DIR", filepath.Join(tmp, "index.db"))
	root := os.Getenv("DEJA_CLAUDE_ROOT")
	// A local session running a distinctive command.
	writeClaudeFixture(t, filepath.Join(root, "m", "l1.jsonl"), "l1", []string{
		`{"type":"user","sessionId":"l1","cwd":"/m","timestamp":"2026-01-02T03:04:05Z","message":{"role":"user","content":"deploy"}}`,
		`{"type":"assistant","sessionId":"l1","cwd":"/m","timestamp":"2026-01-02T03:04:06Z","message":{"role":"assistant","content":[{"type":"tool_use","id":"t1","name":"Bash","input":{"command":"terraform apply -auto-approve"}}]}}`,
	})
	if err := indexPkg.Ensure(indexPkg.DefaultDir(), "", true, nil); err != nil {
		t.Fatal(err)
	}
	// Import three peer sessions running a different distinctive command.
	in := filepath.Join(tmp, "batch")
	if err := os.MkdirAll(in, 0o755); err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	for i := 1; i <= 3; i++ {
		id := "pe" + string(rune('0'+i))
		b.WriteString(`{"harness":"claude","session_id":"` + id + `","project":"peerproj","role":"user","text":"peer deploy","time":"2026-07-2` + string(rune('0'+i)) + `T10:00:00Z"}` + "\n")
		b.WriteString(`{"harness":"claude","session_id":"` + id + `","project":"peerproj","role":"command","text":"helmfile sync --peer-only","time":"2026-07-2` + string(rune('0'+i)) + `T10:01:00Z"}` + "\n")
	}
	if err := os.WriteFile(filepath.Join(in, "deja-sync-peer.jsonl"), []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := indexPkg.Import(indexPkg.DefaultDir(), in); err != nil {
		t.Fatal(err)
	}
	// Rebuild so commands.gob mines the imported sessions too.
	if err := indexPkg.Ensure(indexPkg.DefaultDir(), "", true, nil); err != nil {
		t.Fatal(err)
	}
	// Deny imported for the auto activation.
	pol := filepath.Join(os.Getenv("HOME"), ".config", "deja", "policy.json")
	if err := os.MkdirAll(filepath.Dir(pol), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(pol, []byte(`{"activations":{"auto":{"imported":false}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	// The peer-only command must stay silent under the deny policy.
	if got := toolHookRun(t, `{"hook_event_name":"PreToolUse","tool_name":"Bash","tool_input":{"command":"helmfile sync --peer-only"},"session_id":"now"}`); got != "" {
		t.Errorf("a command that ran only in a withheld project surfaced: %q", got)
	}
}

// An inspection command carries no reusable signal, and a PreToolUse hook must
// not repeat the same line to the same agent session on every action.
func TestToolHookSkipsInspectionAndDedupes(t *testing.T) {
	tmp := hermeticEnv(t)
	t.Setenv("DEJA_INDEX_DIR", filepath.Join(tmp, "index.db"))
	root := os.Getenv("DEJA_CLAUDE_ROOT")
	for _, id := range []string{"a", "b", "c"} {
		writeClaudeFixture(t, filepath.Join(root, "p", id+".jsonl"), id, []string{
			`{"type":"user","sessionId":"` + id + `","timestamp":"2026-01-02T03:04:05Z","message":{"role":"user","content":"go"}}`,
			`{"type":"assistant","sessionId":"` + id + `","timestamp":"2026-01-02T03:04:06Z","message":{"role":"assistant","content":[{"type":"tool_use","id":"t1","name":"Bash","input":{"command":"git status --short"}}]}}`,
			`{"type":"assistant","sessionId":"` + id + `","timestamp":"2026-01-02T03:04:07Z","message":{"role":"assistant","content":[{"type":"tool_use","id":"t2","name":"Bash","input":{"command":"make deploy-prod REGION=eu"}}]}}`,
		})
	}
	if _, err := captureRun(t, "index"); err != nil {
		t.Fatal(err)
	}
	// git status ran in 3 sessions but is inspection — silent.
	if got := toolHookRun(t, `{"hook_event_name":"PreToolUse","tool_name":"Bash","tool_input":{"command":"git status --short"},"session_id":"agent1"}`); got != "" {
		t.Errorf("an inspection command produced a line: %q", got)
	}
	// A real deploy command speaks once...
	if got := toolHookRun(t, `{"hook_event_name":"PreToolUse","tool_name":"Bash","tool_input":{"command":"make deploy-prod REGION=eu"},"session_id":"agent1"}`); got == "" {
		t.Fatal("a command with real history stayed silent")
	}
	// ...but not a second time in the same agent session.
	if got := toolHookRun(t, `{"hook_event_name":"PreToolUse","tool_name":"Bash","tool_input":{"command":"make deploy-prod REGION=eu"},"session_id":"agent1"}`); got != "" {
		t.Errorf("the same line was re-injected to the same session: %q", got)
	}
	// A different agent session still hears it.
	if got := toolHookRun(t, `{"hook_event_name":"PreToolUse","tool_name":"Bash","tool_input":{"command":"make deploy-prod REGION=eu"},"session_id":"agent2"}`); got == "" {
		t.Error("a fresh agent session was wrongly deduped")
	}
}

// The per-action byte cap must fall on a rune boundary, or a non-ASCII line is
// corrupted into U+FFFD when marshalled.
func TestTruncateToolLineKeepsRunesWhole(t *testing.T) {
	// A run of two-byte runes; cutting at an odd byte would split one.
	s := strings.Repeat("é", 200) // 400 bytes
	got := truncateToolLine(s, 301)
	if !utf8.ValidString(got) {
		t.Fatalf("truncation produced invalid UTF-8: %q", got)
	}
	if len(got) > 301 {
		t.Errorf("over budget: %d bytes", len(got))
	}
	// A short line is returned unchanged.
	if truncateToolLine("ok", 300) != "ok" {
		t.Error("a short line was altered")
	}
}

// The hook-tool injection is recorded so it is visible to stats and the
// receipt, and deduped so it is counted once per fact, not per action.
func TestToolHookRecordsTheInjection(t *testing.T) {
	tmp := hermeticEnv(t)
	dir := filepath.Join(tmp, "index.db")
	t.Setenv("DEJA_INDEX_DIR", dir)
	root := os.Getenv("DEJA_CLAUDE_ROOT")
	for _, id := range []string{"a", "b"} {
		writeClaudeFixture(t, filepath.Join(root, "p", id+".jsonl"), id, []string{
			`{"type":"user","sessionId":"` + id + `","timestamp":"2026-01-02T03:04:05Z","message":{"role":"user","content":"go"}}`,
			`{"type":"assistant","sessionId":"` + id + `","timestamp":"2026-01-02T03:04:06Z","message":{"role":"assistant","content":[{"type":"tool_use","id":"t1","name":"Bash","input":{"command":"make deploy-prod REGION=eu"}}]}}`,
		})
	}
	if _, err := captureRun(t, "index"); err != nil {
		t.Fatal(err)
	}
	before := usage.Totals(dir).Injections
	payload := `{"hook_event_name":"PreToolUse","tool_name":"Bash","tool_input":{"command":"make deploy-prod REGION=eu"},"session_id":"agentX"}`
	toolHookRun(t, payload)
	if got := usage.Totals(dir).Injections; got != before+1 {
		t.Fatalf("injection not recorded: before=%d after=%d", before, got)
	}
	// The dedup means a repeat in the same session records nothing more.
	toolHookRun(t, payload)
	if got := usage.Totals(dir).Injections; got != before+1 {
		t.Errorf("a deduped repeat still recorded an injection: %d", got)
	}
}
