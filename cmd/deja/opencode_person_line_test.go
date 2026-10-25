package main

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// The per-prompt line never reached a person in opencode: the plugins kept the
// model's half of hook-prompt's reply and dropped systemMessage, while deja
// counted the line shown and held the next one back for twenty minutes. 1.x
// has a toast and shows it there; a 2.x server plugin has none and asks deja
// --strict, so the line is not spent. In both, a task sub-agent's turn, which
// the parent wrote, is not searched again.
func TestOpencodePluginsShowOrKeepThePersonsLine(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the stub deja is a shell script")
	}
	const line = `deja-vu — from Codex CLI, 3 weeks ago: \"drain with --grace 40\" (via zorbit)`
	for _, shape := range []string{"1.x", "2.x"} {
		t.Run(shape, func(t *testing.T) {
			dir := t.TempDir()
			bin, calls, reply := filepath.Join(dir, "deja"), filepath.Join(dir, "calls"), filepath.Join(dir, "reply")
			if err := os.WriteFile(reply, []byte(`{"systemMessage":"`+line+`","hookSpecificOutput":{"additionalContext":"RECALL-ZORBIT"}}`+"\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			script := "#!/bin/sh\nin=$(cat)\nprintf '%s\\t%s\\n' \"$*\" \"$in\" >> " + calls + "\n" +
				"case \"$1:$in\" in hook-prompt:*zorbit*) cat " + reply + " ;; esac\n"
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
const toasts = [];
const client = {
  tui: { showToast: async ({ body }) => { toasts.push(body.message) } },
  session: { get: async ({ path }) => ({ data: { parentID: path.id === "ses_C" ? "ses_P" : undefined } }) },
};
const h = await DejaRecall({ $, client, directory: "`+dir+`" });
const turn = (sid, text) => [{ info: { id: sid + "1", role: "user", sessionID: sid }, parts: [{ type: "text", text }] }];
const main = turn("ses_P", "how should the zorbit queue drain?");
await h["experimental.chat.messages.transform"]({ sessionID: "ses_P" }, { messages: main });
const child = turn("ses_C", "find how the zorbit queue drains");
await h["experimental.chat.messages.transform"]({ sessionID: "ses_C" }, { messages: child });
console.log(JSON.stringify({ toasts, main: main[0].parts[0].text, child: child[0].parts[0].text }));
`
			if shape == "2.x" {
				src, driver = opencodePluginJS(bin), `
import plugin from "`+plugin+`";
const hooks = { session: {}, tool: {} };
const domain = (name) => ({ hook: async (event, fn) => { (hooks[name][event] ||= []).push(fn) } });
const session = { ...domain("session"), get: async ({ sessionID }) => ({ parentID: sessionID === "ses_C" ? "ses_P" : undefined }) };
await plugin.setup({ location: { directory: "`+dir+`" }, session, tool: domain("tool") });
const turn = (sid, text) => [{ id: sid + "1", role: "user", content: [{ type: "text", text }] }];
const main = turn("ses_P", "how should the zorbit queue drain?");
await hooks.session.context[1]({ sessionID: "ses_P", system: [], messages: main });
const child = turn("ses_C", "find how the zorbit queue drains");
await hooks.session.context[1]({ sessionID: "ses_C", system: [], messages: child });
console.log(JSON.stringify({ toasts: [], main: main[0].content[0].text, child: child[0].content[0].text }));
`
			}
			if err := os.WriteFile(plugin, []byte(src), 0o644); err != nil {
				t.Fatal(err)
			}
			out := runNode(t, dir, driver)
			if !strings.Contains(out, `"main":"how should the zorbit queue drain?\n\nRECALL-ZORBIT"`) {
				t.Errorf("the session a person types in lost its recall:\n%s", out)
			}
			if !strings.Contains(out, `"child":"find how the zorbit queue drains"`) {
				t.Errorf("a sub-agent's turn was searched again:\n%s", out)
			}
			b, err := os.ReadFile(calls)
			if err != nil {
				t.Fatal(err)
			}
			log := string(b)
			if !strings.Contains(log, `"prompt":"","session_id":"ses_C","parent_session_id":"ses_P"`) {
				t.Errorf("the sub-agent was not stamped live with an empty prompt:\n%s", log)
			}
			switch shape {
			case "1.x":
				if !strings.Contains(out, `"toasts":["deja-vu — from Codex CLI`) || strings.Count(out, "deja-vu —") != 1 {
					t.Errorf("the person's line was not toasted exactly once:\n%s", out)
				}
			case "2.x":
				if !strings.Contains(log, "hook-prompt --strict\t") {
					t.Errorf("hook-prompt was not told the line has no reader:\n%s", log)
				}
			}
		})
	}
}
