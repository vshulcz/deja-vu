package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// cline's PreToolUse hook cannot carry context — it keeps only cancel and
// overrideInput — so the repair for a command that just failed had no channel.
// The message builder is the one that works: it is handed the messages on their
// way to the model, including the failed command's tool_result, and its return
// is what gets sent (verified on CLI 3.0.57 by reading the wire request). So the
// repair rides beside the failing result in the same turn.
func TestClineFixPairSource(t *testing.T) {
	js := clinePluginJS("/bin/deja")
	for _, want := range []string{
		`"hook-tool-after", "--plain"`,
		"commandFailure",
		"appendRepair",
		"run_commands",
	} {
		if !strings.Contains(js, want) {
			t.Errorf("the fix-pair path is missing %q:\n%s", want, js)
		}
	}
}

// The builder appends the repair to the failing command's tool_result and
// leaves a result that did not fail alone. Driven the way cline drives it.
func TestClineFixPairAppendsToTheFailingResult(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the stub deja is a shell script")
	}
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is needed to run the plugin cline would run")
	}
	home := t.TempDir()
	stub := filepath.Join(home, "deja")
	// Answers only hook-tool-after, and only for the seeded error, so the
	// no-failure case is checked on the same stub.
	script := "#!/bin/sh\nin=$(cat)\ncase \"$1\" in\n" +
		"hook-tool-after) case \"$in\" in *glimwrax*) printf 'RAN NEXT: glimwraxctl --sync' ;; esac ;;\n" +
		"esac\n"
	if err := os.WriteFile(stub, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	plugin := filepath.Join(home, "index.mjs")
	if err := os.WriteFile(plugin, []byte(clinePluginJS(stub)), 0o644); err != nil {
		t.Fatal(err)
	}
	driver := `
import plugin from "` + plugin + `";
let build;
const api = {
  registerMessageBuilder: (b) => { build = b.build; },
  registerRule: () => {}, registerCommand: () => {},
};
plugin.setup(api, { session: { sessionId: "s1" } });

// A turn whose last message is a failed run_commands result, and one that
// succeeded, in the same array.
const messages = [
  { role: "user", content: [{ type: "text", text: "build the project" }] },
  { role: "assistant", content: [{ type: "tool_use", id: "call_ok", name: "run_commands", input: { commands: ["ls"] } }] },
  { role: "user", content: [{ type: "tool_result", tool_use_id: "call_ok", name: "run_commands",
      content: [{ query: "ls", result: "main.go", success: true }] }] },
  { role: "assistant", content: [{ type: "tool_use", id: "call_1", name: "run_commands", input: { commands: ["go build ./..."] } }] },
  { role: "user", content: [{ type: "tool_result", tool_use_id: "call_1", name: "run_commands",
      content: [{ query: "go build ./...", result: "./main.go:9:2: undefined: glimwraxHelper", error: "exit 1", success: false }] }] },
];
const out = build(messages);
if (!out) { console.log("NOEDIT"); process.exit(0); }
const failing = out.find((m) => Array.isArray(m.content) && m.content.some((p) => p.type === "tool_result" && p.tool_use_id === "call_1"));
const ok = out.find((m) => Array.isArray(m.content) && m.content.some((p) => p.type === "tool_result" && p.tool_use_id === "call_ok"));
const failText = JSON.stringify(failing);
const okText = JSON.stringify(ok);
console.log("FAIL_HAS_REPAIR:" + failText.includes("RAN NEXT: glimwraxctl --sync"));
console.log("OK_UNTOUCHED:" + !okText.includes("RAN NEXT"));
// Original error still present, not replaced.
console.log("ERROR_KEPT:" + failText.includes("undefined: glimwraxHelper"));
`
	run := filepath.Join(home, "drive.mjs")
	if err := os.WriteFile(run, []byte(driver), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command(node, run).CombinedOutput()
	if err != nil {
		t.Fatalf("driving the plugin: %v\n%s", err, out)
	}
	s := string(out)
	if strings.Contains(s, "NOEDIT") {
		t.Fatal("the builder returned no edit, so the repair never reached the failing result")
	}
	for _, want := range []string{"FAIL_HAS_REPAIR:true", "OK_UNTOUCHED:true", "ERROR_KEPT:true"} {
		if !strings.Contains(s, want) {
			t.Errorf("expected %q in driver output:\n%s", want, s)
		}
	}
}
