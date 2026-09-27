package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"
)

// candidateTurn is one line of a seeded Claude transcript.
type candidateTurn struct{ role, text string }

// seedCandidateSession writes a Claude transcript whose turns are a minute
// apart from start, in a project outside any temporary directory so the stand
// filter judges it as a person's session.
func seedCandidateSession(t *testing.T, rel, id string, start time.Time, turns ...candidateTurn) {
	t.Helper()
	path := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), rel, id+".jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	for i, tr := range turns {
		rec := map[string]any{
			"type": tr.role, "sessionId": id, "cwd": "/home/u/proj",
			"timestamp": start.Add(time.Duration(i) * time.Minute).UTC().Format(time.RFC3339),
			"message":   map[string]any{"role": tr.role, "content": tr.text},
		}
		line, _ := json.Marshal(rec)
		b.Write(line)
		b.WriteByte('\n')
	}
	if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
}

func candidateEnv(t *testing.T) {
	t.Helper()
	hermeticEnv(t)
	// The fixtures sit in t.TempDir, which is exactly what the stand filter
	// drops; the one test that wants it on sets its own roots.
	old := candidateTempRoots
	candidateTempRoots = func() []string { return nil }
	t.Cleanup(func() { candidateTempRoots = old })
}

func runCandidatesJSON(t *testing.T, args ...string) []ruleCandidate {
	t.Helper()
	out, err := captureRun(t, append([]string{"rules", "candidates", "--json"}, args...)...)
	if err != nil {
		t.Fatalf("rules candidates: %v\n%s", err, out)
	}
	var got []ruleCandidate
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("json: %v\n%s", err, out)
	}
	return got
}

var t0 = time.Date(2026, 3, 10, 12, 0, 0, 0, time.UTC)

// A correction after the agent acted is a candidate; the opening message is
// not, even when it says "don't", because nothing had been done yet; and text
// the harness files as the user's (a reminder block, a `!` echo) is not the
// user speaking.
func TestRulesCandidatesKeepsCorrectionsOnly(t *testing.T) {
	candidateEnv(t)
	seedCandidateSession(t, "-home-u-proj", "s1", t0,
		candidateTurn{"user", "don't touch the tests, just fix the parser"},
		candidateTurn{"assistant", "I rewrote the parser and removed two tests."},
		candidateTurn{"user", "no, I told you to leave the tests alone"},
		candidateTurn{"assistant", "Restored the tests."},
		candidateTurn{"user", "<system-reminder>don't use sleep</system-reminder>"},
		candidateTurn{"assistant", "ok"},
		candidateTurn{"user", "! git status"},
		candidateTurn{"assistant", "ok"},
		candidateTurn{"user", "thanks, looks good"},
	)
	got := runCandidatesJSON(t)
	if len(got) != 1 {
		t.Fatalf("candidates = %+v, want only the correction", got)
	}
	c := got[0]
	if c.N != 1 || c.Harness != "claude" || c.SessionID != "s1" || !strings.HasPrefix(c.Correction, "no, I told you") {
		t.Fatalf("candidate = %+v", c)
	}
	if !strings.Contains(c.AgentBefore, "removed two tests") {
		t.Fatalf("agent_before = %q, want what the agent did", c.AgentBefore)
	}
}

// A spawned agent's turns are its parent's instructions, not the user's, and a
// stand in a temporary directory is a test run.
func TestRulesCandidatesSkipsSubagentsAndStands(t *testing.T) {
	candidateEnv(t)
	t.Setenv("DEJA_INCLUDE_SUBAGENTS", "1")
	seedCandidateSession(t, "-home-u-proj", "parent", t0,
		candidateTurn{"user", "look at the logs"},
		candidateTurn{"assistant", "The logs are clean."},
		candidateTurn{"user", "no, look again, the error is at 3am"},
	)
	seedCandidateSession(t, filepath.Join("-home-u-proj", "parent", "subagents"), "agent-a1", t0.Add(time.Hour),
		candidateTurn{"user", "search the logs"},
		candidateTurn{"assistant", "found nothing"},
		candidateTurn{"user", "no, never stop at the first file"},
	)
	seedCandidateSession(t, "-private-tmp-stand", "stand", t0.Add(2*time.Hour),
		candidateTurn{"user", "run the probe"},
		candidateTurn{"assistant", "done"},
		candidateTurn{"user", "no, run it with the seed"},
	)
	got := runCandidatesJSON(t)
	if len(got) != 1 || got[0].SessionID != "parent" {
		t.Fatalf("candidates = %+v, want only the parent's correction", got)
	}
}

// One correction re-filed by a fork is one candidate, or a single remark would
// look like a rule stated in two sessions.
func TestRulesCandidatesCollapsesForkCopies(t *testing.T) {
	candidateEnv(t)
	turns := []candidateTurn{
		{"user", "write the release notes"},
		{"assistant", "Here are five paragraphs of notes."},
		{"user", "no,   much shorter please"},
	}
	seedCandidateSession(t, "-home-u-proj", "orig", t0, turns...)
	turns[2].text = "No, much shorter please"
	seedCandidateSession(t, "-home-u-proj", "fork", t0.Add(time.Hour), turns...)
	got := runCandidatesJSON(t)
	if len(got) != 1 || got[0].SessionID != "orig" {
		t.Fatalf("candidates = %+v, want the first copy only", got)
	}
}

// The text form is the one the skill was measured with: a numbered header
// naming harness, date and session, the user's words, then what the agent had
// just done. Long text is cut and marked.
func TestRulesCandidatesTextFormat(t *testing.T) {
	candidateEnv(t)
	long := "no, " + strings.Repeat("stop adding comments everywhere ", 20)
	seedCandidateSession(t, "-home-u-proj", "fmt1", t0,
		candidateTurn{"user", "tidy the module"},
		candidateTurn{"assistant", "Added comments\n\nto every   function."},
		candidateTurn{"user", long},
	)
	out, err := captureRun(t, "rules", "candidates")
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(lines) != 3 {
		t.Fatalf("out = %q", out)
	}
	if want := "#1 claude " + t0.Add(2*time.Minute).Local().Format("2006-01-02") + " deja:fmt1"; lines[0] != want {
		t.Fatalf("header = %q, want %q", lines[0], want)
	}
	if !strings.HasPrefix(lines[1], "user: no, stop adding") || !strings.HasSuffix(lines[1], "…") || len([]rune(strings.TrimPrefix(lines[1], "user: "))) != candidateQuote {
		t.Fatalf("user line = %q", lines[1])
	}
	if lines[2] != "before it, the agent: Added comments to every function." {
		t.Fatalf("agent line = %q", lines[2])
	}
}

// --limit keeps the newest and numbers them from one; --since drops what is
// older than the window.
func TestRulesCandidatesLimitAndSince(t *testing.T) {
	candidateEnv(t)
	now := time.Now()
	for i, at := range []time.Time{now.AddDate(0, 0, -200), now.AddDate(0, 0, -20), now.AddDate(0, 0, -2)} {
		id := "w" + string(rune('a'+i))
		seedCandidateSession(t, "-home-u-proj", id, at,
			candidateTurn{"user", "deploy it"},
			candidateTurn{"assistant", "deployed to prod"},
			candidateTurn{"user", "no, staging first, session " + id},
		)
	}
	if got := runCandidatesJSON(t); len(got) != 3 {
		t.Fatalf("all = %d, want 3", len(got))
	}
	got := runCandidatesJSON(t, "--limit", "2")
	if len(got) != 2 || got[0].SessionID != "wb" || got[1].SessionID != "wc" || got[0].N != 1 {
		t.Fatalf("limit 2 = %+v", got)
	}
	got = runCandidatesJSON(t, "--since", "30d")
	if len(got) != 2 || got[0].SessionID != "wb" {
		t.Fatalf("since 30d = %+v", got)
	}
	for _, bad := range [][]string{{"--limit", "0"}, {"--limit"}, {"--since", "-3d"}, {"--since"}, {"--bogus"}} {
		if _, err := captureRun(t, append([]string{"rules", "candidates"}, bad...)...); err == nil {
			t.Fatalf("%v: want an error", bad)
		}
	}
}

// Listing candidates writes nothing a person owns: no rules file, no agent
// file, nothing new under the home directory.
func TestRulesCandidatesWritesNothing(t *testing.T) {
	candidateEnv(t)
	home := os.Getenv("HOME")
	for _, d := range []string{filepath.Join(home, ".claude"), filepath.Join(home, ".codex"), filepath.Join(home, ".config", "opencode")} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	seedCandidateSession(t, "-home-u-proj", "nw", t0,
		candidateTurn{"user", "ship it"},
		candidateTurn{"assistant", "pushed to main"},
		candidateTurn{"user", "never push to main without asking"},
	)
	before := treeListing(t, home)
	if got := runCandidatesJSON(t); len(got) != 1 {
		t.Fatalf("candidates = %+v", got)
	}
	if after := treeListing(t, home); strings.Join(before, "\n") != strings.Join(after, "\n") {
		t.Fatalf("home changed:\nbefore %v\nafter  %v", before, after)
	}
}

func treeListing(t *testing.T, root string) []string {
	t.Helper()
	var out []string
	_ = filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		out = append(out, p+" "+info.ModTime().String())
		return nil
	})
	sort.Strings(out)
	return out
}

// The stand filter itself: a session under a temporary root, or filed under a
// Claude-encoded temporary cwd, is not a person's.
func TestThrowawayPath(t *testing.T) {
	old := candidateTempRoots
	candidateTempRoots = func() []string { return []string{"/tmp", "/private/tmp"} }
	t.Cleanup(func() { candidateTempRoots = old })
	for p, want := range map[string]bool{
		"/tmp/stand/s.jsonl":                          true,
		"/private/tmp/x":                              true,
		"/home/u/.claude/projects/-private-tmp-x/s":   true,
		"/home/u/.claude/projects/-tmp-probe/s.jsonl": true,
		"/home/u/.cache/deja-bench/s":                 true,
		"/home/u/.claude/projects/-home-u-tmpfile/s":  false,
		"/home/u/proj":                                false,
		"":                                            false,
	} {
		if got := throwawayPath(p); got != want {
			t.Errorf("throwawayPath(%q) = %v, want %v", p, got, want)
		}
	}
}

// A machine with no history says so, and the JSON form is an empty list rather
// than null, so a skill parsing it does not have to special-case a new user.
func TestRulesCandidatesEmptyIndex(t *testing.T) {
	candidateEnv(t)
	out, err := captureRun(t, "rules", "candidates")
	if err != nil || !strings.Contains(out, "no corrections found") {
		t.Fatalf("text: %v %q", err, out)
	}
	out, err = captureRun(t, "rules", "candidates", "--json")
	if err != nil || strings.TrimSpace(out) != "[]" {
		t.Fatalf("json: %v %q", err, out)
	}
}

// `candidates` in the wrong place gets a sentence, not a guess at a flag.
func TestRulesCandidatesMustComeFirst(t *testing.T) {
	candidateEnv(t)
	_, err := captureRun(t, "rules", "sync", "candidates")
	if err == nil || !strings.Contains(err.Error(), "candidates goes first") {
		t.Fatalf("err = %v", err)
	}
}

// Both skills carry the procedure, and it stops before writing.
func TestSkillsCarryTheRulesProcedure(t *testing.T) {
	for name, body := range map[string]string{"skill": skillBody, "cli skill": cliSkillBody} {
		for _, want := range []string{"deja rules candidates", "Write nothing until the user picks", "deja rules sync"} {
			if !strings.Contains(body, want) {
				t.Errorf("%s is missing %q", name, want)
			}
		}
	}
	if strings.Contains(guidanceText("vscode"), "deja rules candidates") {
		t.Error("the VS Code instructions file is in every chat; the rules procedure belongs only in the skills")
	}
}
