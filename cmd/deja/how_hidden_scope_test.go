package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/policy"
)

// "the trust policy hides N matching sessions" counted every withheld command
// record in the store — before the terms were checked and before --project.
// So asking about terraform on a machine whose hidden sessions only ever ran
// `go test` was told three matching sessions were being kept from it, and an
// agent reading that has been handed a reason to distrust an honest answer
// (#2766).
func TestHowCountsOnlyWhatTheQuestionAsked(t *testing.T) {
	tmp := hermeticEnv(t)
	claude := filepath.Join(tmp, "claude")
	proj := filepath.Join(claude, "-app")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	// One hidden session, and what it ran has nothing to do with the question
	// asked below.
	body := `{"type":"user","sessionId":"sess-one","cwd":"/app","timestamp":"2026-10-01T09:00:00Z","message":{"role":"user","content":"run the suite"}}
{"type":"assistant","sessionId":"sess-one","cwd":"/app","timestamp":"2026-10-01T09:01:00Z","message":{"role":"assistant","content":[{"type":"tool_use","name":"Bash","input":{"command":"go test ./..."}}]}}
{"type":"assistant","sessionId":"sess-one","cwd":"/app","timestamp":"2026-10-01T09:02:00Z","message":{"role":"assistant","content":[{"type":"tool_use","name":"Bash","input":{"command":"go vet ./..."}}]}}
`
	if err := os.WriteFile(filepath.Join(proj, "sess-one.jsonl"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_CLAUDE_ROOT", claude)

	cfg := filepath.Join(tmp, "config", "deja")
	if err := os.MkdirAll(cfg, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cfg, "policy.json"),
		[]byte(`{"activations":{"search":{"local":false}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(tmp, "config"))
	if policy.Load().Allows(policy.ActivationSearch, "app") {
		t.Fatal("the policy did not take effect, so this test would pass for the wrong reason")
	}

	dir := filepath.Join(tmp, "index.db")
	if err := index.Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}

	// Asked about something the hidden session never ran.
	_, hidden, _, err := howEntries(dir, []string{"terraform"}, "", policy.ActivationSearch)
	if err != nil {
		t.Fatal(err)
	}
	if hidden != 0 {
		t.Errorf("said %d sessions matching \"terraform\" are hidden; the hidden session ran go test", hidden)
	}

	// Asked about what it did run, the count has to survive — a scoped number
	// that is always zero would pass the check above and hide a real leak.
	_, hidden, _, err = howEntries(dir, []string{"vet"}, "", policy.ActivationSearch)
	if err != nil {
		t.Fatal(err)
	}
	if hidden == 0 {
		t.Error("a command the question does match was withheld with nothing said")
	}
}
