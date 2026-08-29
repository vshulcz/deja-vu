package index

import (
	"os"
	"path/filepath"
	"testing"
)

// The withheld count (#2562) intersects raw term postings, while the tiers
// stem: a query typed as "pool" is answered by a session that says "pools".
// Measured over 22 real queries the two agreed everywhere, but the case is
// constructible, and there the line goes quiet exactly when it is needed —
// search serves nothing, the rule is why, and nobody says so (#2573).
func TestTheWithheldCountFollowsTheWordFormsSearchDoes(t *testing.T) {
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
	// The withheld session writes the plural; the reader types the singular.
	write(jobs, "scratch", "the quokka pools kept starving under load")
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
	// The premise: this store really does know the plural as another form.
	if forms := OtherWordForms(dir, []string{"pool"}); len(forms["pool"]) == 0 {
		t.Fatal("the store knows no other form of \"pool\", so this measures nothing")
	}
	if n := IgnoredWithAllTerms(dir, []string{"quokka", "pool", "starving"}); n != 1 {
		t.Errorf("withheld = %d, want the session that says \"pools\"", n)
	}
	// The exact spelling still counts once, not twice.
	if n := IgnoredWithAllTerms(dir, []string{"quokka", "pools", "starving"}); n != 1 {
		t.Errorf("withheld = %d for the exact spelling", n)
	}
	// And a query the rule does not touch stays at zero.
	if n := IgnoredWithAllTerms(dir, []string{"invoice", "renderer"}); n != 0 {
		t.Errorf("withheld = %d for a served answer", n)
	}
}
