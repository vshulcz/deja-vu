package index

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The per-term counts and the withheld count are both read off the postings,
// which hold every session — including the ones the ignore rule keeps out of
// every answer (#2562).
func TestTermCountsAndWithheldCountFollowTheIgnoreRule(t *testing.T) {
	tmp := t.TempDir()
	home := filepath.Join(tmp, "home")
	root := filepath.Join(home, "claude")
	jobs := filepath.Join(root, ".claude", "jobs", "abc", "-w-app")
	real := filepath.Join(root, "-w-app")
	for _, d := range []string{jobs, real} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	write := func(dir, sid, text string) {
		body := `{"type":"user","sessionId":"` + sid + `","cwd":"/w/app","timestamp":"2026-01-02T03:04:05Z",` +
			`"message":{"role":"user","content":"` + text + `"}}` + "\n"
		if err := os.WriteFile(filepath.Join(dir, sid+".jsonl"), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write(jobs, "scratch1", "why did the quokka telemetry sharding keep failing")
	write(jobs, "scratch2", "the quokka telemetry sharding again")
	write(real, "keeper", "the invoice renderer lost its footer")

	setHome(t, home)
	t.Setenv("DEJA_CLAUDE_ROOT", root)
	t.Setenv("DEJA_CODEX_ROOT", filepath.Join(tmp, "codex"))
	t.Setenv("DEJA_OPENCODE_DB", filepath.Join(tmp, "opencode.db"))
	dir := filepath.Join(tmp, "index.db")
	t.Setenv("DEJA_INDEX_DIR", dir)
	if err := Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}

	terms := []string{"quokka", "telemetry", "sharding"}
	counts := TermSessionCounts(dir, terms)
	for _, term := range terms {
		if counts[term] != 0 {
			t.Errorf("%q counted in %d sessions search cannot serve", term, counts[term])
		}
	}
	// The words of a session that is served are still counted.
	if got := TermSessionCounts(dir, []string{"invoice"}); got["invoice"] != 1 {
		t.Errorf("invoice counted %d times, want 1", got["invoice"])
	}
	if n := IgnoredWithAllTerms(dir, terms); n != 2 {
		t.Errorf("withheld = %d, want the two sessions that hold every term", n)
	}
	// Nothing withheld for a query the rule does not touch, and nothing for a
	// query the store has never seen.
	if n := IgnoredWithAllTerms(dir, []string{"invoice", "renderer"}); n != 0 {
		t.Errorf("withheld = %d for a served answer", n)
	}
	if n := IgnoredWithAllTerms(dir, []string{"zzzqqq"}); n != 0 {
		t.Errorf("withheld = %d for a word nobody wrote", n)
	}
	if n := IgnoredWithAllTerms(dir, nil); n != 0 {
		t.Errorf("withheld = %d for no terms at all", n)
	}
	// An unreadable store answers "nothing withheld" rather than failing the
	// command it decorates.
	if n := IgnoredWithAllTerms(filepath.Join(tmp, "nowhere"), terms); n != 0 {
		t.Errorf("withheld = %d with no index", n)
	}
	if got := servableSids(filepath.Join(tmp, "nowhere")); got != nil {
		t.Errorf("servableSids on a missing index = %v, want nil so counts stand", got)
	}
	if strings.TrimSpace(dir) == "" {
		t.Fatal("unreachable")
	}
}
