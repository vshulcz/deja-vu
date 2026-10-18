package index

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/vshulcz/deja-vu/internal/query"
)

// A session forgotten here was forgotten under its own id. Import checked only
// the imported id, so a peer's copy walked the same text back into local search
// under a new one while `forget --list` still called it forgotten (#968).
func TestImportLeavesOutSessionsThisMachineForgot(t *testing.T) {
	tmp := t.TempDir()
	setHome(t, filepath.Join(tmp, "home"))
	t.Setenv("USERPROFILE", filepath.Join(tmp, "home"))
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(tmp, "config"))
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "claude"))
	t.Setenv("DEJA_NOTES_FILE", filepath.Join(tmp, "notes.jsonl"))

	store := filepath.Join(tmp, "claude", "proj-p")
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	rec := `{"type":"user","sessionId":"shared","timestamp":"2026-07-11T10:00:00Z","cwd":"/p","message":{"role":"user","content":"the secret I decided to forget"}}` + "\n"
	if err := os.WriteFile(filepath.Join(store, "shared.jsonl"), []byte(rec), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(tmp, "index.db")
	if err := Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := Forget(dir, ForgetOptions{Session: "shared"}); err != nil {
		t.Fatal(err)
	}

	exp := filepath.Join(tmp, "transfer")
	if err := os.MkdirAll(exp, 0o755); err != nil {
		t.Fatal(err)
	}
	var batch []byte
	for _, r := range []SyncRecord{
		{Harness: "claude", SessionID: "shared", Project: "p", Role: "user", Text: "the secret I decided to forget"},
		{Harness: "claude", SessionID: "fresh", Project: "p", Role: "user", Text: "a peer session about the ticker"},
	} {
		b, err := json.Marshal(r)
		if err != nil {
			t.Fatal(err)
		}
		batch = append(batch, append(b, '\n')...)
	}
	if err := os.WriteFile(filepath.Join(exp, "deja-sync-x.jsonl"), batch, 0o644); err != nil {
		t.Fatal(err)
	}

	added, err := Import(dir, exp)
	if err != nil {
		t.Fatal(err)
	}
	if added != 1 {
		t.Errorf("imported %d records, want 1 — the forgotten one must stay out", added)
	}
	if n := ImportSkippedForgotten(); n != 1 {
		t.Errorf("reported %d records left out, want 1", n)
	}
	ss, err := Search(dir, query.Options{Query: "secret I decided to forget", All: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(ss) != 0 {
		t.Errorf("forgotten text came back through a peer's batch: %d sessions", len(ss))
	}
	// What the peer has that this machine never forgot still arrives.
	ss, err = Search(dir, query.Options{Query: "peer session about the ticker", All: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(ss) == 0 {
		t.Error("a fresh peer session was dropped too")
	}
}

// A Copilot CLI sub-agent run has no tombstone of its own: its parent's covers
// it. A peer's copy of the run names the parent, and stays out with it (#5216).
func TestImportLeavesOutTheRunOfAForgottenCopilotParent(t *testing.T) {
	tmp := t.TempDir()
	setHome(t, filepath.Join(tmp, "home"))
	t.Setenv("USERPROFILE", filepath.Join(tmp, "home"))
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(tmp, "config"))
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "claude"))
	t.Setenv("DEJA_COPILOT_ROOT", filepath.Join(tmp, "copilot"))
	t.Setenv("DEJA_NOTES_FILE", filepath.Join(tmp, "notes.jsonl"))
	const parent, child = "f67048ad-042c-42f4-ac33-5db51c4d48b0", "717532a2-bf69-4164-be09-03daacd1762f"
	store := filepath.Join(tmp, "copilot", parent)
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	log := `{"type":"session.start","data":{"sessionId":"` + parent + `","startTime":"2026-10-18T05:31:46.707Z","context":{"cwd":"/p/app"}},"timestamp":"2026-10-18T05:31:46.707Z"}` + "\n" +
		`{"type":"user.message","data":{"content":"the parent turn"},"timestamp":"2026-10-18T05:31:47.305Z"}` + "\n"
	if err := os.WriteFile(filepath.Join(store, "events.jsonl"), []byte(log), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(tmp, "index.db")
	if err := Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := Forget(dir, ForgetOptions{Session: parent}); err != nil {
		t.Fatal(err)
	}
	exp := filepath.Join(tmp, "transfer")
	if err := os.MkdirAll(exp, 0o755); err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(SyncRecord{Harness: "copilot", SessionID: child, Project: "p/app", Role: "user", Text: "the helper's grackleplume draft", Parent: parent})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(exp, "deja-sync-x.jsonl"), append(b, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
	if added, err := Import(dir, exp); err != nil || added != 0 {
		t.Errorf("imported %d records (%v), want the forgotten parent's run left out", added, err)
	}
}
