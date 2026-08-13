package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// The plugin's message builder is the only prompt-aware channel cline has, and
// its contract is narrow enough that reading the source proves nothing: cline
// calls build more than once for one prompt, discards a mutated argument, and
// sends only the array returned from the last call. So the plugin is loaded and
// driven the way cline drives it, with a stub standing in for deja.
func TestClinePluginInjectsRecallForThePrompt(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the stub deja is a shell script")
	}
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is needed to run the plugin cline would run")
	}
	home := t.TempDir()

	// Records its calls so the double invocation is visible, and answers the
	// way the prompt hook answers when history matches.
	stub := filepath.Join(home, "deja")
	calls := filepath.Join(home, "calls")
	script := "#!/bin/sh\nin=$(cat)\nprintf '%s\\n' \"$* $in\" >> " + calls + "\n" +
		"case \"$1\" in hook-prompt) printf 'RECALLED the token rotates every 47 days' ;; esac\n"
	if err := os.WriteFile(stub, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}

	plugin := filepath.Join(home, "index.mjs")
	if err := os.WriteFile(plugin, []byte(clinePluginJS(stub)), 0o644); err != nil {
		t.Fatal(err)
	}

	// Two builds of the same prompt, as cline does: the second is the one that
	// is sent.
	driver := `
import plugin from ` + jsString(plugin) + `;
let build;
const api = {
  registerMessageBuilder: (b) => { build = b.build },
  registerRule: () => {},
  registerCommand: () => {},
};
plugin.setup(api, { session: { sessionId: "s1" } });
const messages = () => ([{ role: "user",
  content: [{ type: "text", text: '<user_input mode="act">how often does the token rotate?</user_input>' }] }]);
build(messages());
const out = build(messages());
const text = JSON.stringify(out);
console.log(text.includes("RECALLED") ? "INJECTED" : "MISSING");
console.log(text.includes("how often does the token rotate?") ? "KEPT" : "LOST");
// Printed as text: node colours a bare number.
console.log(String(out ? out.filter((m) => m.role === "user").length : 0));
`
	run := filepath.Join(home, "drive.mjs")
	if err := os.WriteFile(run, []byte(driver), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command(node, run).CombinedOutput()
	if err != nil {
		t.Fatalf("driving the plugin: %v\n%s", err, out)
	}
	lines := strings.Fields(string(out))
	if len(lines) < 3 {
		t.Fatalf("driver said %q", out)
	}
	if lines[0] != "INJECTED" {
		t.Errorf("the recall never reached the message cline sends:\n%s", out)
	}
	if lines[1] != "KEPT" {
		t.Errorf("injecting the recall dropped what the user typed:\n%s", out)
	}
	// Two user messages in a row is what providers with strict alternation
	// reject, so the recall has to ride inside the existing one.
	if lines[2] != "1" {
		t.Errorf("the recall was added as a second user message (%s of them)", lines[2])
	}

	body, err := os.ReadFile(calls)
	if err != nil {
		t.Fatalf("the plugin never ran deja: %v", err)
	}
	asked := strings.Count(string(body), "hook-prompt")
	if asked != 1 {
		t.Errorf("deja was asked %d times for one prompt; cline calls build "+
			"repeatedly and the answer has to be cached", asked)
	}
	if !strings.Contains(string(body), "how often does the token rotate?") {
		t.Errorf("the prompt was not passed to the hook:\n%s", body)
	}
	if strings.Contains(string(body), "user_input") {
		t.Errorf("cline's mode wrapper was searched for as if it were words the "+
			"user typed:\n%s", body)
	}
}

// jsString quotes a path for embedding in a module specifier.
func jsString(s string) string {
	return `"` + strings.ReplaceAll(s, `"`, `\"`) + `"`
}
