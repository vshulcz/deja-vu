package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/index"
)

func TestFrictionLineKeepsSpecificErrors(t *testing.T) {
	for _, l := range []string{
		"zsh:1: command not found: shellcheck",
		"ModuleNotFoundError: No module named 'yaml'",
		"internal/index/store.go:41:2: undefined: signalLines",
		"dial tcp 127.0.0.1:5432: connect: connection refused",
	} {
		if !frictionLine(normalizeFriction(l)) {
			t.Errorf("dropped a specific error: %q", l)
		}
	}
	for _, l := range []string{
		"Traceback (most recent call last):",
		"Error: exit status 1",
		"--- FAIL: TestThing (0.01s)",
		"not found",                          // too short to name anything
		`echo "❌ App not found: $APP"`,       // source, not a result
		`  9 sessions  command not found: x`, // this command's own output
		"ok  github.com/vshulcz/deja-vu/internal/index  1.2s",
	} {
		if frictionLine(normalizeFriction(l)) {
			t.Errorf("kept a line that names nothing: %q", l)
		}
	}
}

// The same missing command reaches the corpus under three shell prefixes. Left
// unnormalized each lands below the threshold and none of them is ever
// reported, which is the bug this function exists for.
func TestNormalizeFrictionStripsShellPosition(t *testing.T) {
	want := "command not found: timeout"
	for _, l := range []string{
		"zsh:1: command not found: timeout",
		"(eval):2: command not found: timeout",
		"  bash:15: command not found: timeout  ",
	} {
		if got := normalizeFriction(l); got != want {
			t.Errorf("normalize(%q) = %q, want %q", l, got, want)
		}
	}
	// A colon that is not a line number keeps the line intact — a Go compile
	// error names its file and column and both matter.
	for _, l := range []string{
		"sh: tsc: command not found",
		"Error: cannot find module x: y",
		"ModuleNotFoundError: No module named 'PIL'",
	} {
		if got := normalizeFriction(l); got != l {
			t.Errorf("normalize(%q) = %q, want it unchanged", l, got)
		}
	}
}

func TestTrimFriction(t *testing.T) {
	long := strings.Repeat("x", 90)
	got := trimFriction(long)
	if len([]rune(got)) != 77 || !strings.HasSuffix(got, "…") {
		t.Fatalf("got %d runes: %q", len([]rune(got)), got)
	}
	if got := trimFriction("short"); got != "short" {
		t.Fatalf("got %q", got)
	}
}

// writeFrictionCorpus lays down n claude sessions that each hit the same error,
// plus one that does not.
func writeFrictionCorpus(t *testing.T, root string, n int) {
	t.Helper()
	proj := filepath.Join(root, "projects", "repo")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	line := func(sid, role, text string) string {
		b, err := json.Marshal(map[string]any{
			"type": role, "sessionId": sid, "cwd": "/repo",
			"timestamp": "2026-07-30T03:04:05Z",
			"message":   map[string]any{"role": role, "content": text},
		})
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	for i := 0; i < n; i++ {
		sid := fmt.Sprintf("frict%02d", i)
		rows := []string{
			line(sid, "user", "run the deploy script for the staging cluster"),
			line(sid, "assistant", "running it now"),
			`{"type":"user","sessionId":"` + sid + `","cwd":"/repo",` +
				`"timestamp":"2026-07-30T03:05:05Z","message":{"role":"user","content":` +
				`[{"type":"tool_result","content":"zsh:1: command not found: shellcheck\nexit status 127"}]}}`,
		}
		p := filepath.Join(proj, sid+".jsonl")
		if err := os.WriteFile(p, []byte(strings.Join(rows, "\n")+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	sid := "clean01"
	rows := []string{
		line(sid, "user", "review the block layout change"),
		line(sid, "assistant", "looks right"),
	}
	if err := os.WriteFile(filepath.Join(proj, sid+".jsonl"), []byte(strings.Join(rows, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
}

func frictionEnv(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	t.Setenv("HOME", t.TempDir())
	t.Setenv("USERPROFILE", os.Getenv("HOME"))
	t.Setenv("DEJA_CLAUDE_ROOT", root)
	t.Setenv("DEJA_CODEX_ROOT", filepath.Join(t.TempDir(), "codex"))
	t.Setenv("DEJA_OPENCODE_DB", filepath.Join(t.TempDir(), "opencode.db"))
	t.Setenv("DEJA_INDEX_DIR", filepath.Join(t.TempDir(), "index.db"))
	return root
}

func TestFrictionReportsRecurringErrors(t *testing.T) {
	root := frictionEnv(t)
	writeFrictionCorpus(t, root, frictionMinSessions)
	var buf bytes.Buffer
	if err := runFriction(index.DefaultDir(), nil, &buf); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if !strings.Contains(out, "command not found: shellcheck") {
		t.Fatalf("the recurring error is missing:\n%s", out)
	}
	if !strings.Contains(out, "claude") {
		t.Fatalf("the harness is missing:\n%s", out)
	}
	if !strings.Contains(out, fmt.Sprintf("%d sessions", frictionMinSessions)) {
		t.Fatalf("wrong session count:\n%s", out)
	}
	// exit status 127 sits beside the error in every one of those sessions and
	// says nothing about what is missing.
	if strings.Contains(out, "exit status") {
		t.Fatalf("a generic line was reported:\n%s", out)
	}
}

// Twice is a coincidence. Reporting it would make the command noise on any
// store, which is the whole reason for the threshold.
func TestFrictionStaysQuietBelowThreshold(t *testing.T) {
	root := frictionEnv(t)
	writeFrictionCorpus(t, root, frictionMinSessions-1)
	var buf bytes.Buffer
	if err := runFriction(index.DefaultDir(), nil, &buf); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "nothing recurring") {
		t.Fatalf("want the empty state, got:\n%s", buf.String())
	}
}

func TestFrictionLimit(t *testing.T) {
	root := frictionEnv(t)
	writeFrictionCorpus(t, root, frictionMinSessions)
	var buf bytes.Buffer
	if err := runFriction(index.DefaultDir(), []string{"--limit", "1"}, &buf); err != nil {
		t.Fatal(err)
	}
	if err := runFriction(index.DefaultDir(), []string{"--limit", "0"}, &buf); err == nil {
		t.Fatal("--limit 0 accepted")
	}
	if err := runFriction(index.DefaultDir(), []string{"--limit", "many"}, &buf); err == nil {
		t.Fatal("--limit many accepted")
	}
}

func TestFrictionOnBlockedIndex(t *testing.T) {
	frictionEnv(t)
	blocked := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(blocked, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_INDEX_DIR", filepath.Join(blocked, "child"))
	if err := runFriction(index.DefaultDir(), nil, &bytes.Buffer{}); err == nil {
		t.Fatal("friction accepted a blocked index")
	}
}
