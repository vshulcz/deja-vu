package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/index"
)

func seedDecision(t *testing.T, project, id, text string) {
	t.Helper()
	store := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-"+project)
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	rec := `{"type":"user","message":{"role":"user","content":"` + text + `"},"timestamp":"2026-07-12T10:00:00Z","sessionId":"` + id + `","cwd":"/` + project + `"}` + "\n"
	if err := os.WriteFile(filepath.Join(store, id+".jsonl"), []byte(rec), 0o644); err != nil {
		t.Fatal(err)
	}
}

// projectConventions returns only the accepted notes for the named project, and
// excludes rejected notes and other projects' decisions.
func TestProjectConventionsFiltersToAcceptedAndProject(t *testing.T) {
	tmp := hermeticEnv(t)
	dir := filepath.Join(tmp, "index.db")

	seedDecision(t, "proj", "keep", "we chose pgx over database/sql keepmarker")
	seedDecision(t, "proj", "gone", "we tried the ORM but dropped it gonemarker")
	seedDecision(t, "other", "elsewhere", "unrelated project decision othermarker")
	if err := index.Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRunStderr(t, "promote", "keep"); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRunStderr(t, "promote", "gone", "--state", "rejected", "--note", "backed out"); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRunStderr(t, "promote", "elsewhere"); err != nil {
		t.Fatal(err)
	}

	out := projectConventions([]string{"proj"}, 6, 800)
	if !strings.Contains(out, "keepmarker") {
		t.Fatalf("accepted decision for the project was not surfaced:\n%s", out)
	}
	if strings.Contains(out, "gonemarker") {
		t.Fatalf("a rejected decision leaked into the standing block:\n%s", out)
	}
	if strings.Contains(out, "othermarker") {
		t.Fatalf("another project's decision leaked in:\n%s", out)
	}
	// A project with nothing accepted gets no block at all.
	if s := projectConventions([]string{"other"}, 6, 800); strings.Contains(s, "keepmarker") {
		t.Fatalf("proj decision leaked into other's block:\n%s", s)
	}
}

// The standing-decisions block surfaces a settled decision that the reactive
// session digest drops. Without a task signal the digest loads only the few
// newest sessions per project, so an older decision falls outside it — yet it is
// exactly the kind an agent must not re-litigate. The convention block carries
// it regardless of how many newer sessions buried it.
func TestSessionStartSurfacesADecisionTheDigestDropped(t *testing.T) {
	tmp := hermeticEnv(t)
	dir := filepath.Join(tmp, "index.db")
	t.Setenv("CLAUDE_PROJECT_DIR", "/proj")

	// The decision is the oldest; three newer sessions push it out of the
	// recency-limited digest window.
	seedDecision(t, "proj", "decision", "we standardised on pgx not database/sql pgxdecisionmarker")
	for _, s := range []struct{ id, text string }{
		{"newer1", "tweak the landing page copy newer1marker"},
		{"newer2", "bump the linter version newer2marker"},
		{"newer3", "rename a test helper newer3marker"},
	} {
		store := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-proj")
		rec := `{"type":"user","message":{"role":"user","content":"` + s.text + `"},"timestamp":"2026-07-20T10:00:00Z","sessionId":"` + s.id + `","cwd":"/proj"}` + "\n"
		if err := os.WriteFile(filepath.Join(store, s.id+".jsonl"), []byte(rec), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := index.Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRunStderr(t, "promote", "decision"); err != nil {
		t.Fatal(err)
	}

	text, _, _, _, _ := hookDigestResult(dir)
	if !strings.Contains(text, "standing decisions in this project") {
		t.Fatalf("no standing-decisions block was injected:\n%s", text)
	}
	marker := "pgxdecisionmarker"
	idx := strings.Index(text, "standing decisions in this project")
	convBlock := text[idx:]
	rest := text[:idx]
	if !strings.Contains(convBlock, marker) {
		t.Fatalf("the decision was not in the standing block:\n%s", text)
	}
	// The proof that this closes a real gap: the reactive digest did not carry
	// the decision on its own — only the convention block did.
	if strings.Contains(rest, marker) {
		t.Fatalf("the decision was already in the digest; the block proves nothing here:\n%s", text)
	}
}

// Standing decisions are scoped by the same trust policy as the session digest:
// a project whose origin the policy withholds from auto-activation must not have
// its decisions injected either.
func TestStandingDecisionsRespectTheTrustPolicy(t *testing.T) {
	tmp := hermeticEnv(t)
	dir := filepath.Join(tmp, "index.db")
	t.Setenv("CLAUDE_PROJECT_DIR", "/proj")

	seedDecision(t, "proj", "decision", "we standardised on pgx pgxpolicymarker")
	if err := index.Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRunStderr(t, "promote", "decision"); err != nil {
		t.Fatal(err)
	}
	// Deny local memory for auto-activation.
	writePolicyFile(t, `{"activations":{"auto":{"local":false}}}`)

	text, _, _, _, _ := hookDigestResult(dir)
	if strings.Contains(text, "pgxpolicymarker") {
		t.Fatalf("a policy-denied project's decision was injected into the hook:\n%s", text)
	}
}
