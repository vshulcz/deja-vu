package main

import (
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/index"
)

// The recall deja puts into a spawned agent's prompt quoted the session that
// spawned it as "this was asked here before": the spawn recalls under a reader
// of its own, task:<parent>:<hash>, and the self check compared against that
// rather than against the parent, which is live and is the one asking (#4548).
func TestSpawnRecallLeavesOutTheSessionSpawningIt(t *testing.T) {
	tmp := hermeticEnv(t)
	claude := filepath.Join(tmp, "claude")
	t.Setenv("DEJA_CLAUDE_ROOT", claude)
	now := time.Now()
	const parent = "107f9711-0294-4fbd-af91-11892a43e3ff"
	seedClaudeAt(t, claude, "app", "past-session", "the pgbouncer prepared statement error again",
		"We set default_query_exec_mode=exec for pgbouncer, because prepared statements do not survive transaction pooling.", now.Add(-48*time.Hour))
	seedClaudeAt(t, claude, "app", parent, "the pgbouncer prepared statement error is back in the orders service, what did we do about pgbouncer prepared statements?",
		"Sending an agent to dig into the pgbouncer prepared statement failure.", now.Add(-time.Minute))
	dir := filepath.Join(tmp, "index.db")
	t.Setenv("DEJA_INDEX_DIR", dir)
	if err := index.Ensure(dir, "", true, io.Discard); err != nil {
		t.Fatal(err)
	}
	payload := `{"hook_event_name":"PreToolUse","tool_name":"Agent","cwd":"/tmp/app","session_id":"` + parent + `",` +
		`"tool_input":{"prompt":"Investigate the pgbouncer prepared statement error in the orders service: what did we do about pgbouncer prepared statements?"}}`
	out := toolHookRun(t, payload)
	var resp spawnHookResponse
	if err := json.Unmarshal([]byte(out), &resp); err != nil {
		t.Fatalf("no spawn recall to check: %v (%q)", err, out)
	}
	var prompt string
	if err := json.Unmarshal(resp.HookSpecificOutput.UpdatedInput["prompt"], &prompt); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(prompt, "107f9711") {
		t.Errorf("the spawned agent was handed the session spawning it as history:\n%s", prompt)
	}
	if !strings.Contains(prompt, "default_query_exec_mode") {
		t.Errorf("the session that settled it did not reach the spawned agent:\n%s", prompt)
	}
}

// opencode runs a task sub-agent as a session of its own, whose digest led with
// the session that spawned it — live, and asking (#4548). The plugin names the
// parent, and the digest leaves it out as it leaves out the asker.
func TestASubAgentsDigestLeavesOutItsParent(t *testing.T) {
	tmp := hermeticEnv(t)
	claude := filepath.Join(tmp, "claude")
	t.Setenv("DEJA_CLAUDE_ROOT", claude)
	now := time.Now()
	seedClaudeAt(t, claude, "app", "older-session", "the ledger export drops the last row", "the writer missed a final flush", now.Add(-2*time.Hour))
	seedClaudeAt(t, claude, "app", "ses_parent", "the glimmerquest cache misses on every cold start", "warming it at boot from the last snapshot fixed it", now.Add(-time.Minute))
	dir := filepath.Join(tmp, "index.db")
	t.Setenv("DEJA_INDEX_DIR", dir)
	if err := index.Ensure(dir, "", true, io.Discard); err != nil {
		t.Fatal(err)
	}
	run := func(payload string) string {
		withHookStdin(t, payload)
		return captureStdout(t, func() {
			if err := runHookContext(dir, true); err != nil {
				t.Error(err)
			}
		})
	}
	// Control: a session that is not the parent's child gets it.
	if out := run(`{"session_id":"ses_other","cwd":"/tmp/app"}`); !strings.Contains(out, "glimmerquest") {
		t.Fatalf("the control did not recall the parent's session:\n%s", out)
	}
	out := run(`{"session_id":"ses_child","parent_session_id":"ses_parent","cwd":"/tmp/app"}`)
	if strings.Contains(out, "glimmerquest") {
		t.Errorf("the sub-agent's digest led with the session that spawned it:\n%s", out)
	}
	if !strings.Contains(out, "ledger export") {
		t.Errorf("leaving the parent out took the rest of the digest with it:\n%s", out)
	}
}

// The plugin learns the parent from opencode itself: client.session.get answers
// with Session.Info, whose parentID is set on a task sub-agent's session.
func TestOpencodePluginNamesTheParentOfASubAgent(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is needed to run the plugin opencode would run")
	}
	dir := t.TempDir()
	plugin := filepath.Join(dir, "deja.mjs")
	if err = os.WriteFile(plugin, []byte(opencodeLegacyPluginJS("/usr/local/bin/deja")), 0o644); err != nil {
		t.Fatal(err)
	}
	driver := `
import { DejaRecall } from "./deja.mjs";
const ran = [];
const $ = (strings, ...values) => {
  ran.push(strings.reduce((acc, s, i) => acc + s + (i < values.length ? String(values[i]) : ""), ""));
  return { text: async () => "", quiet: async () => {} };
};
const client = {
  tui: { showToast: async () => {} },
  session: { get: async ({ path }) => ({ data: { id: path.id, parentID: path.id === "ses_child" ? "ses_parent" : undefined } }) },
};
const hooks = await DejaRecall({ $, client, directory: "/w/p" });
await hooks["experimental.chat.system.transform"]({ sessionID: "ses_child" }, { system: [] });
await hooks["experimental.chat.messages.transform"]({ sessionID: "ses_child" },
  { messages: [{ info: { role: "user", sessionID: "ses_child" }, parts: [{ type: "text", text: "find the retry fix" }] }] });
for (const c of ran) console.log(c.includes("hook-context") ? "context:" + c : c.includes("hook-prompt") ? "prompt:" + c : "other:" + c);
`
	run := filepath.Join(dir, "drive.mjs")
	if err = os.WriteFile(run, []byte(driver), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := exec.CommandContext(t.Context(), node, run).CombinedOutput()
	if err != nil {
		t.Fatalf("driving the plugin: %v\n%s", err, out)
	}
	for _, kind := range []string{"context:", "prompt:"} {
		found := false
		for l := range strings.SplitSeq(string(out), "\n") {
			if strings.HasPrefix(l, kind) && strings.Contains(l, `"parent_session_id":"ses_parent"`) {
				found = true
			}
		}
		if !found {
			t.Errorf("the %s hook was not told the sub-agent's parent:\n%s", strings.TrimSuffix(kind, ":"), out)
		}
	}
}
