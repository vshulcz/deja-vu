package index

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
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
	var sqlSb117 strings.Builder
	for i, id := range []string{"20260101_120000", "20260102_120000"} {
		base := 1767268800 + i*86400
		jsonl := fmt.Sprintf(`{"id":%q,"description":"pool","working_dir":"/tmp/app"}
{"role":"user","content":[{"type":"text","text":"why does pgbouncer time out"}],"created":%d}
{"role":"assistant","content":%s,"created":%d}
`, id, base, fmt.Sprintf(tool, i), base+10)
		parityWrite(t, filepath.Join(sessions, id+".jsonl"), jsonl, false)
		fmt.Fprintf(&sqlSb117, `insert into sessions values ('%s','n','pool','/tmp/app','2026-01-01 12:00:00','2026-01-01 12:05:00');
insert into messages (session_id, role, content_json, created_timestamp) values ('%s','user','[{"type":"text","text":"why does pgbouncer time out"}]',%d);
insert into messages (session_id, role, content_json, created_timestamp) values ('%s','assistant','%s',%d);
`, id, id, base, id, fmt.Sprintf(tool, i), base+10)
	}
	sql += sqlSb117.String()
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

// A deleted copy keeps the turns it shared with a live copy in another
// project, filed under itself. The command tables go by that filing in a
// build as in a pass: the build counted the live copy's run from the session
// it read, where the record a pass reads names the deleted file.
func TestADeletedCopysSharedTurnCountsAsItsRecordIsFiled(t *testing.T) {
	inc, write := collisionStore(t)
	write("app", "dup", "make zzapp")
	write("infra", "dup", "make zzshared", "make zzinfra")
	parityPass(t, inc, true)
	infra := filepath.Join(filepath.Dir(inc), "claude", "-tmp-infra", "dup.jsonl")
	if err := os.Remove(infra); err != nil {
		t.Fatal(err)
	}
	parityPass(t, inc, false)
	// The live copy now runs what the deleted one ran, at the same turn.
	write("app", "dup", "make zzshared", "make zzapp")
	parityPass(t, inc, false)
	if m, err := readManifest(inc); err != nil || !m.Files[infra].Kept {
		t.Fatalf("the deleted copy is not kept, so this measures nothing: %v", err)
	}
	sameTablesAsItsRebuild(t, inc)
}

// sameTablesAsItsRebuild fails the test where the command tables of inc
// differ from a rebuild of inc itself, which keeps what its deleted
// transcripts held.
func sameTablesAsItsRebuild(t *testing.T, inc string) {
	t.Helper()
	fresh := filepath.Join(t.TempDir(), "rebuilt")
	if err := os.CopyFS(fresh, os.DirFS(inc)); err != nil {
		t.Fatal(err)
	}
	parityPass(t, fresh, true)
	a, b := paritySnapshot(t, inc), paritySnapshot(t, fresh)
	for _, k := range []string{"commands", "fails", "facts"} {
		if a[k] != b[k] {
			t.Errorf("%s after the pass:\n  %s\nafter a rebuild:\n  %s", k, a[k], b[k])
		}
	}
}

// The row's owner appending a turn a copy in another project already holds
// takes the turn, as a rebuild reading the copies in path order gives it: it
// stayed with the copy that held it first, and the tables left it out.
func TestAnOwnerAppendingASharedTurnKeepsItInTheFacts(t *testing.T) {
	inc, write := collisionStore(t)
	write("app", "dup", "make zzapp")
	write("infra", "dup", "make zzapp", "make zzshared")
	parityPass(t, inc, true)
	app := filepath.Join(filepath.Dir(inc), "claude", "-tmp-app", "dup.jsonl")
	b, err := os.ReadFile(filepath.Join(filepath.Dir(inc), "claude", "-tmp-infra", "dup.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	// The infra copy's last turn, the command and its output, as the app copy
	// writes it.
	lines := strings.SplitAfter(string(b), "\n")
	tail := strings.ReplaceAll(strings.Join(lines[len(lines)-3:], ""), `"/tmp/infra"`, `"/tmp/app"`)
	parityWrite(t, app, tail, true)
	parityPass(t, inc, false)
	held := false
	for _, c := range ReadSessionFacts(inc)["claude:dup"].Commands {
		held = held || strings.Contains(c.Text, "zzshared")
	}
	if !held {
		t.Errorf("the owner's appended run left the row's facts: %+v", ReadSessionFacts(inc)["claude:dup"].Commands)
	}
	sameTablesAsItsRebuild(t, inc)
}

// A new transcript that takes a row from a deleted copy in another project
// has the session's failures counted again under the new owner: the append
// left them as counted under the deleted one.
func TestARowMovedByAnAppendRecountsItsFailures(t *testing.T) {
	inc, write := collisionStore(t)
	write("infra", "dup", "make zzinfra")
	write("app", "other", "make zzinfra")
	parityPass(t, inc, true)
	if err := os.Remove(filepath.Join(filepath.Dir(inc), "claude", "-tmp-infra", "dup.jsonl")); err != nil {
		t.Fatal(err)
	}
	parityPass(t, inc, false)
	write("app", "dup", "make zzapp")
	parityPass(t, inc, false)
	if m, err := readManifest(inc); err != nil || !strings.Contains(m.Sessions["claude:dup"].Path, "-tmp-app") {
		t.Fatalf("the new copy did not take the row, so this measures nothing: %v", err)
	}
	for _, f := range ReadCommandFails(inc) {
		if strings.Contains(f.Head, "zzinfra") && f.Sessions > 1 {
			t.Errorf("the deleted copy's failure still counts for the row: %+v", f)
		}
	}
	sameTablesAsItsRebuild(t, inc)
}

// The owner of a shared row appending turns no other copy holds is still an
// append: only a tail holding a turn a later copy has is read again with it.
func TestAnOwnerAppendingItsOwnTurnsBesideACopyStaysAnAppend(t *testing.T) {
	inc, write := collisionStore(t)
	write("app", "dup", "make zzapp")
	write("infra", "dup", "make zzinfra")
	parityPass(t, inc, true)
	app := filepath.Join(filepath.Dir(inc), "claude", "-tmp-app", "dup.jsonl")
	parityWrite(t, app, `{"type":"user","sessionId":"dup","timestamp":"2026-07-17T10:00:00Z","cwd":"/tmp/app","message":{"role":"user","content":"and the changelog"}}`+"\n", true)
	var progress strings.Builder
	if err := Ensure(inc, "", false, &progress); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(progress.String(), replacementPassMarker) {
		t.Errorf("an append of the owner's own turn read the copies again: %q", progress.String())
	}
	sameTablesAsItsRebuild(t, inc)
}

// Every build leaves the copy in another directory out of the tables, the
// early slice a first build publishes included, which files no turn under a
// deleted copy.
func TestCommandSessionsLeavesTheOtherProjectsCopyOut(t *testing.T) {
	m := Manifest{Sessions: map[string]SessionMeta{"claude:dup": {Path: "/p/a/dup.jsonl", Shared: true}}}
	at := time.Date(2026, 1, 2, 3, 0, 0, 0, time.UTC)
	ss := []model.Session{
		{Harness: "claude", ID: "dup", Path: "/p/a/dup.jsonl", Messages: []model.Message{{Role: roleCommand, Text: "$ make own", Time: at}}},
		{Harness: "claude", ID: "dup", Path: "/p/b/dup.jsonl", Messages: []model.Message{{Role: roleCommand, Text: "$ make foreign", Time: at.Add(time.Minute)}}},
	}
	for _, s := range commandSessions(ss, m, nil) {
		for _, msg := range s.Messages {
			if strings.Contains(msg.Text, "foreign") {
				t.Errorf("the other project's run is counted for the row: %+v", msg)
			}
		}
	}
}
