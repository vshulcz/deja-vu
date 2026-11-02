package index

import (
	"os"
	"path/filepath"
	"testing"
)

// TestSelectorLadderResolverAndCounterAlwaysAgree enforces the domain invariant
// across all six rungs of the selector ladder in retrieval_identity.go (#853,
// #921, #1049, #1620, #2401, #4667): for every selector shape, FindByPrefixIn
// resolves a session if and only if PrefixMatchesIn returns > 0.
func TestSelectorLadderResolverAndCounterAlwaysAgree(t *testing.T) {
	tmp := t.TempDir()
	setHome(t, filepath.Join(tmp, "home"))
	claudeDir := filepath.Join(tmp, "claude", "-tmp-w")
	if err := os.MkdirAll(claudeDir, 0o755); err != nil {
		t.Fatal(err)
	}
	const fullUUID = "a1b2c3d4-0000-4000-8000-00000000e1f2"
	body := `{"type":"user","sessionId":"` + fullUUID + `","cwd":"/w","timestamp":"2026-09-01T09:00:00Z","message":{"role":"user","content":"fix the selector ladder drift"}}` + "\n"
	if err := os.WriteFile(filepath.Join(claudeDir, fullUUID+".jsonl"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "claude"))
	dir := filepath.Join(tmp, "index.db")
	if err := Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}

	selectors := []struct {
		sel  string
		want bool
	}{
		{"", false},
		{"   ", false},
		{"nonexistent-selector", false},
		{fullUUID, true},                   // Rung 1: exact ID
		{"a1b2c3d4", true},                 // Rung 1: prefix ID
		{"a1b2c3d4…0000e1f2", true},        // Rung 4: elided ID
		{"claude:a1b2c3d4", true},          // Rung 5: composite harness:id (#921)
		{"claude:a1b2c3d4…0000e1f2", true}, // Rung 5: composite + elided
		{"codex:a1b2c3d4", false},          // Rung 5: wrong harness
		{"A1B2C3D4-0000", true},            // Rung 6: case-folded UUID (#1620)
	}

	for _, tc := range selectors {
		_, ok, err := FindByPrefix(dir, tc.sel)
		if err != nil {
			t.Fatalf("FindByPrefix(%q): %v", tc.sel, err)
		}
		n := PrefixMatches(dir, tc.sel)
		if ok != tc.want {
			t.Errorf("FindByPrefix(%q) ok = %v, want %v (PrefixMatches = %d)", tc.sel, ok, tc.want, n)
		}
		if (n > 0) != ok {
			t.Errorf("selector %q: FindByPrefix ok=%v disagrees with PrefixMatches=%d", tc.sel, ok, n)
		}
	}
}

// TestSessionTitleRankMonotonicity verifies the domain hierarchy in
// session_title.go: placeholder/plumbing (0) < tool output (1) < agent opening
// (2) < thin user greeting (3) < substantial user question (4).
func TestSessionTitleRankMonotonicity(t *testing.T) {
	r0Empty := titleRankOfRow("", false)
	r0Plumbing := titleRankOfRow(harnessOutputTitle("local stdout"), false)
	r1Tool := titleRankOfRow(toolOutputTitle("PASS ok"), false)
	r2Agent := titleRankOfRow("I inspected the repository and found two issues", true)
	r3Thin := titleRankOfRow("привет", false)
	r4Question := titleRankOfRow("почему падает тест на windows", false)

	if r0Empty != 0 || r0Plumbing != 0 {
		t.Fatalf("placeholder ranks = (%d, %d), want (0, 0)", r0Empty, r0Plumbing)
	}
	if r0Plumbing >= r1Tool || r1Tool >= r2Agent || r2Agent >= r3Thin || r3Thin >= r4Question {
		t.Fatalf("title rank hierarchy violated: plumbing=%d tool=%d agent=%d thin=%d question=%d",
			r0Plumbing, r1Tool, r2Agent, r3Thin, r4Question)
	}
}
