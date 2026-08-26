package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/model"
)

func TestLastDistinctTakesTheNewestAndDeduplicates(t *testing.T) {
	ms := []model.Message{
		{Role: "files", Text: "/w/a.go"},
		{Role: "user", Text: "/w/ignored.go"},
		{Role: "files", Text: "/w/b.go\n/w/a.go"},
		{Role: "files", Text: "/w/c.go"},
	}
	got := lastDistinct(ms, "files", 3,
		func(s string) []string { return strings.Split(s, "\n") },
		func(s string) string { return s })
	// Newest first, each path once, and nothing from another role.
	want := []string{"/w/c.go", "/w/b.go", "/w/a.go"}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	if len(lastDistinct(ms, "files", 1, func(s string) []string { return strings.Split(s, "\n") },
		func(s string) string { return s })) != 1 {
		t.Fatal("the limit should bound the block")
	}
}

func TestLastDistinctDropsAgentScratch(t *testing.T) {
	ms := []model.Message{{Role: "files", Text: "/Users/x/.claude/notes.md\n/w/real.go"}}
	got := lastDistinct(ms, "files", 5,
		func(s string) []string { return strings.Split(s, "\n") },
		func(s string) string { return s })
	if len(got) != 1 || got[0] != "/w/real.go" {
		t.Fatalf("got %v, want the repository file only", got)
	}
}

func writeCompactFixture(t *testing.T) string {
	t.Helper()
	tmp := hermeticEnv(t)
	proj := filepath.Join(tmp, "claude", "project")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	line := func(format string, a ...any) { fmt.Fprintf(&b, format+"\n", a...) }
	line(`{"type":"user","sessionId":"claude-compact","cwd":"/w","timestamp":"2026-01-02T03:04:05Z","message":{"role":"user","content":"make the retry loop stop dropping the last attempt"}}`)
	line(`{"type":"assistant","sessionId":"claude-compact","cwd":"/w","timestamp":"2026-01-02T03:05:00Z","message":{"role":"assistant","content":[{"type":"tool_use","name":"Read","input":{"file_path":"/w/pkg/retry.go"}}]}}`)
	line(`{"type":"assistant","sessionId":"claude-compact","cwd":"/w","timestamp":"2026-01-02T03:06:00Z","message":{"role":"assistant","content":[{"type":"tool_use","name":"Bash","input":{"command":"go test ./pkg/ -run Retry -count=3"}}]}}`)
	// A pasted script: whole it swallows the block, truncated it says nothing.
	line(`{"type":"assistant","sessionId":"claude-compact","cwd":"/w","timestamp":"2026-01-02T03:07:00Z","message":{"role":"assistant","content":[{"type":"tool_use","name":"Bash","input":{"command":"python3 - <<'PY'\nimport os\nprint(os.getcwd())\nPY"}}]}}`)
	if err := os.WriteFile(filepath.Join(proj, "s.jsonl"), []byte(b.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := index.Ensure(index.DefaultDir(), "", false, nil); err != nil {
		t.Fatal(err)
	}
	return tmp
}

func TestCompactEvidenceNamesWhatTheSummaryDrops(t *testing.T) {
	writeCompactFixture(t)
	got := compactEvidence(index.DefaultDir(), "claude-compact", "")
	if got == "" {
		t.Fatal("a session with files and commands should produce a block")
	}
	if !strings.Contains(got, "retry.go") {
		t.Errorf("the files it touched should be named:\n%s", got)
	}
	if !strings.Contains(got, "go test ./pkg/ -run Retry -count=3") {
		t.Errorf("the command it ran should be named:\n%s", got)
	}
	if strings.Contains(got, "<<'PY'") {
		t.Errorf("a pasted script is not evidence worth handing back:\n%s", got)
	}
}

func TestCompactEvidenceSaysNothingWhenItCannot(t *testing.T) {
	writeCompactFixture(t)
	dir := index.DefaultDir()
	if got := compactEvidence(dir, "", ""); got != "" {
		t.Errorf("no session id, no block: %q", got)
	}
	if got := compactEvidence(dir, "claude-not-indexed-yet", ""); got != "" {
		t.Errorf("an unknown session says nothing: %q", got)
	}
}

func TestFindByIDFindsWithoutTheHarness(t *testing.T) {
	writeCompactFixture(t)
	dir := index.DefaultDir()
	s, ok, err := index.FindByID(dir, "claude-compact")
	if err != nil || !ok {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
	if s.Harness != "claude" || len(s.Messages) == 0 {
		t.Fatalf("got %+v", s.Harness)
	}
	if _, ok, _ := index.FindByID(dir, "nope"); ok {
		t.Fatal("an unknown id should not resolve")
	}
	if _, ok, _ := index.FindByID(dir, ""); ok {
		t.Fatal("an empty id should not resolve")
	}
}
