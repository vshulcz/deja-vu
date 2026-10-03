package main

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/index"
)

func hostsPrompt(t *testing.T, dir string, payload map[string]any) string {
	t.Helper()
	b, _ := json.Marshal(payload)
	var out bytes.Buffer
	if err := runHookPrompt(dir, bytes.NewReader(b), &out); err != nil {
		t.Fatal(err)
	}
	return out.String()
}

func hostsNoWarmup(t *testing.T) {
	old := spawnWarmup
	spawnWarmup = func(string, string) error { return nil }
	t.Cleanup(func() { spawnWarmup = old })
}

// geminiChat writes a Gemini CLI 0.60 transcript in the layout the CLI keeps,
// with the project folder recorded the way Gemini records it.
func geminiChat(t *testing.T, workspace string, compacted bool) string {
	t.Helper()
	idDir := filepath.Join(t.TempDir(), "tmp", "proj")
	if err := os.MkdirAll(filepath.Join(idDir, "chats"), 0o755); err != nil {
		t.Fatal(err)
	}
	compactionWrite(t, filepath.Join(idDir, ".project_root"), workspace)
	lines := []string{
		`{"sessionId":"gem-1","projectHash":"h","startTime":"2026-10-03T11:49:20Z","lastUpdated":"2026-10-03T11:49:20Z"}`,
		`{"$set":{"messages":[{"id":"ctx","type":"user","content":[{"text":"<session_context>\nsetup"}]}]}}`,
		`{"id":"u1","timestamp":"2026-10-03T11:49:21Z","type":"user","content":[{"text":"fix the parser test"}]}`,
		`{"id":"g1","timestamp":"2026-10-03T11:49:22Z","type":"gemini","content":"","toolCalls":[{"id":"c1","name":"run_shell_command","args":{"command":"go test ./parser/..."},"result":[{"functionResponse":{"id":"c1","name":"run_shell_command","response":{"output":"Output: --- FAIL: TestParseSeed\nwant 3, got 4\nFAIL\nExit Code: 1"}}}],"status":"success"}]}`,
		`{"id":"g2","timestamp":"2026-10-03T11:49:23Z","type":"gemini","content":"The parser test fails: want 3, got 4. I decided to fix parse.go next."}`,
	}
	if compacted {
		lines = append(lines,
			`{"$set":{"sessionId":"gem-1"}}`,
			`{"$set":{"messages":[{"id":"ctx","type":"user","content":[{"text":"<session_context>\nsetup"}]},{"id":"s1","type":"user","content":[{"text":"<state_snapshot>fix parser</state_snapshot>"}]},{"id":"s2","type":"gemini","content":[{"text":"Got it."}]}]}}`,
			`{"id":"g3","timestamp":"2026-10-03T11:49:30Z","type":"gemini","content":"ok"}`)
	}
	path := filepath.Join(idDir, "chats", "session-2026-10-03T11-49-gem1.jsonl")
	compactionWrite(t, path, strings.Join(lines, "\n")+"\n")
	return path
}

// Gemini CLI says nothing after a compaction, so the prompt after one finds it
// in the transcript, delivers the packet once, and leaves a session that never
// compacted alone.
func TestGeminiCompactionIsHandedBackAtTheNextPrompt(t *testing.T) {
	hermeticEnv(t)
	hostsNoWarmup(t)
	workspace := compactionGitRepo(t)
	dir := index.DefaultDir()
	ask := func(transcript string) string {
		return hostsPrompt(t, dir, map[string]any{
			"session_id": "gem-1", "transcript_path": transcript, "cwd": workspace,
			"hook_event_name": "BeforeAgent", "prompt": "what next",
		})
	}
	if out := ask(geminiChat(t, workspace, false)); strings.Contains(out, "Compaction context") {
		t.Fatalf("a session that never compacted got a packet: %s", out)
	}
	transcript := geminiChat(t, workspace, true)
	out := ask(transcript)
	for _, want := range []string{"Compaction context", "go test ./parser/...", "[failed]", "want 3, got 4", "untrusted reference data"} {
		if !strings.Contains(out, want) {
			t.Errorf("packet lacks %q: %s", want, out)
		}
	}
	if strings.Contains(out, "state_snapshot") {
		t.Errorf("the packet was built from the summary: %s", out)
	}
	if again := ask(transcript); strings.Contains(again, "Compaction context") {
		t.Fatalf("the same compaction was handed back twice: %s", again)
	}
	// Only Gemini's prompt event looks: a host that has its own PreCompact
	// hook gets no capture from its prompts.
	b, _ := json.Marshal(map[string]any{"session_id": "gem-2", "transcript_path": transcript, "cwd": workspace, "prompt": "what next"})
	var other bytes.Buffer
	if err := runHookPrompt(dir, bytes.NewReader(b), &other); err != nil {
		t.Fatal(err)
	}
	if _, found, _ := index.Compaction(dir, "gem-2", workspace); found {
		t.Fatal("a UserPromptSubmit payload captured a compaction")
	}
}

// Hermes gives its memory provider the turns it compresses; the provider
// passes them to hook-precompact once the compression commits, under the id
// the next turn asks with.
func TestHermesTurnsBecomeTheNextTurnsPacket(t *testing.T) {
	hermeticEnv(t)
	hostsNoWarmup(t)
	workspace := compactionGitRepo(t)
	dir := index.DefaultDir()
	result, _ := json.Marshal(map[string]any{"output": "--- FAIL: TestParseSeed\nwant 3, got 4\nFAIL", "exit_code": 1})
	payload, _ := json.Marshal(map[string]any{
		"session_id": "hermes-new", "cwd": workspace, "harness": "hermes",
		"messages": []any{
			map[string]any{"role": "user", "content": "fix the parser test"},
			map[string]any{"role": "assistant", "content": "", "tool_calls": []any{map[string]any{"id": "call_1", "type": "function", "function": map[string]any{"name": "terminal", "arguments": `{"command":"go test ./parser/..."}`}}}},
			map[string]any{"role": "tool", "tool_call_id": "call_1", "content": string(result)},
			map[string]any{"role": "assistant", "content": "The parser test fails: want 3, got 4. I decided to fix parse.go next."},
		},
	})
	withHookStdin(t, string(payload))
	runHookPrecompact(dir)
	out := hostsPrompt(t, dir, map[string]any{"session_id": "hermes-new", "cwd": workspace, "prompt": "what next"})
	for _, want := range []string{"Compaction context", "go test ./parser/...", "[failed]", "want 3, got 4"} {
		if !strings.Contains(out, want) {
			t.Errorf("packet lacks %q: %s", want, out)
		}
	}
}

// opencode and Kilo name the session; deja reads it from their store.
func TestOpencodeStoreSessionBecomesTheNextTurnsPacket(t *testing.T) {
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skip("sqlite3 not installed")
	}
	hermeticEnv(t)
	hostsNoWarmup(t)
	workspace := compactionGitRepo(t)
	dir := index.DefaultDir()
	db := filepath.Join(t.TempDir(), "opencode.db")
	quoted, _ := json.Marshal(workspace)
	script := `create table session(id text, directory text, time_created any, time_updated any);
create table message(id text, session_id text, time_created any, data text);
create table part(id text, message_id text, data text);
insert into session values('ses_oc', ` + strings.ReplaceAll(string(quoted), `"`, `'`) + `, '2026-01-02T03:00:00Z','2026-01-03T03:00:00Z');
insert into message values('m1','ses_oc',1767409200000,'{"role":"user"}');
insert into part values('p1','m1','{"type":"text","text":"fix the parser test"}');
insert into message values('m2','ses_oc',1767409201000,'{"role":"assistant"}');
insert into part values('p2','m2','{"type":"tool","tool":"bash","state":{"input":{"command":"go test ./parser/..."},"output":"--- FAIL: TestParseSeed\nwant 3, got 4","metadata":{"exit":1}},"time":{"start":"2026-01-02T03:00:01Z"}}');
insert into part values('p3','m2','{"type":"text","text":"The parser test fails: want 3, got 4. I decided to fix parse.go next."}');`
	if out, err := exec.Command("sqlite3", db, script).CombinedOutput(); err != nil {
		t.Fatalf("sqlite setup: %v %s", err, out)
	}
	t.Setenv("DEJA_OPENCODE_DB", db)
	payload, _ := json.Marshal(map[string]any{"session_id": "ses_oc", "cwd": workspace, "harness": "opencode"})
	withHookStdin(t, string(payload))
	runHookPrecompact(dir)
	out := hostsPrompt(t, dir, map[string]any{"session_id": "ses_oc", "cwd": workspace, "prompt": "Continue if you have next steps"})
	for _, want := range []string{"Compaction context", "go test ./parser/...", "[failed]", "want 3, got 4"} {
		if !strings.Contains(out, want) {
			t.Errorf("packet lacks %q: %s", want, out)
		}
	}
}

// The plugins name the session and the store; the compaction's own request
// gets neither recall nor the packet.
func TestOpencodeShapedPluginsSendTheCompactingSession(t *testing.T) {
	for target, js := range map[string]string{
		"opencode": opencodeLegacyPluginJS("/bin/deja"),
		"kilocode": kilocodePluginJS("/bin/deja"),
	} {
		for _, want := range []string{
			`const HARNESS = "` + target + `"`,
			`session_id: sessionID, cwd, harness: HARNESS`,
			`if (owner && compacting.has(owner)) return`,
			`if (input.sessionID && compacting.delete(input.sessionID)) return`,
		} {
			if !strings.Contains(js, want) {
				t.Errorf("%s plugin lacks %q", target, want)
			}
		}
	}
	if js := opencodePluginJS("/bin/deja"); !strings.Contains(js, `session_id: event?.sessionID || "", cwd, harness: "opencode"`) {
		t.Error("the 2.x plugin does not name the compacting session")
	}
	py := hermesCatalogPy()
	for _, want := range []string{"def on_pre_compress", `kwargs.get("reason") == "compression"`, `"harness": "hermes"`} {
		if !strings.Contains(py, want) {
			t.Errorf("hermes provider lacks %q", want)
		}
	}
}
