package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/index"
)

// Nobody names the session in `deja handoff` with no id — deja chooses it, the
// way a listing does, and a listing obeys the trust policy (#937). Without the
// gate the pick landed on a session the rule keeps out of recall and packaged
// its content for another agent (#953).
func TestHandoffWithNoIdObeysTheTrustPolicy(t *testing.T) {
	tmp := hermeticEnv(t)
	store := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-proj")
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	rec := `{"type":"user","message":{"role":"user","content":"local work on the ticker window"},"timestamp":"2026-07-01T10:00:00Z","sessionId":"loc","cwd":"/proj"}` + "\n"
	if err := os.WriteFile(filepath.Join(store, "loc.jsonl"), []byte(rec), 0o644); err != nil {
		t.Fatal(err)
	}
	exp := filepath.Join(tmp, "transfer")
	if err := os.MkdirAll(exp, 0o755); err != nil {
		t.Fatal(err)
	}
	// Newer than the local one, so it wins any recency contest.
	// Project "work/proj" under a directory of the same shape: it answers to
	// two of the name candidates the directory yields, which is what the
	// count has to survive (#981).
	b, err := json.Marshal(index.SyncRecord{Harness: "claude", SessionID: "peer", Project: "work/proj", Role: "user", Text: "PEER SECRET: the vault rotation runs at 03:00"})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(exp, "deja-sync-x.jsonl"), append(b, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(tmp, "index.db")
	if err := index.Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRun(t, "sync", "import", exp); err != nil {
		t.Fatal(err)
	}
	// handoff takes the project from the working directory, so the test has to
	// stand in one whose name matches.
	work := filepath.Join(tmp, "work", "proj")
	if err := os.MkdirAll(work, 0o755); err != nil {
		t.Fatal(err)
	}
	back, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(work); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(back) })

	policyFile := filepath.Join(tmp, "policy.json")
	if err := os.WriteFile(policyFile, []byte(`{"activations":{"search":{"local":true,"imported":false}}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_POLICY_FILE", policyFile)

	s, err := handoffSource(dir, "")
	if err != nil {
		t.Fatalf("handoff found nothing to hand off: %v", err)
	}
	if strings.HasPrefix(s.Project, "imported:") {
		t.Errorf("handoff picked a session the policy hides: %s · %s", s.Project, s.ID)
	}

	// With the local session gone, every candidate is hidden: the reader gets
	// the rule, not someone else's work.
	if err := os.Remove(filepath.Join(store, "loc.jsonl")); err != nil {
		t.Fatal(err)
	}
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	_, err = handoffSource(dir, "")
	if err == nil || !strings.Contains(err.Error(), "trust policy") {
		t.Errorf("with everything hidden, handoff said: %v", err)
	}
	// One session is hidden here, and a directory answers to several project
	// names — the count is the only thing telling the reader how much the rule
	// holds back, and per-pass addition named more than the project holds
	// (#981).
	if err != nil && !strings.Contains(err.Error(), "hides 1 matching session ") {
		t.Errorf("the count is not what the project holds: %v", err)
	}
}

// deja's own pick changes meaning when a rule removed newer work from the
// choice: handing over a month-old session while today's is withheld reads
// exactly like a project nobody has touched since (#1013).
func TestHandoffSaysWhenNewerWorkIsWithheld(t *testing.T) {
	tmp := hermeticEnv(t)
	store := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-proj")
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	rec := `{"type":"user","message":{"role":"user","content":"the older local session about the pool cap"},"timestamp":"2026-07-01T10:00:00Z","sessionId":"old","cwd":"/proj"}` + "\n"
	if err := os.WriteFile(filepath.Join(store, "old.jsonl"), []byte(rec), 0o644); err != nil {
		t.Fatal(err)
	}
	exp := filepath.Join(tmp, "transfer")
	if err := os.MkdirAll(exp, 0o755); err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(index.SyncRecord{Harness: "claude", SessionID: "newpeer", Project: "proj", Role: "user",
		Text: "the newest work on this project, from another machine", Time: time.Date(2026, 8, 1, 10, 0, 0, 0, time.UTC)})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(exp, "deja-sync-x.jsonl"), append(b, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(tmp, "index.db")
	if err := index.Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRun(t, "sync", "import", exp); err != nil {
		t.Fatal(err)
	}
	work := filepath.Join(tmp, "proj")
	if err := os.MkdirAll(work, 0o755); err != nil {
		t.Fatal(err)
	}
	back, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(work); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(back) })

	policyFile := filepath.Join(tmp, "policy.json")
	if err := os.WriteFile(policyFile, []byte(`{"activations":{"search":{"local":true,"imported":false}}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_POLICY_FILE", policyFile)

	note, err := captureRunStderr(t, "handoff")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(note, "newer work in this project is withheld") {
		t.Errorf("handoff packaged older work without saying newer was withheld:\n%s", note)
	}

	// Without the rule the newest session is the pick, and there is nothing to
	// warn about.
	t.Setenv("DEJA_POLICY_FILE", filepath.Join(tmp, "no-policy.json"))
	note, err = captureRunStderr(t, "handoff")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(note, "withheld") {
		t.Errorf("an unrestricted handoff warned about withheld work:\n%s", note)
	}
}
