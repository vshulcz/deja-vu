package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The opencode plugin stamps every session live through hook-context and
// hook-prompt, and nothing ever cleared the stamp: a session that had just
// ended stayed out of the next one's MCP recall for twenty minutes (#4546).
// opencode 1.18.33 publishes session.idle to a plugin's event hook when a turn
// is over, and awaits a plugin's dispose before `opencode run` exits, which an
// event handler it does not wait for cannot be trusted to finish.
func TestOpencodePluginEndsTheSessionsItStamped(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is needed to run the plugin opencode would run")
	}
	dir := t.TempDir()
	plugin := filepath.Join(dir, "deja.mjs")
	if err = os.WriteFile(plugin, []byte(opencodeLegacyPluginJS("/usr/local/bin/deja")), 0o644); err != nil {
		t.Fatal(err)
	}
	// Bun's $ stood in for by a tag that writes down each command line.
	driver := `
import { DejaRecall } from "./deja.mjs";
const ran = [];
const $ = (strings, ...values) => {
  ran.push(strings.reduce((acc, s, i) => acc + s + (i < values.length ? String(values[i]) : ""), ""));
  return { text: async () => "", quiet: async () => {} };
};
const client = { tui: { showToast: async () => {} } };
const hooks = await DejaRecall({ $, client, directory: "/w/p" });
await hooks["experimental.chat.system.transform"]({ sessionID: "ses_F" }, { system: [] });
await hooks["experimental.chat.messages.transform"]({ sessionID: "ses_F" },
  { messages: [{ info: { role: "user", sessionID: "ses_F" }, parts: [{ type: "text", text: "the retry loop" }] }] });
ran.length = 0;
if (typeof hooks.event !== "function") { console.log("NOEVENT"); process.exit(0) }
await hooks.event({ event: { type: "session.status", properties: { sessionID: "ses_F", status: { type: "busy" } } } });
console.log("busy:" + ran.filter((c) => c.includes("hook-session-end")).length);
await hooks.event({ event: { type: "session.idle", properties: { sessionID: "ses_F" } } });
console.log("idle:" + JSON.stringify(ran.filter((c) => c.includes("hook-session-end"))));
await hooks["experimental.chat.messages.transform"]({ sessionID: "ses_G" },
  { messages: [{ info: { role: "user", sessionID: "ses_G" }, parts: [{ type: "text", text: "the flaky test" }] }] });
ran.length = 0;
if (typeof hooks.dispose !== "function") { console.log("NODISPOSE"); process.exit(0) }
await hooks.dispose();
console.log("dispose:" + JSON.stringify(ran.filter((c) => c.includes("hook-session-end"))));
`
	run := filepath.Join(dir, "drive.mjs")
	if err = os.WriteFile(run, []byte(driver), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := exec.CommandContext(t.Context(), node, run).CombinedOutput()
	if err != nil {
		t.Fatalf("driving the plugin: %v\n%s", err, out)
	}
	got := string(out)
	if strings.Contains(got, "NOEVENT") || strings.Contains(got, "NODISPOSE") {
		t.Fatalf("the plugin has no event or dispose hook, so a session it stamped live is never ended:\n%s", got)
	}
	// A busy status is the turn starting, not ending.
	if !strings.Contains(got, "busy:0") {
		t.Errorf("a busy status ended the session:\n%s", got)
	}
	// idle ends ses_F; dispose ends only what is still live, ses_G, and does
	// not spawn deja for ses_F a second time.
	for _, c := range []struct{ phase, ends, not string }{{"idle:", "ses_F", ""}, {"dispose:", "ses_G", "ses_F"}} {
		line := ""
		for l := range strings.SplitSeq(got, "\n") {
			if strings.HasPrefix(l, c.phase) {
				line = l
			}
		}
		if !strings.Contains(line, `\"session_id\":\"`+c.ends+`\"`) || !strings.Contains(line, "/usr/local/bin/deja") {
			t.Errorf("%s did not end %s through hook-session-end: %q\n%s", c.phase, c.ends, line, got)
		}
		if c.not != "" && strings.Contains(line, c.not) {
			t.Errorf("%s ended %s again: %q", c.phase, c.not, line)
		}
	}
}
