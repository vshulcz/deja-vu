package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

// hermesNotesStand writes just enough of Hermes for the provider to import,
// and a deja that answers --plain with the model's text and the JSON form with
// the same text plus a line for the person. withStatus=false is Hermes 0.17,
// which has no RecallStatus.
func hermesNotesStand(t *testing.T, withStatus bool) (dir, calls string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("stub deja is a shebang script")
	}
	py, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 not on PATH")
	}
	dir = t.TempDir()
	calls = filepath.Join(dir, "calls")
	stub := filepath.Join(dir, "deja")
	src := "#!" + py + `
import json, os, sys
args = sys.argv[1:]
open(` + strconv.Quote(calls) + `, "a").write(" ".join(args) + "\n")
quiet = os.path.exists(` + strconv.Quote(filepath.Join(dir, "quiet")) + `)
context = "" if quiet else "<deja-recall>\n- **zorbit** dec-0001 · retry in-process, no queue\n</deja-recall>"
line = {"hook-prompt": "deja-vu — you have been here: \"ledger webhook retry\" (3d ago · via: ledger, webhook)",
        "hook-context": "deja recalled 1 prior session from this project · 1.2 KB of context\ndeja indexed 15 sessions from 2 agents on this machine"}.get(args[0], "")
if "--plain" in args:
    if context:
        print(context)
else:
    out = {"hookSpecificOutput": {"hookEventName": "UserPromptSubmit"}}
    if context:
        out["hookSpecificOutput"]["additionalContext"] = context
    if line:
        out["systemMessage"] = line
    print(json.dumps(out))
`
	if err := os.WriteFile(stub, []byte(src), 0o755); err != nil {
		t.Fatal(err)
	}
	mp := "class MemoryProvider:\n    pass\n"
	if withStatus {
		mp += "\nfrom dataclasses import dataclass\n\n\n@dataclass\nclass RecallStatus:\n    provider_label: str\n    count: int\n    glyph: str = \"G\"\n"
	}
	if err := os.MkdirAll(filepath.Join(dir, "agent"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "agent", "__init__.py"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "agent", "memory_provider.py"), []byte(mp), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "provider.py"), []byte(hermesMemoryPy(stub)), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir, calls
}

func runHermesProvider(t *testing.T, dir, script string) string {
	t.Helper()
	cmd := exec.Command("python3", "-c", "import provider\n"+script)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("provider: %v\n%s", err, out)
	}
	return string(out)
}

// Hermes shows the person one line per recall, "🧠 deja — recalled N
// memories", and deja asked for --plain, so its receipt and notes never
// reached anyone in Hermes. One run per hook now gives the model the text
// --plain printed and puts deja's lines on that indicator.
func TestHermesProviderPutsDejasLinesOnTheRecallIndicator(t *testing.T) {
	dir, calls := hermesNotesStand(t, true)
	out := runHermesProvider(t, dir, `
p = provider.DejaMemoryProvider()
p.initialize("s1", platform="cli")
text = p.prefetch("the ledger webhook times out again")
assert text == "<deja-recall>\n- **zorbit** dec-0001 · retry in-process, no queue\n</deja-recall>\n\n<deja-recall>\n- **zorbit** dec-0001 · retry in-process, no queue\n</deja-recall>", repr(text)
print(repr(p.recall_status().provider_label))
text = p.prefetch("and the backoff cap?")
assert text == "<deja-recall>\n- **zorbit** dec-0001 · retry in-process, no queue\n</deja-recall>", repr(text)
print(repr(p.recall_status().provider_label))
`)
	want := []string{
		`'deja · recalled 1 prior session from this project · 1.2 KB of context\n   indexed 15 sessions from 2 agents on this machine\n   you have been here: "ledger webhook retry" (3d ago · via ledger, webhook)\n  '`,
		`'deja · you have been here: "ledger webhook retry" (3d ago · via ledger, webhook)'`,
	}
	if got := strings.Split(strings.TrimSpace(out), "\n"); len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("indicator labels:\n%s\nwant:\n%s", out, strings.Join(want, "\n"))
	}
	b, err := os.ReadFile(calls)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "--plain") || strings.Count(string(b), "\n") != 3 {
		t.Fatalf("want one JSON run per hook, got:\n%s", b)
	}
}

// Where nothing shows the indicator, deja is asked for --plain: it marks a
// note shown when it prints it, and a note spent there is never seen.
func TestHermesProviderKeepsPlainWhereNobodySeesTheIndicator(t *testing.T) {
	for _, tc := range []struct {
		name       string
		withStatus bool
		platform   string
	}{
		{"hermes 0.17 has no RecallStatus", false, "cli"},
		{"the API server drops status lines", true, "api_server"},
		{"the TUI only flashes them in its status bar", true, "tui"},
		{"a gateway chat may be a group other people read", true, "telegram"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir, calls := hermesNotesStand(t, tc.withStatus)
			out := runHermesProvider(t, dir, `
p = provider.DejaMemoryProvider()
p.initialize("s1", platform="`+tc.platform+`")
text = p.prefetch("the ledger webhook times out again")
assert "<deja-recall>" in text, repr(text)
print(p.recall_status() is None or p.recall_status().provider_label)
`)
			if strings.TrimSpace(out) != "True" && strings.TrimSpace(out) != "deja" {
				t.Errorf("label = %q, want none or bare deja", out)
			}
			b, _ := os.ReadFile(calls)
			if strings.Count(string(b), "--plain") != 2 {
				t.Fatalf("want both hooks asked for --plain, got:\n%s", b)
			}
		})
	}
}

// Hermes prints the indicator only after a prefetch that returned text. A
// line deja sent with nothing for the model waits for the next indicator
// instead of going nowhere.
func TestHermesProviderHoldsALineThatCameWithoutRecall(t *testing.T) {
	dir, _ := hermesNotesStand(t, true)
	quiet := filepath.Join(dir, "quiet")
	out := runHermesProvider(t, dir, `
import os
open(`+strconv.Quote(quiet)+`, "w").close()
p = provider.DejaMemoryProvider()
p.initialize("s1", platform="cli")
assert p.prefetch("hello there friend") == ""
assert p.recall_status() is None
os.remove(`+strconv.Quote(quiet)+`)
p.prefetch("the ledger webhook times out again")
print(repr(p.recall_status().provider_label))
`)
	if !strings.Contains(out, "indexed 15 sessions") || strings.Count(out, "you have been here") != 2 {
		t.Fatalf("held lines did not reach the next indicator:\n%s", out)
	}
}
