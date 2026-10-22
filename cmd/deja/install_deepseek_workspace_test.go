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

// The generated plugins are ES modules, and Node takes a file's module type
// from the nearest package.json above it. A home directory whose own
// package.json declares CommonJS made dsh refuse both plugins ("Failed to load
// the ES module"), so the plugin directory declares its own type.
func TestInstallDeepSeekDeclaresThePluginsAsModules(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("DSH_HOME", filepath.Join(home, ".dsh"))

	if _, err := installDeepSeekAuto("/usr/local/bin/deja", false); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(home, ".dsh", "plugins", "deja")
	body, err := os.ReadFile(filepath.Join(dir, "package.json"))
	if err != nil {
		t.Fatalf("no package.json beside the plugins: %v", err)
	}
	var pkg struct {
		Type string `json:"type"`
	}
	if uerr := json.Unmarshal(body, &pkg); uerr != nil {
		t.Fatalf("package.json: %v\n%s", uerr, body)
	}
	if pkg.Type != "module" {
		t.Errorf("package.json type = %q, want module", pkg.Type)
	}
	if !mentionsDeja(body) {
		t.Errorf("package.json is not recognisable as deja's, so an uninstall would keep its snapshot:\n%s", body)
	}

	// Installing again changes nothing, and uninstall takes the marker with
	// the directory.
	res, err := installDeepSeekAuto("/usr/local/bin/deja", false)
	if err != nil {
		t.Fatal(err)
	}
	if res.Action != "unchanged" {
		t.Errorf("a second install reported %q, want unchanged", res.Action)
	}
	if _, err := installDeepSeekAuto("/usr/local/bin/deja", true); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Errorf("uninstall left the plugin directory: %v", err)
	}
}

// One dsh web process serves sessions from every workspace and never changes
// directory, so process.cwd() is wherever dsh was launched. The plugin has to
// ask deja about the workspace the session header names, and the same question
// asked in a second workspace is a new question rather than a cache hit.
//
// This loads the generated file in Node the way dsh does, below a package.json
// that declares CommonJS, with a stand-in deja that records what it was asked.
func TestDeepSeekAutoPluginAsksAboutTheSessionsWorkspace(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the stand-in deja is a shell script")
	}
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is not installed")
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("DSH_HOME", filepath.Join(home, ".dsh"))
	if werr := os.WriteFile(filepath.Join(home, "package.json"), []byte(`{"type": "commonjs"}`+"\n"), 0o644); werr != nil {
		t.Fatal(werr)
	}
	calls := filepath.Join(home, "calls.jsonl")
	fake := filepath.Join(home, "deja")
	if werr := os.WriteFile(fake, []byte("#!/bin/sh\ncat >> '"+calls+"'\necho >> '"+calls+"'\n"), 0o755); werr != nil {
		t.Fatal(werr)
	}
	if _, ierr := installDeepSeekAuto(fake, false); ierr != nil {
		t.Fatal(ierr)
	}

	const drive = `
const { default: apply } = await import(process.argv[1]);
const contexts = {};
apply({ systemPrompt: { context: (c) => { contexts[c.name] = c; } } });
const agent = (id, cwd) => ({
  sessionId: id,
  session: {
    id,
    header: { id, cwd },
    events: [{ type: "user/message", data: { source: { kind: "user" }, content: [{ type: "text", text: "where does the shard map live" }] } }],
  },
});
for (const [id, cwd] of [["s1", "/work/alpha"], ["s2", "/work/beta"]]) {
  contexts["deja:project"].text({ agent: agent(id, cwd) });
  contexts["deja:recall"].text({ agent: agent(id, cwd) });
}
`
	cmd := exec.CommandContext(t.Context(), node, "--input-type=module", "-e", drive, "file://"+dshAutoPath())
	cmd.Dir = home
	if out, cerr := cmd.CombinedOutput(); cerr != nil {
		t.Fatalf("node could not run the plugin: %v\n%s", cerr, out)
	}

	body, err := os.ReadFile(calls)
	if err != nil {
		t.Fatalf("deja was never asked: %v", err)
	}
	var got []string
	for line := range strings.SplitSeq(strings.TrimSpace(string(body)), "\n") {
		var payload struct {
			CWD string `json:"cwd"`
		}
		if err := json.Unmarshal([]byte(line), &payload); err != nil {
			t.Fatalf("payload %q: %v", line, err)
		}
		got = append(got, payload.CWD)
	}
	// The digest and the recall for each session, in that order.
	want := []string{"/work/alpha", "/work/alpha", "/work/beta", "/work/beta"}
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Errorf("deja was asked about %v, want %v", got, want)
	}
}
