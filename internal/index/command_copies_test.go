package index

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Two transcripts in two projects sharing an id are one row, of the project
// the row names. The command tables count the copy the row names and leave
// the other out, in a full build and in a pass alike: a full build filed the
// other copy's failures and runs under its own project while a pass dropped
// them, and the session facts took both copies' commands in a pass and one
// copy's in a build (#699).
func TestCollidingCopiesCommandTablesMatchARebuild(t *testing.T) {
	inc, write := collisionStore(t)
	write("infra", "dup", "terraform plan -target=zzvault", "make zzinfra")
	write("app", "dup", "make zzapp", "go test ./zzapp")
	write("infra", "y", "terraform plan -target=zzvault", "make zzinfra")
	write("app", "z", "make zzapp", "go test ./zzapp")
	parityPass(t, inc, false)
	if m, err := readManifest(inc); err != nil || !m.Sessions["claude:dup"].Shared {
		t.Fatalf("the two dup transcripts do not collide, so this measures nothing: %v", err)
	}
	sameCommandTables(t, inc)

	// A pass that reads each copy again, and one that only adds a session
	// beside them.
	write("app", "dup", "make zzapp-lint", "go test ./zzapp", "make zzapp")
	parityPass(t, inc, false)
	sameCommandTables(t, inc)
	write("infra", "dup", "make zzinfra", "terraform plan -target=zzvault", "make zzapp-lint")
	parityPass(t, inc, false)
	sameCommandTables(t, inc)
	write("app", "w", "make zzapp-lint")
	parityPass(t, inc, false)
	sameCommandTables(t, inc)

	m, err := readManifest(inc)
	if err != nil {
		t.Fatal(err)
	}
	row := m.Sessions["claude:dup"]
	other := "zzinfra"
	if strings.Contains(row.Path, "-tmp-infra") {
		other = "zzapp"
	}
	for _, c := range ReadSessionFacts(inc)["claude:dup"].Commands {
		if strings.Contains(c.Text, other) {
			t.Errorf("the row's facts hold the other copy's command %q", c.Text)
		}
	}
	for _, f := range ReadCommandFails(inc) {
		if strings.Contains(f.Head, other) && f.Sessions > 1 {
			t.Errorf("the other copy's failure is counted for the row: %+v", f)
		}
	}
}

// sameCommandTables fails the test where the command tables of inc differ
// from a fresh build's of the same stores.
func sameCommandTables(t *testing.T, inc string) {
	t.Helper()
	fresh := inc + "-fresh"
	_ = os.RemoveAll(fresh)
	parityPass(t, fresh, true)
	a, b := paritySnapshot(t, inc), paritySnapshot(t, fresh)
	for _, k := range []string{"commands", "fails", "facts"} {
		if a[k] != b[k] {
			t.Errorf("%s after the pass:\n  %s\nafter a rebuild:\n  %s", k, a[k], b[k])
		}
	}
}

// goose's migration left each session in its JSONL file and in sessions.db.
// The record log holds each turn once, and a full build counted every run
// twice: 4 runs of a command two sessions ran once each.
func TestAMigratedGooseSessionsRunsCountOnceInABuild(t *testing.T) {
	inc, fresh := gooseMigratedPair(t)
	for _, dir := range []string{inc, fresh} {
		found := false
		for _, u := range ReadCommands(dir) {
			if strings.HasSuffix(u.Command, "zzgoosecmd") {
				found = true
				if u.Runs != 2 || u.Sessions != 2 {
					t.Errorf("%s: %q has %d runs in %d sessions, want 2 in 2", filepath.Base(dir), u.Command, u.Runs, u.Sessions)
				}
			}
		}
		if !found {
			t.Errorf("%s: the command is not in the table: %v", filepath.Base(dir), ReadCommands(dir))
		}
	}
}

// gooseMigratedPair is two goose sessions each in its JSONL file and in
// sessions.db, indexed by a pass after a full build, and a full build of the
// same stores.
func gooseMigratedPair(t *testing.T) (inc, fresh string) {
	t.Helper()
	tmp := parityEnv(t, map[string]string{"DEJA_CLAUDE_ROOT": "claude", "DEJA_GOOSE_ROOT": "goose"})
	loc := time.Local
	t.Cleanup(func() { time.Local = loc })
	time.Local = time.UTC
	sessions := filepath.Join(tmp, "goose", "sessions")
	db := filepath.Join(sessions, "sessions.db")
	t.Setenv("DEJA_GOOSE_DB", db)
	tool := `[{"type":"toolRequest","id":"c%d","toolCall":{"status":"success","value":{"name":"developer__shell","arguments":{"command":"make zzgoosecmd"}}}}]`
	sql := `create table sessions (id text primary key, name text, description text, working_dir text, created_at text, updated_at text);
create table messages (id integer primary key autoincrement, session_id text, role text, content_json text, created_timestamp integer);
`
	for i, id := range []string{"20260101_120000", "20260102_120000"} {
		base := 1767268800 + i*86400
		jsonl := fmt.Sprintf(`{"id":%q,"description":"pool","working_dir":"/tmp/app"}
{"role":"user","content":[{"type":"text","text":"why does pgbouncer time out"}],"created":%d}
{"role":"assistant","content":%s,"created":%d}
`, id, base, fmt.Sprintf(tool, i), base+10)
		parityWrite(t, filepath.Join(sessions, id+".jsonl"), jsonl, false)
		sql += fmt.Sprintf(`insert into sessions values ('%s','n','pool','/tmp/app','2026-01-01 12:00:00','2026-01-01 12:05:00');
insert into messages (session_id, role, content_json, created_timestamp) values ('%s','user','[{"type":"text","text":"why does pgbouncer time out"}]',%d);
insert into messages (session_id, role, content_json, created_timestamp) values ('%s','assistant','%s',%d);
`, id, id, base, id, fmt.Sprintf(tool, i), base+10)
	}
	paritySQL(t, db, sql)
	proj := filepath.Join(tmp, "claude", "-tmp-proj")
	parityWrite(t, filepath.Join(proj, "a.jsonl"), parityClaudeLine("a", "00:00Z", "user", `"hi there build"`), false)
	inc = filepath.Join(tmp, "inc")
	parityPass(t, inc, true)
	parityWrite(t, filepath.Join(proj, "a.jsonl"),
		parityClaudeLine("a", "01:01Z", "assistant", `[{"type":"tool_use","id":"t1","name":"Bash","input":{"command":"ls"}}]`)+
			parityClaudeLine("a", "01:02Z", "user", `[{"type":"tool_result","tool_use_id":"t1","content":"x"}]`), true)
	parityPass(t, inc, false)
	fresh = inc + "-fresh"
	parityPass(t, fresh, true)
	return inc, fresh
}

// A copy with a smaller path that turns up after the first was indexed takes
// the row. A turn both copies hold is the new owner's, as a rebuild files it,
// and stays in the row's facts (#5492).
func TestASharedTurnFollowsTheRowToANewOwner(t *testing.T) {
	inc, write := collisionStore(t)
	write("infra", "dup", "make zzshared", "make zzinfra")
	parityPass(t, inc, true)
	write("app", "dup", "make zzshared", "make zzapp")
	parityPass(t, inc, false)
	m, err := readManifest(inc)
	if err != nil || !m.Sessions["claude:dup"].Shared || !strings.Contains(m.Sessions["claude:dup"].Path, "-tmp-app") {
		t.Fatalf("the app copy did not take the shared row, so this measures nothing: %+v %v", m.Sessions["claude:dup"], err)
	}
	held := false
	for _, c := range ReadSessionFacts(inc)["claude:dup"].Commands {
		held = held || strings.Contains(c.Text, "zzshared")
	}
	if !held {
		t.Errorf("the shared turn left the row's facts: %+v", ReadSessionFacts(inc)["claude:dup"].Commands)
	}
	sameCommandTables(t, inc)
}

// Copies read again in one pass are written in path order, as a rebuild
// writes them, so a turn they share goes to the copy that owns the row. Map
// order filed it under either; eight sessions make a pass right by chance
// once in 256 (#5492).
func TestCopiesReadInOnePassFileASharedTurnAsARebuild(t *testing.T) {
	inc, write := collisionStore(t)
	for i := range 8 {
		write("app", fmt.Sprintf("dup%d", i), "make zzapp")
		write("infra", fmt.Sprintf("dup%d", i), "make zzinfra")
	}
	parityPass(t, inc, true)
	for i := range 8 {
		write("app", fmt.Sprintf("dup%d", i), "make zzshared", "make zzapp")
		write("infra", fmt.Sprintf("dup%d", i), "make zzshared", "make zzinfra")
	}
	parityPass(t, inc, false)
	sameCommandTables(t, inc)
}
