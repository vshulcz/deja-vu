package index

import (
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/vshulcz/deja-vu/internal/search"
)

// seedGrokDB writes one session and its messages into a grok store, replacing
// what is there.
func seedGrokDB(t *testing.T, db string, msgs [][2]string) {
	t.Helper()
	stmts := `create table if not exists sessions (id text primary key, workspace_id text, title text, cwd_last text, created_at text);
create table if not exists messages (session_id text, seq integer, role text, message_json text, created_at text);
insert or replace into sessions values ('s1','w1','pool timeouts','/work/api','2026-01-01T10:00:00.000Z');
`
	for i, m := range msgs {
		stmts += fmt.Sprintf("insert into messages values ('s1',%d,'user',json('{\"role\":\"user\",\"content\":\"%s\"}'),'%s');\n",
			i, m[0], m[1])
	}
	if out, err := exec.Command("sqlite3", db, stmts).CombinedOutput(); err != nil {
		t.Fatalf("sqlite3 seed: %v %s", err, out)
	}
}

// grok's database has a since-the-watermark parser wired into the registry and
// nothing ever stamped it, so its LastUpdated stayed 0 for the life of the
// index and every pass read the store whole — the pass with one new line to
// index cost what reading everything costs (#2075).
//
// The store is only safe to stamp because its parser selects messages rather
// than sessions and normalises both sides of the comparison with a millisecond
// backoff (#2150): the same shape as opencode and cursor. hermes and zed do
// neither, which is why they are not stamped here.
func TestTheGrokStoreIsAskedOnlyForWhatIsNew(t *testing.T) {
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skip("sqlite3 CLI not available")
	}
	tmp := t.TempDir()
	setHome(t, tmp)
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "claude"))
	t.Setenv("DEJA_CODEX_ROOT", filepath.Join(tmp, "codex"))
	t.Setenv("DEJA_GOOSE_DB", filepath.Join(tmp, "none-goose.db"))
	t.Setenv("DEJA_OPENCODE_DB", filepath.Join(tmp, "none-opencode.db"))
	t.Setenv("DEJA_NOTES_FILE", filepath.Join(tmp, "notes.jsonl"))
	db := filepath.Join(tmp, "grok.db")
	t.Setenv("DEJA_GROK_DB", db)
	t.Setenv("DEJA_GROK_ROOT", filepath.Join(tmp, "grok"))

	seedGrokDB(t, db, [][2]string{{"marker-grok-one", "2026-01-01T10:00:00.000Z"}})
	dir := filepath.Join(tmp, "index.db")
	if err := Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	hits := func(marker string) int {
		t.Helper()
		got, err := Search(dir, search.Options{Query: marker, All: true})
		if err != nil {
			t.Fatal(err)
		}
		return len(got)
	}
	if hits("marker-grok-one") != 1 {
		t.Fatalf("the store was not indexed at all, so this measures nothing")
	}
	m, err := readManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	if m.Files[db].LastUpdated == 0 {
		t.Errorf("the store carries no watermark, so the next pass reads it whole")
	}

	// A line lands after the watermark, and one lands in the same millisecond
	// as it — the case the backoff exists for.
	seedGrokDB(t, db, [][2]string{
		{"marker-grok-same", "2026-01-01T10:00:00.000Z"},
		{"marker-grok-two", "2026-01-01T11:00:00.000Z"},
	})
	if err := Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	if n := hits("marker-grok-two"); n != 1 {
		t.Errorf("the line added after the watermark is not indexed: %d hits", n)
	}
	if n := hits("marker-grok-same"); n != 1 {
		t.Errorf("a line stamped at the watermark itself was skipped: %d hits", n)
	}
	// And the session is counted once: a cursor that hands back turns already
	// held would grow the count on every pass (#2025).
	if n := hits("marker-grok-one"); n != 1 {
		t.Errorf("the first line is held %d times", n)
	}
}

// The other half of stamping a shared store: the database changes whenever any
// session in it does, so a pass that reads only the new messages must not drop
// the records of the sessions it did not ask about — which is what the
// changed-file rule would do without fromDatabase knowing this store (#2033).
func TestAQuietGrokSessionSurvivesAPassThatDidNotAskForIt(t *testing.T) {
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skip("sqlite3 CLI not available")
	}
	tmp := t.TempDir()
	setHome(t, tmp)
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "claude"))
	t.Setenv("DEJA_CODEX_ROOT", filepath.Join(tmp, "codex"))
	t.Setenv("DEJA_GOOSE_DB", filepath.Join(tmp, "none-goose.db"))
	t.Setenv("DEJA_OPENCODE_DB", filepath.Join(tmp, "none-opencode.db"))
	t.Setenv("DEJA_NOTES_FILE", filepath.Join(tmp, "notes.jsonl"))
	db := filepath.Join(tmp, "grok.db")
	t.Setenv("DEJA_GROK_DB", db)
	t.Setenv("DEJA_GROK_ROOT", filepath.Join(tmp, "grok"))

	stmts := `create table sessions (id text primary key, workspace_id text, title text, cwd_last text, created_at text);
create table messages (session_id text, seq integer, role text, message_json text, created_at text);
insert into sessions values ('quiet','w1','quiet','/work/api','2026-01-01T09:00:00.000Z');
insert into sessions values ('busy','w1','busy','/work/api','2026-01-01T10:00:00.000Z');
insert into messages values ('quiet',0,'user',json('{"role":"user","content":"marker-grok-quiet"}'),'2026-01-01T09:00:00.000Z');
insert into messages values ('busy',0,'user',json('{"role":"user","content":"marker-grok-busy"}'),'2026-01-01T10:00:00.000Z');
`
	if out, err := exec.Command("sqlite3", db, stmts).CombinedOutput(); err != nil {
		t.Fatalf("sqlite3 seed: %v %s", err, out)
	}
	dir := filepath.Join(tmp, "index.db")
	if err := Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	hits := func(marker string) int {
		t.Helper()
		got, err := Search(dir, search.Options{Query: marker, All: true})
		if err != nil {
			t.Fatal(err)
		}
		return len(got)
	}
	if hits("marker-grok-quiet") != 1 || hits("marker-grok-busy") != 1 {
		t.Fatalf("both sessions were not indexed, so this measures nothing")
	}

	// Only the busy session gains a turn. The quiet one is not in what the
	// store hands back, and it has to still be there afterwards.
	add := `insert into messages values ('busy',1,'user',json('{"role":"user","content":"marker-grok-added"}'),'2026-01-01T11:00:00.000Z');`
	if out, err := exec.Command("sqlite3", db, add).CombinedOutput(); err != nil {
		t.Fatalf("sqlite3 add: %v %s", err, out)
	}
	if err := Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	if n := hits("marker-grok-added"); n != 1 {
		t.Errorf("the added turn is not indexed: %d hits", n)
	}
	if n := hits("marker-grok-quiet"); n != 1 {
		t.Errorf("the session the pass did not ask about was dropped: %d hits", n)
	}
}

// grok registers its database and its session files under one kind name, so
// "this pass read the store whole" cannot be decided by the kind. Deciding it
// that way marks the whole harness whole whenever a session file changes, and
// then the database's continued session — read from its watermark, so only the
// new turn came back — has its earlier turns dropped by key (#2075, the shape
// readWholeThisPass exists to prevent).
func TestATouchedGrokFileDoesNotCostTheDatabaseItsEarlierTurns(t *testing.T) {
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skip("sqlite3 CLI not available")
	}
	tmp := t.TempDir()
	setHome(t, tmp)
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "claude"))
	t.Setenv("DEJA_CODEX_ROOT", filepath.Join(tmp, "codex"))
	t.Setenv("DEJA_GOOSE_DB", filepath.Join(tmp, "none-goose.db"))
	t.Setenv("DEJA_OPENCODE_DB", filepath.Join(tmp, "none-opencode.db"))
	t.Setenv("DEJA_NOTES_FILE", filepath.Join(tmp, "notes.jsonl"))
	db := filepath.Join(tmp, "grok.db")
	t.Setenv("DEJA_GROK_DB", db)
	root := filepath.Join(tmp, "grok")
	t.Setenv("DEJA_GROK_ROOT", root)

	// A session file beside the store, so both grok kinds are in this pass.
	fileDir := filepath.Join(root, "sessions", url.PathEscape("/work/app"), "019f-file-session")
	if err := os.MkdirAll(fileDir, 0o755); err != nil {
		t.Fatal(err)
	}
	updates := filepath.Join(fileDir, "updates.jsonl")
	line := func(ts int, text string) string {
		return fmt.Sprintf(`{"timestamp":%d,"method":"session/update","params":{"update":{"sessionUpdate":"user_message_chunk","content":{"type":"text","text":"%s"},"_meta":{"promptIndex":0}}}}`+"\n", ts, text)
	}
	if err := os.WriteFile(updates, []byte(line(1782900001, "marker-grok-file")), 0o644); err != nil {
		t.Fatal(err)
	}
	seedGrokDB(t, db, [][2]string{{"marker-grok-first", "2026-01-01T10:00:00.000Z"}})

	dir := filepath.Join(tmp, "index.db")
	if err := Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	hits := func(marker string) int {
		t.Helper()
		got, err := Search(dir, search.Options{Query: marker, All: true})
		if err != nil {
			t.Fatal(err)
		}
		return len(got)
	}
	if hits("marker-grok-first") != 1 || hits("marker-grok-file") != 1 {
		t.Fatalf("the store and the session file were not both indexed, so this measures nothing")
	}

	// Both move in the same pass: the file gains a line, and the store's one
	// session gains a turn past its watermark.
	if err := os.WriteFile(updates,
		[]byte(line(1782900001, "marker-grok-file")+line(1782900009, "marker-grok-file-two")), 0o644); err != nil {
		t.Fatal(err)
	}
	add := `insert into messages values ('s1',9,'user',json('{"role":"user","content":"marker-grok-later"}'),'2026-01-01T12:00:00.000Z');`
	if out, err := exec.Command("sqlite3", db, add).CombinedOutput(); err != nil {
		t.Fatalf("sqlite3 add: %v %s", err, out)
	}
	if err := Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	if n := hits("marker-grok-later"); n != 1 {
		t.Errorf("the turn added to the store is not indexed: %d hits", n)
	}
	if n := hits("marker-grok-first"); n != 1 {
		t.Errorf("the store's earlier turn went with the touched session file: %d hits", n)
	}
}
