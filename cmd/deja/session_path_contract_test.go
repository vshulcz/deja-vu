package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// docs/json-output.md is the published contract for the session fields, and it
// says `path` is the file the session was read from. opencode's is the project
// directory instead (#2033), so the sentence has to name that (#2077).
func TestTheDocumentedPathNamesTheOpencodeException(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("..", "..", "docs", "json-output.md"))
	if err != nil {
		t.Fatal(err)
	}
	line := ""
	for _, l := range strings.Split(string(b), "\n") {
		if strings.HasPrefix(strings.TrimSpace(l), "| `path` |") {
			line = strings.TrimSpace(l)
			break
		}
	}
	if line == "" {
		t.Fatal("docs/json-output.md no longer describes `path`, so this pins nothing")
	}
	if !strings.Contains(line, "opencode") {
		t.Errorf("the contract does not say opencode's path is its project directory: %s", line)
	}
}

// And the behaviour the sentence describes, measured rather than assumed: one
// harness hands back a directory where the others hand back a file.
func TestTheOpencodePathIsAProjectDirectory(t *testing.T) {
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skip("sqlite3 CLI not available")
	}
	hermeticEnv(t)
	tmp := t.TempDir()
	db := filepath.Join(tmp, "opencode.db")
	t.Setenv("DEJA_OPENCODE_DB", db)
	proj := filepath.Join(tmp, "app")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	sql := `create table session (id text primary key, directory text, time_created integer, time_updated integer);
create table message (id text primary key, session_id text, data text, time_created integer);
create table part (id text primary key, message_id text, data text);
insert into session values ('s1','` + proj + `',1767268800000,1767268800000);
insert into message values ('m1','s1','{"role":"user"}',1767268800000);
insert into part values ('p1','m1',json_object('type','text','text','the pool was exhausted'));`
	if out, err := exec.Command("sqlite3", db, sql).CombinedOutput(); err != nil {
		t.Fatalf("sqlite3 seed: %v %s", err, out)
	}
	if _, err := captureRun(t, "index"); err != nil {
		t.Fatal(err)
	}
	out, err := captureRun(t, "show", "s1", "--json", "--harness", "opencode")
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Session struct {
			Path string `json:"path"`
		} `json:"session"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("show --json: %v: %s", err, out)
	}
	fi, err := os.Stat(got.Session.Path)
	if err != nil {
		t.Fatalf("the path deja published does not exist: %v", err)
	}
	if !fi.IsDir() {
		t.Skip("opencode now records a file, so the documented exception can go")
	}
}
