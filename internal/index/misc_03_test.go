package index

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/query"
	"github.com/vshulcz/deja-vu/internal/search"
	"github.com/vshulcz/deja-vu/internal/sources"
	"io"
	"io/fs"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"runtime"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

// ownCollisionCount makes a test's collision count its own.
//
// The counter is package state that ReportCollisions drains, so a test that
// asserts a number reads whatever an earlier one left behind. Under -shuffle
// that is exactly what happened: "reported 3 collisions on a clean store",
// from a build three tests away.
//
// Drained twice on purpose. Before, so the assertion is about this build; and
// after, so a test that legitimately produces collisions does not hand them to
// whoever runs next — which is how production reads it too, one drain per
// build rather than one before each read.
func ownCollisionCount(t *testing.T) {
	t.Helper()
	ReportCollisions()
	t.Cleanup(func() { ReportCollisions() })
}

// Identity is harness:id and nothing guarantees it is unique: two files named
// session-1.jsonl in different projects produced one manifest row whose project
// came from one transcript and whose title came from the other, differently on
// every build (#698).
func TestSessionsSharingAnIDAreAttributedTheSameWayEveryBuild(t *testing.T) {
	ownCollisionCount(t)
	tmp := t.TempDir()
	setHome(t, filepath.Join(tmp, "home"))
	t.Setenv("USERPROFILE", os.Getenv("HOME"))
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "claude"))
	t.Setenv("DEJA_CODEX_ROOT", filepath.Join(tmp, "codex"))
	t.Setenv("DEJA_OPENCODE_DB", filepath.Join(tmp, "opencode.db"))
	for proj, text := range map[string]string{
		"alpha": "the alpha work on pool sizing",
		"beta":  "the beta work on cache eviction",
	} {
		dir := filepath.Join(tmp, "claude", proj)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		body := `{"type":"user","timestamp":"2026-07-21T10:00:00Z","message":{"role":"user","content":"` + text + `"}}` + "\n"
		if err := os.WriteFile(filepath.Join(dir, "session-1.jsonl"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	dir := filepath.Join(tmp, "index.db")
	var first []SessionMeta
	for run := range 5 {
		if err := os.RemoveAll(dir); err != nil {
			t.Fatal(err)
		}
		if err := Ensure(dir, "", true, nil); err != nil {
			t.Fatal(err)
		}
		if n := ReportCollisions(); n == 0 {
			t.Errorf("run %d reported no collision", run)
		}
		metas, err := AllMeta(dir)
		if err != nil {
			t.Fatal(err)
		}
		if run == 0 {
			first = metas
			// The file that sorts first owns the row, so the answer does not
			// depend on which one was read first.
			if len(metas) != 1 || metas[0].Project != "alpha" {
				t.Fatalf("metas = %#v", metas)
			}
			continue
		}
		if len(metas) != len(first) || metas[0].Project != first[0].Project || metas[0].Title != first[0].Title {
			t.Fatalf("run %d: %#v, first build had %#v", run, metas, first)
		}
	}
	// Both conversations stay searchable — the collision costs attribution,
	// not content.
	for _, q := range []string{"alpha work pool", "beta work cache"} {
		hits, err := Search(dir, search.Options{Query: q, All: true})
		if err != nil || len(hits) == 0 {
			t.Errorf("%q: %d hits err=%v", q, len(hits), err)
		}
	}
}

func TestReportCollisionsIsZeroWithoutOne(t *testing.T) {
	ownCollisionCount(t)
	tmp := t.TempDir()
	setHome(t, filepath.Join(tmp, "home"))
	t.Setenv("USERPROFILE", os.Getenv("HOME"))
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "claude"))
	t.Setenv("DEJA_CODEX_ROOT", filepath.Join(tmp, "codex"))
	t.Setenv("DEJA_OPENCODE_DB", filepath.Join(tmp, "opencode.db"))
	dir := filepath.Join(tmp, "claude", "solo")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := `{"type":"user","timestamp":"2026-07-21T10:00:00Z","message":{"role":"user","content":"a single conversation"}}` + "\n"
	if err := os.WriteFile(filepath.Join(dir, "only.jsonl"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Ensure(filepath.Join(tmp, "index.db"), "", true, nil); err != nil {
		t.Fatal(err)
	}
	if n := ReportCollisions(); n != 0 {
		t.Errorf("reported %d collisions on a clean store", n)
	}
}

// The incremental pass walks a map of changed files; unsorted, which session
// won a shared id depended on that order (#698).
func TestIncrementalPassAttributesCollisionsTheSameWay(t *testing.T) {
	ownCollisionCount(t)
	tmp := t.TempDir()
	setHome(t, filepath.Join(tmp, "home"))
	t.Setenv("USERPROFILE", os.Getenv("HOME"))
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "claude"))
	t.Setenv("DEJA_CODEX_ROOT", filepath.Join(tmp, "codex"))
	t.Setenv("DEJA_OPENCODE_DB", filepath.Join(tmp, "opencode.db"))
	write := func(proj, text string) {
		dir := filepath.Join(tmp, "claude", proj)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		body := `{"type":"user","timestamp":"2026-07-21T10:00:00Z","message":{"role":"user","content":"` + text + `"}}` + "\n"
		if err := os.WriteFile(filepath.Join(dir, "session-1.jsonl"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	dir := filepath.Join(tmp, "index.db")
	write("alpha", "the alpha work on pool sizing")
	if err := Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	ReportCollisions()

	// Two more projects arrive, both claiming the same id. Neither may take the
	// row from the transcript that sorts first.
	write("beta", "the beta work on cache eviction")
	write("gamma", "the gamma work on token limits")
	if err := Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	if n := ReportCollisions(); n == 0 {
		t.Error("incremental pass reported no collision")
	}
	metas, err := AllMeta(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(metas) != 1 || metas[0].Project != "alpha" {
		t.Fatalf("metas = %#v", metas)
	}
	// The late arrivals are still searchable: what the collision costs is
	// attribution, not content.
	for _, q := range []string{"beta work cache", "gamma work token"} {
		hits, err := Search(dir, search.Options{Query: q, All: true})
		if err != nil || len(hits) == 0 {
			t.Errorf("%q: %d hits err=%v", q, len(hits), err)
		}
	}
}

// Re-indexing one file of a colliding pair must not drop the other. The
// replacement pass rebuilds the manifest from the files it re-read, so the row
// held by the transcript that did not change has to be carried over — losing it
// was how the first attempt at #698 deleted a session.
func TestReindexingOneHalfOfACollisionKeepsTheOther(t *testing.T) {
	ownCollisionCount(t)
	tmp := t.TempDir()
	setHome(t, filepath.Join(tmp, "home"))
	t.Setenv("USERPROFILE", os.Getenv("HOME"))
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "claude"))
	t.Setenv("DEJA_CODEX_ROOT", filepath.Join(tmp, "codex"))
	t.Setenv("DEJA_OPENCODE_DB", filepath.Join(tmp, "opencode.db"))
	write := func(proj, text string) {
		dir := filepath.Join(tmp, "claude", proj)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		body := `{"type":"user","timestamp":"2026-07-21T10:00:00Z","message":{"role":"user","content":"` + text + `"}}` + "\n"
		if err := os.WriteFile(filepath.Join(dir, "session-1.jsonl"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	dir := filepath.Join(tmp, "index.db")
	write("alpha", "the alpha work on pool sizing")
	write("beta", "the beta work on cache eviction")
	if err := Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	ReportCollisions()

	// Rewrite beta from scratch — shorter, so it takes the replacement path
	// rather than the append one.
	write("beta", "beta rewritten")
	if err := Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	metas, err := AllMeta(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(metas) != 1 || metas[0].Project != "alpha" || metas[0].Title != "the alpha work on pool sizing" {
		t.Fatalf("metas = %#v — alpha's row was lost or overwritten", metas)
	}
	// #699 was the other half of the same collision: records are keyed by
	// harness:id too, so re-indexing beta dropped alpha's text from the postings
	// even though alpha's row survived — content loss until a full rebuild.
	// Alpha was never re-read, so its content must still be there.
	alpha, err := Search(dir, search.Options{Query: "pool sizing", All: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(alpha) == 0 {
		t.Fatal("re-indexing beta dropped alpha's content from the index (#699)")
	}
	beta, err := Search(dir, search.Options{Query: "beta rewritten", All: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(beta) == 0 {
		t.Fatal("beta's rewritten content was not indexed")
	}
}

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

// The question the tool hook asks of a promoted session: did it run this
// command. Compared the way CommandHistory compares (#2516).
func TestSessionRanCommand(t *testing.T) {
	s := model.Session{Messages: []model.Message{
		{Role: "user", Text: "run the orders migration"},
		{Role: roleCommand, Text: "$ Make Migrate-Orders"},
		{Role: roleCommand, Text: "git status --short"},
		// An assistant sentence that names the command is not a run of it.
		{Role: "assistant", Text: "make something-else"},
	}}
	for _, tc := range []struct {
		cmd  string
		want bool
	}{
		{cmd: "make migrate-orders", want: true},
		{cmd: "  make migrate-orders  ", want: true},
		{cmd: "git status --short", want: true},
		{cmd: "make migrate", want: false},
		{cmd: "make something-else", want: false},
		{cmd: "", want: false},
		// A multi-line command is never the stored single-line one, for the
		// reason CommandHistory gives: the harmless first line would carry the
		// rest.
		{cmd: "make migrate-orders\nrm -rf /", want: false},
	} {
		if got := SessionRanCommand(s, tc.cmd); got != tc.want {
			t.Errorf("SessionRanCommand(%q) = %v, want %v", tc.cmd, got, tc.want)
		}
	}
	if SessionRanCommand(model.Session{Updated: time.Now()}, "make migrate-orders") {
		t.Error("a session with no records ran nothing")
	}
}

// What a session settled and which session ran a command last are both facts
// the build already has in hand, and the point-of-action hook used to go
// looking for them at the moment of the action: a ranking pass to guess which
// sessions might be about the command, then a whole-session load each to ask
// whether they had run it. 133 ms an action against 21 ms (#3001, #3605).
func TestCommandSettledIsALookup(t *testing.T) {
	dir := t.TempDir()
	at := time.Date(2026, 9, 1, 10, 0, 0, 0, time.UTC)
	session := func(id, project, conclusion string, when time.Time) model.Session {
		return model.Session{
			ID: id, Harness: "claude", Project: project, Started: when, Updated: when,
			Messages: []model.Message{
				{Role: "user", Text: "should we run the orders migration against production?", Time: when},
				{Role: roleCommand, Text: "make migrate-orders", Time: when.Add(time.Minute)},
				{Role: "assistant", Text: conclusion, Time: when.Add(2 * time.Minute)},
			},
		}
	}
	ss := []model.Session{
		session("old", "work/app", "ran the migration against production and it locked the table", at),
		session("new", "work/app", "no: run the orders migration against the replica first", at.Add(time.Hour)),
		session("other", "work/other", "the other project runs it straight through", at.Add(2*time.Hour)),
	}
	writeStore(t, dir, ss)

	yes := func(string) bool { return true }
	got := CommandSettled(dir, "make migrate-orders", []string{"work/app"}, yes)
	if !strings.Contains(got, "replica first") {
		t.Errorf("the newest session in the project did not settle it: %q", got)
	}
	if strings.Contains(got, "locked the table") {
		t.Errorf("an older session's conclusion won: %q", got)
	}

	// Another project's history is not this project's, which is the whole
	// reason ByProject exists.
	got = CommandSettled(dir, "make migrate-orders", []string{"work/other"}, yes)
	if !strings.Contains(got, "straight through") {
		t.Errorf("the other project's own conclusion is missing: %q", got)
	}

	// The trust policy is asked per project, the way every other caller of this
	// table asks it: a withheld project is skipped, not an empty answer.
	no := func(project string) bool { return project != "work/app" }
	got = CommandSettled(dir, "make migrate-orders", []string{"work/app", "work/other"}, no)
	if !strings.Contains(got, "straight through") {
		t.Errorf("a withheld first project emptied the answer instead of being skipped: %q", got)
	}

	// A command nobody ran, and a project with no history, both say nothing.
	if got := CommandSettled(dir, "make something-else", []string{"work/app"}, yes); got != "" {
		t.Errorf("a command with no history answered: %q", got)
	}
	if got := CommandSettled(dir, "make migrate-orders", []string{"work/nowhere"}, yes); got != "" {
		t.Errorf("a project with no history answered: %q", got)
	}
}

// A table written before the session key existed has to send its reader back to
// the search rather than answer nothing: an index built by an older deja is the
// ordinary state for the first session after an upgrade.
func TestCommandSettledSaysNothingWithoutTheSessionKey(t *testing.T) {
	dir := t.TempDir()
	at := time.Date(2026, 9, 1, 10, 0, 0, 0, time.UTC)
	ss := []model.Session{{
		ID: "one", Harness: "claude", Project: "work/app", Started: at, Updated: at,
		Messages: []model.Message{
			{Role: "user", Text: "run the build", Time: at},
			{Role: roleCommand, Text: "make build", Time: at},
			{Role: "assistant", Text: "the build needs the vendor directory first", Time: at},
		},
	}, {
		ID: "two", Harness: "claude", Project: "work/app", Started: at, Updated: at,
		Messages: []model.Message{
			{Role: "user", Text: "run the build again", Time: at},
			{Role: roleCommand, Text: "make build", Time: at},
			{Role: "assistant", Text: "still needs the vendor directory", Time: at},
		},
	}}
	writeStore(t, dir, ss)

	// The shape an older table has: the split is there, the key is not.
	table := ReadCommands(dir)
	if len(table) == 0 {
		t.Fatal("the fixture produced no command table")
	}
	for i := range table {
		for proj, pu := range table[i].ByProject {
			pu.LastSession = ""
			table[i].ByProject[proj] = pu
		}
	}
	if err := writeGob(filepath.Join(dir, commandsFile), table); err != nil {
		t.Fatal(err)
	}
	if got := CommandSettled(dir, "make build", []string{"work/app"}, func(string) bool { return true }); got != "" {
		t.Errorf("a table with no session key answered anyway: %q", got)
	}
}

// writeStore builds a real index directory from sessions, the way the other
// tests in this package do: writeSessions publishes the manifest and the
// command table together, which is the pairing CommandSettled reads.
func writeStore(t *testing.T, dir string, ss []model.Session) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(dir+".tmp", "buckets"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := writeSessions(dir+".tmp", dir, ss, nil, ""); err != nil {
		t.Fatal(err)
	}
}

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

// codex and opencode record what a command exited with, appended to the command
// line itself: "$ make test  → exit 2". That is the outcome, not the invocation,
// and the commands table keyed on the whole line — so one command became two
// rows, the runs that worked and the runs that did not, counted apart. On this
// machine's index four commands are split that way, `git status --short` into
// 445 runs and 7 (#2590).
func TestTheCommandsTableIgnoresTheExitStatus(t *testing.T) {
	dir := t.TempDir()
	at := time.Date(2026, 8, 1, 10, 0, 0, 0, time.UTC)
	msg := func(text string, minute int) model.Message {
		return model.Message{Role: roleCommand, Text: text, Time: at.Add(time.Duration(minute) * time.Minute)}
	}
	sessions := []model.Session{
		{ID: "s1", Harness: "codex", Project: "app", Messages: []model.Message{
			msg("$ make test", 0),
			msg("$ make test  → exit 2", 1),
		}},
		{ID: "s2", Harness: "codex", Project: "app", Messages: []model.Message{
			msg("$ make test  → exit 2", 2),
		}},
	}
	buildCommands(dir, sessions)
	uses := ReadCommands(dir)
	var found []CommandUse
	for _, u := range uses {
		if strings.Contains(strings.ToLower(u.Command), "make test") {
			found = append(found, u)
		}
	}
	if len(found) != 1 {
		got := make([]string, 0, len(found))
		for _, u := range found {
			got = append(got, u.Command)
		}
		t.Fatalf("make test is %d rows, want one: %v", len(found), got)
	}
	if found[0].Runs != 3 || found[0].Sessions != 2 {
		t.Errorf("runs=%d sessions=%d, want 3 runs in 2 sessions", found[0].Runs, found[0].Sessions)
	}
	if strings.Contains(found[0].Command, "exit") {
		t.Errorf("the row deja shows carries the outcome: %q", found[0].Command)
	}
	// A command that merely mentions the marker is still that command: cutting
	// on the marker anywhere turned `echo "→ exit 0"` into `echo "` once (#2048).
	if got := withoutExitStatus(`echo "→ exit 0"`); got != `echo "→ exit 0"` {
		t.Errorf("a command naming the marker was cut to %q", got)
	}
	if got := withoutExitStatus("$ make test  → exit 2"); got != "$ make test" {
		t.Errorf("the real suffix survived: %q", got)
	}
	// And the lookup the tool hook makes, with the command as a harness hands
	// it over, finds all of it.
	use, ok := CommandHistory(dir, "make test")
	if !ok || use.Runs != 3 {
		t.Errorf("CommandHistory(make test) = %+v ok=%v", use, ok)
	}
}

// A pass read the command times from the record log in local time and a build
// from the transcripts in UTC, so the day `deja how` and orient printed for
// "last" depended on which one wrote the table.
func TestCommandTimesReadBackInLocalTimeWhicheverPathWrote(t *testing.T) {
	tmp := parityEnv(t, map[string]string{"DEJA_CLAUDE_ROOT": "claude"})
	loc := time.Local
	t.Cleanup(func() { time.Local = loc })
	time.Local = time.FixedZone("east", 5*3600)
	proj := filepath.Join(tmp, "claude", "-tmp-proj")
	run := func(id string, n int) string {
		tid := fmt.Sprintf("t%d", n)
		return parityClaudeLine(id, fmt.Sprintf("%02d:01Z", n), "assistant", `[{"type":"tool_use","id":"`+tid+`","name":"Bash","input":{"command":"make zzlocal"}}]`) +
			parityClaudeLine(id, fmt.Sprintf("%02d:02Z", n), "user", `[{"type":"tool_result","tool_use_id":"`+tid+`","content":"ok"}]`)
	}
	for _, id := range []string{"a", "b"} {
		parityWrite(t, filepath.Join(proj, id+".jsonl"), parityClaudeLine(id, "00:00Z", "user", `"ship it"`)+run(id, 1), false)
	}
	inc := filepath.Join(tmp, "inc")
	parityPass(t, inc, true)
	parityWrite(t, filepath.Join(proj, "a.jsonl"), run("a", 2), true)
	parityPass(t, inc, false)
	fresh := inc + "-fresh"
	parityPass(t, fresh, true)
	for _, dir := range []string{inc, fresh} {
		cmds := ReadCommands(dir)
		if len(cmds) == 0 {
			t.Fatalf("%s: no command table, so this measures nothing", filepath.Base(dir))
		}
		for _, c := range cmds {
			if c.Last.Location() != time.Local {
				t.Errorf("%s: %q last ran %v, not in local time", filepath.Base(dir), c.Command, c.Last)
			}
			for p, pu := range c.ByProject {
				if pu.Last.Location() != time.Local {
					t.Errorf("%s: %q in %s last ran %v, not in local time", filepath.Base(dir), c.Command, p, pu.Last)
				}
			}
		}
	}
	if a, b := fmt.Sprint(ReadCommands(inc)), fmt.Sprint(ReadCommands(fresh)); a != b {
		t.Errorf("command table after the pass:\n  %s\nafter a rebuild:\n  %s", a, b)
	}
}

// A recurring command can carry a live secret in an env assignment or a URL.
// commands.gob is exported through CommandHistory to a PreToolUse hook, so it
// must be redacted like the record log — the writeSessionsWithSync build path
// used to mine it from raw ss.
func TestCommandsAreRedactedLikeTheRecordLog(t *testing.T) {
	dir := t.TempDir()
	secret := "AKIAIOSFODNN7EXAMPLE" //nolint:gosec // a fake secret the redaction test plants
	cmd := "AWS_ACCESS_KEY_ID=" + secret + " aws s3 ls"
	sess := func(id string) model.Session {
		return model.Session{
			Harness: "claude", ID: id, Project: "p",
			Messages: []model.Message{
				{Role: "user", Text: "listing the bucket"},
				{Role: "command", Text: cmd},
			},
		}
	}
	// Two sessions so the command clears commandMinSessions.
	ss := []model.Session{sess("a"), sess("b")}
	if err := os.MkdirAll(filepath.Join(dir+".tmp", "buckets"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := writeSessions(dir+".tmp", dir, ss, nil, ""); err != nil {
		t.Fatal(err)
	}
	got := ReadCommands(dir)
	if len(got) == 0 {
		t.Fatal("the recurring command was not recorded")
	}
	for _, u := range got {
		if strings.Contains(u.Command, secret) {
			t.Fatalf("commands.gob stored a live secret: %q", u.Command)
		}
	}
}

// A compound command must not be endorsed on the strength of its first line:
// "git status\nrm -rf /" shares a first line with a stored "git status" but is
// a different, dangerous command.
func TestCommandHistoryRejectsAMultilineLookup(t *testing.T) {
	dir := t.TempDir()
	sess := func(id string) model.Session {
		return model.Session{
			Harness: "claude", ID: id, Project: "p",
			Messages: []model.Message{
				{Role: "user", Text: "check status"},
				{Role: "command", Text: "git status"},
			},
		}
	}
	ss := []model.Session{sess("a"), sess("b")}
	if err := os.MkdirAll(filepath.Join(dir+".tmp", "buckets"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := writeSessions(dir+".tmp", dir, ss, nil, ""); err != nil {
		t.Fatal(err)
	}
	if _, ok := CommandHistory(dir, "git status"); !ok {
		t.Fatal("the single-line command it did run is not recognised")
	}
	if _, ok := CommandHistory(dir, "git status\nrm -rf /"); ok {
		t.Error("a compound command was endorsed on its harmless first line")
	}
}

// A command that ran only in a withheld project must not surface, and the count
// shown must exclude withheld sessions. ByProject carries the per-project split
// so the caller can apply its trust policy.
func TestCommandsCarryPerProjectCounts(t *testing.T) {
	dir := t.TempDir()
	when := time.Date(2026, 7, 20, 10, 0, 0, 0, time.UTC)
	mk := func(id, project string) model.Session {
		return model.Session{
			Harness: "claude", ID: id, Project: project,
			Messages: []model.Message{
				{Role: "user", Text: "run it"},
				{Role: "command", Text: "go test ./...", Time: when},
			},
		}
	}
	ss := []model.Session{
		mk("l1", "local"), mk("l2", "local"),
		mk("p1", "imported:peer"), mk("p2", "imported:peer"), mk("p3", "imported:peer"),
	}
	if err := os.MkdirAll(filepath.Join(dir+".tmp", "buckets"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := writeSessions(dir+".tmp", dir, ss, nil, ""); err != nil {
		t.Fatal(err)
	}
	use, ok := CommandHistory(dir, "go test ./...")
	if !ok {
		t.Fatal("the recurring command was not recorded")
	}
	if use.ByProject["local"].Sessions != 2 || use.ByProject["imported:peer"].Sessions != 3 {
		t.Fatalf("per-project counts wrong: %v", use.ByProject)
	}
	if use.ByProject["imported:peer"].Last.IsZero() {
		t.Error("per-project last-run not recorded")
	}
	if use.Sessions != 5 {
		t.Errorf("machine-wide count wrong: %d", use.Sessions)
	}
}

// The last-run date must come from allowed projects only. A command run more
// recently in a withheld project must not print that project's date when it
// surfaces from an allowed one.
func TestCommandLastIsPerProject(t *testing.T) {
	dir := t.TempDir()
	old := time.Date(2026, 1, 1, 10, 0, 0, 0, time.UTC)
	newer := time.Date(2026, 8, 1, 10, 0, 0, 0, time.UTC)
	cmd := func(id, project string, when time.Time) model.Session {
		return model.Session{
			Harness: "claude", ID: id, Project: project,
			Messages: []model.Message{
				{Role: "user", Text: "run"},
				{Role: "command", Text: "make release", Time: when},
			},
		}
	}
	ss := []model.Session{
		cmd("l1", "local", old), cmd("l2", "local", old),
		cmd("p1", "imported:peer", newer), cmd("p2", "imported:peer", newer),
	}
	if err := os.MkdirAll(filepath.Join(dir+".tmp", "buckets"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := writeSessions(dir+".tmp", dir, ss, nil, ""); err != nil {
		t.Fatal(err)
	}
	use, ok := CommandHistory(dir, "make release")
	if !ok {
		t.Fatal("not recorded")
	}
	// The allowed project's last is the older date; the machine-wide Last is
	// the newer withheld one. The per-project split must keep them apart.
	if !use.ByProject["local"].Last.Equal(old) {
		t.Errorf("allowed project's last-run wrong: %v", use.ByProject["local"].Last)
	}
	if !use.Last.Equal(newer) {
		t.Errorf("machine-wide last should be the newer date: %v", use.Last)
	}
}

// A path synced from Windows uses backslashes; CrossBase must split them like a
// Unix path so a same-file lookup still matches.
func TestCrossBaseSplitsBothSeparators(t *testing.T) {
	cases := map[string]string{
		`C:\src\pkg\main.go`: "main.go",
		"/home/u/proj/x.go":  "x.go",
		`a\b\c`:              "c",
		"bare.txt":           "bare.txt",
	}
	for in, want := range cases {
		if got := CrossBase(in); got != want {
			t.Errorf("CrossBase(%q) = %q, want %q", in, got, want)
		}
	}
}

// Switching IsCurrentVersion/Damaged to the cached manifest read must not let a
// stale cache mask a store that changed under it: the cache is keyed on
// manifest.gob's mtime+size, so a corrupted manifest is still seen as damaged.
func TestCachedManifestChecksSeeCorruption(t *testing.T) {
	dir := t.TempDir()
	ss := []model.Session{{
		Harness: "claude", ID: "s", Project: "p",
		Messages: []model.Message{{Role: "user", Text: "hi"}},
	}}
	if err := os.MkdirAll(filepath.Join(dir+".tmp", "buckets"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := writeSessions(dir+".tmp", dir, ss, nil, ""); err != nil {
		t.Fatal(err)
	}
	if !IsCurrentVersion(dir) {
		t.Fatal("a fresh build is not current")
	}
	if Damaged(dir) {
		t.Fatal("a fresh build reads as damaged")
	}
	// Corrupt the manifest — a different size/mtime invalidates the cache.
	if err := os.WriteFile(filepath.Join(dir, "manifest.gob"), []byte("not a gob at all, longer than before"), 0o600); err != nil {
		t.Fatal(err)
	}
	if !Damaged(dir) {
		t.Error("a corrupted manifest is not detected through the cache")
	}
}

// The streamed pass must write what moving the whole map wrote, including
// overlays, fresh-only buckets and neighbour words in different buckets (#6072).
func TestCompactingPostingsMatchWholeMap(t *testing.T) {
	for _, tc := range []string{"whole", "missing", "bad base", "bad overlay", "search only", "no neighbours", "no carried"} {
		t.Run(tc, func(t *testing.T) {
			proj, inc := replaceStore(t, 8)
			old, err := readManifest(inc)
			if err != nil {
				t.Fatal(err)
			}
			bdir := filepath.Join(inc, "buckets")
			data, err := readBucket(filepath.Join(bdir, bucket("twhy")+".bin"))
			if err != nil {
				t.Fatal(err)
			}
			// An overlay-only word survives through carried records. A base
			// bucket containing only a replaced record must disappear.
			ov := overlayPath(filepath.Join(bdir, bucket("twhy")+".bin"), 1)
			if err = writeBucket(ov, map[string][]posting{"twalrus": data["twhy"]}); err != nil {
				t.Fatal(err)
			}
			data, err = readBucket(filepath.Join(bdir, bucket("ts007")+".bin"))
			if err != nil {
				t.Fatal(err)
			}
			if err = writeBucket(filepath.Join(bdir, bucket("txdeleted")+".bin"), map[string][]posting{"txdeleted": data["ts007"]}); err != nil {
				t.Fatal(err)
			}
			old.BucketFiles = countBucketFiles(bdir)
			workerCap := min(runtime.NumCPU(), compactPostingsWorkers)
			baseNames, err := postingBucketNames(bdir, old.BucketFiles)
			if err != nil {
				t.Fatal(err)
			}
			if len(baseNames) <= workerCap {
				t.Fatalf("control: store has %d buckets, need more than the %d workers", len(baseNames), workerCap)
			}
			neighbours := map[string][]string{
				"why": {"walrus", "quokka", "ledgerfresh", "xdeleted", "qqabsent", "zabsent"},
			}
			if tc != "no neighbours" {
				if err = writeGobAtomic(cooccurPath(inc), neighbours); err != nil {
					t.Fatal(err)
				}
			} else if err = os.Remove(cooccurPath(inc)); err != nil && !os.IsNotExist(err) {
				t.Fatal(err)
			}
			switch tc {
			case "missing":
				if err = os.Remove(ov); err != nil {
					t.Fatal(err)
				}
			case "bad base", "bad overlay":
				p := ov
				if tc == "bad base" {
					p = filepath.Join(bdir, bucket("twhy")+".bin")
				}
				if err = os.WriteFile(p, []byte("not a bucket"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			backup := t.TempDir()
			if err = os.Mkdir(filepath.Join(backup, "buckets"), 0o700); err != nil {
				t.Fatal(err)
			}
			for rel := range indexFiles(t, inc) {
				if rel != "records.bin" && rel != "cooccur.gob" && filepath.Dir(rel) != "buckets" {
					continue
				}
				b, rerr := os.ReadFile(filepath.Join(inc, rel))
				if rerr != nil {
					t.Fatal(rerr)
				}
				if err = os.WriteFile(filepath.Join(backup, rel), b, 0o600); err != nil {
					t.Fatal(err)
				}
			}
			changed := filepath.Join(proj, "s007.jsonl")
			rewriteSession(t, proj, "s007")
			parityWrite(t, changed, replaceBody("s007", "quokka ledgerfresh "), false)
			old.SearchOnly = tc == "search only"
			rp := &replacePass{
				dir: inc, old: old, progress: io.Discard, files: maps.Clone(old.Files),
				changed: map[string]FileState{changed: old.Files[changed]}, removed: map[string]bool{},
			}
			if tc == "no carried" {
				for p := range old.Files {
					if p != changed {
						rp.removed[p] = true
						delete(rp.files, p)
					}
				}
			}
			wasAppend := appendReplacing
			appendReplacing = false
			t.Cleanup(func() { appendReplacing = wasAppend })
			compactBucketsHeld.Store(0)
			compactBucketsPeak.Store(0)
			if err = rp.run(nil); err != nil {
				t.Fatal(err)
			}
			if rp.appending || !rp.swapped || len(rp.buckets) == 0 {
				t.Fatal("control: no compacting pass with fresh postings committed")
			}
			if tc == "whole" {
				peak := compactBucketsPeak.Load()
				if peak == 0 {
					t.Fatal("compacting replacement did not stream any decoded buckets")
				}
				if peak > int64(workerCap) {
					t.Errorf("compacting replacement held %d decoded buckets, worker cap is %d", peak, workerCap)
				}
				t.Logf("compacting replacement: %d base buckets, peak %d decoded buckets, worker cap %d", len(baseNames), peak, workerCap)
			}
			if held := compactBucketsHeld.Load(); held != 0 {
				t.Errorf("compacting replacement still holds %d decoded buckets", held)
			}
			want, err := carriedPostings(backup, old, rp.carried)
			if err != nil {
				t.Fatal(err)
			}
			mergePostings(want, rp.buckets)
			ref := t.TempDir()
			if err = writeBucketsConcurrent(ref, want); err != nil {
				t.Fatal(err)
			}
			assertBucketFilesEqual(t, filepath.Join(inc, "buckets"), ref)
			var absent map[string]bool
			if !rp.m.SearchOnly {
				absent = cooccurUnindexed(backup, want)
			}
			if tc == "whole" && (!absent["xdeleted"] || !absent["qqabsent"] || !absent["zabsent"] || absent["walrus"] || absent["quokka"] || absent["ledgerfresh"]) {
				t.Fatalf("control: neighbour words do not cover kept, fresh and absent postings: %v", absent)
			}
			// Ask the same path for its result before commit scrubs the map,
			// and hold its progress count to the whole-map writer's count.
			staged := t.TempDir()
			if err = os.Mkdir(filepath.Join(staged, "buckets"), 0o700); err != nil {
				t.Fatal(err)
			}
			copyPass := *rp
			copyPass.dir, copyPass.tmp = backup, staged
			progress := &recordingProgress{}
			progress.Phase("writing postings", 0)
			SetProgress(progress)
			t.Cleanup(func() { SetProgress(nil) })
			got, err := copyPass.writeCompactedPostings()
			SetProgress(nil)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, absent) {
				t.Errorf("unposted = %v, want %v", got, absent)
			}
			if got := progress.advances["writing postings"]; got != len(want) {
				t.Errorf("advanced %d buckets, want %d", got, len(want))
			}
			assertBucketFilesEqual(t, filepath.Join(staged, "buckets"), ref)
			if tc == "whole" {
				// A write failure must not be mistaken for a damaged input.
				blocked := t.TempDir()
				if err = os.WriteFile(filepath.Join(blocked, "buckets"), nil, 0o600); err != nil {
					t.Fatal(err)
				}
				copyPass.tmp = blocked
				passKeyed.Store(0)
				if _, werr := copyPass.writeCompactedPostings(); werr == nil {
					t.Fatal("writing through a regular file succeeded")
				}
				if got := passKeyed.Load(); got != 0 {
					t.Errorf("write failure retokenized %d records", got)
				}
			}
		})
	}
}

// carriedPostings and movedPostings keep the former whole-map implementation
// as the reference for the streaming writer, including its retokenize fallback.
func carriedPostings(dir string, old Manifest, c *carriedRecords) (bucketPostings, error) {
	if len(c.old) == 0 {
		return bucketPostings{}, nil
	}
	c.index()
	if out, err := movedPostings(filepath.Join(dir, "buckets"), old.BucketFiles, c); err == nil {
		return out, nil
	}
	return retokenizeCarried(filepath.Join(dir, "records.bin"), tablesFromManifest(old), c)
}

func movedPostings(bdir string, want int, c *carriedRecords) (bucketPostings, error) {
	names, err := postingBucketNames(bdir, want)
	if err != nil {
		return nil, err
	}
	workers := min(runtime.NumCPU(), len(names))
	parts := make([]map[string][]posting, len(names))
	var next atomic.Int64
	// The first error, under a lock: readBucket's errors are of several
	// types, which an atomic.Value refuses to hold in turn.
	var mu sync.Mutex
	var failed error
	fail := func(err error) {
		mu.Lock()
		if failed == nil {
			failed = err
		}
		mu.Unlock()
	}
	stopped := func() bool {
		mu.Lock()
		defer mu.Unlock()
		return failed != nil
	}
	var wg sync.WaitGroup
	for range workers {
		wg.Go(func() {
			for {
				i := int(next.Add(1)) - 1
				if i >= len(names) || stopped() {
					return
				}
				data, err := readBucket(filepath.Join(bdir, names[i]))
				if err != nil {
					fail(err)
					return
				}
				parts[i] = c.move(data)
			}
		})
	}
	wg.Wait()
	if failed != nil {
		return nil, failed
	}
	out := make(bucketPostings, len(names))
	for i, n := range names {
		if len(parts[i]) > 0 {
			out[strings.TrimSuffix(n, ".bin")] = parts[i]
		}
	}
	return out, nil
}

func assertBucketFilesEqual(t *testing.T, gotDir, wantDir string) {
	t.Helper()
	got, want := indexFiles(t, gotDir), indexFiles(t, wantDir)
	if len(got) != len(want) {
		t.Errorf("bucket files = %d, want %d", len(got), len(want))
	}
	for name := range want {
		a, err := os.ReadFile(filepath.Join(gotDir, name))
		if err != nil {
			t.Fatal(err)
		}
		b, err := os.ReadFile(filepath.Join(wantDir, name))
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(a, b) {
			t.Errorf("bucket %s differs from the whole-map writer", name)
		}
	}
}

func compactionState(id, workspace string, at time.Time) CompactionState {
	return CompactionState{
		SessionID: id, Harness: "codex", Workspace: workspace, Project: "example",
		TranscriptPath: filepath.Join(workspace, "rollout.jsonl"), SourceDigest: "digest-" + id,
		SourceSize: 42, SourceMTime: at, CapturedAt: at,
		Data: model.CompactionContext{Conclusions: []model.ContextFact{{Text: "api_key=ABCDEFGHIJKLMNOPQRSTUV"}}},
	}
}

func TestCompactionPersistsRedactedStateWithoutAnIndex(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "index.db")
	workspace := filepath.Join(t.TempDir(), "workspace")
	at := time.Date(2026, 9, 10, 1, 2, 3, 0, time.UTC)
	saved, err := PutCompaction(dir, compactionState("native-1", workspace, at))
	if err != nil {
		t.Fatal(err)
	}
	if saved.Revision != 1 || strings.Contains(saved.Data.Conclusions[0].Text, "ABCDEFGHIJKLMNOPQRSTUV") {
		t.Fatalf("stored=%#v", saved)
	}
	if IsCurrentVersion(dir) {
		t.Fatal("compaction-only manifest must not claim a searchable current index")
	}
	got, found, err := Compaction(dir, "native-1", workspace)
	if err != nil || !found || got.Revision != 1 {
		t.Fatalf("got=%#v found=%t err=%v", got, found, err)
	}
	if _, found, err := Compaction(dir, "native-1", filepath.Join(workspace, "other")); err != nil || found {
		t.Fatalf("workspace isolation found=%t err=%v", found, err)
	}
}

func TestCompactionDeduplicatesAndRejectsOlderCapture(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "index.db")
	workspace := t.TempDir()
	newer := compactionState("native-1", workspace, time.Unix(20, 0))
	first, err := PutCompaction(dir, newer)
	if err != nil {
		t.Fatal(err)
	}
	duplicate := newer
	duplicate.CapturedAt = newer.CapturedAt.Add(time.Hour)
	if got, err := PutCompaction(dir, duplicate); err != nil || got.Revision != first.Revision || !got.CapturedAt.Equal(first.CapturedAt) {
		t.Fatalf("duplicate=%#v err=%v", got, err)
	}
	older := newer
	older.SourceDigest = "older"
	older.CapturedAt = newer.CapturedAt.Add(-time.Second)
	if _, err := PutCompaction(dir, older); !errors.Is(err, ErrCompactionStale) {
		t.Fatalf("older err=%v, want ErrCompactionStale", err)
	}
}

func TestCompactionBoundsRetentionAndHonorsPrivacy(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "index.db")
	workspace := t.TempDir()
	for i := range compactionStateCap + 1 {
		state := compactionState("native-"+string(rune('a'+i)), workspace, time.Unix(int64(i+1), 0))
		if _, err := PutCompaction(dir, state); err != nil {
			t.Fatal(err)
		}
	}
	core, err := readCompactionCore(dir)
	if err != nil || len(core.Compactions) != compactionStateCap {
		t.Fatalf("states=%d err=%v", len(core.Compactions), err)
	}
	if _, found, err := Compaction(dir, "native-a", workspace); err != nil || found {
		t.Fatalf("oldest found=%t err=%v", found, err)
	}

	home := t.TempDir()
	setHome(t, home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "config"))
	if err := writeTombstones(map[string]bool{"codex:native-b": true}); err != nil {
		t.Fatal(err)
	}
	if _, found, err := Compaction(dir, "native-b", workspace); err != nil || found {
		t.Fatalf("forgotten found=%t err=%v", found, err)
	}
	if _, err := PutCompaction(dir, compactionState("native-b", workspace, time.Now())); !errors.Is(err, ErrCompactionForgotten) {
		t.Fatalf("put forgotten err=%v", err)
	}

	t.Setenv("DEJA_EXCLUDE_PROJECTS", "private-*")
	excluded := compactionState("native-private", workspace, time.Now())
	excluded.Project = "private-app"
	if _, err := PutCompaction(dir, excluded); !errors.Is(err, ErrCompactionIgnored) {
		t.Fatalf("put excluded err=%v", err)
	}
}

func TestCompactionRemovalMatchesForgetSelectors(t *testing.T) {
	states := map[string]CompactionState{}
	workspace := t.TempDir()
	state := compactionState("native-1", workspace, time.Unix(20, 0))
	states[compactionKey(state.SessionID, workspace)] = state
	m := Manifest{Compactions: states}
	keys := removeMatchingCompactions(&m, ForgetOptions{Project: "exam"}, "")
	if len(keys) != 1 || keys[0] != "codex:native-1" || len(m.Compactions) != 0 {
		t.Fatalf("keys=%v states=%#v", keys, m.Compactions)
	}
}

func TestCompactionCoreSurvivesTheFirstIndexBuildBoundary(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "index.db")
	workspace := t.TempDir()
	state := compactionState("native-1", workspace, time.Unix(20, 0))
	if _, err := PutCompaction(dir, state); err != nil {
		t.Fatal(err)
	}
	carried := compactionsForRebuild(dir, nil)
	if len(carried) != 1 {
		t.Fatalf("carried=%#v", carried)
	}
	if err := writeManifest(dir, Manifest{Version: version, Files: map[string]FileState{}, Sessions: map[string]SessionMeta{}, Compactions: carried}); err != nil {
		t.Fatal(err)
	}
	m, err := readManifest(dir)
	if err != nil || len(m.Compactions) != 1 {
		t.Fatalf("manifest=%#v err=%v", m.Compactions, err)
	}
	if got := compactionsForRebuild(dir, map[string]bool{"codex:native-1": true}); len(got) != 0 {
		t.Fatalf("tombstoned state carried=%#v", got)
	}
}

func TestPutCompactionFallsBackToSidecarWhileIndexLocked(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "config"))

	dir := filepath.Join(t.TempDir(), "index.db")
	workspace := filepath.Join(t.TempDir(), "workspace")
	unlock, locked, err := tryLockDir(dir)
	if err != nil || !locked {
		t.Fatalf("tryLockDir locked=%t err=%v", locked, err)
	}
	lockedHeld := true
	defer func() {
		if lockedHeld {
			unlock()
		}
	}()

	saved, err := PutCompaction(dir, compactionState("native-busy", workspace, time.Unix(40, 0)))
	if err != nil {
		t.Fatalf("PutCompaction while locked failed: %v", err)
	}
	if saved.Revision != 1 {
		t.Fatalf("saved revision=%d, want 1", saved.Revision)
	}
	got, found, err := Compaction(dir, "native-busy", workspace)
	if err != nil || !found || got.Revision != 1 {
		t.Fatalf("Compaction while locked got=%#v found=%t err=%v", got, found, err)
	}

	unlock()
	lockedHeld = false

	carried := compactionsForRebuild(dir, nil)
	if _, ok := carried[compactionKey("native-busy", workspace)]; !ok {
		t.Fatalf("compactionsForRebuild missing sidecar entry: %#v", carried)
	}
	if _, err := Forget(dir, ForgetOptions{Session: "codex:native-busy"}); err != nil {
		t.Fatalf("Forget sidecar entry: %v", err)
	}
	if _, found, err := Compaction(dir, "native-busy", workspace); err != nil || found {
		t.Fatalf("forgotten sidecar entry still present: found=%t err=%v", found, err)
	}
	if _, err := os.Stat(compactionSidecarPath(dir)); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("sidecar file not removed after Forget: err=%v", err)
	}
}

func TestBuildCommandsSkipsAntigravitySchedulerTasks(t *testing.T) {
	dir := t.TempDir()
	at := time.Date(2026, 8, 1, 10, 0, 0, 0, time.UTC)
	msgs := []model.Message{
		{Role: roleCommand, Text: "$ Timer: 60s (check status)", Time: at},
		{Role: roleCommand, Text: "$ Cron: */5 * * * * cleanup", Time: at.Add(time.Minute)},
		{Role: roleCommand, Text: "$ golangci-lint run", Time: at.Add(2 * time.Minute)},
	}
	sessions := []model.Session{
		{ID: "ag-1", Harness: "antigravity", Project: "app", Messages: msgs},
		{ID: "ag-2", Harness: "antigravity", Project: "app", Messages: msgs},
	}
	buildCommands(dir, sessions)
	uses := ReadCommands(dir)
	if len(uses) != 1 || uses[0].Command != "$ golangci-lint run" {
		t.Fatalf("ReadCommands = %#v, want only $ golangci-lint run", uses)
	}
}

func TestCompactionBoundsWholePersistedState(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "index.db")
	state := compactionState("native-1", t.TempDir(), time.Now())
	state.Project = strings.Repeat("p", compactionStateSize)
	if _, err := PutCompaction(dir, state); err == nil || !strings.Contains(err.Error(), "24 KiB") {
		t.Fatalf("oversized state err=%v", err)
	}
}

func TestForgetRemovesCompactionPacketAndTombstonesItsSession(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "config"))
	dir := filepath.Join(home, "index.db")
	t.Setenv("DEJA_INDEX_DIR", dir)
	workspace := t.TempDir()
	state := compactionState("native-forget", workspace, time.Now())
	if err := os.MkdirAll(filepath.Join(dir, "buckets"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := writeManifest(dir, Manifest{
		Version: version, Files: map[string]FileState{}, Sessions: map[string]SessionMeta{
			"codex:native-forget": {ID: "native-forget", Harness: "codex", Project: "example", Updated: state.CapturedAt},
		},
		Compactions: map[string]CompactionState{compactionKey(state.SessionID, state.Workspace): state},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := Forget(dir, ForgetOptions{Session: "native-forget"}); err != nil {
		t.Fatal(err)
	}
	if _, found, err := Compaction(dir, state.SessionID, workspace); err != nil || found {
		t.Fatalf("forgotten compaction found=%t err=%v", found, err)
	}
	if !Tombstoned("codex:native-forget") {
		t.Fatal("forget did not tombstone the compaction session")
	}
}

func TestForgetRemovesCompactionOnlyPackets(t *testing.T) {
	for _, tc := range []struct {
		name string
		o    ForgetOptions
	}{
		{name: "session", o: ForgetOptions{Session: "native-only"}},
		{name: "project", o: ForgetOptions{Project: "example"}},
		{name: "before", o: ForgetOptions{Before: time.Unix(21, 0)}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			setHome(t, home)
			t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "config"))
			dir := filepath.Join(home, "index.db")
			workspace := t.TempDir()
			state := compactionState("native-only", workspace, time.Unix(20, 0))
			if _, err := PutCompaction(dir, state); err != nil {
				t.Fatal(err)
			}
			result, err := Forget(dir, tc.o)
			if err != nil || result.Sessions != 1 || result.Tombstones != 1 {
				t.Fatalf("result=%#v err=%v", result, err)
			}
			if _, found, err := Compaction(dir, state.SessionID, workspace); err != nil || found {
				t.Fatalf("forgotten compaction found=%t err=%v", found, err)
			}
			if _, err := PutCompaction(dir, state); !errors.Is(err, ErrCompactionForgotten) {
				t.Fatalf("recapture err=%v", err)
			}
		})
	}
}

func TestForgetCompactionOnlyExactSessionDoesNotUsePrefix(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "config"))
	dir := filepath.Join(home, "index.db")
	workspace := t.TempDir()
	first := compactionState("native-only", workspace, time.Unix(20, 0))
	second := compactionState("native-only-extra", workspace, time.Unix(21, 0))
	if _, err := PutCompaction(dir, first); err != nil {
		t.Fatal(err)
	}
	if _, err := PutCompaction(dir, second); err != nil {
		t.Fatal(err)
	}
	result, err := Forget(dir, ForgetOptions{Session: first.SessionID})
	if err != nil || result.Sessions != 1 || result.Keys[0] != "codex:native-only" {
		t.Fatalf("result=%#v err=%v", result, err)
	}
	if _, found, err := Compaction(dir, first.SessionID, workspace); err != nil || found {
		t.Fatalf("exact compaction found=%t err=%v", found, err)
	}
	if _, found, err := Compaction(dir, second.SessionID, workspace); err != nil || !found {
		t.Fatalf("prefix compaction found=%t err=%v", found, err)
	}
}

func TestForgetCompactionOnlyPersistsMirrorBeforeFirstIndex(t *testing.T) {
	tmp := hermeticIndexEnv(t)
	dir := filepath.Join(tmp, "index")
	t.Setenv("DEJA_INDEX_DIR", dir)
	path := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "project", "dead.jsonl")
	write(t, path, claudeLine("dead-session", "2026-01-02T03:04:05Z", "compaction privacy mirror needle"))
	state := compactionState("dead-session", filepath.Dir(path), time.Unix(20, 0))
	state.Harness = "claude"
	state.Project = "project"
	state.TranscriptPath = path
	if _, err := PutCompaction(dir, state); err != nil {
		t.Fatal(err)
	}
	if _, err := Forget(dir, ForgetOptions{Session: state.SessionID}); err != nil {
		t.Fatal(err)
	}
	if !readTombstoneFile(tombstoneMirrorPath(dir))["claude:dead-session"] {
		t.Fatal("compaction-only forget did not write the index-local tombstone mirror")
	}
	if err := os.RemoveAll(privacyDir()); err != nil {
		t.Fatal(err)
	}
	if err := Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	if got, err := Search(dir, search.Options{Query: "compaction privacy mirror", All: true}); err != nil || len(got) != 0 {
		t.Fatalf("rebuilt forgotten source=%#v err=%v", got, err)
	}
}

// A compaction summary is the harness handing a conversation back to itself.
// The preamble that introduced it is stripped as an injected line, so what is
// left starts at the summary — and 23 of the last 800 sessions on a real store
// were named after it, which is what the recall block then quoted back.
func TestACompactionSummaryDoesNotNameASession(t *testing.T) {
	tmp := t.TempDir()
	setHome(t, tmp)
	t.Setenv("USERPROFILE", tmp)
	claude := filepath.Join(tmp, "claude")
	t.Setenv("DEJA_CLAUDE_ROOT", claude)
	t.Setenv("DEJA_CODEX_ROOT", filepath.Join(tmp, "codex"))
	t.Setenv("DEJA_OPENCODE_DB", filepath.Join(tmp, "none.db"))
	t.Setenv("DEJA_NOTES_FILE", filepath.Join(tmp, "notes.jsonl"))
	proj := filepath.Join(claude, "-work-app")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	summary := "This session is being continued from a previous conversation.\\n\\n" +
		"Summary:\\n1. Primary Request and Intent:\\n   The user asked for the retry budget.\\n" +
		"2. Key Technical Concepts:\\n   the quokkabloom fetcher\\n"
	lines := `{"type":"user","sessionId":"s1","cwd":"/work/app","timestamp":"2026-01-02T03:04:05Z","message":{"role":"user","content":"` + summary + `"}}
{"type":"user","sessionId":"s1","cwd":"/work/app","timestamp":"2026-01-02T03:05:05Z","message":{"role":"user","content":"why does the quokkabloom fetcher time out"}}
{"type":"assistant","sessionId":"s1","cwd":"/work/app","timestamp":"2026-01-02T03:06:05Z","message":{"role":"assistant","content":"the retry budget is capped at four"}}
`
	if err := os.WriteFile(filepath.Join(proj, "s1.jsonl"), []byte(lines), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(tmp, "index.db")
	if err := Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	m, err := readManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Sessions) == 0 {
		t.Fatal("nothing indexed, so this measures nothing")
	}
	for _, meta := range m.Sessions {
		if strings.HasPrefix(strings.ToLower(meta.Title), "summary:") {
			t.Errorf("a session is named by its compaction summary: %q", meta.Title)
		}
		if !strings.Contains(meta.Title, "quokkabloom") {
			t.Errorf("the question under the summary did not name the session: %q", meta.Title)
		}
	}
}

// And a person's own "Summary:" is theirs.
func TestAPersonsSummaryStillNamesTheirSession(t *testing.T) {
	if compactionSummary("Summary: we moved the retry budget to four and closed the ticket") {
		t.Error("a sentence a person wrote was read as a compaction")
	}
	if !compactionSummary("Summary:\n1. Primary Request and Intent:\n   the retry budget") {
		t.Error("the compaction template was not recognised")
	}
}

// `passParsed`, `passRead` and `passWholeStores` are package state, cleared by
// `beginPass` and read by the fold, on the grounds that a pass holds the
// directory lock and only one is ever in flight. Two passes in one process is
// the case that grounds rests on — the MCP server can start one while a hook
// warmup starts another — and nothing exercised it: the swap tests read during
// a rebuild, they do not run two.
//
// The reader half is here too, and asks for more than the swap test does: not
// only that a search avoids naming an internal path, but that it keeps
// answering. A rebuild that empties the answer for a moment is a search that
// says "no matches" about a store that has them.
func TestTwoPassesAtOnceKeepTheCountsAndTheAnswers(t *testing.T) {
	// Not a Windows-shaped test: a real defect there, which this test found the
	// first time the leg was run against it. The swap renames the index
	// directory, and Windows refuses that while any handle inside it is open,
	// so the losing pass gives up and the store is left a session short
	// (#2228). Skipped rather than deleted, so it comes back with the fix.
	if runtime.GOOS == "windows" {
		t.Skip("the index swap's rename is refused while a handle is open — #2228")
	}
	tmp := t.TempDir()
	setHome(t, tmp)
	claude := filepath.Join(tmp, "claude")
	t.Setenv("DEJA_CLAUDE_ROOT", claude)
	t.Setenv("DEJA_CODEX_ROOT", filepath.Join(tmp, "codex"))
	t.Setenv("DEJA_OPENCODE_DB", filepath.Join(tmp, "none.db"))
	t.Setenv("DEJA_NOTES_FILE", filepath.Join(tmp, "notes.jsonl"))
	proj := filepath.Join(claude, "-work-app")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	stamp := time.Now().Add(-time.Hour).UTC().Format(time.RFC3339)
	const sessions = 40
	for i := range sessions {
		line := fmt.Sprintf(`{"type":"user","sessionId":"s%02d","timestamp":%q,"cwd":"/work/app",`+
			`"message":{"role":"user","content":"turn %d: the pool timed out during the migration"}}`, i, stamp, i)
		if err := os.WriteFile(filepath.Join(proj, fmt.Sprintf("s%02d.jsonl", i)), []byte(line+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	dir := filepath.Join(tmp, "index.db")
	// A store that already exists, so the readers are asking a real index
	// rather than a missing one.
	if err := Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}

	var wg sync.WaitGroup
	errs := make([]error, 4)
	// Both passes have work to do: a rebuild reads everything, and the file
	// written here is what makes the incremental one more than an early
	// return. Without it the second call finds the manifest fresh and never
	// takes the lock at all.
	newLine := fmt.Sprintf(`{"type":"user","sessionId":"s%02d","timestamp":%q,"cwd":"/work/app",`+
		`"message":{"role":"user","content":"one more turn about the pool and the migration"}}`, sessions, stamp)
	if err := os.WriteFile(filepath.Join(proj, fmt.Sprintf("s%02d.jsonl", sessions)), []byte(newLine+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// All four start together, and the windows the two passes occupy are kept,
	// so the test can say whether they really contended: without a barrier the
	// scheduler can run one pass to completion before the other begins, and
	// the overlap this is about would be a matter of luck.
	start := make(chan struct{})
	var mu sync.Mutex
	starts := make([]time.Time, 2)
	ends := make([]time.Time, 2)
	passesDone := make(chan struct{})
	for i := range errs {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			switch i {
			case 0, 1:
				// One rebuild and one incremental pass, which is the pair a
				// warmup and a tool call make.
				mu.Lock()
				starts[i] = time.Now()
				mu.Unlock()
				err := Ensure(dir, "", i == 0, nil)
				mu.Lock()
				ends[i], errs[i] = time.Now(), err
				mu.Unlock()
			default:
				// Until the passes are finished, not a fixed number of rounds:
				// forty sessions rebuild in milliseconds, and a loop that
				// counts can be over before the pass begins.
				// At least one round happens whatever the scheduler does, so a
				// reader that lands late fails nothing; what it must never do
				// is come back empty or corrupt.
				rounds := 0
				for done := false; !done; {
					select {
					case <-passesDone:
						done = true
					default:
					}
					// The door every surface uses. The plain one fails here
					// about one round in eighty — a reader that lands between
					// a pass's append and its manifest write is told the store
					// is crash-truncated (#2176) — which is what the recovery
					// wrapper is for, and what `deja files` was missing.
					hits, err := SearchWithRecovery(dir, search.Options{Query: "pool migration", All: true}, nil)
					rounds++
					if err != nil {
						errs[i] = fmt.Errorf("round %d: %w", rounds, err)
						return
					}
					if len(hits) == 0 {
						errs[i] = fmt.Errorf("round %d: the search found nothing while a pass ran", rounds)
						return
					}
				}
			}
		}(i)
	}
	close(start)
	// The readers stop once both passes are back.
	go func() {
		for i := range 2 {
			for {
				mu.Lock()
				done := !ends[i].IsZero()
				mu.Unlock()
				if done {
					break
				}
				time.Sleep(time.Millisecond)
			}
		}
		close(passesDone)
	}()
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Errorf("goroutine %d: %v", i, err)
		}
	}
	// The two calls really did overlap: one was inside the other's window,
	// waiting on the lock. Without this the assertions below could be about
	// two passes that never met.
	if !starts[0].Before(ends[1]) || !starts[1].Before(ends[0]) {
		t.Errorf("the passes did not overlap (%v..%v and %v..%v), so nothing here was contended",
			starts[0], ends[0], starts[1], ends[1])
	}

	// The store is what one pass would have left: every session once.
	m, err := readManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Sessions) != sessions+1 {
		t.Errorf("the store holds %d sessions after two passes, want %d", len(m.Sessions), sessions+1)
	}
	counted := 0
	for _, meta := range m.Sessions {
		counted += meta.Counted
	}
	if counted != sessions+1 {
		t.Errorf("the sessions count %d messages between them, want %d", counted, sessions+1)
	}

	// And the state one pass left behind does not move the next one.
	if err = Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	after, err := readManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(after.Sessions) != sessions+1 {
		t.Errorf("a pass after the two holds %d sessions, want %d", len(after.Sessions), sessions+1)
	}
	countedAfter := 0
	for _, meta := range after.Sessions {
		countedAfter += meta.Counted
	}
	if countedAfter != counted {
		t.Errorf("the counts moved from %d to %d with nothing new to read", counted, countedAfter)
	}
}

// Continue compacts by rewriting the session file to the summary alone: on a
// stand with cn 1.5.47, seven items became one and the second turn was gone
// from disk. The index held those turns and threw them away on the next pass
// (#4795). They have to survive that pass, a second compaction, and a rebuild,
// with the summary filed under its own role.
func TestContinueTurnsSurviveTheCompactionThatRewroteTheirFile(t *testing.T) {
	tmp := hermeticIndexEnv(t)
	root := filepath.Join(tmp, "continue")
	t.Setenv("DEJA_CONTINUE_ROOT", root)
	p := filepath.Join(root, "sessions", "s1.json")
	idx := filepath.Join(tmp, "idx")
	stamp := time.Now().Add(-time.Hour)
	put := func(history string) {
		t.Helper()
		write(t, p, `{"sessionId":"s1","title":"t","workspaceDirectory":"/tmp/proj","history":[`+history+`]}`)
		stamp = stamp.Add(time.Minute)
		if err := os.Chtimes(p, stamp, stamp); err != nil {
			t.Fatal(err)
		}
		if err := Ensure(idx, "", false, nil); err != nil {
			t.Fatal(err)
		}
	}
	held := func(dir string) []string {
		t.Helper()
		recs, err := ReadRecords(dir)
		if err != nil {
			t.Fatal(err)
		}
		var out []string
		for _, r := range recs {
			if r.Record.Key == "continue:s1" {
				out = append(out, r.Record.Role+": "+r.Record.Text)
			}
		}
		sort.Strings(out)
		return out
	}
	user := func(text string) string { return `{"message":{"role":"user","content":"` + text + `"}}` }
	said := func(text string) string { return `{"message":{"role":"assistant","content":"` + text + `"}}` }
	summary := func(text string) string {
		return `{"message":{"role":"assistant","content":"` + text + `"},"conversationSummary":"` + text + `"}`
	}

	put(user("why does zanzibarflux break the build") + "," + said("quokkaplex is stale") + "," + user("second turn TURN2zq"))
	put(summary("SUMMARY-ONE the build was fixed"))
	put(summary("SUMMARY-ONE the build was fixed") + "," + user("third turn TURN3zq"))
	put(summary("SUMMARY-TWO all of it"))

	want := []string{
		"assistant: quokkaplex is stale",
		sources.RoleSummary + ": SUMMARY-ONE the build was fixed",
		sources.RoleSummary + ": SUMMARY-TWO all of it",
		"user: second turn TURN2zq",
		"user: third turn TURN3zq",
		"user: why does zanzibarflux break the build",
	}
	if got := held(idx); strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("after the compactions the index holds:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	if err := Ensure(idx, "", true, nil); err != nil {
		t.Fatal(err)
	}
	if got := held(idx); strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("a rebuild lost what the file no longer holds:\n%s", strings.Join(got, "\n"))
	}
}

// Copilot CLI writes session.start before the first prompt, and a build that
// lands in between (the prompt hook's) stores the file's offset with no
// session. The rest arrived as a tail with no project and the prompt's time as
// the start, and the session dropped out of its project's recall (#5136).
func TestACopilotSessionIndexedBeforeItsFirstPromptMatchesRebuild(t *testing.T) {
	tmp := parityEnv(t, map[string]string{"DEJA_COPILOT_ROOT": "copilot"})
	const id = "0199cccc-0000-7000-8000-000000000002"
	p := filepath.Join(tmp, "copilot", id, "events.jsonl")
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	head := `{"type":"session.start","timestamp":"2026-07-17T09:00:00Z","data":{"sessionId":"` + id + `","startTime":"2026-07-17T09:00:00Z","context":{"cwd":"/w/proj"}}}` + "\n"
	if err := os.WriteFile(p, []byte(head), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(tmp, "idx.db")
	parityPass(t, dir, false)
	f, err := os.OpenFile(p, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = f.WriteString(`{"type":"user.message","timestamp":"2026-07-17T09:00:07Z","data":{"content":"fix the retry loop"}}` + "\n" +
		`{"type":"assistant.message","timestamp":"2026-07-17T09:00:09Z","data":{"content":"capped it at five"}}` + "\n")
	_ = f.Close()
	parityPass(t, dir, false)
	matchesRebuild(t, dir, "copilot", id)
}

func TestCorruptBucketDoesNotPanic(t *testing.T) {
	dir := t.TempDir()
	cases := map[string][]byte{
		"huge count":   append(append(slices.Clone(bucketMagic), encodeUvarint(1<<62)...), 0),
		"huge token":   append(append(slices.Clone(bucketMagic), encodeUvarint(1)...), encodeUvarint(1<<62)...),
		"truncated":    slices.Clone(bucketMagic),
		"bad magic":    []byte("XXXXXXXX"),
		"empty file":   {},
		"garbage tail": append(slices.Clone(bucketMagic), 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff),
	}
	for name, body := range cases {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, body, 0o644); err != nil {
			t.Fatal(err)
		}
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("%s: panicked instead of failing: %v", name, r)
				}
			}()
			_, f, err := openBucketDir(p)
			if f != nil {
				_ = f.Close()
			}
			if err == nil {
				t.Fatalf("%s: accepted as valid", name)
			}
			if !errors.Is(err, errCorruptIndex) && !os.IsNotExist(err) {
				t.Logf("%s: err=%v (not errCorruptIndex)", name, err)
			}
		}()
	}
}

func encodeUvarint(v uint64) []byte {
	b := make([]byte, binary.MaxVarintLen64)
	return b[:binary.PutUvarint(b, v)]
}

// A block length under MaxUint32 still passed, so a bucket naming four
// gigabytes for one token turned a recoverable corruption into an OOM.
func TestBucketBlockLengthBoundedByFile(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "big.bin")
	body := append(slices.Clone(bucketMagic), encodeUvarint(1)...)
	body = append(body, encodeUvarint(1)...)
	body = append(body, 'x')
	body = append(body, encodeUvarint(4_000_000_000)...)
	if err := os.WriteFile(p, body, 0o644); err != nil {
		t.Fatal(err)
	}
	_, f, err := openBucketDir(p)
	if f != nil {
		_ = f.Close()
	}
	if err == nil {
		t.Fatal("a four-gigabyte posting block in a 20-byte file was accepted")
	}
	if !errors.Is(err, errCorruptIndex) {
		t.Fatalf("err = %v, want errCorruptIndex so the caller rebuilds", err)
	}
}

// buildWithFiles writes the named transcripts into a fresh store and builds it,
// returning the project directory and the index directory so the caller can
// take files away and build again.
func buildWithFiles(t *testing.T, ids ...string) (proj, dir string) {
	t.Helper()
	home := t.TempDir()
	setHome(t, home)
	t.Setenv("USERPROFILE", home)
	root := filepath.Join(home, "claude")
	t.Setenv("DEJA_CLAUDE_ROOT", root)
	proj = filepath.Join(root, "-tmp-e")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, id := range ids {
		line := `{"type":"user","sessionId":"` + id + `","cwd":"/tmp/e","timestamp":"2026-08-01T10:00:00Z","message":{"role":"user","content":"pool exhausted"}}` + "\n"
		if err := os.WriteFile(filepath.Join(proj, id+".jsonl"), []byte(line), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	dir = filepath.Join(home, "idx")
	if err := Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	return proj, dir
}

// The third counter of the family: it says how many indexed files went away
// with their store, and the command layer turns that into "the history that was
// here has gone" rather than "nothing to index yet" (#1762). It accumulated
// across builds like the other two (#1861).
func TestTheEvictedCounterBelongsToTheLastBuild(t *testing.T) {
	proj, dir := buildWithFiles(t, "a", "b", "c")

	// Whole stores, not single files: a file that goes while its directory
	// stays is kept on purpose now (#2970), and an eviction is a store that
	// went away. First a second store with one file, then the first with
	// three; the second count must be its own three, not four.
	other := filepath.Join(filepath.Dir(proj), "other")
	if err := os.MkdirAll(other, 0o755); err != nil {
		t.Fatal(err)
	}
	writeLines(t, filepath.Join(other, "d.jsonl"), claudeLine("d", "2026-01-04T00:00:00Z", "delta text"))
	if err := Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(other); err != nil {
		t.Fatal(err)
	}
	if err := Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	// Deliberately not read: the leftover is what the next build must not
	// inherit.
	if err := os.RemoveAll(proj); err != nil {
		t.Fatal(err)
	}
	if err := Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	if n := ReportEvictedFiles(); n != 3 {
		t.Errorf("the second eviction reported %d files, want its own 3", n)
	}
}

// The family itself: every counter drained by a Report function has to be
// cleared where a build starts, or it reports the process rather than the
// build. Three were found one at a time — emptied and collisions in #1850,
// evicted in #1861 — and this is what makes the fourth a build failure.
func TestEveryReportedCounterIsClearedByABuild(t *testing.T) {
	src, err := os.ReadFile("ingest.go")
	if err != nil {
		t.Fatal(err)
	}
	body := string(src)

	drained := regexp.MustCompile(`func Report\w+\(\) int \{\s*return int\((\w+)\.Swap\(0\)\)`)
	found := drained.FindAllStringSubmatch(body, -1)
	if len(found) < 3 {
		t.Fatalf("expected the counter family, found %d — has the shape changed?", len(found))
	}
	for _, m := range found {
		name := m[1]
		if !strings.Contains(body, name+".Store(0)") {
			t.Errorf("%s is drained by a Report function but never cleared where a build starts: a second build in one process reports the first one's number too", name)
		}
	}
}

func claudeLine(sid, ts, text string) string {
	return `{"type":"user","sessionId":"` + sid + `","timestamp":"` + ts + `","message":{"role":"user","content":"` + text + `"}}` + "\n"
}

// A records.bin cut short by a crash must not read as a fresh index that
// silently returns fewer messages: the size stamp catches the short file and
// the next run rebuilds.
func TestSearchRecoversFromTruncatedRecords(t *testing.T) {
	root, dir := allHarnessEnv(t)
	claudeFile := filepath.Join(root, "claude", "-tmp-p", "s.jsonl")
	write(t, claudeFile, claudeLine("s1", "2026-01-02T03:04:05Z", "recneedle one")+claudeLine("s1", "2026-01-02T03:04:06Z", "recneedle two"))

	o := search.Options{Query: "recneedle", All: true}
	if err := EnsureForSearch(dir, o, false, nil); err != nil {
		t.Fatal(err)
	}
	if ss, err := Search(dir, o); err != nil || len(ss) != 1 || len(ss[0].Messages) != 2 {
		t.Fatalf("baseline search wrong: %#v err=%v", ss, err)
	}

	// Truncate records.bin to simulate a torn write mid-record.
	recs := filepath.Join(dir, "records.bin")
	fi, err := os.Stat(recs)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.Truncate(recs, fi.Size()/2); err != nil {
		t.Fatal(err)
	}

	// Direct Search must flag corruption rather than return a half-scan.
	if _, err = Search(dir, o); !IsCorrupt(err) {
		t.Fatalf("Search over truncated records.bin err=%v, want corrupt index", err)
	}
	// The source files are unchanged, so the only way to heal is the size stamp
	// forcing a rebuild.
	if err = EnsureForSearch(dir, o, false, nil); err != nil {
		t.Fatal(err)
	}
	ss, err := Search(dir, o)
	if err != nil || len(ss) != 1 || len(ss[0].Messages) != 2 {
		t.Fatalf("not healed after rebuild: %#v err=%v", ss, err)
	}
}

// A manifest.gob left half-written by a crash must fail to decode and trigger a
// rebuild, never load as an empty-but-valid index.
func TestEnsureRecoversFromTornManifest(t *testing.T) {
	root, dir := allHarnessEnv(t)
	claudeFile := filepath.Join(root, "claude", "-tmp-p", "s.jsonl")
	write(t, claudeFile, claudeLine("s1", "2026-01-02T03:04:05Z", "manneedle here"))

	o := search.Options{Query: "manneedle", All: true}
	if err := EnsureForSearch(dir, o, false, nil); err != nil {
		t.Fatal(err)
	}

	man := filepath.Join(dir, "manifest.gob")
	fi, err := os.Stat(man)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Truncate(man, fi.Size()/2); err != nil {
		t.Fatal(err)
	}

	if err := EnsureForSearch(dir, o, false, nil); err != nil {
		t.Fatalf("ensure did not heal torn manifest: %v", err)
	}
	if ss, err := Search(dir, o); err != nil || len(ss) != 1 {
		t.Fatalf("not healed: %#v err=%v", ss, err)
	}
}

// The incremental append path writes sessions.gob before manifest.gob so a
// crash between them leaves the *old* manifest in place. This reconstructs that
// exact crash state (old manifest, new sessions + records) and asserts the new
// message is reindexed and found rather than silently dropped.
func TestAppendCrashBetweenSessionsAndManifestReindexes(t *testing.T) {
	root, dir := allHarnessEnv(t)
	claudeFile := filepath.Join(root, "claude", "-tmp-p", "s.jsonl")
	write(t, claudeFile, claudeLine("s1", "2026-01-02T03:04:05Z", "appendneedle first"))

	o := search.Options{Query: "appendneedle", All: true}
	if err := EnsureForSearch(dir, o, false, nil); err != nil {
		t.Fatal(err)
	}
	oldManifest, err := os.ReadFile(filepath.Join(dir, "manifest.gob"))
	if err != nil {
		t.Fatal(err)
	}

	// Append a second complete line and let the incremental path commit it.
	f, err := os.OpenFile(claudeFile, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.WriteString(claudeLine("s1", "2026-01-02T03:04:07Z", "appendneedle second")); err != nil {
		t.Fatal(err)
	}
	_ = f.Close()
	if err = EnsureForSearch(dir, o, false, nil); err != nil {
		t.Fatal(err)
	}

	// Simulate the crash: manifest.gob reverts to its pre-append contents while
	// sessions.gob and records.bin already hold the new message.
	if err = os.WriteFile(filepath.Join(dir, "manifest.gob"), oldManifest, 0o644); err != nil {
		t.Fatal(err)
	}

	if err = EnsureForSearch(dir, o, false, nil); err != nil {
		t.Fatal(err)
	}
	ss, err := Search(dir, search.Options{Query: "appendneedle second", All: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(ss) == 0 {
		t.Fatalf("appended message silently lost after simulated crash: %#v", ss)
	}
}

// writeManifest must commit atomically: no leftover temp files, and the size
// stamp must match records.bin exactly so recordsIntact never false-positives.
func TestWriteManifestAtomicAndStamped(t *testing.T) {
	root, dir := allHarnessEnv(t)
	write(t, filepath.Join(root, "claude", "-tmp-p", "s.jsonl"),
		claudeLine("s1", "2026-01-02T03:04:05Z", "stampneedle"))
	if err := EnsureForSearch(dir, search.Options{Query: "stampneedle"}, false, nil); err != nil {
		t.Fatal(err)
	}
	leftovers, _ := filepath.Glob(filepath.Join(dir, "*.tmp"))
	if len(leftovers) != 0 {
		t.Fatalf("temp files left behind: %v", leftovers)
	}
	m, err := readManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(filepath.Join(dir, "records.bin"))
	if err != nil {
		t.Fatal(err)
	}
	if m.RecordsSize != fi.Size() {
		t.Fatalf("RecordsSize=%d, records.bin=%d", m.RecordsSize, fi.Size())
	}
	if !recordsIntact(dir, m) {
		t.Fatal("recordsIntact false on a clean index")
	}
}

// A torn trailing line longer than the scan window used to make SafeSize
// fall back to the file size, silently skipping the message once completed.
func TestSafeSizeTornLineLongerThanWindow(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "big.jsonl")
	complete := `{"type":"user","sessionId":"s","message":{"role":"user","content":"ok"}}` + "\n"
	torn := `{"type":"user","sessionId":"s","message":{"role":"user","content":"` + strings.Repeat("x", 100*1024)
	if err := os.WriteFile(p, []byte(complete+torn), 0o644); err != nil {
		t.Fatal(err)
	}
	fi, _ := os.Stat(p)
	got := lastCompleteLineOffset(p, fi.Size())
	if got != int64(len(complete)) {
		t.Fatalf("SafeSize=%d want %d (end of last complete line)", got, len(complete))
	}
	// no newline at all -> nothing is safe yet
	p2 := filepath.Join(dir, "noline.jsonl")
	if err := os.WriteFile(p2, []byte(strings.Repeat("y", 70*1024)), 0o644); err != nil {
		t.Fatal(err)
	}
	fi2, _ := os.Stat(p2)
	if got := lastCompleteLineOffset(p2, fi2.Size()); got != 0 {
		t.Fatalf("SafeSize=%d want 0 for newline-free file", got)
	}
}

// The index must not be wider-readable than the agent logs it derives from.
func TestIndexPermissionsAreOwnerOnly(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix permission bits")
	}
	root, dir := allHarnessEnv(t)
	write(t, filepath.Join(root, "claude", "-p-app", "s1.jsonl"),
		`{"type":"user","sessionId":"s1","timestamp":"2026-02-01T10:00:00Z","message":{"role":"user","content":"perm probe"}}`+"\n")
	if err := EnsureForSearch(dir, search.Options{Query: "x", All: true}, true, nil); err != nil {
		t.Fatal(err)
	}
	if fi, err := os.Stat(dir); err != nil || fi.Mode().Perm() != 0o700 {
		t.Fatalf("index dir perm=%v err=%v, want 0700", fi.Mode().Perm(), err)
	}
	err := filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		fi, err := os.Stat(p)
		if err != nil {
			return err
		}
		if d.IsDir() {
			if fi.Mode().Perm() != 0o700 {
				t.Errorf("%s perm=%v want 0700", p, fi.Mode().Perm())
			}
		} else if fi.Mode().Perm() != 0o600 {
			t.Errorf("%s perm=%v want 0600", p, fi.Mode().Perm())
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestIndexPermissionsSupportSharedGroup(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix permission bits")
	}
	root, dir := allHarnessEnv(t)
	t.Setenv("DEJA_SHARED_GROUP", "1")
	write(t, filepath.Join(root, "claude", "-p-app", "s1.jsonl"),
		`{"type":"user","sessionId":"s1","timestamp":"2026-02-01T10:00:00Z","message":{"role":"user","content":"shared group perm probe"}}`+"\n")
	if err := EnsureForSearch(dir, search.Options{Query: "x", All: true}, true, nil); err != nil {
		t.Fatal(err)
	}
	if fi, err := os.Stat(dir); err != nil || fi.Mode().Perm() != 0o770 {
		t.Fatalf("shared index dir perm=%v err=%v, want 0770", fi.Mode().Perm(), err)
	}
	err := filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		fi, err := os.Stat(p)
		if err != nil {
			return err
		}
		if d.IsDir() {
			if fi.Mode().Perm() != 0o770 {
				t.Errorf("%s perm=%v want 0770", p, fi.Mode().Perm())
			}
		} else if fi.Mode().Perm() != 0o660 {
			t.Errorf("%s perm=%v want 0660", p, fi.Mode().Perm())
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := UnsharedFiles(dir); got != 0 {
		t.Fatalf("UnsharedFiles(dir) = %d, want 0", got)
	}
	if err := os.Chmod(filepath.Join(dir, "manifest.gob"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := UnsharedFiles(dir); got == 0 {
		t.Fatalf("UnsharedFiles(dir) = 0 after tightening manifest.gob to 0600, want >0")
	}
	ensureSharedIndexPerms(dir)
	if err := os.WriteFile(dir+".live", []byte("s1 2026-02-01T10:00:00Z\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := UnsharedFiles(dir); got == 0 {
		t.Fatalf("UnsharedFiles(dir) = 0 after tightening .live sidecar to 0600, want >0")
	}
	ensureSharedIndexPerms(dir)
	if got := UnsharedFiles(dir); got != 0 {
		t.Fatalf("UnsharedFiles(dir) = %d after ensureSharedIndexPerms, want 0", got)
	}
}

// threeWordStore builds three sessions, one distinctive word each, and
// returns the env and the index dir.
func threeWordStore(t *testing.T) (*twoWayEnv, string) {
	t.Helper()
	h := newTwoWayEnv(t)
	h.put("app", "s1", hPrompt("app", "s1", 2, 0, "alphaword question about caching")+hSay("app", "s1", 2, 1, "alphaword answer"))
	h.put("app", "s2", hPrompt("app", "s2", 3, 0, "bravoword question about pools")+hSay("app", "s2", 3, 1, "bravoword answer"))
	h.put("app", "s3", hPrompt("app", "s3", 4, 0, "charlieword question about locks")+hSay("app", "s3", 4, 1, "charlieword answer"))
	dir := filepath.Join(h.tmp, "idx.db")
	if err := Ensure(dir, "claude", true, nil); err != nil {
		t.Fatal(err)
	}
	return h, dir
}

// zeroRecordContaining zeroes the whole frame (header + payload) of the first
// record whose text contains word: the hole a crash on a writeback filesystem
// leaves. File length is unchanged, so the manifest size check passes.
func zeroRecordContaining(t *testing.T, dir, word string) { //nolint:unparam // the call names the record it tears
	t.Helper()
	recs, err := ReadRecords(dir)
	if err != nil {
		t.Fatal(err)
	}
	rp := filepath.Join(dir, "records.bin")
	b, err := os.ReadFile(rp)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range recs {
		if strings.Contains(r.Record.Text, word) {
			n := int(binary.LittleEndian.Uint32(b[r.Offset:]))
			for i := r.Offset; i < r.Offset+4+int64(n); i++ {
				b[i] = 0
			}
			if err := os.WriteFile(rp, b, 0o600); err != nil {
				t.Fatal(err)
			}
			invalidateManifestCache(dir)
			return
		}
	}
	t.Fatalf("no record with %q", word)
}

// A zeroed frame in the middle of records.bin is corruption: the search for a
// word in that record says so, so the recovery path rebuilds, instead of
// answering nothing.
func TestAZeroedRecordFailsTheSearchInsteadOfAnsweringNothing(t *testing.T) {
	_, dir := threeWordStore(t)
	zeroRecordContaining(t, dir, "bravoword question")
	if _, err := Search(dir, query.Options{Query: "pools", Limit: 10}); !IsCorrupt(err) {
		t.Fatalf("search over a zeroed record answered err=%v; want a corrupt-index error", err)
	}
}

// A zeroed frame is not the end of the log: the walkers (deep verify counts,
// the rewrite pass's carry-over, secrets, sync push) used to stop there and
// skip every record after it without a word.
func TestARecordWalkDoesNotEndAtAHoleInTheLog(t *testing.T) {
	_, dir := threeWordStore(t)
	zeroRecordContaining(t, dir, "bravoword question")
	tbl, err := loadRecordTables(dir)
	if err != nil {
		t.Fatal(err)
	}
	var sawCharlie bool
	err = eachRecord(filepath.Join(dir, "records.bin"), tbl, func(r Record) {
		if strings.Contains(r.Text, "charlieword") {
			sawCharlie = true
		}
	})
	if !IsCorrupt(err) || sawCharlie {
		t.Fatalf("eachRecord over a mid-log zeroed frame: err=%v, read past it %v; want corruption at the hole", err, sawCharlie)
	}
}

// The rewrite pass carries old records through eachRecord. Over a hole it
// used to keep what came before and commit the loss of the rest; it rebuilds
// instead.
func TestAPassOverAHoleKeepsTheSessionsAfterIt(t *testing.T) {
	h, dir := threeWordStore(t)
	zeroRecordContaining(t, dir, "bravoword question")
	// Rewrite s1's file in place: not an append, so the log is rewritten.
	h.put("app", "s1", hPrompt("app", "s1", 2, 0, "deltaword replaced content entirely")+hSay("app", "s1", 2, 1, "deltaword"))
	if err := Ensure(dir, "claude", false, nil); err != nil {
		t.Fatal(err)
	}
	m, err := readManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Sessions) != 3 {
		t.Errorf("the pass over the hole left %d sessions, want 3", len(m.Sessions))
	}
	for _, word := range []string{"bravoword", "charlieword", "deltaword"} {
		got, err := Search(dir, query.Options{Query: word, Limit: 10})
		if err != nil || len(got) == 0 {
			t.Errorf("%s after the pass: %d hits, err=%v", word, len(got), err)
		}
	}
}

// DeepVerify (deja doctor --deep) must notice a zeroed frame.
func TestDeepVerifyFindsAZeroedRecord(t *testing.T) {
	_, dir := threeWordStore(t)
	zeroRecordContaining(t, dir, "bravoword question")
	rep, err := DeepVerify(dir)
	if err != nil {
		return
	}
	if rep.Clean() {
		t.Fatalf("deep verify reports a store with a zeroed mid-log record as clean: %+v", rep)
	}
}

// A pass killed after it appended records (and postings) but before it
// committed the manifest leaves records.bin longer than RecordsSize. A writer
// that only rewrites the manifest (sync push watermarks, forget's compaction
// removal, AdoptWriteBack) used to re-stamp RecordsSize from stat and adopt
// that tail, and the next pass appended the same turns again.
func TestAManifestOnlyWriteKeepsAnUncommittedTailUncommitted(t *testing.T) {
	h, dir := threeWordStore(t)
	saved := map[string][]byte{}
	for _, n := range []string{"manifest.gob", "sessions.bin"} {
		b, err := os.ReadFile(filepath.Join(dir, n))
		if err != nil {
			t.Fatal(err)
		}
		saved[n] = b
	}
	h.add("app", "s3", hSay("app", "s3", 4, 2, "echoword appended turn"))
	if err := Ensure(dir, "claude", false, nil); err != nil {
		t.Fatal(err)
	}
	// Simulate kill -9 between the record/bucket writes and writeManifest.
	for n, b := range saved {
		if err := os.WriteFile(filepath.Join(dir, n), b, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	invalidateManifestCache(dir)
	// Any manifest-only writer under the lock, e.g. AdoptWriteBack(kept==path).
	if _, err := AdoptWriteBack(dir, h.path("app", "s1"), h.path("app", "s1")); err != nil {
		t.Fatal(err)
	}
	invalidateManifestCache(dir)
	if err := Ensure(dir, "claude", false, nil); err != nil {
		t.Fatal(err)
	}
	recs, err := ReadRecords(dir)
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, r := range recs {
		if strings.Contains(r.Record.Text, "echoword") {
			n++
		}
	}
	if n != 1 {
		t.Errorf("appended turn stored %d times after crash + manifest-only write + pass; want 1", n)
	}
}

// An index written by a newer deja (binary rolled back, or two installs side
// by side) answers as it is and is left alone. It used to be rebuilt down to
// this build's version, with a line saying this build was the newer one.
func TestANewerIndexIsLeftAlone(t *testing.T) {
	_, dir := threeWordStore(t)
	m, err := readManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	m.Version = version + 1
	if err := writeManifest(dir, m); err != nil {
		t.Fatal(err)
	}
	invalidateManifestCache(dir)
	if ReadStateOf(dir) != ReadStateNewer {
		t.Fatalf("setup: want ReadStateNewer")
	}
	var out strings.Builder
	if err := Ensure(dir, "claude", false, &out); err != nil {
		t.Fatal(err)
	}
	after, _ := readManifest(dir)
	if strings.Contains(out.String(), "this build reads a newer index than the one on disk") {
		t.Errorf("message claims this build is newer, but the index on disk (%d) is newer than the build (%d)", version+1, version)
	}
	if !strings.Contains(out.String(), "written by a newer deja") {
		t.Errorf("the pass did not say why it left the index alone: %q", out.String())
	}
	if after.Version != version+1 {
		t.Errorf("a newer index was rebuilt down to version %d without being asked", after.Version)
	}
	if got, err := Search(dir, query.Options{Query: "alphaword", Limit: 10}); err != nil || len(got) == 0 {
		t.Errorf("the newer index does not answer: %d hits, err=%v", len(got), err)
	}
	// Asked for, the rebuild still happens.
	if err := Ensure(dir, "claude", true, nil); err != nil {
		t.Fatal(err)
	}
	if after, _ := readManifest(dir); after.Version != version {
		t.Errorf("a forced rebuild left version %d, want %d", after.Version, version)
	}
}

// A torn fixes.gob (crash on a filesystem without ordered data, a partial
// copy) used to read as "no pairs", and the next pass wrote back only the pairs
// it had just mined. It is mined again from the records.
func TestATornFixTableIsMinedAgain(t *testing.T) {
	h := newTwoWayEnv(t)
	h.put("app", "s1", richSession("app", "s1", 2, "pool"))
	h.put("app", "s2", hPrompt("app", "s2", 3, 0, "first question about caching"))
	dir := filepath.Join(h.tmp, "idx.db")
	if err := Ensure(dir, "claude", true, nil); err != nil {
		t.Fatal(err)
	}
	before := len(ReadFixes(dir))
	if before == 0 {
		t.Fatal("setup: no fix pairs mined")
	}
	fp := filepath.Join(dir, fixesFile)
	b, err := os.ReadFile(fp)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(fp, b[:len(b)/2], 0o600); err != nil {
		t.Fatal(err)
	}
	h.add("app", "s2", hSay("app", "s2", 3, 1, "caching answer"))
	if err := Ensure(dir, "claude", false, nil); err != nil {
		t.Fatal(err)
	}
	if after := len(ReadFixes(dir)); after < before {
		t.Fatalf("a torn fixes.gob lost %d fix pairs for good", before-after)
	}
}

// A posting block whose bytes were zeroed (same length, directory intact)
// decodes to offset 0 again and again. That resolved to another session's
// record and was dropped, so the search answered "no matches"; it is
// corruption.
func TestAZeroedPostingBlockIsCorruption(t *testing.T) {
	_, dir := threeWordStore(t)
	files, _ := filepath.Glob(filepath.Join(dir, "buckets", "*.bin"))
	hit := false
	for _, p := range files {
		entries, f, err := openBucketDir(p)
		if err != nil {
			t.Fatal(err)
		}
		_ = f.Close()
		for _, e := range entries {
			if !strings.Contains(e.tok, "charlieword") {
				continue
			}
			b, err := os.ReadFile(p)
			if err != nil {
				t.Fatal(err)
			}
			for i := e.off; i < e.off+uint64(e.n); i++ {
				b[i] = 0
			}
			if err := os.WriteFile(p, b, 0o600); err != nil {
				t.Fatal(err)
			}
			hit = true
		}
	}
	if !hit {
		t.Fatal("setup: no charlieword token")
	}
	got, err := Search(dir, query.Options{Query: "charlieword", Limit: 10})
	if IsCorrupt(err) {
		return
	}
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range got {
		for _, m := range s.Messages {
			if !strings.Contains(m.Text, "charlieword") {
				t.Errorf("search for charlieword answered with session %s message %q and no error", s.ID, m.Text)
			}
		}
	}
	if len(got) == 0 {
		t.Errorf("search for charlieword returned nothing and no error over a zeroed posting block")
	}
}

// Lock-free searches racing forced rebuilds whose session set changes: every
// hit must be the session that holds the word. A reader that takes the
// manifest of one generation and the records/postings of the next resolves
// interned ids through the wrong table.
func TestASearchRacingRebuildsKeepsEveryHitInItsSession(t *testing.T) {
	h := newTwoWayEnv(t)
	for i := range 20 {
		id := fmt.Sprintf("s%02d", i)
		h.put("app", id, hPrompt("app", id, 2+i%20, 0, fmt.Sprintf("sessword%02d lives only here", i)))
	}
	dir := filepath.Join(h.tmp, "idx.db")
	if err := Ensure(dir, "claude", true, nil); err != nil {
		t.Fatal(err)
	}
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		for k := 0; ; k++ {
			select {
			case <-stop:
				return
			default:
			}
			// a new session that sorts first shifts every interned id
			id := fmt.Sprintf("a%04d", k)
			h.put("app", id, hPrompt("app", id, 1, 0, fmt.Sprintf("newcomer%04d text", k)))
			_ = Ensure(dir, "claude", true, nil)
		}
	}()
	bad, errs, empty, n := 0, 0, 0, 0
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		i := n % 20
		n++
		want := fmt.Sprintf("s%02d", i)
		got, err := Search(dir, query.Options{Query: fmt.Sprintf("sessword%02d", i), Limit: 5})
		if err != nil {
			errs++
			if errs < 3 {
				t.Logf("search error: %v", err)
			}
			continue
		}
		for _, s := range got {
			ok := s.ID == want
			for _, m := range s.Messages {
				if !strings.Contains(m.Text, fmt.Sprintf("sessword%02d", i)) {
					ok = false
				}
			}
			if !ok {
				bad++
				if bad < 5 {
					t.Errorf("query sessword%02d answered session %s with %d messages: %+v", i, s.ID, len(s.Messages), s.Messages)
				}
			}
		}
		if len(got) == 0 {
			empty++
		}
	}
	close(stop)
	<-done
	t.Logf("%d searches, %d misattributed, %d errors, %d empty without error", n, bad, errs, empty)
}

// One opencode session stamped ahead of the clock (a skewed machine, a synced
// store) set the store watermark in the future, and turns written after it at
// the real time sat below it unread.
func TestASessionAheadOfTheClockDoesNotFreezeTheStoreMark(t *testing.T) {
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skip("sqlite3 CLI not available")
	}
	tmp := t.TempDir()
	setHome(t, tmp)
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "claude"))
	t.Setenv("DEJA_CODEX_ROOT", filepath.Join(tmp, "codex"))
	t.Setenv("DEJA_GOOSE_DB", filepath.Join(tmp, "none-goose.db"))
	t.Setenv("DEJA_NOTES_FILE", filepath.Join(tmp, "notes.jsonl"))
	db := filepath.Join(tmp, "opencode.db")
	t.Setenv("DEJA_OPENCODE_DB", db)
	run := func(sql string) {
		t.Helper()
		if out, err := exec.CommandContext(t.Context(), "sqlite3", db, sql).CombinedOutput(); err != nil {
			t.Fatalf("sqlite3: %v %s", err, out)
		}
	}
	now := time.Now().UnixMilli()
	fut := now + 24*3600*1000
	run(fmt.Sprintf(`create table session(id text primary key, directory text, title text, time_created integer, time_updated integer);
create table message(id text primary key, session_id text, time_created integer, time_updated integer, data text);
create table part(id text primary key, message_id text, session_id text, time_created integer, time_updated integer, data text);
insert into session values('s1','/w/app','skewed',%[1]d,%[1]d);
insert into message values('m1','s1',%[1]d,%[1]d,'{"role":"user","time":{"created":%[1]d}}');
insert into part values('p1','m1','s1',%[1]d,%[1]d,'{"type":"text","text":"skewword from a machine ahead of time"}');`, fut))
	dir := filepath.Join(tmp, "index.db")
	if err := Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	if m, _ := readManifest(dir); m.Files[db].LastUpdated > time.Now().UnixNano() {
		t.Errorf("the store mark sits %v ahead of the clock", time.Until(time.Unix(0, m.Files[db].LastUpdated)))
	}
	n := now + 1000
	run(fmt.Sprintf(`insert into session values('s2','/w/app','fresh',%[1]d,%[1]d);
insert into message values('m2','s2',%[1]d,%[1]d,'{"role":"user","time":{"created":%[1]d}}');
insert into part values('p2','m2','s2',%[1]d,%[1]d,'{"type":"text","text":"punctualword written now"}');`, n))
	later := time.Now().Add(2 * time.Second)
	_ = os.Chtimes(db, later, later)
	if err := Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	hits, err := Search(dir, query.Options{Query: "punctualword", Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) == 0 {
		t.Fatalf("a session written now never reached the index: the store watermark sits a day ahead")
	}
	// The session ahead of the clock is above the capped mark on every pass;
	// reading it again must not store it again.
	for range 2 {
		later = later.Add(2 * time.Second)
		_ = os.Chtimes(db, later, later)
		if err := Ensure(dir, "", false, nil); err != nil {
			t.Fatal(err)
		}
	}
	recs, rerr := ReadRecords(dir)
	if rerr != nil {
		t.Fatal(rerr)
	}
	stored := 0
	for _, r := range recs {
		if strings.Contains(r.Record.Text, "skewword") {
			stored++
		}
	}
	if stored != 1 {
		t.Errorf("the session stamped ahead is stored %d times after three passes, want 1", stored)
	}
}

// A filesystem mounted read-only answers EROFS, not a permission error, and
// the read-only fallbacks only knew the second: search and MCP recall failed
// outright on a mount a chmod'ed directory would have answered from.
func TestAReadOnlyMountIsUnwritable(t *testing.T) {
	for _, err := range []error{
		&fs.PathError{Op: "open", Path: "index.db.lock", Err: syscall.EROFS},
		&fs.PathError{Op: "open", Path: "index.db.lock", Err: fs.ErrPermission},
	} {
		if !Unwritable(err) {
			t.Errorf("Unwritable(%v) = false", err)
		}
	}
	if Unwritable(&fs.PathError{Op: "open", Path: "index.db.lock", Err: syscall.ENOENT}) {
		t.Error("a missing file reads as an unwritable index")
	}
}

// What makes the pass's package state safe is the directory lock, and every
// test of it has run goroutines in one process. Two `deja index` processes at
// once is the shape a cron and a session-start hook make, and nothing measured
// it: a flock that did not hold across processes would leave the store to
// whichever pass wrote last.
//
// The child half of this test is `TestCrossProcessIndexChild`, which the parent
// runs by re-executing the test binary.
func TestTwoIndexProcessesLeaveOneGoodStore(t *testing.T) {
	tmp := t.TempDir()
	setHome(t, tmp)
	claude := filepath.Join(tmp, "claude")
	t.Setenv("DEJA_CLAUDE_ROOT", claude)
	t.Setenv("DEJA_CODEX_ROOT", filepath.Join(tmp, "codex"))
	t.Setenv("DEJA_OPENCODE_DB", filepath.Join(tmp, "none.db"))
	t.Setenv("DEJA_NOTES_FILE", filepath.Join(tmp, "notes.jsonl"))
	proj := filepath.Join(claude, "-work-app")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	stamp := time.Now().Add(-time.Hour).UTC().Format(time.RFC3339)
	const sessions = 120
	for i := range sessions {
		line := fmt.Sprintf(`{"type":"user","sessionId":"s%03d","timestamp":%q,"cwd":"/work/app",`+
			`"message":{"role":"user","content":"turn %d: the pool timed out during the migration"}}`, i, stamp, i)
		if err := os.WriteFile(filepath.Join(proj, fmt.Sprintf("s%03d.jsonl", i)), []byte(line+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	dir := filepath.Join(tmp, "index.db")

	// The child inherits this environment and puts it back after the package's
	// TestMain has pointed every store at a temp root of its own.
	env := append(os.Environ(),
		"DEJA_PASS_INDEX="+dir,
		"DEJA_PASS_CLAUDE="+claude,
		"DEJA_PASS_CODEX="+filepath.Join(tmp, "codex"),
		"DEJA_PASS_OPENCODE="+filepath.Join(tmp, "none.db"),
		"DEJA_PASS_NOTES="+filepath.Join(tmp, "notes.jsonl"),
		// The child's own TestMain re-points these at a temp root of its own,
		// so they travel under names it will not overwrite and are put back on
		// the other side. Without them the two children would keep their
		// tombstones and exclude lists somewhere the parent never wrote.
		"DEJA_PASS_HOME="+tmp,
		"DEJA_PASS_XDG_CONFIG="+filepath.Join(tmp, "config"),
		"DEJA_PASS_APPDATA="+filepath.Join(tmp, "AppData", "Roaming"),
	)
	start := make(chan struct{})
	var wg sync.WaitGroup
	type run struct {
		out        string
		err        error
		from, upto time.Time
	}
	runs := make([]run, 2)
	for i := range runs {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			// A copy, so the two children never share a backing array, and a
			// deadline, so a pass that never gets the lock fails this test
			// rather than hanging the package.
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
			defer cancel()
			cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=TestCrossProcessIndexChild")
			cmd.Env = append(append([]string{}, env...), fmt.Sprintf("DEJA_PASS_FULL=%d", boolToInt(i == 0)))
			<-start
			out, err := cmd.CombinedOutput()
			runs[i].out, runs[i].err = string(out), err
			runs[i].from, runs[i].upto = childWindow(string(out))
		}(i)
	}
	close(start)
	wg.Wait()

	for i, r := range runs {
		if r.err != nil {
			t.Fatalf("process %d failed: %v\n%s", i, r.err, r.out)
		}
		if !strings.Contains(r.out, "pass-ok") {
			t.Fatalf("process %d did not report a finished pass:\n%s", i, r.out)
		}
	}
	// Whether the lock was actually contended, from the lock itself rather
	// than from wall-clock windows: a child that waited says so. This is
	// reported and not asserted, because a machine that runs the first pass to
	// completion before the second process has finished starting is slow or
	// lucky, not broken — the assertions that matter are below.
	waited := 0
	for _, r := range runs {
		if strings.Contains(r.out, "waited-for-lock") {
			waited++
		}
	}
	if waited == 0 {
		t.Logf("neither process waited for the lock: the passes did not overlap this run (%s..%s and %s..%s)",
			runs[0].from, runs[0].upto, runs[1].from, runs[1].upto)
	}

	// One good store: every session once, and a manifest that reads back.
	m, err := readManifest(dir)
	if err != nil {
		t.Fatalf("the store two processes left cannot be read: %v", err)
	}
	if len(m.Sessions) != sessions {
		t.Errorf("the store holds %d sessions, want %d", len(m.Sessions), sessions)
	}
	counted := 0
	for _, meta := range m.Sessions {
		counted += meta.Counted
	}
	if counted != sessions {
		t.Errorf("the sessions count %d messages between them, want %d", counted, sessions)
	}
	if !recordsReadable(dir, m) {
		t.Error("records.bin does not match the manifest the two processes left")
	}
}

// childWindow reads the stamps the child printed around its pass.
func childWindow(out string) (from, upto time.Time) {
	for line := range strings.SplitSeq(out, "\n") {
		if !strings.HasPrefix(line, "pass-ok ") {
			continue
		}
		parts := strings.Fields(line)
		if len(parts) != 3 {
			continue
		}
		a, err1 := strconv.ParseInt(parts[1], 10, 64)
		b, err2 := strconv.ParseInt(parts[2], 10, 64)
		if err1 != nil || err2 != nil {
			continue
		}
		return time.Unix(0, a), time.Unix(0, b)
	}
	return time.Time{}, time.Time{}
}

// TestCrossProcessIndexChild is one index pass, run as its own process by the
// test above. It is a no-op anywhere else.
func TestCrossProcessIndexChild(t *testing.T) {
	dir := os.Getenv("DEJA_PASS_INDEX")
	if dir == "" {
		t.Skip("the parent runs this one")
	}
	// After TestMain, which points every store at a temp root of its own.
	for k, v := range map[string]string{
		"DEJA_CLAUDE_ROOT": os.Getenv("DEJA_PASS_CLAUDE"),
		"DEJA_CODEX_ROOT":  os.Getenv("DEJA_PASS_CODEX"),
		"DEJA_OPENCODE_DB": os.Getenv("DEJA_PASS_OPENCODE"),
		"DEJA_NOTES_FILE":  os.Getenv("DEJA_PASS_NOTES"),
		"HOME":             os.Getenv("DEJA_PASS_HOME"),
		"USERPROFILE":      os.Getenv("DEJA_PASS_HOME"),
		"XDG_CONFIG_HOME":  os.Getenv("DEJA_PASS_XDG_CONFIG"),
		"APPDATA":          os.Getenv("DEJA_PASS_APPDATA"),
	} {
		t.Setenv(k, v)
	}
	// The lock's own signal, which is what says the two passes met.
	LockWaitNotice = func() { fmt.Fprintln(os.Stdout, "waited-for-lock") }
	from := time.Now()
	if err := Ensure(dir, "", os.Getenv("DEJA_PASS_FULL") == "1", nil); err != nil {
		t.Fatalf("pass failed: %v", err)
	}
	fmt.Fprintf(os.Stdout, "pass-ok %d %d\n", from.UnixNano(), time.Now().UnixNano())
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// Two agents in one checkout, and the product's whole claim: a fix found in
// one comes back in the other. What carries it is not the project name — the
// harnesses do not agree on that. In real-world stores, of 170 pairs of
// sessions from different harnesses that edited the same absolute file, 26
// agreed on the project name and 144 did not: one name contained the other in
// 84 cases and one was a path where the other was a bare name in 52.
//
// What makes the claim hold anyway is the rule that admits a session by the
// files it touched under this checkout, whatever it was recorded as. Over 515
// handovers on that store, the session before this one was never missing from
// the pool — so nothing on this path is load-bearing for the product except
// that rule, and nothing else here would catch its removal.
func TestASessionStartReachesTheOtherAgentsWorkInThisCheckout(t *testing.T) {
	tmp := t.TempDir()
	home := filepath.Join(tmp, "home")
	setHome(t, home)
	t.Setenv("USERPROFILE", home)
	claude := filepath.Join(tmp, "claude")
	codex := filepath.Join(tmp, "codex")
	t.Setenv("DEJA_CLAUDE_ROOT", claude)
	t.Setenv("DEJA_CODEX_ROOT", codex)
	t.Setenv("DEJA_OPENCODE_DB", filepath.Join(tmp, "opencode.db"))

	// One repository, worked in by both agents.
	repo := filepath.Join(home, "src", "pool")
	if err := os.MkdirAll(filepath.Join(repo, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	touched := filepath.Join(repo, "pool.go")

	// Claude was started in a subdirectory of the checkout, so its project is
	// named after that directory, pool/internal, where codex's is src/pool:
	// the "one contains the other" shape, 84 of the 144 disagreements. Both
	// name a directory the same way now (#4457), so a different starting
	// directory is what still gives one checkout two names.
	// A drive letter's colon cannot be part of a directory name on Windows,
	// and the encoder drops it there too.
	sub := filepath.Join(repo, "internal")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	encoded := strings.ReplaceAll(strings.ReplaceAll(filepath.ToSlash(sub), ":", ""), "/", "-")
	writeLines(t, filepath.Join(claude, encoded, "c1.jsonl"),
		claudeLineAt("c1", "2026-03-01T09:00:00Z", "the pool leaked connections under load", sub),
		claudeEditAt("c1", "2026-03-01T09:05:00Z", touched, sub))

	// Codex records the checkout itself, so the same work arrives under a
	// different project string. This is the disagreement the measurement found
	// in 85% of cross-harness pairs.
	write(t, filepath.Join(codex, "sessions", "2026", "03", "01", "rollout-2026-03-01T10-00-00-x1.jsonl"),
		`{"type":"session_meta","timestamp":"2026-03-01T10:00:00Z","payload":{"session_id":"x1","cwd":`+jsonStr(repo)+`}}`+"\n"+
			`{"timestamp":"2026-03-01T10:00:01Z","payload":{"role":"user","content":"the pool still leaks, fixed by closing the rows"}}`+"\n"+
			`{"timestamp":"2026-03-01T10:00:02Z","payload":{"role":"assistant","content":[{"type":"text","text":"closed the rows in `+filepath.ToSlash(touched)+`"}]}}`+"\n")

	dir := filepath.Join(tmp, "index.db")
	if err := Ensure(dir, "", true, io.Discard); err != nil {
		t.Fatal(err)
	}

	metas, err := AllMeta(dir)
	if err != nil {
		t.Fatal(err)
	}
	byHarness := map[string]SessionMeta{}
	for _, m := range metas {
		byHarness[m.Harness] = m
	}
	c, okC := byHarness["claude"]
	x, okX := byHarness["codex"]
	if !okC || !okX {
		t.Fatalf("both agents have to be indexed for this to mean anything: %v", metas)
	}
	// The premise: the names differ. If a later change makes the two harnesses
	// agree, this test stops testing the rescue and has to be rewritten rather
	// than quietly passing for the wrong reason.
	t.Logf("claude recorded %q, codex recorded %q", c.Project, x.Project)
	if c.Project == x.Project {
		t.Fatalf("both recorded the project as %q; the disagreement this guards is gone, so rewrite the test", c.Project)
	}

	// A session starting in the checkout sees both, whatever each was called.
	got, err := RecentProjectsUnder(dir, []string{"src/pool"}, repo, 12)
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, s := range got {
		seen[s.Harness] = true
	}
	if !seen["claude"] || !seen["codex"] {
		t.Fatalf("a session start in the checkout reached %v; the other agent's work is the claim", ids(got))
	}

	// And the name alone does not: without the rescue, the agent whose project
	// string differs is invisible, which is what the 144 pairs would have been.
	byName, err := RecentProjects(dir, []string{"src/pool"}, 12)
	if err != nil {
		t.Fatal(err)
	}
	reached := map[string]bool{}
	for _, s := range byName {
		reached[s.Harness] = true
	}
	if len(reached) == 2 {
		t.Fatalf("the name reached both harnesses (%v), so this store no longer reproduces the disagreement", ids(byName))
	}
}

// crushEnv registers one Crush project in a fresh home and returns the path of
// its store, which does not exist until crushSQL writes to it.
func crushEnv(t *testing.T) (tmp, db string) {
	t.Helper()
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skip("sqlite3 not available")
	}
	tmp = t.TempDir()
	setHome(t, tmp)
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "claude"))
	t.Setenv("DEJA_CODEX_ROOT", filepath.Join(tmp, "codex"))
	t.Setenv("DEJA_NOTES_FILE", filepath.Join(tmp, "notes.jsonl"))
	data := filepath.Join(tmp, "crushdata")
	t.Setenv("DEJA_CRUSH_ROOT", data)
	project := filepath.Join(tmp, "proj")
	if err := os.MkdirAll(filepath.Join(project, ".crush"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(data, 0o755); err != nil {
		t.Fatal(err)
	}
	reg := fmt.Sprintf(`{"projects":[{"path":%q,"data_dir":%q}]}`, project, filepath.Join(project, ".crush"))
	if err := os.WriteFile(filepath.Join(data, "projects.json"), []byte(reg), 0o644); err != nil {
		t.Fatal(err)
	}
	db = filepath.Join(project, ".crush", "crush.db")
	crushSQL(t, db, `create table sessions (id text primary key, parent_session_id text, title text not null,
  updated_at integer not null, created_at integer not null);
create table messages (id text primary key, session_id text not null, role text not null,
  parts text not null default '[]', created_at integer not null, updated_at integer not null);`)
	return tmp, db
}

func crushSQL(t *testing.T, db, sql string) {
	t.Helper()
	c := exec.CommandContext(t.Context(), "sqlite3", db)
	c.Stdin = strings.NewReader(sql)
	if o, err := c.CombinedOutput(); err != nil {
		t.Fatalf("sqlite3: %v %s", err, o)
	}
}

// crushTurn adds a user message and moves the session's updated_at to its
// stamp, which is what Crush's own update does.
func crushTurn(t *testing.T, db, session, id, text string, at int) {
	t.Helper()
	crushSQL(t, db, fmt.Sprintf(`insert or ignore into sessions values ('%[1]s',null,'retry',%[4]d,%[4]d);
insert into messages values ('%[2]s','%[1]s','user','[{"type":"text","data":{"text":%[3]q}}]',%[4]d,%[4]d);
update sessions set updated_at=%[4]d where id='%[1]s';`, session, id, text, at))
}

// Every Crush message used to re-read the whole project store: the store was
// never stamped, so the since parser never ran, and 3000 sessions were replaced
// for one turn (#4381). A quiet session's row rewritten in place without its
// updated_at moving is the witness: a whole read picks the new text up, a read
// from the watermark does not.
func TestTheCrushStoreIsAskedOnlyForWhatIsNew(t *testing.T) {
	tmp, db := crushEnv(t)
	const at = 1784282400
	crushTurn(t, db, "quiet", "q1", "marker-crush-quiet", at-100)
	crushTurn(t, db, "busy", "b1", "marker-crush-one", at)

	dir := filepath.Join(tmp, "index.db")
	if err := Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	if zedHits(t, dir, "marker-crush-quiet") != 1 || zedHits(t, dir, "marker-crush-one") != 1 {
		t.Fatalf("the store was not indexed, so this measures nothing")
	}
	m, err := readManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	if m.Files[db].LastUpdated == 0 {
		t.Errorf("the store carries no watermark, so the next pass reads it whole")
	}

	crushSQL(t, db, `update messages set parts='[{"type":"text","data":{"text":"marker-crush-rewritten"}}]' where id='q1';`)
	// In the watermark's own second: Crush stamps whole seconds, and a strict
	// > against the last turn's second would never ask for this one.
	crushTurn(t, db, "busy", "b2", "marker-crush-two", at)
	if err := Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	if n := zedHits(t, dir, "marker-crush-rewritten"); n != 0 {
		t.Errorf("the quiet session was read again: the pass read the whole store")
	}
	if n := zedHits(t, dir, "marker-crush-quiet"); n != 1 {
		t.Errorf("the session the pass did not ask about was dropped: %d hits", n)
	}
	if n := zedTurns(t, dir, "marker-crush-two"); n != 1 {
		t.Errorf("the added turn is held %d times", n)
	}
	// The busy session comes back whole and replaces what was held.
	if n := zedTurns(t, dir, "marker-crush-one"); n != 1 {
		t.Errorf("the earlier turn is held %d times", n)
	}
}

// A cursor session untouched since the watermark must survive an incremental
// pass triggered by a change to the shared state.vscdb.
func TestCursorIncrementalKeepsUntouchedSessions(t *testing.T) {
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skip("sqlite3 not available")
	}
	tmp := hermeticIndexEnv(t)
	dir := filepath.Join(tmp, "index")
	db := filepath.Join(os.Getenv("DEJA_CURSOR_ROOT"), "globalStorage", "state.vscdb")
	if err := os.MkdirAll(filepath.Dir(db), 0o755); err != nil {
		t.Fatal(err)
	}
	seed := func(extra string) {
		schema := `create table if not exists cursorDiskKV (key text primary key, value text);
insert or replace into cursorDiskKV values
 ('composerData:old', json('{"composerId":"old","name":"Old chat","createdAt":1752600000000,"lastUpdatedAt":1752600100000,"fullConversationHeadersOnly":[{"bubbleId":"b1","type":1}]}')),
 ('bubbleId:old:b1', json('{"type":1,"text":"oldsessionfact about the pager","timestamp":1752600001000,"workspaceProjectDir":"/w/app"}'))` + extra + `;`
		if out, err := exec.CommandContext(t.Context(), "sqlite3", db, schema).CombinedOutput(); err != nil {
			t.Fatalf("seed: %v: %s", err, out)
		}
	}
	seed("")
	if err := Ensure(dir, "", false, os.Stderr); err != nil {
		t.Fatal(err)
	}
	if hits, _ := Search(dir, search.Options{Query: "oldsessionfact", All: true}); len(hits) == 0 {
		t.Fatal("old cursor session not indexed on first pass")
	}
	// Add a NEW session (post-watermark) and touch the DB mtime.
	seed(`,
 ('composerData:new', json('{"composerId":"new","name":"New chat","createdAt":1752700000000,"lastUpdatedAt":1752700100000,"fullConversationHeadersOnly":[{"bubbleId":"c1","type":1}]}')),
 ('bubbleId:new:c1', json('{"type":1,"text":"newsessionfact about caching","timestamp":1752700001000,"workspaceProjectDir":"/w/app"}'))`)
	future := time.Now().Add(time.Hour)
	_ = os.Chtimes(db, future, future)
	if err := Ensure(dir, "", false, os.Stderr); err != nil {
		t.Fatal(err)
	}
	if hits, _ := Search(dir, search.Options{Query: "newsessionfact", All: true}); len(hits) == 0 {
		t.Fatal("new cursor session not indexed on incremental pass")
	}
	if hits, _ := Search(dir, search.Options{Query: "oldsessionfact", All: true}); len(hits) == 0 {
		t.Fatal("REGRESSION: untouched cursor session vanished after incremental pass")
	}
	fmt.Fprintln(os.Stderr, "both cursor sessions survived incremental")
}

func cursorBubble(cid, bid string, typ int, text string, at int64) string {
	return fmt.Sprintf(`insert or replace into cursorDiskKV values ('bubbleId:%s:%s', json('{"type":%d,"text":"%s","timestamp":%d,"workspaceProjectDir":"/tmp/proj"}'));`+"\n", cid, bid, typ, text, at)
}

func cursorComposer(cid, name string, created, updated int64) string { //nolint:unparam // the call spells out the row's times
	return fmt.Sprintf(`insert or replace into cursorDiskKV values ('composerData:%s', json('{"composerId":"%s","name":"%s","createdAt":%d,"lastUpdatedAt":%d}'));`+"\n", cid, cid, name, created, updated)
}

// The since read handed back a composer's new bubbles alone. A rename, which
// rewrites composerData and adds no bubble, was dropped for having none
// (#4450), and a continued chat had its words, asked and touched recomputed
// from the latest turns only (#4451).
func TestACursorChatContinuedOrRenamedMatchesARebuild(t *testing.T) {
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skip("sqlite3 CLI not available")
	}
	tmp := t.TempDir()
	isolateStores(t, tmp)
	root := filepath.Join(tmp, "cursor")
	t.Setenv("DEJA_CURSOR_ROOT", root)
	db := filepath.Join(root, "globalStorage", "state.vscdb")
	if err := os.MkdirAll(filepath.Dir(db), 0o755); err != nil {
		t.Fatal(err)
	}
	sql := func(stmts string) {
		t.Helper()
		if out, err := exec.CommandContext(t.Context(), "sqlite3", db, stmts).CombinedOutput(); err != nil {
			t.Fatalf("sqlite3: %v %s", err, out)
		}
	}
	base := int64(1788253200000)
	sql("create table cursorDiskKV (key text primary key, value text);\n" +
		cursorComposer("comp-retry", "fix the retry loop", base, base+2000) +
		cursorBubble("comp-retry", "b1", 1, "fix the retry loop", base+1000) +
		cursorBubble("comp-retry", "b2", 2, "looking at the retry loop", base+2000) +
		cursorComposer("comp-other", "other chat", base, base+500) +
		cursorBubble("comp-other", "o1", 1, "inspect the cursor db", base+500))
	dir := filepath.Join(tmp, "index.db")
	indexPass(t, dir)

	sql(cursorBubble("comp-retry", "b3", 1, "now run the tests", base+60000) +
		cursorBubble("comp-retry", "b4", 2, "the tests fail in retry", base+61000) +
		cursorComposer("comp-retry", "fix the retry loop", base, base+61000))
	indexPass(t, dir)
	matchesRebuild(t, dir, "cursor", "comp-retry")

	sql(cursorComposer("comp-retry", "Retry loop never stops", base, base+300000))
	indexPass(t, dir)
	matchesRebuild(t, dir, "cursor", "comp-retry")
	matchesRebuild(t, dir, "cursor", "comp-other")
}

// bucket() sliced two bytes, so every non-ASCII token — all of Russian, all
// of Chinese, all of Greek — collapsed into the single bucket "t_" and each
// lookup scanned the entire non-ASCII vocabulary.
func TestBucketsShardNonASCIITokens(t *testing.T) {
	words := []string{"миграция", "репликация", "сеть", "база", "ключ", "ошибка", "запрос", "тест", "индекс", "поиск"}
	seen := map[string]bool{}
	for _, w := range words {
		seen[bucket("t"+w)] = true
	}
	if len(seen) < 5 {
		t.Fatalf("%d Russian words landed in %d buckets: %v", len(words), len(seen), seen)
	}
	cjk := []string{"数据库", "迁移", "网络", "错误", "请求", "测试"}
	seen = map[string]bool{}
	for _, w := range cjk {
		seen[bucket("t"+w)] = true
	}
	if len(seen) < 4 {
		t.Fatalf("%d Chinese words landed in %d buckets: %v", len(cjk), len(seen), seen)
	}
	// ASCII sharding must be byte-identical to the old scheme, or every
	// English token silently moves house on upgrade.
	for _, tok := range []string{"tmigration", "tdatabase", "t203", "tpkg/index", "tx"} {
		if len(tok) >= 2 {
			if got, want := bucket(tok), safe(tok[:2]); got != want {
				t.Fatalf("bucket(%q) = %q, want %q — ASCII layout changed", tok, got, want)
			}
		}
	}
}

// The relevance tier folds a term onto its inflections. Cyrillic terms used to
// be handed to the ASCII stemmer, which appended English suffixes to Russian
// words, so the fold never matched anything (#340 shipped as a no-op).
func TestRussianInflectionsFoldInRelevanceTier(t *testing.T) {
	for _, c := range []struct{ query, indexed string }{
		{"миграция", "миграциями"},
		{"миграциями", "миграция"},
		{"репликация", "репликации"},
		{"агентом", "агент"},
		{"база", "базы"},
		{"кластера", "кластеров"},
		// Third-declension feminine nouns — 11% of the vocabulary, and the
		// paradigm this fold was written for.
		{"сеть", "сети"},
		{"сеть", "сетью"},
		{"жизнь", "жизни"},
		{"ночь", "ночи"},
		{"очередь", "очереди"},
		// Short verb stems must still reach their own paradigm: a guard keyed
		// on stem length alone cannot tell вес (noun) from зна (verb).
		{"знать", "знал"},
		{"знаю", "знать"},
		{"могу", "могли"},
		{"говорит", "говорить"},
		{"знать", "знаю"},
		{"считаем", "считать"},
		// Soft-masculine nouns take the hard endings, unlike the feminines.
		{"автомобиль", "автомобиля"},
		{"писатель", "писателем"},
		// -ость nouns never folded before: "ть" matched ahead of "ь".
		{"новость", "новостей"},
		{"активность", "активности"},
		// Real infinitives are vowel + ть and keep the verb branch.
		{"делать", "делаю"},
		{"работать", "работаю"},
		{"видеть", "видел"},
		// ...and ть-final nouns still fold within their own paradigm.
		{"часть", "части"},
		{"весть", "вести"},
		{"власть", "власти"},
	} {
		forms := stemMatchForms(c.query)
		found := slices.Contains(forms, c.indexed)
		if !found {
			t.Errorf("stemMatchForms(%q) does not reach %q: %v", c.query, c.indexed, forms)
		}
		for _, f := range forms {
			if strings.ContainsAny(f, "abcdefghijklmnopqrstuvwxyz") {
				t.Errorf("stemMatchForms(%q) produced a Latin-suffixed form %q", c.query, f)
				break
			}
		}
	}
	// Three-letter function words must not fan out into the ending table.
	for _, w := range []string{"что", "как", "его"} {
		if got := stemMatchForms(w); len(got) != 0 {
			t.Errorf("stemMatchForms(%q) = %v, want none", w, got)
		}
	}
}

// The relevance tier has no catalog gate — whatever the fold invents is looked
// up for real — so an over-eager ending reaches a word from another paradigm
// and recalls a session that has nothing to do with the query. A wrong recall
// costs more than a missed inflection.
func TestRussianFoldDoesNotReachUnrelatedWords(t *testing.T) {
	for _, c := range []struct{ query, unrelated string }{
		{"цель", "целая"},
		{"цель", "целый"},
		{"борись", "борис"},
		{"весом", "весть"},
		{"бить", "битой"},
		{"верь", "вера"},
		{"весь", "веса"},
		{"цель", "целое"},
		{"голубь", "голубой"},
		{"столь", "стол"},
		// A noun can end in "ть" too. Taking the verb branch sent часть to
		// час and весть to вес — the latter being the surviving inverse of
		// весом -> весть.
		{"часть", "час"},
		{"часть", "часа"},
		{"весть", "вес"},
		{"весть", "веса"},
		{"лесть", "лес"},
	} {
		if slices.Contains(stemMatchForms(c.query), c.unrelated) {
			t.Errorf("stemMatchForms(%q) reaches the unrelated %q", c.query, c.unrelated)
		}
	}
}

// Both tiers must fold Russian the same way. The stem tier used to strip and
// re-attach from one shared table, so пусть recalled a session that only said
// пустой — the same defect the relevance tier was fixed for, one tier over.
func TestStemTierFoldsLikeTheRelevanceTier(t *testing.T) {
	for _, c := range []struct{ query, unrelated string }{
		{"пусть", "пустой"},
		{"пусть", "пустая"},
		{"часть", "часто"},
		{"цель", "целая"},
	} {
		if slices.Contains(cyrSuffixForms(c.query), c.unrelated) {
			t.Errorf("stem tier folds %q onto the unrelated %q", c.query, c.unrelated)
		}
	}
	for _, c := range []struct{ query, want string }{
		{"жизнь", "жизни"},
		{"дверь", "двери"},
		{"приятель", "приятеля"},
		{"часть", "части"},
		{"миграция", "миграциями"},
	} {
		found := slices.Contains(cyrSuffixForms(c.query), c.want)
		if !found {
			t.Errorf("stem tier lost %q -> %q", c.query, c.want)
		}
	}
}

// End to end through the relevance tier, which is what auto-recall uses: it
// has no close/fuzzy ladder to fall back on, so a dead fold there means a
// Russian prompt silently recalls nothing.
func TestRussianRelevanceFoldsInflections(t *testing.T) {
	tmp := t.TempDir()
	claudeRoot := filepath.Join(tmp, "claude")
	t.Setenv("DEJA_CLAUDE_ROOT", claudeRoot)
	dir := filepath.Join(tmp, "index.db")
	t.Setenv("DEJA_INDEX_DIR", dir)
	proj := filepath.Join(claudeRoot, "-tmp-app")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	mk := func(id, text string) {
		line := `{"type":"user","sessionId":"` + id + `","timestamp":"2026-01-02T03:04:05Z","message":{"role":"user","content":"` + text + `"}}` + "\n"
		if err := os.WriteFile(filepath.Join(proj, id+".jsonl"), []byte(line), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	mk("s1", "обсуждали миграциями базы данных и чинили репликациями слейва")
	// Filler so the target is not the only session in the corpus.
	for i := range 12 {
		mk(fmt.Sprintf("f%d", i), "обычная рабочая сессия про сборку и деплой контейнеров")
	}
	if err := Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	// Nominative singular in the prompt, instrumental plural in the session.
	got, matched, _, _, err := ProjectRelevant(dir, []string{"tmp/app"}, []string{"миграция", "репликация"}, 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) == 0 {
		t.Fatal("relevance tier returned nothing for a Russian prompt")
	}
	if got[0].ID != "s1" || matched[0] == 0 {
		t.Fatalf("top relevant session = %q (matched=%v), want s1 with a match", got[0].ID, matched)
	}
}

// A manifest.gob that will not decode reads as "built, up to date" to doctor,
// which stats the file rather than parsing it, while the next search throws the
// store away and rebuilds it.
func TestDamagedUnreadableManifest(t *testing.T) {
	tmp := t.TempDir()
	setHome(t, filepath.Join(tmp, "home"))
	t.Setenv("USERPROFILE", os.Getenv("HOME"))
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "claude"))
	t.Setenv("DEJA_CODEX_ROOT", filepath.Join(tmp, "codex"))
	t.Setenv("DEJA_OPENCODE_DB", filepath.Join(tmp, "opencode.db"))
	root := filepath.Join(tmp, "claude", "proj-p")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	body := `{"type":"user","sessionId":"s1","timestamp":"2026-07-21T10:00:00Z","message":{"role":"user","content":"the retry storm"}}` + "\n"
	if err := os.WriteFile(filepath.Join(root, "s1.jsonl"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(tmp, "index.db")
	if err := Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	if Damaged(dir) {
		t.Fatal("a freshly built index reported damage")
	}

	path := filepath.Join(dir, "manifest.gob")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(data) < 8 {
		t.Fatalf("manifest too small to corrupt: %d bytes", len(data))
	}
	garbled := append([]byte{0xde, 0xad, 0xbe, 0xef}, data[4:]...)
	if err := os.WriteFile(path, garbled, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := readManifest(dir); err == nil {
		t.Fatal("corrupted manifest still decoded; the test no longer corrupts anything")
	}
	if !Damaged(dir) {
		t.Error("an unreadable manifest was not reported as damage")
	}
}

// The exclude list was applied when a store was read and never when its
// sessions were served, so a store already in the index kept answering. The
// case that matters is the harness the reader is sitting in: its live
// transcript is indexed, and a recall came back with the session that asked
// (#3965).
func TestAnExcludedHarnessIsKeptOutOfTheAnswersToo(t *testing.T) {
	setHome(t, t.TempDir())
	ss := []model.Session{
		{Harness: "claude", ID: "prior", Project: "svc", Updated: time.Now().Add(-time.Hour),
			Messages: []model.Message{{Role: "user", Text: "the golden suite needs SVC_FIXTURES"}}},
		{Harness: "opencode", ID: "live", Project: "svc", Updated: time.Now(),
			Messages: []model.Message{{Role: "user", Text: "the golden suite needs SVC_FIXTURES"}}},
	}
	if got := ignoredByPolicy(ss); len(got) != 2 {
		t.Fatalf("with nothing excluded the filter served %d of 2", len(got))
	}
	t.Setenv("DEJA_EXCLUDE_HARNESSES", "opencode")
	got := ignoredByPolicy(ss)
	if len(got) != 1 || got[0].Harness != "claude" {
		names := make([]string, 0, len(got))
		for _, s := range got {
			names = append(names, s.Harness+":"+s.ID)
		}
		t.Errorf("the excluded harness is still served: %s", strings.Join(names, " "))
	}
}
