package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/model"
)

// deja keeps a session after the agent deletes it, and resume printed a
// command the agent then refused: codex answered "No saved session found"
// (#4185). An archived rollout is still codex's and still resumes.
func TestResumeRefusesASessionTheAgentDeleted(t *testing.T) {
	tmp := t.TempDir()
	home := filepath.Join(tmp, "home")
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	codexRoot := filepath.Join(tmp, "codex")
	day := filepath.Join(codexRoot, "sessions", "2026", "10", "01")
	if err := os.MkdirAll(day, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "claude"))
	t.Setenv("DEJA_CODEX_ROOT", codexRoot)
	t.Setenv("CODEX_HOME", codexRoot)
	t.Setenv("DEJA_OPENCODE_DB", filepath.Join(tmp, "opencode.db"))
	t.Setenv("DEJA_INDEX_DIR", filepath.Join(tmp, "index.db"))

	rollout := func(id, text string) string {
		p := filepath.Join(day, "rollout-2026-10-01T12-58-16-"+id+".jsonl")
		body := `{"timestamp":"2026-10-01T09:58:16Z","type":"session_meta","payload":{"id":"` + id + `","timestamp":"2026-10-01T09:58:16Z","cwd":"/w","originator":"codex_exec","cli_version":"0.149.0","source":"exec"}}` + "\n" +
			`{"timestamp":"2026-10-01T09:58:17Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"` + text + `"}]}}` + "\n"
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	deleted := "01a0f6e6-8cd3-72c2-84ff-367e6191e458"
	archived := "01a0f6e6-7f79-7703-9654-e9ac45b389fc"
	delPath := rollout(deleted, "pgbouncer prepared statements")
	arcPath := rollout(archived, "redis cluster ttl")

	var out bytes.Buffer
	if err := runResume(index.DefaultDir(), []string{deleted}, &out); err != nil || !strings.Contains(out.String(), "codex resume "+deleted) {
		t.Fatalf("before the delete: %q %v", out.String(), err)
	}

	if err := os.Remove(delPath); err != nil {
		t.Fatal(err)
	}
	arcDir := filepath.Join(codexRoot, "archived_sessions")
	if err := os.MkdirAll(arcDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(arcPath, filepath.Join(arcDir, filepath.Base(arcPath))); err != nil {
		t.Fatal(err)
	}

	out.Reset()
	err := runResume(index.DefaultDir(), []string{deleted}, &out)
	if err == nil || !strings.Contains(err.Error(), "deja show") || out.Len() != 0 {
		t.Errorf("a deleted session: out %q, err %v; want a refusal that points at deja show", out.String(), err)
	}
	out.Reset()
	if err := runResume(index.DefaultDir(), []string{archived}, &out); err != nil || !strings.Contains(out.String(), "codex resume "+archived) {
		t.Errorf("an archived session: out %q, err %v; want the resume command", out.String(), err)
	}
}

// opencode keeps a deleted session out of its database while deja keeps it
// searchable; the printed `opencode -s` then failed with "Session not found"
// (#4205).
func TestResumeRefusesAnOpencodeSessionDeletedInOpencode(t *testing.T) {
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skip("sqlite3 not installed")
	}
	tmp := hermeticEnv(t)
	db := filepath.Join(tmp, "opencode.db")
	script := `create table session(id text, directory text, time_created any, time_updated any);
create table message(id text, session_id text, time_created any, data text);
create table part(id text, message_id text, data text);
insert into session values('ses_kept','/work/app','2026-01-02T03:00:00Z','2026-01-02T03:00:00Z');
insert into message values('m1','ses_kept',1,'{"role":"user"}');
insert into part values('p1','m1','{"type":"text","text":"hello"}');`
	if out, err := exec.Command("sqlite3", db, script).CombinedOutput(); err != nil {
		t.Fatalf("seed: %v %s", err, out)
	}
	t.Setenv("DEJA_OPENCODE_DB", db)

	gone := model.Session{Harness: "opencode", ID: "ses_deleted", Path: "/work/app"}
	if err := resumeGoneError(gone); err == nil || !strings.Contains(err.Error(), "deja show") {
		t.Fatalf("a session opencode no longer has was offered for resume: %v", err)
	}
	kept := model.Session{Harness: "opencode", ID: "ses_kept", Path: "/work/app"}
	if err := resumeGoneError(kept); err != nil {
		t.Fatalf("a session still in opencode was refused: %v", err)
	}
	// No store to ask: nothing is refused on a guess.
	t.Setenv("DEJA_OPENCODE_DB", filepath.Join(tmp, "missing.db"))
	if err := resumeGoneError(gone); err != nil {
		t.Fatalf("refused with no store to ask: %v", err)
	}
}
