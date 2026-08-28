package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// `deja sources` subtracts excluded sessions from every store's row and says
// how many it dropped. opencode's row was counted straight out of sqlite, so
// the one store whose count came from SQL kept reporting sessions that are not
// indexed, not searchable and not exported (#2247).
func TestSourcesCountsOpencodeWithoutExcludedProjects(t *testing.T) {
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skip("sqlite3 not installed")
	}
	tmp := t.TempDir()
	db := filepath.Join(tmp, "opencode.db")
	script := `create table session(id text, directory text, time_created any, time_updated any);
create table message(id text, session_id text, time_created any, data text);
create table part(id text, message_id text, data text);
insert into session values('s1','/work/keep','2026-01-02T03:00:00Z','2026-01-02T03:00:00Z');
insert into session values('s2','/work/secret','2026-01-02T03:00:00Z','2026-01-02T03:00:00Z');
insert into message values('m1','s1',1,'{"role":"user"}');
insert into message values('m2','s2',1,'{"role":"user"}');
insert into part values('p1','m1','{"type":"text","text":"hello from keep"}');
insert into part values('p2','m2','{"type":"text","text":"hello from secret"}');`
	if out, err := exec.Command("sqlite3", db, script).CombinedOutput(); err != nil {
		t.Fatalf("seed: %v %s", err, out)
	}

	home := filepath.Join(tmp, "home")
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("DEJA_OPENCODE_DB", db)
	t.Setenv("DEJA_CODEX_ROOT", filepath.Join(tmp, "codex"))
	t.Setenv("DEJA_NOTES_FILE", filepath.Join(tmp, "notes.jsonl"))

	// A claude session in the same excluded project, as the row to compare
	// against: that store has always subtracted.
	claude := filepath.Join(tmp, "claude")
	proj := filepath.Join(claude, "-work-secret")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_CLAUDE_ROOT", claude)
	at := time.Now().Add(-time.Hour).UTC().Format(time.RFC3339)
	line := fmt.Sprintf(`{"type":"user","sessionId":"c1","timestamp":%q,"cwd":"/work/secret",`+
		`"message":{"role":"user","content":"hello from secret"}}`, at)
	if err := os.WriteFile(filepath.Join(proj, "c1.jsonl"), []byte(line+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_EXCLUDE_PROJECTS", "secret")

	out := captureStdout(t, func() { printSources(filepath.Join(tmp, "index.db")) })
	row := func(name string) string {
		for _, l := range strings.Split(out, "\n") {
			if strings.HasPrefix(l, name+"\t") {
				return l
			}
		}
		t.Fatalf("no %s row in:\n%s", name, out)
		return ""
	}

	// The premise: the store that has always subtracted still does, or there
	// is nothing to compare against.
	if got := row("claude"); !strings.Contains(got, "sessions=0") || !strings.Contains(got, "excluded-sessions=1") {
		t.Fatalf("claude row does not show the exclusion applied: %s", got)
	}
	if got := row("opencode"); !strings.Contains(got, "sessions=1 messages=1") {
		t.Errorf("one of two opencode sessions is excluded, the row says: %s", got)
	}
	if got := row("opencode"); !strings.Contains(got, "excluded-sessions=1") {
		t.Errorf("the opencode row does not say what it dropped: %s", got)
	}
}
