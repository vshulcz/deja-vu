package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// opencodeStubDeja is a deja that writes down each subcommand and its stdin.
func opencodeStubDeja(t *testing.T, dir string) (bin, calls string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the stub deja is a shell script")
	}
	bin, calls = filepath.Join(dir, "deja"), filepath.Join(dir, "calls")
	script := "#!/bin/sh\nprintf '%s %s\\n' \"$1\" \"$(cat)\" >> " + calls + "\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return bin, calls
}

func runNode(t *testing.T, dir, driver string) string {
	t.Helper()
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is needed to run the plugin opencode would run")
	}
	run := filepath.Join(dir, "drive.mjs")
	if err = os.WriteFile(run, []byte(driver), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := exec.CommandContext(t.Context(), node, run).CombinedOutput()
	if err != nil {
		t.Fatalf("driving the plugin: %v\n%s", err, out)
	}
	return string(out)
}

func stubCalls(calls, sub string) []string {
	b, _ := os.ReadFile(calls)
	var out []string
	for l := range strings.SplitSeq(string(b), "\n") {
		if strings.HasPrefix(l, sub+" ") {
			out = append(out, l)
		}
	}
	return out
}

// The 2.x plugin registered context, compaction and tool hooks and nothing
// else, so the #4546 session end never ran there (#4571). 2.x hands a plugin
// its events through ctx.event.subscribe and runs the cleanup setup returns;
// opencode 2.0.22 ends a turn with session.execution.succeeded, .failed or
// .interrupted, and `opencode run` awaits the cleanup before it exits.
func TestOpencode2PluginEndsTheSessionsItStamped(t *testing.T) {
	dir := t.TempDir()
	bin, calls := opencodeStubDeja(t, dir)
	plugin := filepath.Join(dir, "deja.mjs")
	if err := os.WriteFile(plugin, []byte(opencodePluginJS(bin)), 0o644); err != nil {
		t.Fatal(err)
	}
	out := runNode(t, dir, `
import plugin from "`+plugin+`";
const hooks = { session: {}, tool: {} };
const domain = (name) => ({ hook: async (event, fn) => { (hooks[name][event] ||= []).push(fn) } });
let give = null, open = false;
const subscribe = (options) => ({
  async *[Symbol.asyncIterator]() {
    open = true;
    try {
      while (!options?.signal?.aborted) {
        const next = await new Promise((resolve) => { give = resolve; options?.signal?.addEventListener("abort", () => resolve(null), { once: true }) });
        if (!next) return;
        yield next;
      }
    } finally { open = false }
  },
});
const push = async (e) => { for (let i = 0; !give && i < 200; i++) await new Promise((r) => setTimeout(r, 5)); if (!give) return; const g = give; give = null; g(e); await new Promise((r) => setTimeout(r, 300)) };
const ctx = { location: { directory: "`+dir+`" }, session: domain("session"), tool: domain("tool"), event: { subscribe } };
const cleanup = await plugin.setup(ctx);
for (const fn of hooks.session.context) {
  await fn({ sessionID: "ses_F", system: [], messages: [{ role: "user", content: [{ type: "text", text: "the retry loop" }] }] });
}
await push({ type: "session.execution.started", data: { sessionID: "ses_F" } });
console.log("started");
await push({ type: "session.execution.succeeded", data: { sessionID: "ses_F" } });
console.log("succeeded");
console.log("cleanup:" + typeof cleanup);
if (typeof cleanup === "function") await cleanup();
console.log("open:" + open);
`)
	ended := stubCalls(calls, "hook-session-end")
	if !strings.Contains(out, "cleanup:function") {
		t.Errorf("setup returned no cleanup, so `opencode run` exits with the session stamped:\n%s", out)
	}
	if len(ended) == 0 || !strings.Contains(ended[0], `"session_id":"ses_F"`) {
		t.Fatalf("the end of the turn did not end ses_F through hook-session-end: %q\n%s", ended, out)
	}
	if strings.Contains(out, "open:true") {
		t.Errorf("the event subscription outlived the plugin:\n%s", out)
	}
}

// The plugins end a session at the end of its turn and count on the next
// prompt's hook-prompt to stamp it live again. A turn that is only an image
// returned before that call, so its MCP recall could hand the session back to
// itself (#4573).
func TestOpencodePluginsStampAnImageOnlyTurn(t *testing.T) {
	dir := t.TempDir()
	bin, calls := opencodeStubDeja(t, dir)
	v1 := filepath.Join(dir, "deja1.mjs")
	if err := os.WriteFile(v1, []byte(opencodeLegacyPluginJS(bin)), 0o644); err != nil {
		t.Fatal(err)
	}
	v2 := filepath.Join(dir, "deja2.mjs")
	if err := os.WriteFile(v2, []byte(opencodePluginJS(bin)), 0o644); err != nil {
		t.Fatal(err)
	}
	runNode(t, dir, `
import { execSync } from "node:child_process";
import { DejaRecall } from "`+v1+`";
import plugin from "`+v2+`";
// Bun's $ stood in for by a tag that runs the line through sh, each value
// one quoted word as Bun passes it.
const q = (v) => "'" + String(v).replace(/'/g, "'\\''") + "'";
const $ = (strings, ...values) => {
  const line = strings.reduce((acc, s, i) => acc + s + (i < values.length ? q(values[i]) : ""), "");
  const run = () => { try { return execSync(line, { shell: "/bin/sh" }).toString() } catch { return "" } };
  return { text: async () => run(), quiet: async () => { run() } };
};
const image = { type: "file", mime: "image/png", url: "data:image/png;base64,AAAA" };
const h1 = await DejaRecall({ $, client: { tui: { showToast: async () => {} } }, directory: "`+dir+`" });
await h1["experimental.chat.messages.transform"]({ sessionID: "ses_v1" }, { messages: [{ info: { role: "user", sessionID: "ses_v1" }, parts: [image] }] });
const hooks = { session: {}, tool: {} };
const domain = (name) => ({ hook: async (event, fn) => { (hooks[name][event] ||= []).push(fn) } });
await plugin.setup({ location: { directory: "`+dir+`" }, session: domain("session"), tool: domain("tool") });
await hooks.session.context[1]({ sessionID: "ses_v2", system: [], messages: [{ role: "user", content: [image] }] });
`)
	got := strings.Join(stubCalls(calls, "hook-prompt"), "\n")
	for _, id := range []string{"ses_v1", "ses_v2"} {
		if !strings.Contains(got, `"session_id":"`+id+`"`) {
			t.Errorf("an image-only turn in %s never reached hook-prompt, so it was not stamped live:\n%s", id, got)
		}
	}
}
