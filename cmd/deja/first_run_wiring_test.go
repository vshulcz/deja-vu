package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/index"
)

// A fresh install lands with an index and no agent reading it. install.sh drops
// a binary, a bare `deja` builds the memory and reports it, and the command
// that makes any of it arrive on its own — `deja install --auto` — was named
// nowhere in that path: not by the installer, and not by the screen the reader
// is looking at.
//
// Someone who stops there has installed a search tool by accident. They have to
// remember to run deja by hand, which is the one thing this is built not to
// need.
func TestTheBriefSaysWhenNoAgentIsWired(t *testing.T) {
	tmp := hermeticEnv(t)
	proj := filepath.Join(tmp, "claude", "project")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	line := `{"type":"user","sessionId":"wire-1","cwd":"/w","timestamp":"2026-01-02T03:04:05Z","message":{"role":"user","content":"the retry loop drops the last attempt"}}`
	if err := os.WriteFile(filepath.Join(proj, "s.jsonl"), []byte(line+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := runBrief(index.DefaultDir(), &out); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	if !strings.Contains(got, "install --auto") {
		t.Errorf("history was found and no agent is wired, and the screen never says so:\n%s", got)
	}
}

// And it goes quiet once something is wired: a line that is always there is
// not a prompt, it is furniture.
func TestTheBriefStopsSayingItOnceWired(t *testing.T) {
	tmp := hermeticEnv(t)
	proj := filepath.Join(tmp, "claude", "project")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	line := `{"type":"user","sessionId":"wire-2","cwd":"/w","timestamp":"2026-01-02T03:04:05Z","message":{"role":"user","content":"the retry loop drops the last attempt"}}`
	if err := os.WriteFile(filepath.Join(proj, "s.jsonl"), []byte(line+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	// One wiring file is enough: the question is whether anything reads this.
	wired := autoWirings()[0].path()
	if err := os.MkdirAll(filepath.Dir(wired), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(wired, []byte("hook-context\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := runBrief(index.DefaultDir(), &out); err != nil {
		t.Fatal(err)
	}
	// On the label rather than the sentence: the sentence is copy and has been
	// reworded once already (#1411), and a guard that reads a literal quietly
	// stops guarding the moment the words change.
	if strings.Contains(out.String(), "deja install --auto") {
		t.Errorf("a wired machine is still being told to wire itself:\n%s", out.String())
	}
}
