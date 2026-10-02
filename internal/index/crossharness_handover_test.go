package index

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Two agents in one checkout, and the product's whole claim: a fix found in
// one comes back in the other. What carries it is not the project name — the
// harnesses do not agree on that. Measured on a real store, of 170 pairs of
// sessions from different harnesses that edited the same absolute file, 26
// agreed on the project name and 144 did not: one name contained the other in
// 84 cases and one was a path where the other was a bare name in 52.
//
// What makes the claim hold anyway is the rule that admits a session by the
// files it touched under this checkout, whatever it was recorded as. Over 515
// handovers on that store, the session before this one was never missing from
// the pool — so nothing on this path is load-bearing for the product except
// that rule, and nothing else here would catch its removal.
func TestASessionStartReachesTheOtherAgentsWorkInThisCheckout(t *testing.T) {
	tmp := t.TempDir()
	home := filepath.Join(tmp, "home")
	setHome(t, home)
	t.Setenv("USERPROFILE", home)
	claude := filepath.Join(tmp, "claude")
	codex := filepath.Join(tmp, "codex")
	t.Setenv("DEJA_CLAUDE_ROOT", claude)
	t.Setenv("DEJA_CODEX_ROOT", codex)
	t.Setenv("DEJA_OPENCODE_DB", filepath.Join(tmp, "opencode.db"))

	// One repository, worked in by both agents.
	repo := filepath.Join(home, "src", "pool")
	if err := os.MkdirAll(filepath.Join(repo, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	touched := filepath.Join(repo, "pool.go")

	// Claude was started in a subdirectory of the checkout, so its project is
	// named after that directory, pool/internal, where codex's is src/pool:
	// the "one contains the other" shape, 84 of the 144 disagreements. Both
	// name a directory the same way now (#4457), so a different starting
	// directory is what still gives one checkout two names.
	// A drive letter's colon cannot be part of a directory name on Windows,
	// and the encoder drops it there too.
	sub := filepath.Join(repo, "internal")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	encoded := strings.ReplaceAll(strings.ReplaceAll(filepath.ToSlash(sub), ":", ""), "/", "-")
	writeLines(t, filepath.Join(claude, encoded, "c1.jsonl"),
		claudeLineAt("c1", "2026-03-01T09:00:00Z", "the pool leaked connections under load", sub),
		claudeEditAt("c1", "2026-03-01T09:05:00Z", touched, sub))

	// Codex records the checkout itself, so the same work arrives under a
	// different project string. This is the disagreement the measurement found
	// in 85% of cross-harness pairs.
	write(t, filepath.Join(codex, "sessions", "2026", "03", "01", "rollout-2026-03-01T10-00-00-x1.jsonl"),
		`{"type":"session_meta","timestamp":"2026-03-01T10:00:00Z","payload":{"session_id":"x1","cwd":`+jsonStr(repo)+`}}`+"\n"+
			`{"timestamp":"2026-03-01T10:00:01Z","payload":{"role":"user","content":"the pool still leaks, fixed by closing the rows"}}`+"\n"+
			`{"timestamp":"2026-03-01T10:00:02Z","payload":{"role":"assistant","content":[{"type":"text","text":"closed the rows in `+filepath.ToSlash(touched)+`"}]}}`+"\n")

	dir := filepath.Join(tmp, "index.db")
	if err := Ensure(dir, "", true, io.Discard); err != nil {
		t.Fatal(err)
	}

	metas, err := AllMeta(dir)
	if err != nil {
		t.Fatal(err)
	}
	byHarness := map[string]SessionMeta{}
	for _, m := range metas {
		byHarness[m.Harness] = m
	}
	c, okC := byHarness["claude"]
	x, okX := byHarness["codex"]
	if !okC || !okX {
		t.Fatalf("both agents have to be indexed for this to mean anything: %v", metas)
	}
	// The premise: the names differ. If a later change makes the two harnesses
	// agree, this test stops testing the rescue and has to be rewritten rather
	// than quietly passing for the wrong reason.
	t.Logf("claude recorded %q, codex recorded %q", c.Project, x.Project)
	if c.Project == x.Project {
		t.Fatalf("both recorded the project as %q; the disagreement this guards is gone, so rewrite the test", c.Project)
	}

	// A session starting in the checkout sees both, whatever each was called.
	got, err := RecentProjectsUnder(dir, []string{"src/pool"}, repo, 12)
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, s := range got {
		seen[s.Harness] = true
	}
	if !seen["claude"] || !seen["codex"] {
		t.Fatalf("a session start in the checkout reached %v; the other agent's work is the claim", ids(got))
	}

	// And the name alone does not: without the rescue, the agent whose project
	// string differs is invisible, which is what the 144 pairs would have been.
	byName, err := RecentProjects(dir, []string{"src/pool"}, 12)
	if err != nil {
		t.Fatal(err)
	}
	reached := map[string]bool{}
	for _, s := range byName {
		reached[s.Harness] = true
	}
	if len(reached) == 2 {
		t.Fatalf("the name reached both harnesses (%v), so this store no longer reproduces the disagreement", ids(byName))
	}
}
