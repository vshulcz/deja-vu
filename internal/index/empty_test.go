package index

import (
	"github.com/vshulcz/deja-vu/internal/query"
	"github.com/vshulcz/deja-vu/internal/search"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// An empty postings directory is the same loss as a missing one — what
// `rm buckets/*.bin` or a copy that skipped the contents leaves behind. Every
// check passed, doctor said "built, up to date", and search answered "no
// matches in N indexed sessions" about text still in the record log (#946).
func TestAnEmptyBucketDirectoryCountsAsDamage(t *testing.T) {
	tmp := t.TempDir()
	store := filepath.Join(tmp, "claude", "proj-p")
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	// Every root the build consults, or another package's store leaks in and
	// this index is not the one the test wrote.
	setHome(t, filepath.Join(tmp, "home"))
	t.Setenv("USERPROFILE", filepath.Join(tmp, "home"))
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "claude"))
	t.Setenv("DEJA_CODEX_ROOT", filepath.Join(tmp, "codex"))
	t.Setenv("DEJA_OPENCODE_DB", filepath.Join(tmp, "opencode.db"))
	t.Setenv("DEJA_NOTES_FILE", filepath.Join(tmp, "notes.jsonl"))
	rec := `{"type":"user","message":{"role":"user","content":"the ticker window is 30s"},"timestamp":"2026-07-11T10:00:00Z","sessionId":"s1","cwd":"/proj"}` + "\n"
	if err := os.WriteFile(filepath.Join(store, "s1.jsonl"), []byte(rec), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(tmp, "index.db")
	if err := Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	if Damaged(dir) {
		t.Fatal("a fresh index was called damaged")
	}

	entries, err := os.ReadDir(filepath.Join(dir, "buckets"))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) == 0 {
		t.Fatal("no bucket files to remove")
	}
	for _, e := range entries {
		if err = os.Remove(filepath.Join(dir, "buckets", e.Name())); err != nil {
			t.Fatal(err)
		}
	}
	if !Damaged(dir) {
		t.Error("an index with no postings at all was called intact")
	}

	// And the next search rebuilds rather than reporting the history empty.
	ss, err := SearchWithRecovery(dir, query.Options{Query: "ticker window"}, os.Stderr)
	if err != nil {
		t.Fatal(err)
	}
	if len(ss) == 0 {
		t.Error("search still answers nothing after the recovery path ran")
	}
}

// buildWithEmpties writes one real transcript and n empty ones into a fresh
// store, builds it, and returns the index directory.
func buildWithEmpties(t *testing.T, empties int) {
	t.Helper()
	home := t.TempDir()
	setHome(t, home)
	t.Setenv("USERPROFILE", home)
	root := filepath.Join(home, "claude")
	t.Setenv("DEJA_CLAUDE_ROOT", root)
	proj := filepath.Join(root, "-tmp-g")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	realLine := `{"type":"user","sessionId":"real","cwd":"/tmp/g","timestamp":"2026-08-01T10:00:00Z","message":{"role":"user","content":"pool exhausted"}}` + "\n"
	if err := os.WriteFile(filepath.Join(proj, "real.jsonl"), []byte(realLine), 0o644); err != nil {
		t.Fatal(err)
	}
	for i := range empties {
		id := string(rune('a' + i))
		body := `{"type":"user","sessionId":"empty` + id + `","cwd":"/tmp/g","timestamp":"2026-08-01T08:00:00Z","message":{"role":"user","content":"   "}}` + "\n"
		if err := os.WriteFile(filepath.Join(proj, "empty"+id+".jsonl"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	dir := filepath.Join(home, "idx")
	if err := Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
}

// The counter says "since the last build" and counted since the last read, so a
// second build in one process reported its own empty transcripts plus whatever
// an earlier build had left behind (#1850). One process is one build for the
// CLI, which is why it never showed there — the test binary and the server's
// warmup are where several builds share a process.
func TestTheEmptyCounterBelongsToTheLastBuild(t *testing.T) {
	buildWithEmpties(t, 2)
	// Deliberately not read: the leftover is what the next build must not
	// inherit.
	buildWithEmpties(t, 1)
	if n := ReportEmptySessions(); n != 1 {
		t.Errorf("the second build reported %d empty transcripts, want its own 1", n)
	}
	// And a build with none reports none, rather than the previous build's.
	buildWithEmpties(t, 0)
	if n := ReportEmptySessions(); n != 0 {
		t.Errorf("a build with nothing empty reported %d", n)
	}
}

// The collision counter sits beside the empty one and had the same flaw: a
// second build in one process reported its own colliding ids plus the ones an
// earlier build had counted (#1850).
func TestTheCollisionCounterBelongsToTheLastBuild(t *testing.T) {
	build := func(pairs int) {
		home := t.TempDir()
		setHome(t, home)
		t.Setenv("USERPROFILE", home)
		root := filepath.Join(home, "claude")
		t.Setenv("DEJA_CLAUDE_ROOT", root)
		proj := filepath.Join(root, "-tmp-c")
		if err := os.MkdirAll(proj, 0o755); err != nil {
			t.Fatal(err)
		}
		for i := range pairs {
			id := "dup" + string(rune('a'+i))
			line := `{"type":"user","sessionId":"` + id + `","cwd":"/tmp/c","timestamp":"2026-08-01T10:00:00Z","message":{"role":"user","content":"pool exhausted"}}` + "\n"
			// One id in two files is what makes a collision.
			for _, half := range []string{"-one.jsonl", "-two.jsonl"} {
				if err := os.WriteFile(filepath.Join(proj, id+half), []byte(line), 0o644); err != nil {
					t.Fatal(err)
				}
			}
		}
		if err := Ensure(filepath.Join(home, "idx"), "", false, nil); err != nil {
			t.Fatal(err)
		}
	}
	build(2)
	// Deliberately not read.
	build(1)
	if n := ReportCollisions(); n != 1 {
		t.Errorf("the second build reported %d collisions, want its own 1", n)
	}
	build(0)
	if n := ReportCollisions(); n != 0 {
		t.Errorf("a build with no collisions reported %d", n)
	}
}

// An incremental pass is a build too. Two of them with nobody reading in
// between used to report the first one's colliding ids alongside the second's
// (#1850) — the reviewer of #1851 found this path after the two full ones were
// fixed.
func TestAnIncrementalPassCountsOnlyItsOwn(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)
	t.Setenv("USERPROFILE", home)
	root := filepath.Join(home, "claude")
	t.Setenv("DEJA_CLAUDE_ROOT", root)
	proj := filepath.Join(root, "-tmp-c")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	line := func(id string) string {
		return `{"type":"user","sessionId":"` + id + `","cwd":"/tmp/c","timestamp":"2026-08-01T10:00:00Z","message":{"role":"user","content":"pool exhausted"}}` + "\n"
	}
	pair := func(id string) {
		t.Helper()
		for _, half := range []string{"-one.jsonl", "-two.jsonl"} {
			if err := os.WriteFile(filepath.Join(proj, id+half), []byte(line(id)), 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
	dir := filepath.Join(home, "idx")

	pair("dupa")
	pair("dupb")
	if err := Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	if n := ReportCollisions(); n != 2 {
		t.Fatalf("the full build found %d colliding ids, want the 2 it was given", n)
	}

	// Two incremental passes, and nothing reads between them.
	pair("dupc")
	if err := Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	pair("dupd")
	if err := Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	if n := ReportCollisions(); n != 1 {
		t.Errorf("the last incremental reported %d colliding ids, want its own 1", n)
	}
}

// PrefixMatches has always answered 0 for an empty prefix — "not a question
// worth answering" — while FindByPrefix resolved it to whichever session sorted
// first, because every id has "" as a prefix. #853 is the rule that the count
// and the resolver must agree; this is that same disagreement with the sign
// flipped, and it reached a user through the MCP resource URI in #1728.
func TestEmptyPrefixResolvesToNothing(t *testing.T) {
	dir := askedFixture(t,
		map[string][]string{
			"aa1": {"why does the pool exhaust under load?"},
			"bb1": {"why is the build slow?"},
		},
		map[string]string{
			"aa1": "2026-03-01T10:00:00Z",
			"bb1": "2026-03-03T10:00:00Z",
		})

	s, ok, err := FindByPrefix(dir, "")
	if err != nil {
		t.Fatalf("empty prefix: %v", err)
	}
	if ok {
		t.Fatalf("an empty prefix opened session %q; every id has \"\" as a prefix, so this is whichever one sorted first", s.ID)
	}

	// The two answers agree, which is the whole point.
	if n := PrefixMatches(dir, ""); n != 0 {
		t.Fatalf("PrefixMatches = %d, want 0", n)
	}

	// A real prefix is untouched: the guard must not be a refusal of everything.
	if got, ok, err := FindByPrefix(dir, "aa"); err != nil || !ok || got.ID != "aa1" {
		t.Fatalf("got %q ok=%v err=%v, want aa1", got.ID, ok, err)
	}
}

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

// A transcript whose every message strips to empty still got a manifest row:
// `last` printed a blank line for it, `show` printed a bare header, and the
// counters disagreed — brief and doctor read the manifest, stats reads the
// records (1159 against 1157 on my store) (#868).
func TestSessionsWithNothingToIndexAreNotCounted(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)
	t.Setenv("USERPROFILE", home)
	root := filepath.Join(home, "claude")
	t.Setenv("DEJA_CLAUDE_ROOT", root)
	proj := filepath.Join(root, "-tmp-g")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(name, body string) {
		if err := os.WriteFile(filepath.Join(proj, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("a.jsonl", `{"type":"user","sessionId":"n1","cwd":"/tmp/g","timestamp":"2026-08-01T10:00:00Z","message":{"role":"user","content":"pool exhausted"}}`+"\n")
	// Nothing but whitespace: parsed as a message, never written as a record.
	write("b.jsonl", `{"type":"user","sessionId":"e2","cwd":"/tmp/g","timestamp":"2026-08-01T08:00:00Z","message":{"role":"user","content":"   "}}`+"\n")

	dir := filepath.Join(home, "idx")
	if err := Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	if n := ReportEmptySessions(); n != 1 {
		t.Errorf("empty transcripts reported = %d, want 1", n)
	}
	counts := HarnessSessionCounts(dir)
	if counts["claude"] != 1 {
		t.Errorf("manifest holds %d claude sessions, want 1", counts["claude"])
	}
	ss, err := Recent(dir, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(ss) != 1 || ss[0].ID != "n1" {
		t.Fatalf("recent = %+v, want only n1", ss)
	}
	// The session that does have content keeps its row across a rebuild.
	if err := Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	if counts := HarnessSessionCounts(dir); counts["claude"] != 1 {
		t.Errorf("after rebuild manifest holds %d claude sessions, want 1", counts["claude"])
	}
}

// OlderFormat must separate an index this build cannot read from one it cannot
// read at all: doctor says "written by an older deja" on the first, and a
// corrupt manifest is not that (#877).
func TestOlderFormatOnlyReportsAReadableOldIndex(t *testing.T) {
	dir := t.TempDir()
	if err := writeManifest(dir, Manifest{Version: version, Sessions: map[string]SessionMeta{}}); err != nil {
		t.Fatal(err)
	}
	if OlderFormat(dir) {
		t.Error("a current index was called old")
	}
	if err := writeManifest(dir, Manifest{Version: version - 1, Sessions: map[string]SessionMeta{}}); err != nil {
		t.Fatal(err)
	}
	if !OlderFormat(dir) {
		t.Error("an index from an older format was not reported")
	}
	if err := os.WriteFile(filepath.Join(dir, "manifest.gob"), []byte("not a gob"), 0o600); err != nil {
		t.Fatal(err)
	}
	if OlderFormat(dir) {
		t.Error("an unreadable manifest was called an older format")
	}
}

// FormatDirection has to name which side is behind, not merely that the two
// differ (#890).
func TestFormatDirection(t *testing.T) {
	dir := t.TempDir()
	if got := FormatDirection(dir); got != 0 {
		t.Errorf("no manifest = %d, want 0", got)
	}
	for _, tc := range []struct {
		v    int
		want int
	}{{version, 0}, {version - 1, -1}, {version + 1, 1}} {
		if err := writeManifest(dir, Manifest{Version: tc.v, Sessions: map[string]SessionMeta{}}); err != nil {
			t.Fatal(err)
		}
		if got := FormatDirection(dir); got != tc.want {
			t.Errorf("version %d = %d, want %d", tc.v, got, tc.want)
		}
		if got, want := OlderFormat(dir), tc.want != 0; got != want {
			t.Errorf("OlderFormat at version %d = %v", tc.v, got)
		}
	}
}
