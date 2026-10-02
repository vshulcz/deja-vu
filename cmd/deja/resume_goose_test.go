package main

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/model"
)

// A session goose deleted stays searchable in deja, and the printed
// `goose session --resume --session-id <id>` then failed with "Cannot resume
// session … no such session exists" (#4271) — the opencode case of #4205.
func TestResumeRefusesAGooseSessionDeletedInGoose(t *testing.T) {
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skip("sqlite3 not installed")
	}
	tmp := hermeticEnv(t)
	db := filepath.Join(tmp, "sessions.db")
	script := `create table sessions(id text primary key, name text, working_dir text not null);
create table messages(id integer primary key, session_id text, role text, content_json text);
insert into sessions values('20261001_1','hunt','/tmp/proj');
insert into messages values(1,'20261001_1','user','[{"type":"text","text":"hello"}]');`
	if out, err := exec.Command("sqlite3", db, script).CombinedOutput(); err != nil {
		t.Fatalf("seed: %v %s", err, out)
	}
	gone := model.Session{Harness: "goose", ID: "20261001_2", Path: db}
	if err := resumeGoneError(gone); err == nil || !strings.Contains(err.Error(), "deja show") {
		t.Fatalf("a session goose no longer has was offered for resume: %v", err)
	}
	kept := model.Session{Harness: "goose", ID: "20261001_1", Path: db}
	if err := resumeGoneError(kept); err != nil {
		t.Fatalf("a session still in goose was refused: %v", err)
	}
	// No store to ask: nothing is refused on a guess.
	missing := model.Session{Harness: "goose", ID: "20261001_2", Path: filepath.Join(tmp, "missing.db")}
	if err := resumeGoneError(missing); err != nil {
		t.Fatalf("refused with no store to ask: %v", err)
	}
}
