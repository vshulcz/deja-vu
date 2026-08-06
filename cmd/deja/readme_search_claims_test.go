package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/index"
)

// substringStore holds one session with the literal word and one with the
// longer word that contains it, which is the case that tells a matching
// property apart from a zero-result fallback.
func substringStore(t *testing.T, withLiteral bool) string {
	t.Helper()
	tmp := hermeticEnv(t)
	root := filepath.Join(tmp, "claude", "-tmp-projj")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "claude"))
	write := func(sid, text string) {
		body := `{"type":"user","sessionId":"` + sid + `","cwd":"/tmp/projj","timestamp":"2026-08-01T10:00:00Z","message":{"role":"user","content":"` + text + `"}}` + "\n"
		if err := os.WriteFile(filepath.Join(root, sid+".jsonl"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("j00", "raised max_open_conns in opencode config and the pool stopped starving")
	if withLiteral {
		write("j06", "please review this code before I merge it")
	}
	dir := index.DefaultDir()
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	return dir
}

// README's CLI table listed substring matching beside AND-semantics and phrase
// matching, as though it were a property of matching. It is the close tier: a
// query that matches a word literally returns only that, and `code` stops
// finding `opencode` the moment any session contains the word `code` (#1089).
//
// The behaviour is the right one — a literal match should not drag in every
// longer word containing it — so this pins the behaviour and the sentence
// together.
func TestSubstringReachesItsWordOnlyWithoutAnExactMatch(t *testing.T) {
	// With a literal `code` in the store, `code` is not a zero-result query.
	_ = substringStore(t, true)
	out, err := captureRun(t, "search", "code")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "opencode config") {
		t.Errorf("`code` reached `opencode` even though the query matched a word literally:\n%s", out)
	}
	if !strings.Contains(out, "review this code") {
		t.Fatalf("`code` did not return its literal match, wrong fixture:\n%s", out)
	}
	// Both are indexed: the miss above is about matching, not about the store.
	if out, err := captureRun(t, "search", "opencode"); err != nil || !strings.Contains(out, "opencode config") {
		t.Fatalf("`opencode` is not searchable: %v\n%s", err, out)
	}

	// Without the literal, the same query falls through to the close tier and
	// the README's example works — which is the only state in which it does.
	_ = substringStore(t, false)
	out, err = captureRun(t, "search", "code")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "opencode config") {
		t.Errorf("zero-result `code` did not reach `opencode` through the close tier:\n%s", out)
	}
	if !strings.Contains(out, "close") {
		t.Errorf("the hit is not labelled as the close tier, so the README's framing would be right:\n%s", out)
	}
}

// The sentence has to keep saying which of the two it is. A README that
// promises substring matching outright is the defect this pins.
func TestREADMEDoesNotPromiseUnconditionalSubstringMatching(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("..", "..", "README.md"))
	if err != nil {
		t.Fatal(err)
	}
	readme := string(b)
	if strings.Contains(readme, "substrings match") {
		t.Errorf("README still lists substring matching as a matching property; " +
			"it is the close tier and only fires on a query with no exact match (#1089)")
	}
	// And it must still tell the reader the example works at all.
	if !strings.Contains(readme, "`code` finds `opencode`") {
		t.Errorf("README dropped the example instead of placing it in the fallback clause")
	}
}
