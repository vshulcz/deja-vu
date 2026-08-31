package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/index"
)

// The sentence says the trust policy "hides N matching sessions", and the
// count was taken before the terms were matched — so a question about
// terraform on a machine whose hidden sessions only ever ran `go test` was
// told three of them were hidden from the answer (#2766).
func TestHowCountsTheHiddenSessionsThatMatchTheQuestion(t *testing.T) {
	tmp := hermeticEnv(t)
	store := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-api")
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	seed := func(name, id, day, command string) {
		t.Helper()
		line := `{"type":"assistant","timestamp":"2026-07-` + day + `T10:00:00Z","sessionId":"` + id +
			`","cwd":"/api","message":{"role":"assistant","content":[{"type":"tool_use","name":"Bash","input":{"command":"` + command + `"}}]}}`
		if err := os.WriteFile(filepath.Join(store, name), []byte(line+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	seed("one.jsonl", "aaaa0001-1111-4000-8000-d6e7f8a9b0c1", "10", "go test ./...")
	seed("two.jsonl", "bbbb0002-1111-4000-8000-d6e7f8a9b0c1", "11", "go test ./internal/...")
	seed("three.jsonl", "cccc0003-1111-4000-8000-d6e7f8a9b0c1", "12", "terraform apply -auto-approve")

	dir := filepath.Join(tmp, "index.db")
	if err := index.Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	pol := filepath.Join(tmp, "policy.json")
	if err := os.WriteFile(pol, []byte(`{"activations":{"search":{"*":false}}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_POLICY_FILE", pol)

	out, err := captureRun(t, "how", "terraform")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "hides 1 matching session") {
		t.Errorf("one hidden session ran terraform; the note says otherwise:\n%s", out)
	}
	if strings.Contains(out, "hides 3") {
		t.Errorf("sessions that ran something else were counted as matching:\n%s", out)
	}

	// And a question nothing hidden answers is not told anything was hidden
	// from it.
	out, err = captureRun(t, "how", "kubectl")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "hides") {
		t.Errorf("a question no hidden session answers was told the policy withheld something:\n%s", out)
	}
}

// The ignore rule moved with the trust policy and says the same kind of thing
// — "keeps N sessions out of this answer" — so it is counted the same way:
// against what was asked, not against the store.
func TestHowCountsTheIgnoredSessionsThatMatchTheQuestion(t *testing.T) {
	tmp := hermeticEnv(t)
	store := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-api")
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	seed := func(name, id, day, command string) {
		t.Helper()
		line := `{"type":"assistant","timestamp":"2026-07-` + day + `T10:00:00Z","sessionId":"` + id +
			`","cwd":"/api","message":{"role":"assistant","content":[{"type":"tool_use","name":"Bash","input":{"command":"` + command + `"}}]}}`
		if err := os.WriteFile(filepath.Join(store, name), []byte(line+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	seed("one.jsonl", "aaaa0001-1111-4000-8000-d6e7f8a9b0c1", "10", "go test ./...")
	seed("two.jsonl", "cccc0003-1111-4000-8000-d6e7f8a9b0c1", "12", "terraform apply -auto-approve")

	dir := filepath.Join(tmp, "index.db")
	if err := index.Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	pol := filepath.Join(tmp, "policy.json")
	body := `{"ignore":["` + strings.ReplaceAll(store, `\`, `\\`) + `/*"]}`
	if err := os.WriteFile(pol, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_POLICY_FILE", pol)

	// The ignore note goes to stderr, beside the answer rather than in it.
	out, err := captureRunStderr(t, "how", "terraform")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "keeps 1 session") {
		t.Errorf("one ignored session ran terraform; the note says otherwise:\n%s", out)
	}
	out, err = captureRunStderr(t, "how", "kubectl")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "ignore rule keeps") {
		t.Errorf("a question no ignored session answers was told the rule withheld something:\n%s", out)
	}
}
