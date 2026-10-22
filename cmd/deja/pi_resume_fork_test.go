package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// pi stores the recall a prompt was given in the session and sends it to the
// model again on a resume, and a fork copies its source's. Both pi
// extensions asked for a digest once per process, so a resumed session got a
// second copy, and a fork a third that led with its source. Driven the way pi
// drives them: default(pi), then before_agent_start with a session manager
// whose branch and header are a resume's and a fork's, against a stub deja
// that logs what it was asked.
func TestPiResumeAndForkGetNoSecondDigest(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the stub deja is a shell script")
	}
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is not installed")
	}
	if err = exec.CommandContext(t.Context(), node, "--experimental-strip-types", "-e", "").Run(); err != nil {
		t.Skip("this node cannot strip types")
	}
	pkg, err := os.ReadFile(filepath.Join("..", "..", "extensions", "pi", "index.ts"))
	if err != nil {
		t.Fatal(err)
	}
	lib, err := os.ReadFile(filepath.Join("..", "..", "extensions", "pi", "lib.mjs"))
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	log := filepath.Join(dir, "calls.log")
	stub := filepath.Join(dir, "deja")
	if err := os.WriteFile(stub, []byte("#!/bin/sh\n"+
		`in=$(cat); printf '%s %s\n' "$1" "$in" >> '`+log+`'; case "$1" in hook-context) echo '{"hookSpecificOutput":{"additionalContext":"<deja-recall>digest</deja-recall>"}}';; hook-prompt) echo '{}';; esac`+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	// The source's file, named the way pi names it, with its id on the header.
	src := filepath.Join(dir, "2026-10-17T22-49-43-673Z_src-1.jsonl")
	if err := os.WriteFile(src, []byte(`{"type":"session","version":3,"id":"src-1","cwd":"/w"}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gen := filepath.Join(dir, "gen", "deja.ts")
	npm := filepath.Join(dir, "npm", "index.ts")
	for path, body := range map[string]string{
		gen:                                  piExtensionTS(stub),
		npm:                                  string(pkg),
		filepath.Join(dir, "npm", "lib.mjs"): string(lib),
	} {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	drive := filepath.Join(dir, "drive.mjs")
	if err := os.WriteFile(drive, []byte(`
const mod = await import(process.argv[2]);
const handlers = {};
const pi = { on(n, f) { (handlers[n] ||= []).push(f); }, registerCommand() {} };
mod.default(pi);
const ui = { notify() {}, setStatus() {} };
const held = [{ type: "message" }, { type: "custom_message", customType: "deja-recall", content: "old" }, { type: "message" }];
const sm = (id, branch, header) => ({ getSessionId: () => id, getSessionFile: () => "", getBranch: () => branch, getHeader: () => header });
async function prompt(m) {
  let out = "";
  for (const fn of handlers.before_agent_start || []) {
    const r = await fn({ prompt: "p" }, { ui, sessionManager: m });
    if (r && r.message) out += r.message.content;
  }
  return out;
}
const tag = (s) => (s.includes("digest") ? "digest" : "none");
console.log("new", tag(await prompt(sm("new-1", [], { id: "new-1" }))));
console.log("resume", tag(await prompt(sm("res-1", held, { id: "res-1" }))));
console.log("fork", tag(await prompt(sm("fork-1", held, { id: "fork-1", parentSession: process.argv[3] }))));
console.log("compacted", tag(await prompt(sm("cmp-1", held.concat([{ type: "compaction" }]), { id: "cmp-1" }))));
// pi sends the entries a compaction kept, firstKeptEntryId on.
const kept = [{ type: "message", id: "e1" }, { type: "custom_message", customType: "deja-recall", id: "e2" }, { type: "compaction", firstKeptEntryId: "e1" }];
console.log("kept", tag(await prompt(sm("kept-1", kept, { id: "kept-1" }))));
// A fork at the first message copies nothing: its source is no part of it.
console.log("empty fork", tag(await prompt(sm("efork-1", [], { id: "efork-1", parentSession: process.argv[3] }))));
`), 0o644); err != nil {
		t.Fatal(err)
	}
	for name, ext := range map[string]string{"pi-auto": gen, "pi package": npm} {
		_ = os.Remove(log)
		cmd := exec.CommandContext(t.Context(), node, "--experimental-strip-types", "--no-warnings", drive, ext, src)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "DEJA_BIN="+stub, "HOME="+dir)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("%s: node: %v\n%s", name, err, out)
		}
		if got, want := strings.TrimSpace(string(out)), "new digest\nresume none\nfork none\ncompacted digest\nkept none\nempty fork digest"; got != want {
			t.Errorf("%s: digests got:\n%s\nwant:\n%s", name, got, want)
		}
		calls, _ := os.ReadFile(log)
		var forkPrompt string
		for line := range strings.SplitSeq(string(calls), "\n") {
			if strings.HasPrefix(line, "hook-context") && (strings.Contains(line, `"res-1"`) || strings.Contains(line, `"fork-1"`)) {
				t.Errorf("%s: asked for a start digest for a session that holds one: %s", name, line)
			}
			if strings.HasPrefix(line, "hook-prompt") && strings.Contains(line, `"fork-1"`) {
				forkPrompt = line
			}
			if strings.Contains(line, `"efork-1"`) && strings.Contains(line, "src-1") {
				t.Errorf("%s: a fork that copied nothing left its source out: %s", name, line)
			}
		}
		if !strings.Contains(forkPrompt, `"parent_session_id":"src-1"`) {
			t.Errorf("%s: the fork's prompt recall does not name the session it was forked from: %q", name, forkPrompt)
		}
	}
}
