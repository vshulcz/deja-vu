package index

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/search"
)

// A session with nothing to index is set aside in Manifest.Empty, out of every
// listing, and a pass takes its row back when the id is read again (#5563).

// A goose db session with nothing to index is set aside by a rebuild, and a
// jsonl copy with text arrives in a re-read. The db owns the row by the goose
// rule, so the row is the one set aside, and it needs an Ord of its own:
// postings key on it, and two rows sharing one serve each other's turns.
func TestARowTakenBackByAReReadGetsItsOwnOrd(t *testing.T) {
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skip("no sqlite3")
	}
	tmp := hermeticIndexEnv(t)
	root := filepath.Join(tmp, "goose")
	sessions := filepath.Join(root, "sessions")
	if err := os.MkdirAll(sessions, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_GOOSE_ROOT", root)
	db := filepath.Join(sessions, "sessions.db")
	t.Setenv("DEJA_GOOSE_DB", db)
	sql := `create table sessions (id text primary key, name text, description text, working_dir text, created_at text, updated_at text);
create table messages (id integer primary key autoincrement, session_id text, role text, content_json text, created_timestamp integer);
insert into sessions values ('20260101_120000','n','pgbouncer pool','/tmp/app','2026-01-01 12:00:00','2026-01-01 12:05:00');
insert into messages values (1,'20260101_120000','user','[{"type":"text","text":"<deja-recall>\nRecalled history from prior sessions\n- x\n</deja-recall>"}]',1767268800);
insert into sessions values ('20260102_120000','n','kafka lag','/tmp/app2','2026-01-02 12:00:00','2026-01-02 12:05:00');
insert into messages values (2,'20260102_120000','user','[{"type":"text","text":"<deja-recall>\nRecalled history from prior sessions\n- y\n</deja-recall>"}]',1767355200);`
	if out, err := exec.CommandContext(t.Context(), "sqlite3", db, sql).CombinedOutput(); err != nil {
		t.Fatalf("sqlite3: %v %s", err, out)
	}
	other := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "p", "other.jsonl")
	write(t, other, claudeLine("s-other", "2026-01-02T03:04:05Z", "alpha one")+claudeLine("s-other", "2026-01-02T03:04:06Z", "alpha two"))
	dir := filepath.Join(tmp, "idx")
	if err := Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	m, _ := readManifest(dir)
	key := "goose:20260101_120000"
	if _, ok := m.Empty[key]; !ok {
		t.Fatalf("premise: db session not set aside; sessions=%v empty=%v", m.Sessions[key], m.Empty)
	}
	jsonl := `{"id":"20260101_120000","description":"pgbouncer pool","working_dir":"/tmp/app"}
{"role":"user","content":[{"type":"text","text":"why does pgbouncer time out"}],"created":1767268800}
`
	write(t, filepath.Join(sessions, "20260101_120000.jsonl"), jsonl)
	write(t, filepath.Join(sessions, "20260102_120000.jsonl"), `{"id":"20260102_120000","description":"kafka lag","working_dir":"/tmp/app2"}
{"role":"user","content":[{"type":"text","text":"why is kafkaconsumer lagging"}],"created":1767355200}
`)
	write(t, other, claudeLine("s-other", "2026-01-02T03:04:05Z", "alpha one"))
	var progress strings.Builder
	if err := Ensure(dir, "", false, &progress); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(progress.String(), replacementPassMarker) {
		t.Fatalf("the pass was not a re-read: %q", progress.String())
	}
	m, _ = readManifest(dir)
	if _, ok := m.Sessions[key]; !ok {
		t.Fatalf("%s has no row after its text arrived", key)
	}
	ords := map[uint32]string{}
	for k, r := range m.Sessions {
		if prev, dup := ords[r.Ord]; dup {
			t.Errorf("Ord %d shared by %s and %s", r.Ord, prev, k)
		}
		ords[r.Ord] = k
		if r.Ord == 0 {
			t.Errorf("listed row %s has Ord 0", k)
		}
	}
	for _, q := range []string{"pgbouncer", "kafkaconsumer"} {
		ss, err := Search(dir, search.Options{Query: q, All: true})
		if err != nil || len(ss) != 1 {
			t.Errorf("search %s: %d sessions err=%v", q, len(ss), err)
		}
	}
}

// The stub file rewritten to hold another session: the row set aside for
// the stub's id goes, or the transcript that takes the id later spans a file
// that no longer holds it.
func TestASetAsideRowGoesWhenItsFileNoLongerHoldsTheID(t *testing.T) {
	for _, by := range []string{"grow", "shrink"} {
		t.Run(by, func(t *testing.T) {
			tmp := hermeticIndexEnv(t)
			dir := filepath.Join(tmp, "idx")
			other := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "p", "other.jsonl")
			write(t, other, claudeLine("s-other", "2026-01-02T03:04:05Z", "alpha one"))
			stub := filepath.Join(geminiChats(), "session-a-008140a7.jsonl")
			write(t, stub, geminiStub())
			if err := Ensure(dir, "", true, nil); err != nil {
				t.Fatal(err)
			}
			m, _ := readManifest(dir)
			if _, ok := m.Empty["gemini:"+resumeID]; !ok {
				t.Fatalf("premise: stub not in Empty")
			}
			otherID := "11111111-3349-4cca-bc66-b32418429ebf"
			body := strings.ReplaceAll(geminiOriginal(), resumeID, otherID)
			if by == "shrink" {
				body = `{"sessionId":"` + otherID + `","projectHash":"bc04","startTime":"2026-11-01T13:04:39.847Z","lastUpdated":"2026-11-01T13:04:39.847Z","kind":"main"}` + "\n" +
					`{"id":"u1","timestamp":"2026-11-01T13:04:40.000Z","type":"user","content":[{"text":"zz"}]}` + "\n"
			}
			write(t, stub, body)
			bumpMTime(t, stub)
			var progress strings.Builder
			if err := Ensure(dir, "", false, &progress); err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(progress.String(), replacementPassMarker) {
				t.Fatalf("the rewrite was not re-read: %q", progress.String())
			}
			write(t, filepath.Join(geminiChats(), "session-b-008140a7.jsonl"), geminiOriginal())
			if err := Ensure(dir, "", false, nil); err != nil {
				t.Fatal(err)
			}
			m, _ = readManifest(dir)
			got := m.Sessions["gemini:"+resumeID]
			if err := Ensure(dir, "", true, nil); err != nil {
				t.Fatal(err)
			}
			m, _ = readManifest(dir)
			want := m.Sessions["gemini:"+resumeID]
			if !got.Started.Equal(want.Started) || !got.Updated.Equal(want.Updated) || strings.Join(got.Copies, ",") != strings.Join(want.Copies, ",") || got.Shared != want.Shared {
				t.Errorf("incremental Started=%v Updated=%v Copies=%v Shared=%v; rebuild Started=%v Updated=%v Copies=%v Shared=%v",
					got.Started, got.Updated, got.Copies, got.Shared, want.Started, want.Updated, want.Copies, want.Shared)
			}
		})
	}
}

func TestForgetTakesASetAsideRowWithIt(t *testing.T) {
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skip("no sqlite3")
	}
	tmp := hermeticIndexEnv(t)
	// Tombstones live in the config dir, which the package shares otherwise.
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(tmp, "config"))
	root := filepath.Join(tmp, "goose")
	sessions := filepath.Join(root, "sessions")
	if err := os.MkdirAll(sessions, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_GOOSE_ROOT", root)
	db := filepath.Join(sessions, "sessions.db")
	t.Setenv("DEJA_GOOSE_DB", db)
	sql := `create table sessions (id text primary key, name text, description text, working_dir text, created_at text, updated_at text);
create table messages (id integer primary key autoincrement, session_id text, role text, content_json text, created_timestamp integer);
insert into sessions values ('s1','n','pgbouncer pool','/tmp/secretproj','2026-01-01 12:00:00','2026-01-01 12:05:00');
insert into messages values (1,'s1','user','[{"type":"text","text":"why does pgbouncer time out"}]',1767268800);
insert into sessions values ('s2','n','acquisition of rivalco','/tmp/secretproj','2026-01-02 12:00:00','2026-01-02 12:05:00');
insert into messages values (2,'s2','user','[{"type":"text","text":"<deja-recall>\nRecalled history from prior sessions\n- y\n</deja-recall>"}]',1767355200);`
	if out, err := exec.CommandContext(t.Context(), "sqlite3", db, sql).CombinedOutput(); err != nil {
		t.Fatalf("sqlite3: %v %s", err, out)
	}
	dir := filepath.Join(tmp, "idx")
	if err := Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	if m, _ := readManifest(dir); len(m.Empty) != 1 {
		t.Fatalf("premise: the recall-only session is not set aside: %v", m.Empty)
	}
	res, err := Forget(dir, ForgetOptions{Project: "secretproj"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Sessions != 2 {
		t.Errorf("forget --project matched %d sessions, want both, the set-aside one included", res.Sessions)
	}
	m, _ := readManifest(dir)
	for k, r := range m.Empty {
		t.Errorf("after forget --project, Empty holds %s project=%s title=%q", k, r.Project, r.Title)
	}
	if !readTombstones()["goose:s2"] {
		t.Error("the set-aside session got no tombstone, so it comes back once it holds text")
	}
}

// A stub renamed before its transcript arrives: its row set aside follows
// the new name, as a rebuild reading the renamed file gives (#5563).
func TestASetAsideRowFollowsARename(t *testing.T) {
	tmp := hermeticIndexEnv(t)
	dir := filepath.Join(tmp, "idx")
	write(t, filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "p", "other.jsonl"), claudeLine("s-other", "2026-01-02T03:04:05Z", "alpha one"))
	stub := filepath.Join(geminiChats(), "session-a-008140a7.jsonl")
	write(t, stub, geminiStub())
	if err := Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	moved := filepath.Join(geminiChats(), "session-0-008140a7.jsonl")
	if err := os.Rename(stub, moved); err != nil {
		t.Fatal(err)
	}
	var progress strings.Builder
	if err := Ensure(dir, "", false, &progress); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(progress.String(), "renamed") {
		t.Fatalf("the move was not taken for a rename: %q", progress.String())
	}
	write(t, filepath.Join(geminiChats(), "session-b-008140a7.jsonl"), geminiOriginal())
	if err := Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	row := func() SessionMeta {
		t.Helper()
		m, err := readManifest(dir)
		if err != nil {
			t.Fatal(err)
		}
		return m.Sessions["gemini:"+resumeID]
	}
	got := row()
	if err := Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	want := row()
	if !got.Started.Equal(want.Started) || !got.Updated.Equal(want.Updated) || !slices.Equal(got.Copies, want.Copies) {
		t.Errorf("incremental Started=%v Updated=%v Copies=%v; rebuild Started=%v Updated=%v Copies=%v",
			got.Started, got.Updated, got.Copies, want.Started, want.Updated, want.Copies)
	}
}

// Forget counts a set-aside row like a listed one, so the dry run, the
// multi-match guard and the exact-id rule (#870) see it (#5563).
func TestForgetCountsSetAsideRows(t *testing.T) {
	recallOnly := func(sid string) string {
		return hjson(map[string]any{"type": "user", "sessionId": sid, "uuid": "u1", "timestamp": "2026-10-15T01:00:01.000Z", "cwd": "/tmp/demo",
			"message": map[string]any{"role": "user", "content": []any{map[string]any{"tool_use_id": "toolu_1", "type": "tool_result",
				"content": []any{map[string]any{"type": "text", "text": "<deja-recall>\nRecalled history\n</deja-recall>"}}}}}})
	}
	tmp := hermeticIndexEnv(t)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(tmp, "config"))
	root := os.Getenv("DEJA_CLAUDE_ROOT")
	write(t, filepath.Join(root, "p", "abc.jsonl"), recallOnly("abc"))
	write(t, filepath.Join(root, "p", "abcd.jsonl"), recallOnly("abcd"))
	write(t, filepath.Join(root, "p", "listed.jsonl"), claudeLine("abc-listed", "2026-10-15T01:00:00Z", "alpha one"))
	dir := filepath.Join(tmp, "idx")
	if err := Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	if m, _ := readManifest(dir); len(m.Empty) != 2 {
		t.Fatalf("premise: want 2 rows set aside, got %v", m.Empty)
	}
	dry, err := Forget(dir, ForgetOptions{Session: "abc", DryRun: true})
	if err != nil {
		t.Fatal(err)
	}
	if dry.Sessions != 1 || strings.Join(dry.Keys, ",") != "claude:abc" {
		t.Errorf("--session abc names one set-aside session exactly, the dry run matched %d: %v", dry.Sessions, dry.Keys)
	}
	if readTombstones()["claude:abc"] {
		t.Fatal("the dry run wrote a tombstone")
	}
	all, err := Forget(dir, ForgetOptions{Session: "abcd"})
	if err != nil {
		t.Fatal(err)
	}
	if all.Sessions != 1 || all.Tombstones != 1 {
		t.Errorf("forget of a set-aside session reported %d sessions, %d tombstones", all.Sessions, all.Tombstones)
	}
	if !readTombstones()["claude:abcd"] || readTombstones()["claude:abc"] {
		t.Errorf("tombstones after forget --session abcd: %v", readTombstones())
	}
	m, _ := readManifest(dir)
	if _, ok := m.Empty["claude:abcd"]; ok {
		t.Error("the forgotten row is still set aside")
	}
	if _, ok := m.Empty["claude:abc"]; !ok {
		t.Error("forget --session abcd took abc's row too")
	}
}
