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
	writeFrictionCorpus(t, root, index.FrictionMinSessions)
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
	if !strings.Contains(out, fmt.Sprintf("%d sessions", index.FrictionMinSessions)) {
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
	writeFrictionCorpus(t, root, index.FrictionMinSessions-1)
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
	writeFrictionCorpus(t, root, index.FrictionMinSessions)
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

// The count is sessions-with-tool-output and it read as sessions-indexed, so a
// store with six conversations reported zero — the sentence #637 was about
// (#705).
func TestFrictionEmptyAnswerDistinguishesThreeStores(t *testing.T) {
	t.Run("no sessions at all", func(t *testing.T) {
		seedFrictionStore(t, nil)
		out := runFrictionFor(t)
		if !strings.Contains(out, "no sessions are indexed yet") {
			t.Errorf("empty store: %q", out)
		}
	})
	t.Run("sessions without tool output", func(t *testing.T) {
		seedFrictionStore(t, []string{
			`{"type":"user","sessionId":"a","cwd":"/w/p","timestamp":"2026-07-21T10:00:00Z","message":{"role":"user","content":"plain talk only"}}`,
			`{"type":"user","sessionId":"b","cwd":"/w/p","timestamp":"2026-07-22T10:00:00Z","message":{"role":"user","content":"more plain talk"}}`,
		})
		out := runFrictionFor(t)
		if !strings.Contains(out, "none of the 2 indexed sessions recorded tool output") {
			t.Errorf("no tool output: %q", out)
		}
	})
	t.Run("tool output but nothing recurring", func(t *testing.T) {
		// Three sessions, two of which recorded tool output: the two numbers
		// have to differ, or either one passes for the other.
		seedFrictionStore(t, []string{
			`{"type":"user","sessionId":"a","cwd":"/w/p","timestamp":"2026-07-21T10:00:00Z","message":{"role":"user","content":[{"type":"tool_result","content":"ERROR: one of a kind"}]}}`,
			`{"type":"user","sessionId":"b","cwd":"/w/p","timestamp":"2026-07-22T10:00:00Z","message":{"role":"user","content":[{"type":"tool_result","content":"ERROR: also unique"}]}}`,
			`{"type":"user","sessionId":"c","cwd":"/w/p","timestamp":"2026-07-23T10:00:00Z","message":{"role":"user","content":"no commands were run here"}}`,
		})
		out := runFrictionFor(t)
		if !strings.Contains(out, "in the 2 sessions that recorded tool output (of 3 indexed)") {
			t.Errorf("tool output present: %q", out)
		}
	})
}

func seedFrictionStore(t *testing.T, lines []string) {
	t.Helper()
	tmp := hermeticEnv(t)
	root := filepath.Join(tmp, "claude", "proj-p")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "claude"))
	for i, line := range lines {
		name := fmt.Sprintf("s%d.jsonl", i)
		if err := os.WriteFile(filepath.Join(root, name), []byte(line+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := index.Ensure(index.DefaultDir(), "", true, nil); err != nil {
		t.Fatal(err)
	}
}

func runFrictionFor(t *testing.T) string {
	t.Helper()
	var buf bytes.Buffer
	if err := runFriction(index.DefaultDir(), nil, &buf); err != nil {
		t.Fatal(err)
	}
	return buf.String()
}
