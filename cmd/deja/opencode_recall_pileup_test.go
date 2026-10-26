package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// A user message carried the per-prompt block six times over a multi-step
// turn, alternating between two pointers (#5682). The plugins appended on
// every model call and only checked the text did not already end with their
// own block. Driven here the way that happens: a host that keeps the message
// between calls, a message with no id, two copies of the plugin, and a
// message that already carries a pile. The stub answers a different pointer
// on every call, as deja does once it has shown the first one.
func TestOpencodePromptRecallDoesNotPileUp(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the stub deja is a shell script")
	}
	for _, shape := range []string{"1.x", "2.x"} {
		t.Run(shape, func(t *testing.T) {
			dir := t.TempDir()
			bin, calls := filepath.Join(dir, "deja"), filepath.Join(dir, "calls")
			script := "#!/bin/sh\nin=$(cat)\nprintf '%s %s\\n' \"$1\" \"$in\" >> " + calls + "\n" +
				"if [ \"$1\" = hook-prompt ]; then n=$(grep -c '^hook-prompt' " + calls + "); " +
				"if [ $((n % 2)) = 1 ]; then p=POINTER-A; else p=POINTER-B; fi; " +
				"printf '%s\\n' '{\"hookSpecificOutput\":{\"additionalContext\":\"<deja-recall>\\nRecalled history.\\n'$p'\\n</deja-recall>\"}}'; fi\n"
			if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
				t.Fatal(err)
			}
			plugin := filepath.Join(dir, "deja.mjs")
			src, driver := opencodeLegacyPluginJS(bin), `
import { execSync } from "node:child_process";
import { DejaRecall } from "`+plugin+`";
const q = (v) => "'" + String(v).replace(/'/g, "'\\''") + "'";
const $ = (strings, ...values) => {
  const line = strings.reduce((acc, s, i) => acc + s + (i < values.length ? q(values[i]) : ""), "");
  const run = () => { try { return execSync(line, { shell: "/bin/sh" }).toString() } catch { return "" } };
  return { text: async () => run(), quiet: async () => { run() } };
};
const make = async () => {
  const h = await DejaRecall({ $, client: { tui: { showToast: async () => {} } }, directory: "`+dir+`" });
  return (msgs) => h["experimental.chat.messages.transform"]({ sessionID: "ses_P" }, { messages: msgs });
};
const user = (id, text) => ({ info: { ...(id ? { id } : {}), role: "user", sessionID: "ses_P" }, parts: [{ type: "text", text }] });
const text = (m) => m.parts[0].text;
`
			if shape == "2.x" {
				src, driver = opencodePluginJS(bin), `
import plugin from "`+plugin+`";
const make = async () => {
  const hooks = { session: {}, tool: {} };
  const domain = (name) => ({ hook: async (event, fn) => { (hooks[name][event] ||= []).push(fn) } });
  await plugin.setup({ location: { directory: "`+dir+`" }, session: domain("session"), tool: domain("tool") });
  return (msgs) => hooks.session.context[1]({ sessionID: "ses_P", system: [], messages: msgs });
};
const user = (id, text) => ({ ...(id ? { id } : {}), role: "user", content: [{ type: "text", text }] });
const text = (m) => m.content[0].text;
`
			}
			driver += `
const out = {};
// One copy, a message with no id, kept by the host across six calls.
{
  const call = await make();
  const msgs = [user("", "why does the uploader retry so fast?")];
  for (let i = 0; i < 6; i++) await call(msgs);
  out.kept = text(msgs[0]);
}
// Two copies of the plugin on the same kept message.
{
  const a = await make(), b = await make();
  const msgs = [user("m7", "what did we settle on the importer?")];
  for (let i = 0; i < 3; i++) { await a(msgs); await b(msgs) }
  out.twice = text(msgs[0]);
}
// A message that already carries a pile from before.
{
  const call = await make();
  let pile = "how is the cache keyed?";
  for (let i = 0; i < 6; i++) pile += "\n\n<deja-recall>\nRecalled history.\nPOINTER-" + (i % 2 ? "B" : "A") + "\n</deja-recall>";
  const msgs = [user("m9", pile)];
  await call(msgs);
  out.pile = text(msgs[0]);
}
console.log(JSON.stringify(out));
`
			if err := os.WriteFile(plugin, []byte(src), 0o644); err != nil {
				t.Fatal(err)
			}
			var got map[string]string
			if err := json.Unmarshal([]byte(runNode(t, dir, driver)), &got); err != nil {
				t.Fatal(err)
			}
			for _, k := range []string{"kept", "twice", "pile"} {
				if n := strings.Count(got[k], "<deja-recall>"); n != 1 {
					t.Errorf("%s: the message carries %d recall blocks, want one:\n%s", k, n, got[k])
				}
			}
			if !strings.HasPrefix(got["pile"], "how is the cache keyed?\n\n<deja-recall>") || !strings.Contains(got["pile"], "POINTER-A") {
				t.Errorf("the pile did not collapse to its first block:\n%s", got["pile"])
			}
			// One ask per question: the kept one and the twice-loaded one. The
			// pile is answered by what it already carries.
			if n := len(stubCalls(calls, "hook-prompt")); n != 2 {
				t.Errorf("hook-prompt ran %d times for two new questions", n)
			}
		})
	}
}
