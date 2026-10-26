package main

import (
	"encoding/json"
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
	if rerr := exec.CommandContext(t.Context(), node, "--experimental-strip-types", "-e", "").Run(); rerr != nil {
		t.Skip("this node cannot strip types")
	}
	dir := t.TempDir()
	stub := filepath.Join(dir, "deja")
	if werr := os.WriteFile(stub, []byte("#!/bin/sh\n"+
		// The stub drains stdin on every branch: the plugin hands hook-context a
		// JSON payload since #3264, and a child that exits without reading it
		// gives execFileSync EPIPE on the runners, which the plugin swallows
		// into "nothing" (#3283).
		`cat >/dev/null; case "$1" in hook-context) echo '{"hookSpecificOutput":{"hookEventName":"SessionStart","additionalContext":"<deja-recall>\nrecent history\n</deja-recall>"},"systemMessage":"deja recalled 1 session"}';; *) echo "";; esac`+"\n"), 0o755); werr != nil {
		t.Fatal(werr)
	}
	plugin := filepath.Join(dir, "deja.ts")
	if werr := os.WriteFile(plugin, []byte(ampPluginTS(stub)), 0o644); werr != nil {
		t.Fatal(werr)
	}
	drive := filepath.Join(dir, "drive.mjs")
	if werr := os.WriteFile(drive, []byte(`
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
`), 0o644); werr != nil {
		t.Fatal(werr)
	}
	cmd := exec.CommandContext(t.Context(), node, "--experimental-strip-types", "--no-warnings", drive, plugin)
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

// Amp runs a plugin from the plugin's own directory, so a plugin that took
// process.cwd() for the thread's directory filed every thread, and sent every
// hook, under ~/.config/amp/plugins (#5672). The directory comes from
// amp.system.workspaceRoot, and process.cwd() is left only for a host that
// gives none.
func TestAmpPluginFilesTheThreadUnderTheWorkspaceRoot(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the stub deja is a shell script")
	}
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is not installed")
	}
	if rerr := exec.CommandContext(t.Context(), node, "--experimental-strip-types", "-e", "").Run(); rerr != nil {
		t.Skip("this node cannot strip types")
	}
	for _, tc := range []struct {
		name     string
		withRoot bool
	}{
		{"workspace root", true},
		{"no workspace root", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			t.Setenv("HOME", dir)
			t.Setenv("USERPROFILE", dir)
			threads := filepath.Join(dir, "threads")
			t.Setenv("DEJA_AMP_ROOT", threads)
			repo := filepath.Join(dir, "repo")
			pluginDir := filepath.Join(dir, "plugins")
			for _, d := range []string{repo, pluginDir} {
				if merr := os.MkdirAll(d, 0o755); merr != nil {
					t.Fatal(merr)
				}
			}
			payloads := filepath.Join(dir, "payloads.jsonl")
			stub := filepath.Join(dir, "deja")
			if werr := os.WriteFile(stub, []byte("#!/bin/sh\n"+
				`case "$1" in hook-*) cat >>"`+payloads+`"; echo >>"`+payloads+`";; *) cat >/dev/null;; esac`+"\n"), 0o755); werr != nil {
				t.Fatal(werr)
			}
			plugin := filepath.Join(pluginDir, "deja.ts")
			if werr := os.WriteFile(plugin, []byte(ampPluginTS(stub)), 0o644); werr != nil {
				t.Fatal(werr)
			}
			drive := filepath.Join(dir, "drive.mjs")
			if werr := os.WriteFile(drive, []byte(`
const [plugin, root] = process.argv.slice(2);
const mod = await import(plugin);
const handlers = {};
const amp = {
  on(n, f) { (handlers[n] ||= []).push(f); },
  registerCommand() { return { dispose() {} }; },
  helpers: { filePathFromURI: (u) => decodeURIComponent(new URL(String(u)).pathname) },
};
if (root) amp.system = { workspaceRoot: { toString: () => root } };
mod.default(amp);
const thread = { messages: async ({ offset }) => (offset ? [] : [{ id: "m1", role: "user", content: [{ type: "text", text: "hello" }] }]) };
const ctx = { thread, ui: { notify: async () => {}, input: async () => "" } };
for (const name of ["agent.start", "agent.end"]) {
  for (const fn of handlers[name] || []) await fn({ thread: { id: "T-W" }, message: "hello" }, ctx);
}
`), 0o644); werr != nil {
				t.Fatal(werr)
			}
			root := ""
			if tc.withRoot {
				root = "file://" + repo
			}
			cmd := exec.CommandContext(t.Context(), node, "--experimental-strip-types", "--no-warnings", drive, plugin, root)
			cmd.Dir = pluginDir
			if out, rerr := cmd.CombinedOutput(); rerr != nil {
				t.Fatalf("node: %v\n%s", rerr, out)
			}
			want := pluginDir
			if tc.withRoot {
				want = repo
			}
			// macOS hands node /private/var for a /var temp dir.
			same := func(got string) bool {
				a, aerr := filepath.EvalSymlinks(got)
				b, berr := filepath.EvalSymlinks(want)
				return aerr == nil && berr == nil && a == b
			}
			raw, rerr := os.ReadFile(payloads)
			if rerr != nil {
				t.Fatal(rerr)
			}
			hooks := 0
			for line := range strings.SplitSeq(strings.TrimSpace(string(raw)), "\n") {
				var p struct {
					Cwd string `json:"cwd"`
				}
				if jerr := json.Unmarshal([]byte(line), &p); jerr != nil {
					t.Fatalf("payload %q: %v", line, jerr)
				}
				hooks++
				if !same(p.Cwd) {
					t.Errorf("hook cwd = %q, want %q", p.Cwd, want)
				}
			}
			if hooks < 2 {
				t.Errorf("hooks = %d, want hook-context and hook-session-end at least", hooks)
			}
			file, rerr := os.ReadFile(filepath.Join(threads, "T-W.json"))
			if rerr != nil {
				t.Fatal(rerr)
			}
			var exported struct {
				Env struct {
					Initial struct {
						Trees []struct {
							URI string `json:"uri"`
						} `json:"trees"`
					} `json:"initial"`
				} `json:"env"`
			}
			if jerr := json.Unmarshal(file, &exported); jerr != nil {
				t.Fatal(jerr)
			}
			if trees := exported.Env.Initial.Trees; len(trees) != 1 || !same(strings.TrimPrefix(trees[0].URI, "file://")) {
				t.Errorf("thread trees = %+v, want one file:// URI for %q", trees, want)
			}
		})
	}
}
