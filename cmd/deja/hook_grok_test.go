package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/index"
)

// Grok Build spells every hook field in camelCase. Measured on 1.0.5: the
// payload names the project under `cwd` like everyone else, which is why the
// recall it produced looked right — and names the session under `sessionId`,
// which deja was not reading, so every grok session on the machine shared the
// empty key in the dedup ledger.
func TestGrokPayloadFilesRecallUnderItsOwnSession(t *testing.T) {
	withStatsStores(t)
	claudeRoot := os.Getenv("DEJA_CLAUDE_ROOT")
	old := time.Now().Add(-72 * time.Hour).UTC().Format(time.RFC3339)
	writeClaudeFixture(t, filepath.Join(claudeRoot, "beta", "one.jsonl"), "grokterm", []string{
		`{"type":"user","sessionId":"grokterm","timestamp":"` + old +
			`","message":{"role":"user","content":"pgbouncer runs in transaction mode and prepared statements are off"}}`,
	})
	if err := index.Ensure(index.DefaultDir(), "", true, nil); err != nil {
		t.Fatal(err)
	}
	cwd := filepath.Join(t.TempDir(), "tmp", "beta")
	if err := os.MkdirAll(cwd, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(cwd)

	payload := `{"hookEventName":"user_prompt_submit","prompt":"do we need pgbouncer here","sessionId":"grok-1"}`
	var first bytes.Buffer
	if err := runHookPromptMode(index.DefaultDir(), strings.NewReader(payload), &first, true); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(first.String(), "transaction mode") {
		t.Fatalf("nothing was recalled to begin with:\n%q", first.String())
	}
	// The whole point: the serving is filed under the session grok named. Under
	// the empty key it is filed against every other grok session at once.
	if got := alreadyInjected(index.DefaultDir(), "grok-1"); len(got) == 0 {
		t.Fatal("the serving was not filed under the session grok named")
	}
	if got := alreadyInjected(index.DefaultDir(), ""); len(got) != 0 {
		t.Errorf("the serving was filed under no session at all: %v", got)
	}

	// And a compaction in that session forgets it, so what the compaction threw
	// away can be sent again.
	withHookStdin(t, `{"sessionId":"grok-1","hookEventName":"pre_compact"}`)
	runHookPrecompact(index.DefaultDir())
	if got := alreadyInjected(index.DefaultDir(), "grok-1"); len(got) != 0 {
		t.Errorf("compaction left this session's seen list behind: %v", got)
	}
	var second bytes.Buffer
	if err := runHookPromptMode(index.DefaultDir(), strings.NewReader(payload), &second, true); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(second.String(), "transaction mode") {
		t.Errorf("after compaction the memory was still withheld:\n%q", second.String())
	}
}

// Grok names the project twice, under `cwd` and under `workspaceRoot`. A
// session started outside a workspace can leave the first empty, and the
// project is what the ranking runs inside — with neither read, the recall is
// for no project at all.
func TestGrokWorkspaceRootNamesTheProject(t *testing.T) {
	withStatsStores(t)
	claudeRoot := os.Getenv("DEJA_CLAUDE_ROOT")
	old := time.Now().Add(-72 * time.Hour).UTC().Format(time.RFC3339)
	writeClaudeFixture(t, filepath.Join(claudeRoot, "beta", "one.jsonl"), "grokroot", []string{
		`{"type":"user","sessionId":"grokroot","timestamp":"` + old +
			`","message":{"role":"user","content":"pgbouncer runs in transaction mode and prepared statements are off"}}`,
	})
	if err := index.Ensure(index.DefaultDir(), "", true, nil); err != nil {
		t.Fatal(err)
	}
	// Somewhere else entirely, so only the payload can name the project.
	elsewhere := filepath.Join(t.TempDir(), "unrelated")
	if err := os.MkdirAll(elsewhere, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(elsewhere)
	root := filepath.Join(t.TempDir(), "tmp", "beta")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}

	payload := `{"hookEventName":"user_prompt_submit","prompt":"do we need pgbouncer here",` +
		`"sessionId":"grok-2","workspaceRoot":` + strconv.Quote(root) + `}`
	var out bytes.Buffer
	if err := runHookPromptMode(index.DefaultDir(), strings.NewReader(payload), &out, true); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "transaction mode") {
		t.Errorf("the project grok named in workspaceRoot was not recalled for:\n%q", out.String())
	}
}

// Grok discards what a hook prints — measured on 1.0.5 for session start, the
// user prompt, and both tool events. The one reply it acts on is a PreToolUse
// that rewrites the tool's input, which is exactly the shape the spawn hook
// already answers in. So the subagent is the one place in grok where deja can
// still put memory in front of a model, and it works only if the spawn is
// recognised under grok's name for it and read out of grok's spelling of the
// payload.
func TestGrokSpawnCarriesMemoryIntoTheSubagent(t *testing.T) {
	tmp := hermeticEnv(t)
	t.Setenv("DEJA_INDEX_DIR", filepath.Join(tmp, "index.db"))
	root := os.Getenv("DEJA_CLAUDE_ROOT")
	writeClaudeFixture(t, filepath.Join(root, "app", "past.jsonl"), "past", []string{
		`{"type":"user","sessionId":"past","timestamp":"2026-01-02T03:04:05Z","message":{"role":"user","content":"the pgbouncer prepared statement error again"}}`,
		`{"type":"assistant","sessionId":"past","timestamp":"2026-01-02T03:04:06Z","message":{"role":"assistant","content":"We decided to set default_query_exec_mode=exec for pgbouncer, because prepared statements do not survive transaction pooling."}}`,
	})
	if _, err := captureRun(t, "index"); err != nil {
		t.Fatal(err)
	}
	cwd := filepath.Join(t.TempDir(), "src", "app")
	if err := os.MkdirAll(cwd, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(cwd)

	payload := `{"hookEventName":"pre_tool_use","toolName":"spawn_subagent","toolInput":` +
		`{"prompt":"Investigate the pgbouncer prepared statement failure in the orders service.",` +
		`"subagent_type":"general-purpose","description":"pgbouncer dig"},"sessionId":"grok-parent"}`
	out := toolHookRun(t, payload)
	if out == "" {
		t.Fatal("a grok spawn whose subject the store answers carried nothing")
	}
	var resp spawnHookResponse
	if err := json.Unmarshal([]byte(out), &resp); err != nil {
		t.Fatalf("not the PreToolUse shape: %v (%q)", err, out)
	}
	// Grok validates the rewritten input against the tool's schema and blocks
	// the call outright when it does not fit, so a dropped field is worse here
	// than a missing memory.
	for _, f := range []string{"prompt", "subagent_type", "description"} {
		if _, ok := resp.HookSpecificOutput.UpdatedInput[f]; !ok {
			t.Errorf("updatedInput dropped %q", f)
		}
	}
	var prompt string
	if err := json.Unmarshal(resp.HookSpecificOutput.UpdatedInput["prompt"], &prompt); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(prompt, "Investigate the pgbouncer prepared statement failure") {
		t.Errorf("the parent's instructions were not kept intact: %q", prompt)
	}
	if !strings.Contains(prompt, "default_query_exec_mode") {
		t.Errorf("the recall did not reach the subagent's prompt: %q", prompt)
	}
}

// The same action, in grok's spelling: the tool it names and the command it
// carries have to be read out of `toolName` and `toolInput` for the hook to
// know what is about to run.
func TestGrokToolPayloadNamesTheCommand(t *testing.T) {
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
	out := toolHookRun(t, `{"hookEventName":"pre_tool_use","toolName":"run_terminal_command",`+
		`"toolInput":{"command":"go test ./... -count=1","description":"suite"},"sessionId":"grok-3"}`)
	if out == "" {
		t.Fatal("a command run in two sessions produced no line from grok's payload")
	}
	var resp sessionStartHookResponse
	if err := json.Unmarshal([]byte(out), &resp); err != nil {
		t.Fatalf("hook output is not the PreToolUse shape: %v (%q)", err, out)
	}
	if !strings.Contains(resp.HookSpecificOutput.AdditionalContext, "2 sessions") {
		t.Errorf("the line does not say how widely it ran: %q", resp.HookSpecificOutput.AdditionalContext)
	}
}

// The name grok gives the tool that spawns an agent. The wiring reaches it
// because grok maps the Claude names onto its own, but the hook still has to
// recognise what arrives.
func TestGrokSpawnToolIsRecognised(t *testing.T) {
	if !isSpawnTool("spawn_subagent") {
		t.Error("grok's spawn was not recognised as one")
	}
	for _, name := range []string{"run_terminal_command", "search_replace", "write"} {
		if isSpawnTool(name) {
			t.Errorf("%q is not a spawn", name)
		}
	}
}
