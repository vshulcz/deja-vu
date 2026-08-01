package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/policy"
	"github.com/vshulcz/deja-vu/internal/search"
)

// "Nothing matched" and "a rule hides it on this path" are different facts,
// and only the second is something the reader can act on (#680).
func TestPolicyHiddenNoteNamesThePolicy(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "policy.json")
	t.Setenv("DEJA_POLICY_FILE", path)
	if err := os.WriteFile(path, []byte(`{"activations":{"search":{"local":false}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	note := policyHiddenNote(policy.ActivationSearch, 3)
	for _, want := range []string{"3 matching sessions", "search", "deny local", path} {
		if !strings.Contains(note, want) {
			t.Errorf("note %q does not mention %q", note, want)
		}
	}
	// Nothing hidden, nothing to say: a line on every empty result is noise.
	if got := policyHiddenNote(policy.ActivationSearch, 0); got != "" {
		t.Errorf("said %q with nothing hidden", got)
	}
}

// An agent told "no prior sessions matched" concludes the work was never done
// and starts over.
func TestEmptyRecallAnswerNamesThePolicy(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("DEJA_POLICY_FILE", filepath.Join(dir, "policy.json"))
	if err := os.WriteFile(filepath.Join(dir, "policy.json"), []byte(`{"activations":{"mcp":{"local":false}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	hidden := emptyRecallAnswerPolicy(dir, "connection pool", 2)
	for _, want := range []string{"2 prior sessions matched", "trust policy", "not being shown"} {
		if !strings.Contains(hidden, want) {
			t.Errorf("answer %q does not mention %q", hidden, want)
		}
	}
	if plain := emptyRecallAnswerPolicy(dir, "moria", 0); !strings.Contains(plain, "No prior deja sessions matched") {
		t.Errorf("an ordinary miss said %q", plain)
	}
}

func TestPolicyFilterHitsCounts(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "policy.json")
	t.Setenv("DEJA_POLICY_FILE", path)
	if err := os.WriteFile(path, []byte(`{"activations":{"search":{"imported":false}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	hits := []search.Hit{
		{Session: model.Session{Project: "local-one"}},
		{Session: model.Session{Project: "imported:peer/p"}},
		{Session: model.Session{Project: "imported:other/p"}},
	}
	kept, hidden := policyFilterHitsCounted(policy.ActivationSearch, hits)
	if len(kept) != 1 || hidden != 2 {
		t.Fatalf("kept %d hidden %d, want 1 and 2", len(kept), hidden)
	}
	if kept[0].Session.Project != "local-one" {
		t.Errorf("kept the wrong one: %q", kept[0].Session.Project)
	}
}
