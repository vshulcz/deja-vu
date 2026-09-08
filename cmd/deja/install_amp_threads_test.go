package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// Amp keeps one process across threads. The plugin injected the session
// digest once per process, so the second thread of the day opened with no
// memory (#3263). Driven the way Amp drives it: default(amp) once, then the
// events of two threads, with a stub deja that answers hook-context.
func TestAmpPluginInjectsTheDigestOncePerThread(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the stub deja is a shell script")
	}
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is not installed")
	}
	if err := exec.Command(node, "--experimental-strip-types", "-e", "").Run(); err != nil {
		t.Skip("this node cannot strip types")
	}
	dir := t.TempDir()
	stub := filepath.Join(dir, "deja")
	if err := os.WriteFile(stub, []byte("#!/bin/sh\n"+
		// The stub drains stdin on every branch: the plugin hands hook-context a
		// JSON payload since #3264, and a child that exits without reading it
		// gives execFileSync EPIPE on the runners, which the plugin swallows
		// into "nothing" (#3283).
		`cat >/dev/null; case "$1" in hook-context) echo '{"hookSpecificOutput":{"hookEventName":"SessionStart","additionalContext":"<deja-recall>\nrecent history\n</deja-recall>"},"systemMessage":"deja recalled 1 session"}';; *) echo "";; esac`+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	plugin := filepath.Join(dir, "deja.ts")
	if err := os.WriteFile(plugin, []byte(ampPluginTS(stub)), 0o644); err != nil {
		t.Fatal(err)
	}
	drive := filepath.Join(dir, "drive.mjs")
	if err := os.WriteFile(drive, []byte(`
const mod = await import(process.argv[2]);
const handlers = {};
const amp = { on(n, f) { (handlers[n] ||= []).push(f); }, registerCommand() { return { dispose() {} }; } };
mod.default(amp);
const ctx = { ui: { notify: async () => {}, input: async () => "" } };
async function fire(name, event) {
  let out = "";
  for (const fn of handlers[name] || []) {
    const r = await fn(event, ctx);
    if (r && r.message && r.message.content) out += r.message.content;
  }
  return out;
}
for (const id of ["T-A", "T-B"]) {
  await fire("session.start", { thread: { id } });
  const first = await fire("agent.start", { thread: { id }, message: "hello", id: id + "-1" });
  const second = await fire("agent.start", { thread: { id }, message: "hello again", id: id + "-2" });
  console.log(id, first.includes("recent history") ? "digest" : "nothing", second.includes("recent history") ? "digest" : "nothing");
}
`), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(node, "--experimental-strip-types", "--no-warnings", drive, plugin)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("node: %v\n%s", err, out)
	}
	got := strings.TrimSpace(string(out))
	want := "T-A digest nothing\nT-B digest nothing"
	if got != want {
		t.Errorf("threads got:\n%s\nwant:\n%s", got, want)
	}
}
