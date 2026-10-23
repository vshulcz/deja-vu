package index

import (
	"bytes"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// collisionStore is a Claude store where -app/dup.jsonl and -infra/dup.jsonl
// share the id dup (#699), with pads enough that a pass touching dup carries
// the command state rather than walking the log. write rewrites one
// transcript in project proj running cmds.
func collisionStore(t *testing.T) (inc string, write func(proj, id string, cmds ...string)) {
	t.Helper()
	tmp := parityEnv(t, map[string]string{"DEJA_CLAUDE_ROOT": "claude"})
	loc := time.Local
	t.Cleanup(func() { time.Local = loc })
	time.Local = time.UTC
	bump := 0
	write = func(proj, id string, cmds ...string) {
		line := func(ts, role, content string) string {
			return fmt.Sprintf(`{"type":%q,"sessionId":%q,"timestamp":"2026-07-17T09:%s","cwd":"/tmp/%s","message":{"role":%q,"content":%s}}`, role, id, ts, proj, role, content) + "\n"
		}
		body := line("00:00Z", "user", `"ship the release"`)
		var bodySb31 strings.Builder
		for i, c := range cmds {
			tid := fmt.Sprintf("t%d", i)
			bodySb31.WriteString(line(fmt.Sprintf("%02d:01Z", i+1), "assistant", `[{"type":"tool_use","id":"`+tid+`","name":"Bash","input":{"command":"`+c+`"}}]`) +
				line(fmt.Sprintf("%02d:02Z", i+1), "user", `[{"type":"tool_result","tool_use_id":"`+tid+`","content":"make: *** [`+c+`] Error 2"}]`))
		}
		body += bodySb31.String()
		p := filepath.Join(tmp, "claude", "-tmp-"+proj, id+".jsonl")
		parityWrite(t, p, body, false)
		bump++
		later := time.Now().Add(time.Duration(bump) * time.Second)
		_ = os.Chtimes(p, later, later)
	}
	for i := range 12 {
		write("pad", fmt.Sprintf("pad%02d", i), "go test ./pad")
	}
	return filepath.Join(tmp, "inc"), write
}

func commandsHold(dir, cmd string) bool {
	for _, u := range ReadCommands(dir) {
		if strings.TrimPrefix(u.Command, "$ ") == cmd {
			return true
		}
	}
	return false
}

// indexFilesHolding names the files under dir whose bytes hold word.
func indexFilesHolding(t *testing.T, dir, word string) []string {
	t.Helper()
	var out []string
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, err := os.ReadFile(p) //nolint:gosec // walks the test's own temp dir
		if err != nil {
			return err
		}
		if bytes.Contains(b, []byte(word)) {
			rel, _ := filepath.Rel(dir, p)
			out = append(out, rel)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// Two transcripts sharing an id made the command table refuse to rebuild and
// keep the carried one, so a command removed from every transcript stayed in
// commands.gob and `deja how` still listed it (#5477).
func TestACommandRemovedFromCollidingTranscriptsLeavesTheTable(t *testing.T) {
	const cmd = "make deploy-zzkumquatvole"
	inc, write := collisionStore(t)
	write("infra", "dup", cmd)
	write("app", "dup", cmd)
	write("app", "x", cmd)
	parityPass(t, inc, false)
	if m, err := readManifest(inc); err != nil || !m.Sessions["claude:dup"].Shared {
		t.Fatalf("the two dup transcripts do not collide, so this measures nothing: %v", err)
	}
	if !commandsHold(inc, cmd) {
		t.Fatal("control: the command two sessions ran is not in the table")
	}
	if got := indexFilesHolding(t, inc, "zzkumquatvole"); !slices.Contains(got, commandFailsFile) || !slices.Contains(got, sessionFactsFile) {
		t.Fatalf("control: the command tables do not hold the command before it is removed: %v", got)
	}

	write("infra", "dup", "go vet ./infra")
	write("app", "dup", "go vet ./app")
	write("app", "x", "go vet ./app")
	parityPass(t, inc, false)
	if commandsHold(inc, cmd) {
		t.Errorf("commands.gob still lists %q after every transcript dropped it", cmd)
	}
	if got := indexFilesHolding(t, inc, "zzkumquatvole"); len(got) > 0 {
		t.Errorf("index files still hold the removed command: %v", got)
	}
	if !commandsHold(inc, "go vet ./app") {
		t.Error("the table did not take the command the winner and x run now")
	}
	fresh := inc + "-fresh"
	parityPass(t, fresh, true)
	if a, b := fmt.Sprint(ReadCommands(inc)), fmt.Sprint(ReadCommands(fresh)); a != b {
		t.Errorf("command table after the pass:\n  %s\nafter a rebuild:\n  %s", a, b)
	}
}

// The refusal kept the winner's project off the other copy's commands: the
// row names one project, and filing what the other transcript ran under it
// put a command in a project that never ran it (#699). Rebuilding the table
// keeps that.
func TestACollidingCopysCommandsAreNotFiledUnderTheRowsProject(t *testing.T) {
	const cmd = "terraform apply -target=zzvault"
	inc, write := collisionStore(t)
	write("infra", "dup", "go vet ./infra")
	write("app", "dup", "go vet ./app")
	parityPass(t, inc, false)
	m, err := readManifest(inc)
	if err != nil {
		t.Fatal(err)
	}
	row := m.Sessions["claude:dup"]
	if !row.Shared {
		t.Fatal("the two dup transcripts do not collide, so this measures nothing")
	}
	other, mine := "infra", "app"
	if strings.Contains(row.Path, "-tmp-infra") {
		other, mine = "app", "infra"
	}
	write(other, "dup", cmd)
	write(other, "y", cmd)
	write(mine, "z", cmd)
	parityPass(t, inc, false)
	for _, u := range ReadCommands(inc) {
		if strings.TrimPrefix(u.Command, "$ ") != cmd {
			continue
		}
		if pu := u.ByProject[row.Project]; pu.Sessions > 1 {
			t.Errorf("%q is filed under the row's project %q with %d sessions, want only z's: %+v", cmd, row.Project, pu.Sessions, u.ByProject)
		}
		return
	}
	t.Errorf("%q ran in y and z, two sessions, and left the table: %v", cmd, ReadCommands(inc))
}

// An append commits the log before it writes the command tables, so a pass
// killed between the two left them without what it appended, and the next
// pass wrote them again only if it appended a command too.
func TestAPassHealsCommandTablesAKilledAppendLeftBehind(t *testing.T) {
	tmp := parityEnv(t, map[string]string{"DEJA_CLAUDE_ROOT": "claude"})
	loc := time.Local
	t.Cleanup(func() { time.Local = loc })
	time.Local = time.UTC
	proj := filepath.Join(tmp, "claude", "-tmp-proj")
	turn := func(id, ts string, cmd string) {
		body := parityClaudeLine(id, ts+":00Z", "user", `"fix the flaky build"`)
		if cmd != "" {
			body += parityClaudeLine(id, ts+":01Z", "assistant", `[{"type":"tool_use","id":"t`+ts+`","name":"Bash","input":{"command":"`+cmd+`"}}]`) +
				parityClaudeLine(id, ts+":02Z", "user", `[{"type":"tool_result","tool_use_id":"t`+ts+`","content":"FAIL zzheal"}]`)
		}
		parityWrite(t, filepath.Join(proj, id+".jsonl"), body, true)
	}
	for _, id := range []string{"a", "b", "c"} {
		turn(id, "00", "go build ./...")
	}
	inc := filepath.Join(tmp, "inc")
	parityPass(t, inc, false)
	sidecars := []string{commandsFile, sessionFactsFile, commandsStateFile, commandFailsFile, commandFailStateFile}
	saved := map[string][]byte{}
	for _, f := range sidecars {
		if b, err := os.ReadFile(filepath.Join(inc, f)); err == nil {
			saved[f] = b
		}
	}
	turn("a", "01", "make zzheal")
	turn("b", "01", "make zzheal")
	parityPass(t, inc, false)
	if !commandsHold(inc, "make zzheal") {
		t.Fatal("control: the appended command is not in the table, so this measures nothing")
	}
	// What a pass killed after its commit leaves: the tables from before it.
	for _, f := range sidecars {
		_ = os.Remove(filepath.Join(inc, f))
		if b, ok := saved[f]; ok {
			if err := os.WriteFile(filepath.Join(inc, f), b, 0o600); err != nil {
				t.Fatal(err)
			}
		}
	}
	if commandsHold(inc, "make zzheal") {
		t.Fatal("the restored table already holds the command")
	}
	turn("c", "02", "")
	parityPass(t, inc, false)
	if !commandsHold(inc, "make zzheal") {
		t.Error("a pass after the killed one left the command table without what it appended")
	}
	sameAsRebuild(t, inc)
}

// goose's migration left each session in its JSONL file and in sessions.db,
// one row filed under the database while the commands are read from the file
// that sorts first. That is one conversation, and its commands stay in the
// table a pass rebuilds.
func TestAMigratedGooseSessionsCommandsStayInTheTable(t *testing.T) {
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
	var sqlSb230 strings.Builder
	for i, id := range []string{"20260101_120000", "20260102_120000"} {
		base := 1767268800 + i*86400
		jsonl := fmt.Sprintf(`{"id":%q,"description":"pool","working_dir":"/tmp/app"}
{"role":"user","content":[{"type":"text","text":"why does pgbouncer time out"}],"created":%d}
{"role":"assistant","content":%s,"created":%d}
`, id, base, fmt.Sprintf(tool, i), base+10)
		parityWrite(t, filepath.Join(sessions, id+".jsonl"), jsonl, false)
		fmt.Fprintf(&sqlSb230, `insert into sessions values ('%s','n','pool','/tmp/app','2026-01-01 12:00:00','2026-01-01 12:05:00');
insert into messages (session_id, role, content_json, created_timestamp) values ('%s','user','[{"type":"text","text":"why does pgbouncer time out"}]',%d);
insert into messages (session_id, role, content_json, created_timestamp) values ('%s','assistant','%s',%d);
`, id, id, base, id, fmt.Sprintf(tool, i), base+10)
	}
	sql += sqlSb230.String()
	paritySQL(t, db, sql)
	proj := filepath.Join(tmp, "claude", "-tmp-proj")
	parityWrite(t, filepath.Join(proj, "a.jsonl"), parityClaudeLine("a", "00:00Z", "user", `"hi there build"`), false)
	inc := filepath.Join(tmp, "inc")
	parityPass(t, inc, true)
	if !commandsHold(inc, "make zzgoosecmd") {
		t.Fatalf("control: the full build lacks the command: %v", ReadCommands(inc))
	}
	parityWrite(t, filepath.Join(proj, "a.jsonl"),
		parityClaudeLine("a", "01:01Z", "assistant", `[{"type":"tool_use","id":"t1","name":"Bash","input":{"command":"ls"}}]`)+
			parityClaudeLine("a", "01:02Z", "user", `[{"type":"tool_result","tool_use_id":"t1","content":"x"}]`), true)
	parityPass(t, inc, false)
	if !commandsHold(inc, "make zzgoosecmd") {
		t.Errorf("the pass dropped the migrated goose sessions' command: %v", ReadCommands(inc))
	}
	for _, u := range ReadCommands(inc) {
		if strings.HasSuffix(u.Command, "zzgoosecmd") && u.ByProject["tmp/app"].Sessions != 2 {
			t.Errorf("the command is filed under %+v, want both sessions in tmp/app", u.ByProject)
		}
	}
}
