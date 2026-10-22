package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// dsh runs a `tools/post-execute` waterfall on every tool result, and an
// accept decision's additionalContexts reach the model on the next step. The
// plugin puts hook-tool's line there after a read, edit or write, and
// hook-tool-after's after a bash that exited non-zero, the way pi's extension
// does on tool_result (#4293).
func TestDeepSeekAutoNotesTheFileAndTheFailedCommand(t *testing.T) {
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
	script := "#!/bin/sh\nin=$(cat)\nprintf '%s %s %s\\n' \"$1\" \"$2\" \"$in\" >> " + calls + "\n" +
		"case \"$1\" in hook-tool) printf 'NOTE about the file' ;; hook-tool-after) printf 'FIX seen before' ;; esac\n"
	if werr := os.WriteFile(stub, []byte(script), 0o755); werr != nil {
		t.Fatal(werr)
	}
	plugin := filepath.Join(home, "auto.js")
	if werr := os.WriteFile(plugin, []byte(dshAutoJS(stub)), 0o644); werr != nil {
		t.Fatal(werr)
	}
	driver := `
import plugin from "` + plugin + `";
const listeners = {};
plugin({ systemPrompt: { context: () => {} }, on: (name, fn) => { listeners[name] = fn } });
const post = listeners["tools/post-execute"];
if (!post) { console.log("NOLISTENER"); process.exit(0) }
const agent = { sessionId: "sess-A", session: { header: { cwd: "/w/proj" }, events: [] } };
const accept = async () => ({ kind: "accept" });
const text = (t) => ({ content: [{ type: "text", text: t }] });
const show = (d) => console.log(JSON.stringify((d.additionalContexts || []).map((m) => [m.role, m.source && m.source.kind, m.content.map((c) => c.text).join("")])));
show(await post({ name: "read", arguments: { file_path: "/w/proj/retry.go" }, agent }, text("1\tpackage retry"), accept));
show(await post({ name: "bash", arguments: { command: "go test ./..." }, agent }, text("FAIL retry\n[exit code: 1]"), accept));
show(await post({ name: "bash", arguments: { command: "ls" }, agent }, text("retry.go"), accept));
show(await post({ name: "edit", arguments: { file_path: "/w/proj/retry.go" }, agent }, text("ok"), async () => ({ kind: "block", feedback: [] })));
show(await post({ name: "bash", arguments: { command: "cat build.log" }, agent }, text("old run:\n[exit code: 1]\nall green now"), accept));
show(await post({ name: "bash", arguments: { command: "make serve" }, agent }, text("listening\n[killed by signal: SIGKILL]"), accept));
show(await post({ name: "pwsh", arguments: { command: "dotnet build" }, agent }, text("error CS1002\n[exit code: 1]"), accept));
show(await post({ name: "str_replace_editor", arguments: { command: "str_replace", path: "/w/proj/retry.go" }, agent }, text("edited"), accept));
`
	run := filepath.Join(home, "drive.mjs")
	if werr := os.WriteFile(run, []byte(driver), 0o644); werr != nil {
		t.Fatal(werr)
	}
	out, err := exec.CommandContext(t.Context(), node, run).CombinedOutput()
	if err != nil {
		t.Fatalf("driving the plugin: %v\n%s", err, out)
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	if lines[0] == "NOLISTENER" {
		t.Fatal("auto.js registers no tools/post-execute listener, so dsh hears nothing at the point of action")
	}
	if len(lines) != 8 {
		t.Fatalf("driver said %q", out)
	}
	if lines[0] != `[["user","plugin:deja","NOTE about the file"]]` {
		t.Errorf("a read did not carry the file's note: %s", lines[0])
	}
	if lines[1] != `[["user","plugin:deja","FIX seen before"]]` {
		t.Errorf("a failed bash did not carry the earlier fix: %s", lines[1])
	}
	if lines[2] != `[]` {
		t.Errorf("a bash that exited 0 was answered: %s", lines[2])
	}
	if lines[3] != `[]` {
		t.Errorf("a blocked call got a note: %s", lines[3])
	}
	// The marker is dsh's only when it ends the result, as dsh's own
	// parser reads it (dsh-shell parseExitStatus): a log that quotes one
	// mid-text passed.
	if lines[4] != `[]` {
		t.Errorf("a command that exited 0 with a marker in its output was answered: %s", lines[4])
	}
	if lines[5] != `[["user","plugin:deja","FIX seen before"]]` {
		t.Errorf("a command killed by a signal did not carry the earlier fix: %s", lines[5])
	}
	// dsh's other shell and its other editor (#4293).
	if lines[6] != `[["user","plugin:deja","FIX seen before"]]` {
		t.Errorf("a failed pwsh command did not carry the earlier fix: %s", lines[6])
	}
	if lines[7] != `[["user","plugin:deja","NOTE about the file"]]` {
		t.Errorf("a str_replace_editor edit did not carry the file's note: %s", lines[7])
	}
	asked, err := os.ReadFile(calls)
	if err != nil {
		t.Fatal(err)
	}
	got := string(asked)
	for _, want := range []string{
		`hook-tool --plain {"tool_name":"read","tool_input":{"file_path":"/w/proj/retry.go"},"session_id":"sess-A","cwd":"/w/proj"}`,
		`hook-tool-after --plain {"tool_name":"bash","tool_input":{"command":"go test ./..."},"tool_response":"FAIL retry","session_id":"sess-A","cwd":"/w/proj"}`,
		`hook-tool-after --plain {"tool_name":"bash","tool_input":{"command":"make serve"},"tool_response":"listening","session_id":"sess-A","cwd":"/w/proj"}`,
		`hook-tool-after --plain {"tool_name":"powershell","tool_input":{"command":"dotnet build"},"tool_response":"error CS1002","session_id":"sess-A","cwd":"/w/proj"}`,
		`hook-tool --plain {"tool_name":"edit","tool_input":{"file_path":"/w/proj/retry.go"},"session_id":"sess-A","cwd":"/w/proj"}`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("deja was not asked %s\ncalls:\n%s", want, got)
		}
	}
	if strings.Count(got, "hook-tool") != 5 {
		t.Errorf("deja was asked for a call that needs nothing:\n%s", got)
	}
}

// The note is asked for while dsh goes on: a synchronous spawn held the whole
// event loop, the web profile's other sessions included, for as long as deja
// took, up to its ten-second timeout.
func TestDeepSeekAutoToolNoteDoesNotBlockTheHost(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the stub deja is a shell script")
	}
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is needed to run the plugin dsh would run")
	}
	home := t.TempDir()
	stub := filepath.Join(home, "deja")
	if werr := os.WriteFile(stub, []byte("#!/bin/sh\ncat >/dev/null\nsleep 0.5\nprintf 'NOTE'\n"), 0o755); werr != nil {
		t.Fatal(werr)
	}
	plugin := filepath.Join(home, "auto.js")
	if werr := os.WriteFile(plugin, []byte(dshAutoJS(stub)), 0o644); werr != nil {
		t.Fatal(werr)
	}
	driver := `
import plugin from "` + plugin + `";
const listeners = {};
plugin({ systemPrompt: { context: () => {} }, on: (name, fn) => { listeners[name] = fn } });
let ticks = 0;
const timer = setInterval(() => ticks++, 10);
const d = await listeners["tools/post-execute"]({ name: "read", arguments: { file_path: "/w/a.go" }, agent: {} }, { content: [] }, async () => ({ kind: "accept" }));
clearInterval(timer);
console.log(ticks + " " + (d.additionalContexts || []).length);
`
	run := filepath.Join(home, "drive.mjs")
	if werr := os.WriteFile(run, []byte(driver), 0o644); werr != nil {
		t.Fatal(werr)
	}
	out, err := exec.CommandContext(t.Context(), node, run).CombinedOutput()
	if err != nil {
		t.Fatalf("driving the plugin: %v\n%s", err, out)
	}
	var ticks, notes int
	if _, err := fmt.Sscan(string(out), &ticks, &notes); err != nil {
		t.Fatalf("driver said %q", out)
	}
	if notes != 1 {
		t.Errorf("the note did not arrive: %q", out)
	}
	if ticks < 10 {
		t.Errorf("the host's timers ran %d times in half a second: the call blocks the event loop", ticks)
	}
}
