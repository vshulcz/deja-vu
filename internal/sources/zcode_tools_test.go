package sources

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// ZCode's CLI database is OpenCode's schema, but its tool parts carry Claude
// Code's names and arguments: Bash {command}, Read {file_path}, Edit
// {file_path, old_string, new_string}, Write {file_path, content}. The reader
// matched only opencode's lowercase read/bash/apply_patch, so a ZCode session
// was text alone. The parts are the ones the ZCode 3.14.4 runtime wrote, the
// first Edit refused because the file had not been read yet (#4428).
func TestZCodeToolPartsAreIndexed(t *testing.T) {
	if !SQLite3Available() {
		t.Skip("sqlite3 CLI not installed")
	}
	home := t.TempDir()
	db := filepath.Join(home, "db.sqlite")
	t.Setenv("DEJA_ZCODE_DB", db)
	t.Setenv("DEJA_ZCODE_ROOT", filepath.Join(home, "absent"))

	schema := `create table session (id text primary key, directory text, time_created integer, time_updated integer);
create table message (id text primary key, session_id text, time_created integer, data text);
create table part (id text primary key, message_id text, data text);
insert into session values ('sess_1', '/private/tmp/proj-zcode', 1790897586000, 1790897657000);
insert into message values ('m1', 'sess_1', 1790897586942, '{"role":"user","time":{"created":1790897586942}}');
insert into message values ('m2', 'sess_1', 1790897587684, '{"role":"assistant","time":{"created":1790897587684}}');
insert into part values ('p1', 'm1', '{"type":"text","text":"Fix the retry loop in retry.cfg.","time":{"start":1790897586942}}');
insert into part values ('p2', 'm2', '{"type":"tool","callID":"call_27e245a42ff9","declarationIndex":0,"tool":"Bash","state":{"status":"completed","input":{"command":"cat retry.cfg && git status --short"},"output":"retries = 3\nmax_backoff_seconds = 30  # cap for the retry loop","title":"Bash","time":{"start":1790897587741,"end":1790897587773}}}');
insert into part values ('p3', 'm2', '{"type":"tool","callID":"call_f2a9b5330b2d","declarationIndex":0,"tool":"Edit","state":{"status":"error","input":{"file_path":"/tmp/proj-zcode/retry.cfg","old_string":"max_backoff_seconds = 30  # refused before the read","new_string":"max_backoff_seconds = 120  # refused before the read"},"error":"File has not been read yet. Read it first before writing to it.","time":{"start":1790897616631,"end":1790897616631}}}');
insert into part values ('p4', 'm2', '{"type":"tool","callID":"call_85d449f3279f","declarationIndex":0,"tool":"Read","state":{"status":"completed","input":{"file_path":"/tmp/proj-zcode/retry.cfg"},"output":"1\tretries = 3","title":"Read","time":{"start":1790897650138,"end":1790897650207}}}');
insert into part values ('p5', 'm2', '{"type":"tool","callID":"call_094947cf9a78","declarationIndex":0,"tool":"Edit","state":{"status":"completed","input":{"file_path":"/tmp/proj-zcode/retry.cfg","old_string":"max_backoff_seconds = 30  # cap for the retry loop","new_string":"max_backoff_seconds = 120  # cap for the retry loop"},"output":"The file has been updated successfully.","title":"Edit","time":{"start":1790897654455,"end":1790897654465}}}');
insert into part values ('p6', 'm2', '{"type":"tool","callID":"call_w","declarationIndex":0,"tool":"Write","state":{"status":"completed","input":{"file_path":"/tmp/proj-zcode/NOTES.md","content":"backoff doubles on every failed attempt\n"},"title":"Write","time":{"start":1790897655000,"end":1790897655010}}}');
`
	if out, err := exec.Command("sqlite3", db, schema).CombinedOutput(); err != nil {
		t.Fatalf("sqlite3 seed: %v %s", err, out)
	}
	ss, err := ParseZCodeDB(db)
	if err != nil || len(ss) != 1 {
		t.Fatalf("parse: %v, %d sessions", err, len(ss))
	}
	s := ss[0]
	if cmds := rolesOf(s, RoleCommand); len(cmds) != 1 || !strings.Contains(cmds[0], "git status --short") {
		t.Errorf("commands = %q, want the Bash call", cmds)
	}
	if out := strings.Join(rolesOf(s, RoleToolOutput), "\n"); !strings.Contains(out, "max_backoff_seconds = 30") {
		t.Errorf("tool output = %q, want what cat printed", out)
	}
	if files := strings.Join(rolesOf(s, RoleFiles), "\n"); !strings.Contains(files, "/tmp/proj-zcode/retry.cfg") {
		t.Errorf("files = %q, want the Read's file", files)
	}
	edits := strings.Join(rolesOf(s, RoleEdit), "\n")
	if !strings.Contains(edits, "/tmp/proj-zcode/retry.cfg\nmax_backoff_seconds = 30  # cap for the retry loop") {
		t.Errorf("edits = %q, want the replaced line", edits)
	}
	if strings.Contains(edits, "refused") {
		t.Errorf("edits = %q: the refused Edit changed nothing", edits)
	}
	wrote := rolesOf(s, RoleWrote)
	if !wroteHas(t, wrote, "/tmp/proj-zcode/NOTES.md", "backoff doubles on every failed attempt") {
		t.Errorf("wrote = %q, want the Write's content", wrote)
	}
	if !wroteHas(t, wrote, "/tmp/proj-zcode/retry.cfg", "max_backoff_seconds = 120  # cap for the retry loop") {
		t.Errorf("wrote = %q, want the Edit's new text", wrote)
	}
}

// ZCode heads its output with "Exit code N" only for a failed run with a
// non-zero code (isBashProviderErrorStatus); a clean run's output that opens
// "Exit code 0" is the command's own and stays whole.
func TestZCodeExitZeroLineIsOutput(t *testing.T) {
	db := vocabSQL(t, `create table session(id text primary key, directory text, time_created integer, time_updated integer);
create table message(id text, session_id text, time_created integer, data text);
create table part(id text, message_id text, data text);
insert into part values ('p1','m1','{"type":"tool","tool":"Bash","callID":"p1","state":{"status":"completed","input":{"command":"make check"},"output":"Exit code 0\nall checks passed","time":{"start":1790000002000}}}');
insert into message values ('m1','s1',1790000001000,'{"role":"assistant","time":{"created":1790000001000}}');
insert into session values ('s1','/tmp/proj',1790000000000,1790000100000);
`)
	ss := parseKindForTest(t, "zcode-db", db)
	if len(ss) != 1 {
		t.Fatalf("%d sessions", len(ss))
	}
	got := [2]string{strings.Join(rolesOf(ss[0], RoleCommand), "|"), strings.Join(rolesOf(ss[0], RoleToolOutput), "|")}
	want := [2]string{"$ make check", "Exit code 0\nall checks passed"}
	if got != want {
		t.Errorf("command, output = %q, want %q", got, want)
	}
}
