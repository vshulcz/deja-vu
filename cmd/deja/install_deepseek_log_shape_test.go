package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// dsh 0.2.0 made a session's event array private: the log is read one event at
// a time with eventAt(seq), and session.events is undefined. auto.js read only
// session.events, found no prompt, and never asked deja on any turn — the
// per-prompt recall was off for every 0.2.0 profile with nothing reported.
func TestDeepSeekAutoFindsThePromptInTheDsh020Log(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the stub deja is a shell script")
	}
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is needed to run the plugin dsh would run")
	}
	home := t.TempDir()
	stub := filepath.Join(home, "deja")
	calls := filepath.Join(home, "calls")
	script := "#!/bin/sh\nin=$(cat)\nprintf '%s %s\\n' \"$1\" \"$in\" >> " + calls + "\n" +
		"case \"$1\" in hook-prompt) printf 'RECALLED' ;; esac\n"
	if err := os.WriteFile(stub, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	plugin := filepath.Join(home, "auto.js")
	if err := os.WriteFile(plugin, []byte(dshAutoJS(stub)), 0o644); err != nil {
		t.Fatal(err)
	}
	driver := `
import plugin from "` + plugin + `";
const contexts = {};
plugin({ systemPrompt: { context: (c) => { contexts[c.name] = c.text } }, on: () => {} });
// The 0.2.0 shape: no events array, eventAt over a private log.
const log = [
  { type: "agent/inbox/spliced", data: { inserted: [{ role: "user", source: { kind: "user" }, content: [{ type: "text", text: "how do we drain the zorbit queue" }] }] } },
  { type: "turn/start", data: { turn: 1 } },
  { type: "agent/inbox/spliced", data: { removedCount: 1, inserted: [] } },
];
const session = { id: "session-s1", header: { cwd: "/w/proj" }, get seq() { return log.length }, eventAt: (i) => log[i] };
console.log(contexts["deja:recall"]({ agent: { sessionId: "session-s1", session } }) || "SILENT");
`
	run := filepath.Join(home, "drive.mjs")
	if err := os.WriteFile(run, []byte(driver), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command(node, run).CombinedOutput()
	if err != nil {
		t.Fatalf("driving the plugin: %v\n%s", err, out)
	}
	if strings.TrimSpace(string(out)) != "RECALLED" {
		t.Errorf("the recall never reached the model on a 0.2.0 session: %s", out)
	}
	asked, _ := os.ReadFile(calls)
	if !strings.Contains(string(asked), `"prompt":"how do we drain the zorbit queue"`) {
		t.Errorf("deja was not asked about the prompt:\n%s", asked)
	}
}
