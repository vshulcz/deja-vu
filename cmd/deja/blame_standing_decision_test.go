package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/search"
)

// blameStore: one session about retry.go that was promoted, several that only
// edited it.
func blameStore(t *testing.T) string {
	t.Helper()
	tmp := hermeticEnv(t)
	store := filepath.Join(tmp, "claude", "-work-app")
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "claude"))
	now := time.Now().UTC()
	write := func(sid string, minutes int, lines []string) {
		if err := os.WriteFile(filepath.Join(store, sid+".jsonl"), []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		_ = minutes
	}
	at := func(m int) string { return now.Add(-time.Duration(m) * time.Minute).Format(time.RFC3339) }
	edit := func(sid, old, when string) string {
		return `{"type":"assistant","sessionId":"` + sid + `","timestamp":"` + when + `","cwd":"/work/app","message":{"role":"assistant","content":[{"type":"tool_use","name":"Edit","input":{"file_path":"/work/app/retry.go","old_string":"` + old + `","new_string":"y"}}]}}`
	}
	write("dec", 400, []string{
		`{"type":"user","sessionId":"dec","timestamp":"` + at(401) + `","cwd":"/work/app","message":{"role":"user","content":"should the retry budget go up to 10 for the payments client?"}}`,
		edit("dec", "return 3", at(400)),
		`{"type":"assistant","sessionId":"dec","timestamp":"` + at(399) + `","cwd":"/work/app","message":{"role":"assistant","content":"no: the pool change fixed the timeouts, the retry budget stays at 5"}}`,
	})
	for k := 0; k < 3; k++ {
		sid := "f" + string(rune('0'+k))
		write(sid, 100-10*k, []string{
			`{"type":"user","sessionId":"` + sid + `","timestamp":"` + at(100-10*k) + `","cwd":"/work/app","message":{"role":"user","content":"more work on retry.go"}}`,
			edit(sid, "x", at(99-10*k)),
		})
	}
	dir := index.DefaultDir()
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRunStderr(t, "promote", "dec", "--state", "accepted",
		"--note", "the retry budget stays at 5; the pool change is what fixed the timeouts"); err != nil {
		t.Fatal(err)
	}
	return dir
}

// blame answers "who decided this". It marks a decision that was taken back
// (#1017) and said nothing about the one that stands, so the row carrying the
// answer read as the oldest and least relevant (#2514).
func TestBlameMarksTheDecisionThatStands(t *testing.T) {
	dir := blameStore(t)
	target, err := search.ResolveBlamePath("retry.go")
	if err != nil {
		t.Fatal(err)
	}
	hits, _, _, err := findBlameHits(dir, target, search.BlameOptions{}, "search", os.Stderr)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) == 0 {
		t.Fatal("the fixture found no history for retry.go")
	}
	var found bool
	for _, h := range hits {
		if h.Session.ID != "dec" {
			continue
		}
		found = true
		line := search.BlameLifecycleLine(h)
		if line == "" {
			t.Errorf("the session carrying the standing decision is marked with nothing")
		}
		if !strings.Contains(line, "retry budget stays at 5") {
			t.Errorf("the decision itself is not in the line: %q", line)
		}
	}
	if !found {
		t.Fatal("the promoted session is not among the blame rows at all")
	}
}

// A decision that was taken back still reads as a warning, not as one that
// stands.
func TestBlameStillWarnsAboutAWithdrawnDecision(t *testing.T) {
	dir := blameStore(t)
	if _, err := captureRunStderr(t, "promote", "dec", "--state", "rejected", "--note", "we went to 10 after all"); err != nil {
		t.Fatal(err)
	}
	target, err := search.ResolveBlamePath("retry.go")
	if err != nil {
		t.Fatal(err)
	}
	hits, _, _, err := findBlameHits(dir, target, search.BlameOptions{}, "search", os.Stderr)
	if err != nil {
		t.Fatal(err)
	}
	for _, h := range hits {
		if h.Session.ID != "dec" {
			continue
		}
		line := search.BlameLifecycleLine(h)
		if !strings.Contains(line, "tried and rejected") {
			t.Errorf("a withdrawn decision no longer reads as one: %q", line)
		}
		if strings.Contains(line, "standing") {
			t.Errorf("a withdrawn decision reads as standing: %q", line)
		}
	}
}
