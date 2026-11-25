package index

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"github.com/vshulcz/deja-vu/internal/evidence"
	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/query"
	"github.com/vshulcz/deja-vu/internal/search"
	"github.com/vshulcz/deja-vu/internal/sources"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestDevSynonymFoldBridgesAbbreviations(t *testing.T) {
	dir := nlIndex(t,
		"the authentication middleware rejects rotated tokens",
		"kubernetes ingress drops websocket upgrades",
		"unrelated docker session filler",
	)
	// auth->authentication rides the substring tier (prefix); the synonym
	// table exists for pairs with no substring bridge, like k8s->kubernetes.
	for query, wantID := range map[string]string{
		"auth middleware rejects": "a",
		"k8s ingress websocket":   "b",
	} {
		result, err := SearchDetailed(dir, search.Options{Query: query, All: true})
		if err != nil {
			t.Fatal(err)
		}
		if len(result.Sessions) != 1 || result.Sessions[0].ID != wantID {
			t.Fatalf("%q: sessions=%+v", query, result.Sessions)
		}
	}
	k8s, err := SearchDetailed(dir, search.Options{Query: "k8s ingress websocket", All: true})
	if err != nil || !k8s.Stemmed {
		t.Fatalf("k8s must resolve via the stem tier: %+v %v", k8s, err)
	}
}

func TestDevSynonymNeverTouchesExactTier(t *testing.T) {
	dir := nlIndex(t,
		"auth service config drift",
		"authentication deep dive",
	)
	result, err := SearchDetailed(dir, search.Options{Query: "auth service", All: true})
	if err != nil {
		t.Fatal(err)
	}
	if result.Stemmed || len(result.Sessions) != 1 || result.Sessions[0].ID != "a" {
		t.Fatalf("exact tier polluted: %+v", result)
	}
}

func TestCyrillicSuffixFold(t *testing.T) {
	dir := nlIndex(t,
		"миграции таблиц падают на проде после отката",
		"unrelated english session",
	)
	for _, q := range []string{"миграция падает", "миграцию откат"} {
		result, err := SearchDetailed(dir, search.Options{Query: q, All: true})
		if err != nil {
			t.Fatal(err)
		}
		if len(result.Sessions) != 1 || result.Sessions[0].ID != "a" {
			t.Fatalf("%q: sessions=%+v variants=%v", q, result.Sessions, result.Variants)
		}
	}
}

func TestStopWordsDoNotConstrainRetrieval(t *testing.T) {
	tmp := hermeticIndexEnv(t)
	dir := filepath.Join(tmp, "idx")
	// The record does NOT contain the stop word "before".
	sessions := []model.Session{{ID: "s", Harness: "claude", Project: "p", Updated: time.Now(),
		Messages: []model.Message{{Role: "user", Text: "fix the race condition on commit"}}}}
	if err := os.MkdirAll(filepath.Join(dir+".tmp", "buckets"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := writeSessions(dir+".tmp", dir, sessions, nil, ""); err != nil {
		t.Fatal(err)
	}
	// Natural-language query with a stop word ("before") that is NOT in the
	// record. The content tokens race+commit must still find it.
	hits, err := Search(dir, search.Options{Query: "race before commit", All: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 1 {
		t.Fatalf("stop word over-constrained retrieval: got %d hits", len(hits))
	}
}

// seedCursorStore writes one composer and its bubbles into a Cursor store.
func seedCursorStore(t *testing.T, db, composer string, bubbles [][2]any) {
	t.Helper()
	var last int64
	headers := ""
	var headersSb18 strings.Builder
	for i, b := range bubbles {
		ts, ok := b[1].(int64)
		if !ok {
			t.Fatalf("bubble %d: time %v is not an int64", i, b[1])
		}
		if ts > last {
			last = ts
		}
		if i > 0 {
			headersSb18.WriteString(",")
		}
		fmt.Fprintf(&headersSb18, `{"bubbleId":"b%d","type":1}`, i)
	}
	headers += headersSb18.String()
	stmts := "create table if not exists cursorDiskKV (key text primary key, value text);\n"
	stmts += fmt.Sprintf("insert or replace into cursorDiskKV values ('composerData:%[1]s', json('{\"composerId\":\"%[1]s\",\"name\":\"work\",\"createdAt\":%[2]d,\"lastUpdatedAt\":%[2]d,\"fullConversationHeadersOnly\":[%[3]s]}'));\n", composer, last, headers)
	var stmtsSb29 strings.Builder
	for i, b := range bubbles {
		text, ok := b[0].(string)
		if !ok {
			t.Fatalf("bubble %d: text %v is not a string", i, b[0])
		}
		ts, _ := b[1].(int64)
		fmt.Fprintf(&stmtsSb29, "insert or replace into cursorDiskKV values ('bubbleId:%s:b%d', json('{\"type\":1,\"text\":\"%s\",\"timestamp\":%d,\"workspaceProjectDir\":\"/tmp/app\"}'));\n",
			composer, i, text, ts)
	}
	stmts += stmtsSb29.String()
	if out, err := exec.CommandContext(t.Context(), "sqlite3", db, stmts).CombinedOutput(); err != nil {
		t.Fatalf("sqlite3 seed: %v %s", err, out)
	}
}

// A store's watermark is what deja asks it for next time, so it has to be the
// newest session in THAT store. Taking the newest across the harness stamped a
// quiet Cursor workspace with the busy one's time, and everything the quiet
// store gained below that line was skipped for ever (#2071).
func TestEachCursorStoreKeepsItsOwnWatermark(t *testing.T) {
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
	root := filepath.Join(tmp, "Cursor", "User")
	busy := filepath.Join(root, "globalStorage")
	quiet := filepath.Join(root, "workspaceStorage", "ws1")
	for _, d := range []string{busy, quiet} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("DEJA_CURSOR_ROOT", root)
	busyDB := filepath.Join(busy, "state.vscdb")
	quietDB := filepath.Join(quiet, "state.vscdb")

	const base = int64(1767268800000) // 2026-01-01 12:00, in milliseconds
	const hour = int64(3600000)
	seedCursorStore(t, busyDB, "comp-busy", [][2]any{{"marker-busy-one", base + 10*hour}})
	seedCursorStore(t, quietDB, "comp-quiet", [][2]any{{"marker-quiet-one", base}})

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
	// The premise: both stores were read, so the watermarks below are about
	// two stores that each hold something.
	if hits("marker-busy-one") != 1 || hits("marker-quiet-one") != 1 {
		t.Fatalf("the two stores were not both indexed, so this measures nothing")
	}
	m, err := readManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	if m.Files[quietDB].LastUpdated > m.Files[busyDB].LastUpdated {
		t.Fatalf("the quiet store is stamped later than the busy one: %d vs %d",
			m.Files[quietDB].LastUpdated, m.Files[busyDB].LastUpdated)
	}
	if m.Files[quietDB].LastUpdated == m.Files[busyDB].LastUpdated {
		t.Errorf("both stores carry one watermark (%d), so the quiet one is stamped with the busy one's newest",
			m.Files[quietDB].LastUpdated)
	}

	// A turn lands in the quiet workspace: newer than anything that store
	// holds, older than the busy store's newest.
	seedCursorStore(t, quietDB, "comp-quiet", [][2]any{
		{"marker-quiet-one", base}, {"marker-quiet-two", base + hour},
	})
	if err := Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	if n := hits("marker-quiet-two"); n != 1 {
		t.Errorf("the turn added to the quiet store is not indexed: %d hits", n)
	}
}

// opencode is the exception the rule above has to keep: its rows carry the
// project directory in Path, not the store path, so filtering on Path would
// zero its watermark and re-read the whole database on every pass.
func TestTheOpencodeStoreStillGetsAWatermark(t *testing.T) {
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
	stamped := `create table session (id text primary key, directory text, time_created integer, time_updated integer);
create table message (id text primary key, session_id text, data text, time_created integer);
create table part (id text primary key, message_id text, data text);
insert into session values ('s1','/tmp/app',1767268800000,1767268800000);
insert into message values ('m1','s1','{"role":"user"}',1767268800000);
insert into part values ('p1','m1',json_object('type','text','text','the pool was exhausted'));`
	if out, err := exec.CommandContext(t.Context(), "sqlite3", db, stamped).CombinedOutput(); err != nil {
		t.Fatalf("sqlite3 seed: %v %s", err, out)
	}
	dir := filepath.Join(tmp, "index.db")
	if err := Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	m, err := readManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	// The premise: the row really does name the project rather than the store.
	if s := m.Sessions["opencode:s1"]; s.Path == db {
		t.Fatalf("opencode now records the store path, so this measures nothing: %s", s.Path)
	}
	if m.Files[db].LastUpdated == 0 {
		t.Error("the opencode store carries no watermark, so every pass reads it whole")
	}
}

func museRec(stream, typ, payload string) string {
	return `{"schema_version":1,"id":"r","stream":{"kind":"session","id":"` + stream + `"},"sequence":1,"recorded_at":1791200000000000,"record_type":"event","payload_type":"` + typ + `","payload":` + payload + "}\n"
}

func writeMuseLog(t *testing.T, dir, id, workspaceType, prompt string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := museRec(id, workspaceType, `{"record":{"workspace_root":"/w/app"}}`) +
		museRec(id, "runtime.session", `{"kind":"run","event":{"kind":"started","prompt":"`+prompt+`"}}`)
	if err := os.WriteFile(filepath.Join(dir, "session.jsonl"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// A transcript the listing leaves out while it is still on disk was not
// deleted. Turning DEJA_INCLUDE_SUBAGENTS off after a pass with it on kept
// every child in the index and said "N transcripts no longer on disk", about
// files sitting where they always were; a rebuild drops them. The incremental
// pass ends where the rebuild does (#4739).
func TestUnlistedTranscriptOnDiskIsNotKeptAsDeleted(t *testing.T) {
	tmp := t.TempDir()
	root := filepath.Join(tmp, "muse", "sessions")
	parent := filepath.Join(root, "2026", "10", "05", "01a10be5-2bcd-7153-bacf-761a1bd3642b")
	child := filepath.Join(parent, "subagent", "01a10be5-3064-7d21-a788-cb874c890d18")
	writeMuseLog(t, parent, "01a10be5-2bcd-7153-bacf-761a1bd3642b", "runtime.session.metadata", "spawn a lister")
	writeMuseLog(t, child, "01a10be5-3064-7d21-a788-cb874c890d18", "session.workspace_branch.observed", "list the quuxchild files")
	setHome(t, filepath.Join(tmp, "home"))
	t.Setenv("DEJA_MUSE_ROOTS", root)
	dir := filepath.Join(tmp, "index.db")

	t.Setenv("DEJA_INCLUDE_SUBAGENTS", "1")
	if err := Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	if ss, _ := Search(dir, search.Options{Query: "quuxchild"}); len(ss) != 1 {
		t.Fatalf("with the switch on the child is not indexed: %#v", ss)
	}

	t.Setenv("DEJA_INCLUDE_SUBAGENTS", "0")
	var log bytes.Buffer
	if err := Ensure(dir, "", false, &log); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(log.String(), "no longer on disk") {
		t.Errorf("a child still on disk was reported gone:\n%s", log.String())
	}
	if ss, _ := Search(dir, search.Options{Query: "quuxchild"}); len(ss) != 0 {
		t.Fatalf("with the switch off the child is still indexed, which a rebuild would not do: %#v", ss)
	}
	if ss, _ := Search(dir, search.Options{Query: "lister"}); len(ss) != 1 {
		t.Fatalf("the parent left with the child: %#v", ss)
	}
}

// The other half: a pass that cannot see a store at all — a hook started with
// a stripped environment that lost DEJA_MUSE_ROOTS (#4738) — is not a setting
// leaving a file out, and must not take the store's sessions with it. Nothing
// in this run's view claims those paths, so they stay, and they are not
// described as deleted either: they are on disk.
func TestStoreOutsideThisRunsViewIsKept(t *testing.T) {
	tmp := t.TempDir()
	root := filepath.Join(tmp, "custom", "muse", "sessions")
	session := filepath.Join(root, "2026", "10", "05", "01a10be5-2bcd-7153-bacf-761a1bd3642b")
	writeMuseLog(t, session, "01a10be5-2bcd-7153-bacf-761a1bd3642b", "runtime.session.metadata", "the quuxroot migration")
	setHome(t, filepath.Join(tmp, "home"))
	dir := filepath.Join(tmp, "index.db")

	t.Setenv("DEJA_MUSE_ROOTS", root)
	if err := Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	if ss, _ := Search(dir, search.Options{Query: "quuxroot"}); len(ss) != 1 {
		t.Fatalf("the session was not indexed: %#v", ss)
	}

	t.Setenv("DEJA_MUSE_ROOTS", "")
	var log bytes.Buffer
	if err := Ensure(dir, "", false, &log); err != nil {
		t.Fatal(err)
	}
	if ss, _ := Search(dir, search.Options{Query: "quuxroot"}); len(ss) != 1 {
		t.Fatalf("a pass that could not see the store dropped its session: %#v\n%s", ss, log.String())
	}
	if strings.Contains(log.String(), "no longer on disk") {
		t.Errorf("a transcript on disk was described as gone:\n%s", log.String())
	}
	if !strings.Contains(log.String(), "1 transcript outside the stores this run reads") {
		t.Errorf("the pass did not say why it kept the session:\n%s", log.String())
	}
}

// End to end for the role #3384 added, through the path `deja search` takes:
// retrieval, then ranking. A harness's compaction summary is in the index and
// answers when it is asked for, and it does not answer a question that did not.
//
// Both halves matter. Dropping the summary loses the only record of the turns
// that were compacted away; leaving it as speech is what made 19.6% of the
// indexed text of those sessions a paraphrase nobody said, competing with the
// conversation it paraphrases.
func TestACompactionSummaryAnswersOnlyWhenAskedFor(t *testing.T) {
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skip("sqlite3 not installed")
	}
	tmp := hermeticIndexEnv(t)
	db := filepath.Join(tmp, "opencode.db")
	t.Setenv("DEJA_OPENCODE_DB", db)
	script := `create table session(id text, directory text, time_created any, time_updated any);
create table message(id text, session_id text, time_created any, data text);
create table part(id text, message_id text, data text);
insert into session values('s1','/w/app','2026-01-02T03:00:00Z','2026-01-03T03:00:00Z');
insert into message values('m1','s1',1767409200000,'{"role":"user","agent":"build"}');
insert into part values('p1','m1','{"type":"text","text":"the invoice exporter drops every third retry","time":{"start":"2026-01-02T03:00:00Z"}}');
insert into message values('m2','s1',1767409260000,'{"role":"assistant","agent":"build"}');
insert into part values('p2','m2','{"type":"text","text":"capped the retries at three","time":{"start":"2026-01-02T03:01:00Z"}}');
insert into message values('m3','s1',1767409320000,'{"role":"assistant","summary":true,"mode":"compaction","agent":"compaction"}');
insert into part values('p3','m3','{"type":"text","text":"## Goal quazzlebolt the widget pipeline until the drops stop","time":{"start":"2026-01-02T03:02:00Z"}}');`
	if out, err := exec.CommandContext(t.Context(), "sqlite3", db, script).CombinedOutput(); err != nil {
		t.Fatalf("sqlite setup: %v %s", err, out)
	}
	dir := filepath.Join(tmp, "idx")
	if err := Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}

	hits := func(o search.Options) []search.Hit {
		t.Helper()
		ss, err := Search(dir, o)
		if err != nil {
			t.Fatal(err)
		}
		out, err := search.Run(ss, o)
		if err != nil {
			t.Fatal(err)
		}
		return out
	}

	// A word that exists only inside the summary.
	if got := hits(search.Options{Query: "quazzlebolt widget pipeline", All: true}); len(got) != 0 {
		t.Errorf("an ordinary question was answered from the summary: %d hit(s), %q",
			len(got), got[0].Snippets)
	}
	// Asked for by role, it is there: the compacted half is not lost.
	asked := hits(search.Options{Query: "quazzlebolt widget pipeline", Role: "summary", All: true})
	if len(asked) != 1 || len(asked[0].Snippets) == 0 ||
		!strings.Contains(asked[0].Snippets[0], "quazzlebolt") {
		t.Errorf("--role summary does not answer from the summary: %#v", asked)
	}
	// And the conversation is still ordinary speech.
	if got := hits(search.Options{Query: "invoice exporter retry", All: true}); len(got) != 1 {
		t.Fatalf("the question itself is no longer findable: %d hit(s)", len(got))
	}
}

func writeSyncBatch(t *testing.T, dir string, recs []SyncRecord) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	f, err := os.Create(filepath.Join(dir, "deja-sync-test-1.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	enc := json.NewEncoder(f)
	for _, r := range recs {
		if err := enc.Encode(r); err != nil {
			t.Fatal(err)
		}
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
}

// Imported records exist only in the index, not in any source file. A full
// rebuild used to regenerate the index from sources alone and drop them, and
// redaction bookkeeping used to invent a phantom Files entry for the virtual
// deja-sync-import path that the next update classified as a removed source,
// purging every imported record.
func TestImportedRecordsSurviveRebuildAndUpdate(t *testing.T) {
	tmp := t.TempDir()
	claudeRoot := filepath.Join(tmp, "claude")
	proj := filepath.Join(claudeRoot, "-tmp-local")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	local := `{"type":"user","sessionId":"local1","timestamp":"2026-01-02T03:04:05Z","message":{"role":"user","content":"localneedle question"}}` + "\n"
	if err := os.WriteFile(filepath.Join(proj, "local1.jsonl"), []byte(local), 0o644); err != nil {
		t.Fatal(err)
	}
	setHome(t, t.TempDir())
	t.Setenv("USERPROFILE", os.Getenv("HOME"))
	t.Setenv("DEJA_CLAUDE_ROOT", claudeRoot)
	t.Setenv("DEJA_CODEX_ROOT", filepath.Join(tmp, "codex"))
	t.Setenv("DEJA_OPENCODE_DB", filepath.Join(tmp, "opencode.db"))
	dir := filepath.Join(tmp, "index.db")
	if err := EnsureForSearch(dir, search.Options{Query: "localneedle"}, false, nil); err != nil {
		t.Fatal(err)
	}
	// Raw secret in the batch forces redaction bookkeeping during re-ingest,
	// which is what created the phantom Files entry.
	secret := "AKIA" + "IOSFODNN7EXAMPLE"
	base := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	batchDir := filepath.Join(tmp, "batches")
	writeSyncBatch(t, batchDir, []SyncRecord{
		{Harness: "claude", SessionID: "remote1", Project: "deja-vu", Role: "user", Text: "importneedle question key " + secret, Time: base},
		{Harness: "claude", SessionID: "remote1", Project: "deja-vu", Role: "assistant", Text: "importneedle answer", Time: base.Add(time.Minute)},
	})
	if n, err := Import(dir, batchDir); err != nil || n != 2 {
		t.Fatalf("import n=%d err=%v", n, err)
	}

	findImported := func(stage string) {
		t.Helper()
		ss, err := Search(dir, search.Options{Query: "importneedle", All: true})
		if err != nil {
			t.Fatalf("%s: %v", stage, err)
		}
		if len(ss) != 1 || !strings.HasPrefix(ss[0].Project, "imported:") || len(ss[0].Messages) != 2 {
			t.Fatalf("%s: bad imported result: %#v", stage, ss)
		}
	}
	findImported("after import")

	// Full rebuild regenerates the index from sources; imported must survive.
	if err := EnsureForSearch(dir, search.Options{Query: "importneedle"}, true, nil); err != nil {
		t.Fatal(err)
	}
	findImported("after rebuild")

	m, err := readManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := m.Files[syncImportPath]; ok {
		t.Fatalf("phantom Files entry for %s", syncImportPath)
	}

	// Non-append update: one source removed, one added. This is the path that
	// used to purge imported records via the phantom removed entry.
	if err = os.Remove(filepath.Join(proj, "local1.jsonl")); err != nil {
		t.Fatal(err)
	}
	local2 := `{"type":"user","sessionId":"local2","timestamp":"2026-01-03T03:04:05Z","message":{"role":"user","content":"othertext here"}}` + "\n"
	if err = os.WriteFile(filepath.Join(proj, "local2.jsonl"), []byte(local2), 0o644); err != nil {
		t.Fatal(err)
	}
	if err = EnsureForSearch(dir, search.Options{Query: "importneedle"}, false, nil); err != nil {
		t.Fatal(err)
	}
	findImported("after non-append update")

	// Dedupe map and watermarks must survive both paths.
	if n, importErr := Import(dir, batchDir); importErr != nil || n != 0 {
		t.Fatalf("re-import n=%d err=%v", n, importErr)
	}
	findImported("after re-import")

	// Export must not echo imported records back to their origin: only the
	// two native local messages leave this machine.
	outDir := filepath.Join(filepath.Dir(dir), "echo-out")
	n, err := Export(dir, outDir)
	if err != nil {
		t.Fatal(err)
	}
	// Two native records: local2, and local1, whose file was removed while
	// its directory stayed and which the index therefore kept (#2970). What
	// must not be here is the imported one.
	if n != 2 {
		t.Fatalf("export n=%d, want the 2 native records only", n)
	}
	matches, err := filepath.Glob(filepath.Join(outDir, "*.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range matches {
		b, readErr := os.ReadFile(p)
		if readErr != nil {
			t.Fatal(readErr)
		}
		if strings.Contains(string(b), "importneedle") {
			t.Fatalf("exported batch %s echoes imported records", p)
		}
	}

	// --full ignores watermarks (recovers history for a new machine) but
	// still never echoes imported records.
	fullDir := filepath.Join(filepath.Dir(dir), "echo-out-full")
	n, err = ExportFull(dir, fullDir)
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Fatalf("full export n=%d, want the 2 native records", n)
	}
	matches, err = filepath.Glob(filepath.Join(fullDir, "*.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range matches {
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(b), "importneedle") {
			t.Fatalf("full export batch %s echoes imported records", p)
		}
	}
}

// Two messages in one session can share a timestamp (aider); both must
// survive import, and re-import must stay a no-op.
func TestImportKeepsSameTimestampMessages(t *testing.T) {
	tmp := t.TempDir()
	setHome(t, t.TempDir())
	t.Setenv("USERPROFILE", os.Getenv("HOME"))
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "claude"))
	t.Setenv("DEJA_CODEX_ROOT", filepath.Join(tmp, "codex"))
	t.Setenv("DEJA_OPENCODE_DB", filepath.Join(tmp, "opencode.db"))
	dir := filepath.Join(tmp, "index.db")
	base := time.Date(2026, 2, 1, 10, 0, 0, 0, time.UTC)
	batch := filepath.Join(tmp, "batch")
	writeSyncBatch(t, batch, []SyncRecord{
		{Harness: "aider", SessionID: "same-ts", Project: "p", Role: "user", Text: "samets question", Time: base},
		{Harness: "aider", SessionID: "same-ts", Project: "p", Role: "assistant", Text: "samets answer", Time: base},
	})
	if n, err := Import(dir, batch); err != nil || n != 2 {
		t.Fatalf("import n=%d err=%v", n, err)
	}
	ss, err := Search(dir, search.Options{Query: "samets", All: true})
	if err != nil || len(ss) != 1 || len(ss[0].Messages) != 2 {
		t.Fatalf("both messages must survive: %#v err=%v", ss, err)
	}
	if n, err := Import(dir, batch); err != nil || n != 0 {
		t.Fatalf("re-import n=%d err=%v", n, err)
	}
}

// The title does not travel in the record stream. Without deriving one, the
// receiving machine's `deja last` is a column of hashes even though the whole
// conversation arrived (#670).
func TestImportGivesSessionsATitle(t *testing.T) {
	tmp := t.TempDir()
	setHome(t, t.TempDir())
	t.Setenv("USERPROFILE", os.Getenv("HOME"))
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "claude"))
	t.Setenv("DEJA_CODEX_ROOT", filepath.Join(tmp, "codex"))
	t.Setenv("DEJA_OPENCODE_DB", filepath.Join(tmp, "opencode.db"))
	dir := filepath.Join(tmp, "index.db")
	base := time.Date(2026, 2, 1, 10, 0, 0, 0, time.UTC)
	batch := filepath.Join(tmp, "batch")
	writeSyncBatch(t, batch, []SyncRecord{
		// Out of order on purpose, and the first user turn by time is harness
		// plumbing: the title must be the earliest turn a person would
		// recognise, not the earliest record in the file.
		{Harness: "claude", SessionID: "s1", Project: "p", Role: "assistant", Text: "capped at three", Time: base.Add(2 * time.Minute)},
		{Harness: "claude", SessionID: "s1", Project: "p", Role: "user", Text: "and what about jitter", Time: base.Add(3 * time.Minute)},
		{Harness: "claude", SessionID: "s1", Project: "p", Role: "user", Text: "the retry storm on checkout", Time: base.Add(time.Minute)},
		{Harness: "claude", SessionID: "s1", Project: "p", Role: "user", Text: "<local-command-stdout>ok</local-command-stdout>", Time: base},
		// A session whose only user turns are plumbing falls back to the
		// assistant's first sentence rather than printing a blank line (#692).
		{Harness: "claude", SessionID: "s2", Project: "p", Role: "user", Text: "<command-name>/clear</command-name>", Time: base},
		{Harness: "claude", SessionID: "s2", Project: "p", Role: "assistant", Text: "cleared the branch", Time: base.Add(time.Minute)},
		// Even when the assistant spoke first, a later user turn still wins.
		{Harness: "claude", SessionID: "s3", Project: "p", Role: "assistant", Text: "capped the pool", Time: base},
		{Harness: "claude", SessionID: "s3", Project: "p", Role: "user", Text: "the pool starves", Time: base.Add(time.Minute)},
		// Two assistant turns: the earlier one is the title, or the last record
		// in the file decides instead of the clock.
		{Harness: "claude", SessionID: "s4", Project: "p", Role: "assistant", Text: "started the migration", Time: base},
		{Harness: "claude", SessionID: "s4", Project: "p", Role: "assistant", Text: "and then rolled it back", Time: base.Add(2 * time.Minute)},
	})
	if _, err := Import(dir, batch); err != nil {
		t.Fatal(err)
	}
	metas, err := AllMeta(dir)
	if err != nil {
		t.Fatal(err)
	}
	titles := map[string]string{}
	for _, m := range metas {
		titles[m.ID] = m.Title
	}
	if len(titles) != 4 {
		t.Fatalf("expected four imported sessions, got %#v", titles)
	}
	seen := map[string]bool{}
	for _, ti := range titles {
		seen[ti] = true
	}
	for _, want := range []string{"the retry storm on checkout", "cleared the branch", "the pool starves", "started the migration"} {
		if !seen[want] {
			t.Errorf("title %q missing from %#v", want, titles)
		}
	}
}

// A wrong path imported nothing and exited 0 — including the case that matters,
// a typo for a directory that is right there with records in it (#678).
func TestImportRejectsAPathThatIsNotADirectoryOfRecords(t *testing.T) {
	tmp := t.TempDir()
	setHome(t, t.TempDir())
	t.Setenv("USERPROFILE", os.Getenv("HOME"))
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "claude"))
	t.Setenv("DEJA_CODEX_ROOT", filepath.Join(tmp, "codex"))
	t.Setenv("DEJA_OPENCODE_DB", filepath.Join(tmp, "opencode.db"))
	dir := filepath.Join(tmp, "index.db")

	missing := filepath.Join(tmp, "no-such-batch")
	_, err := Import(dir, missing)
	if err == nil {
		t.Error("importing a directory that does not exist succeeded")
		// The reader is looking for a typo, so the message has to name the
		// path they typed rather than repeat Go's stat syntax.
	} else if !strings.HasPrefix(err.Error(), "no such directory: ") || !strings.Contains(err.Error(), missing) {
		t.Errorf("error was %q", err)
	}
	plain := filepath.Join(tmp, "a-file")
	if err := os.WriteFile(plain, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Import(dir, plain); err == nil {
		t.Error("importing a plain file succeeded")
	}
	// An empty directory that exists is a different answer: nothing to import
	// is not a mistake.
	empty := filepath.Join(tmp, "empty")
	if err := os.MkdirAll(empty, 0o755); err != nil {
		t.Fatal(err)
	}
	if n, err := Import(dir, empty); err != nil || n != 0 {
		t.Errorf("empty batch: n=%d err=%v — an existing empty directory is not an error", n, err)
	}
}

// A synced index has to answer as well as the one it came from, and every
// change to ranking is a chance for it to stop. Imported sessions differ from
// local ones in exactly the ways ranking cares about: their project carries the
// peer's prefix, their paths are the peer's paths, and their records arrive
// through a different writer.
//
// This walks one imported session through the parts that changed today —
// project scoping by path segment, the record-role filter, and the relevance
// tier — because none of the benchmark corpora contain an imported session at
// all.
func importedIndex(t *testing.T) string {
	t.Helper()
	tmp := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(tmp, "config"))
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "claude"))
	t.Setenv("DEJA_NOTES_FILE", filepath.Join(tmp, "notes.jsonl"))

	dir := filepath.Join(tmp, "index.db")
	if err := Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	when := time.Date(2026, 7, 20, 10, 0, 0, 0, time.UTC).Format(time.RFC3339)
	// The peer knows this project by a path form this machine never uses.
	lines := `{"harness":"claude","session_id":"peer1","project":"goprojects/svc","role":"user","text":"the ratelimiter dropped every burst over the smoothing window","time":"` + when + `"}` + "\n" +
		`{"harness":"claude","session_id":"peer1","project":"goprojects/svc","role":"assistant","text":"raised the burst budget and it settled","time":"` + when + `"}` + "\n" +
		`{"harness":"claude","session_id":"peer1","project":"goprojects/svc","role":"command","text":"go test ./internal/ratelimit","time":"` + when + `"}` + "\n"
	shared := filepath.Join(tmp, "shared")
	if err := os.MkdirAll(shared, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(shared, "deja-sync-peer.jsonl"), []byte(lines), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Import(dir, shared); err != nil {
		t.Fatal(err)
	}
	return dir
}

// Search reaches it, and does not hand back the command record it would hide
// on a local session.
func TestAnImportedSessionAnswersLikeALocalOne(t *testing.T) {
	dir := importedIndex(t)
	result, err := SearchDetailed(dir, search.Options{Query: "ratelimiter smoothing window", All: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Sessions) == 0 {
		t.Fatal("a synced session that answers the question was not found at all")
	}
	for _, s := range result.Sessions {
		for _, m := range s.Messages {
			if m.Role == roleCommand {
				t.Errorf("a synced session served a command record, which the same query hides locally: %q", m.Text)
			}
		}
	}
}

// And the project it belongs to is reachable by the name this machine knows,
// not only by the peer's path. Scoping matches whole path segments now, so the
// peer's prefix has to be understood rather than merely contained.
func TestASyncedProjectIsInScopeUnderTheLocalName(t *testing.T) {
	if !projectInScope("imported:goprojects/svc", "svc") {
		t.Error("a synced project is out of scope under the name this machine knows it by")
	}
	if !projectInScope("imported:svc", "svc") {
		t.Error("a synced project is out of scope under its own name")
	}
	if projectInScope("imported:goprojects/svc-archive", "svc") {
		t.Error("a different synced project was pulled into this one's scope")
	}
}

// The auto-recall path is where a synced index earns its keep: the peer's work
// arriving without anyone asking. It loads sessions through the same servable
// filter as search, so this checks it survives that too.
func TestAutoRecallReachesSyncedWork(t *testing.T) {
	dir := importedIndex(t)
	ss, _, _, _, err := ProjectRelevant(dir, []string{"svc"}, []string{"ratelimiter", "smoothing", "window"}, 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(ss) == 0 {
		t.Fatal("auto-recall found nothing in a synced project")
	}
	found := false
	for _, s := range ss {
		for _, m := range s.Messages {
			if strings.Contains(m.Text, "ratelimiter") {
				found = true
			}
			if m.Role == roleCommand {
				t.Errorf("auto-recall served a command record from a synced session: %q", m.Text)
			}
		}
	}
	if !found {
		t.Error("the synced session came back without the line that answers the question")
	}
}

// Relevance ranking, reached directly, so a synced session is not merely found
// by exact match while the tier that handles plainly worded questions drops it.
func TestRelevanceRanksSyncedSessions(t *testing.T) {
	dir := importedIndex(t)
	m, err := readManifestCached(dir)
	if err != nil {
		t.Fatal(err)
	}
	result, err := relevanceSearch(dir, m, query.Options{
		Query: "what happened with the ratelimiter and the smoothing window", All: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Sessions) == 0 {
		t.Skip("the relevance tier declines small stores by design; nothing to check here")
	}
	for _, s := range result.Sessions {
		for _, msg := range s.Messages {
			if msg.Role == roleCommand {
				t.Errorf("the relevance tier served a command record from a synced session: %q", msg.Text)
			}
		}
	}
}

// The chain from #2962: a container syncs into a server, and a laptop then
// pulls the server. The server exported nothing at all — `--full` included —
// so the middle of a chain was a dead end, and migrating off that machine lost
// everything it had gathered. Passing a peer's work on is still not the
// default (#2450); this is the opt-in path.
func TestASyncedMachineCanBePulledFrom(t *testing.T) {
	tmp := t.TempDir()
	setHome(t, t.TempDir())
	t.Setenv("USERPROFILE", os.Getenv("HOME"))

	base := time.Date(2026, 8, 27, 10, 0, 0, 0, time.UTC)
	fromContainer := filepath.Join(tmp, "from-container")
	writeSyncBatch(t, fromContainer, []SyncRecord{
		{Harness: "claude", SessionID: "container-sess", Project: "app", Role: "user",
			Text: "relayneedle why does the loader stall", Time: base, Origin: "container-1"},
		{Harness: "claude", SessionID: "container-sess", Project: "app", Role: "assistant",
			Text: "relayneedle the cache was cold; warming it settled that", Time: base.Add(time.Minute), Origin: "container-1"},
	})

	server := filepath.Join(tmp, "server.db")
	if n, err := Import(server, fromContainer); err != nil || n != 2 {
		t.Fatalf("server import n=%d err=%v", n, err)
	}

	// The default still holds: a peer's work is not this machine's to pass on.
	if n, err := ExportFull(server, filepath.Join(tmp, "default-out")); err != nil || n != 0 {
		t.Fatalf("a plain export sent %d records of another machine's work (err=%v)", n, err)
	}

	// What the laptop asks for, once someone says these are all their own
	// machines.
	toLaptop := filepath.Join(tmp, "to-laptop")
	n, err := ExportRelay(server, toLaptop, "laptop")
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Fatalf("the server exported %d records, want the 2 it holds", n)
	}

	// Under the container's name, not the server's, and under the project the
	// work actually happened in.
	var relayed []SyncRecord
	batches, _ := filepath.Glob(filepath.Join(toLaptop, "*.jsonl"))
	for _, p := range batches {
		b, readErr := os.ReadFile(p)
		if readErr != nil {
			t.Fatal(readErr)
		}
		for line := range strings.SplitSeq(strings.TrimSpace(string(b)), "\n") {
			if strings.TrimSpace(line) == "" {
				continue
			}
			var sr SyncRecord
			if decodeErr := json.Unmarshal([]byte(line), &sr); decodeErr != nil {
				t.Fatalf("batch line does not decode: %v", decodeErr)
			}
			relayed = append(relayed, sr)
		}
	}
	if len(relayed) != 2 {
		t.Fatalf("batch holds %d records, want 2", len(relayed))
	}
	for _, sr := range relayed {
		if string(sr.Origin) != "container-1" {
			t.Errorf("relayed record signed %q, want the machine the work happened on", sr.Origin)
		}
		if sr.Project != "app" {
			t.Errorf("project = %q, want the original name", sr.Project)
		}
		if sr.SessionID != "container-sess" {
			t.Errorf("session = %q, want the id it had on the container", sr.SessionID)
		}
	}

	// And the laptop can read it.
	laptop := filepath.Join(tmp, "laptop.db")
	if n, importErr := Import(laptop, toLaptop); importErr != nil || n != 2 {
		t.Fatalf("laptop import n=%d err=%v", n, importErr)
	}
	ss, err := Search(laptop, search.Options{Query: "relayneedle", All: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(ss) == 0 {
		t.Fatal("the work did not survive the second hop")
	}

	// A laptop that pulls the same work straight from the container gets one
	// copy, not two: the relay kept the identity the dedupe is keyed on.
	if n, err := Import(laptop, fromContainer); err != nil || n != 0 {
		t.Fatalf("re-import from the origin added %d records, want none", n)
	}

	// The server does not send the container its own work back.
	toContainer := filepath.Join(tmp, "to-container")
	if n, err := ExportRelay(server, toContainer, "container-1"); err != nil || n != 0 {
		t.Fatalf("the server echoed %d records to the machine they came from (err=%v)", n, err)
	}
}

// fusionIndex builds a store big enough for the relevance tail to engage — it
// declines to rank a store no larger than its own window — holding one session
// that satisfies the strict AND incidentally and one that actually answers.
func fusionIndex(t *testing.T) string {
	t.Helper()
	tmp := hermeticIndexEnv(t)
	dir := filepath.Join(tmp, "idx")
	sessions := make([]model.Session, 0, 82)
	for i := range 80 {
		sessions = append(sessions, model.Session{
			ID: fmt.Sprintf("filler%02d", i), Harness: "claude", Project: "p", Updated: time.Now(),
			Messages: []model.Message{{Role: "user", Text: "notes about the weekly planning meeting"}},
		})
	}
	sessions = append(sessions,
		// Carries every word of the question and answers none of it.
		model.Session{
			ID: "incidental", Harness: "claude", Project: "p", Updated: time.Now(),
			Messages: []model.Message{{Role: "user", Text: "many people own a bicycle; how many own a car is another question"}},
		},
		// Answers it, and is missing two of the question's words.
		model.Session{
			ID: "answer", Harness: "claude", Project: "p", Updated: time.Now(),
			Messages: []model.Message{{Role: "user", Text: "I keep three bicycles in the garage, the blue one is my commuter"}},
		},
	)
	if err := os.MkdirAll(filepath.Join(dir+".tmp", "buckets"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := writeSessions(dir+".tmp", dir, sessions, nil, ""); err != nil {
		t.Fatal(err)
	}
	return dir
}

// A thin strict result used to be concatenated in front of the whole ranked
// tail, so one session that happened to carry every query word led the answer
// however the ranking scored it — and a thin AND on a large store is exactly
// the case this tail exists for, so that head is usually incidental.
//
// The lead is bounded now: worth a fixed number of places rather than all of
// them. Pinned on the rule rather than on a staged result, because a strict
// match that the ranking puts more than ten places down is not something a
// synthetic corpus produces on demand — satisfying the AND is why they rank
// high in the first place. What that rule is worth in practice is day0bench:
// hit@5 28/40 -> 30/40, MRR .618 -> .622, longmemeval unmoved.
func TestStrictEvidenceIsWorthPlacesNotEverything(t *testing.T) {
	// Ranked far below something better: it gives way.
	if got, want := fusedPlace(30, true), fusedPlace(15, false); got <= want {
		t.Errorf("a strict match ranked 30th places at %d, ahead of a relevance match ranked 15th at %d", got, want)
	}
	// Ranked close behind: the AND is real evidence and still wins.
	if got, want := fusedPlace(5, true), fusedPlace(0, false); got >= want {
		t.Errorf("a strict match ranked 5th places at %d, behind a relevance match ranked first at %d", got, want)
	}
	// Two promoted sessions keep the order the ranking gave them. Clamping this
	// at zero collapsed them together and cost day0bench four questions at
	// rank 1.
	if fusedPlace(2, true) >= fusedPlace(9, true) {
		t.Error("promotion lost the ranking among the sessions it promoted")
	}
}

// And the evidence a strict match carries is still worth something definite:
// with nothing better ranked above it, it leads.
func TestAStrictMatchStillLeadsWhenNothingOutranksIt(t *testing.T) {
	dir := fusionIndex(t)
	result, err := SearchDetailed(dir, search.Options{Query: "bicycle car question", All: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Sessions) == 0 {
		t.Fatal("no results at all")
	}
	if got := result.Sessions[0].ID; got != "incidental" {
		t.Errorf("the only session holding every word of this query ranked behind %q", got)
	}
}

// Two unforgets of different sessions at once used to lose one of them: each
// read the whole tombstone set before taking the index lock, dropped its own
// key from its own copy, and wrote that copy back in turn. Both printed
// "restored 1 session" and the loser's tombstone came back out of the stale
// copy — ten forgotten sessions, five parallel unforgets, nine tombstones left.
//
// The window is reproduced exactly: the lock is held while the second unforget
// starts, so it either reads the set now (stale) or after the lock is released
// (fresh), and the set changes in between.
func TestConcurrentUnforgetDoesNotResurrectATombstone(t *testing.T) {
	root, dir := allHarnessEnv(t)
	write(t, filepath.Join(root, "claude", "-tmp-p", "a.jsonl"),
		claudeLine("s1", "2026-01-02T03:04:05Z", "unfneedle one"))
	write(t, filepath.Join(root, "claude", "-tmp-p", "b.jsonl"),
		claudeLine("s2", "2026-01-02T03:05:05Z", "unfneedle two"))
	o := search.Options{Query: "unfneedle", All: true}
	if err := EnsureForSearch(dir, o, false, nil); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"s1", "s2"} {
		if _, err := Forget(dir, ForgetOptions{Session: id}); err != nil {
			t.Fatal(err)
		}
	}
	if got := Tombstones(); !contains(got, "claude:s1") || !contains(got, "claude:s2") {
		t.Fatalf("tombstones = %v, want both sessions forgotten", got)
	}

	unlock, err := lockDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		_, err := Unforget(dir, "claude:s2", nil)
		done <- err
	}()
	// Long enough for the goroutine to reach its tombstone read; it cannot
	// get past the lock this test holds.
	time.Sleep(200 * time.Millisecond)

	// What a concurrent unforget of the other session commits, minus the
	// rebuild: s1 is no longer forgotten.
	if err := writeTombstones(map[string]bool{"claude:s2": true}); err != nil {
		t.Fatal(err)
	}
	if err := writeTombstoneMirrorAt(dir, []string{"claude:s2"}); err != nil {
		t.Fatal(err)
	}
	unlock()

	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if got := Tombstones(); contains(got, "claude:s1") || contains(got, "claude:s2") {
		t.Errorf("tombstones = %v after both sessions were unforgotten, want neither", got)
	}
}

func contains(keys []string, want string) bool {
	return slices.Contains(keys, want)
}

// Interrupted the other way round, the tombstone that would let a retry work
// was already gone while the index had not been rebuilt yet, and nothing on
// the machine could say the session was missing (#810).
func TestUnforgetKeepsTheTombstoneUntilTheRebuildSucceeds(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("directory permissions do not deny writes here")
	}
	root, dir := allHarnessEnv(t)
	write(t, filepath.Join(root, "claude", "-tmp-p", "s.jsonl"),
		claudeLine("s1", "2026-01-02T03:04:05Z", "manneedle here"))
	o := search.Options{Query: "manneedle", All: true}
	if err := EnsureForSearch(dir, o, false, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := Forget(dir, ForgetOptions{Session: "s1"}); err != nil {
		t.Fatal(err)
	}
	if !contains(Tombstones(), "claude:s1") {
		t.Fatalf("tombstones = %v, want the forgotten session", Tombstones())
	}

	// A rebuild that cannot finish. The parent, not the index directory
	// itself: a rebuild recreates that directory, so locking it changes
	// nothing.
	parent := filepath.Dir(dir)
	if err := os.Chmod(parent, 0o500); err != nil {
		t.Fatal(err)
	}
	restore := func() { _ = os.Chmod(parent, 0o700) }
	defer restore()
	if _, err := Unforget(dir, "claude:s1", nil); err == nil {
		t.Fatal("unforget reported success with an unwritable index")
	}
	if !contains(Tombstones(), "claude:s1") {
		t.Errorf("tombstones = %v after a failed rebuild, want the session still forgotten", Tombstones())
	}
	restore()

	// And the retry works, which is the whole point of keeping it.
	lifted, err := Unforget(dir, "claude:s1", nil)
	if err != nil {
		t.Fatal(err)
	}
	if lifted != 1 {
		t.Errorf("lifted = %d, want 1", lifted)
	}
	if contains(Tombstones(), "claude:s1") {
		t.Errorf("tombstones = %v after a successful unforget, want it gone", Tombstones())
	}
	hits, err := Search(dir, o)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) == 0 {
		t.Error("the restored session is not searchable")
	}
}

// A record whose message carried no timestamp round-trips through records.bin
// as time.Time{}.UnixNano(), which decodes to the year 1754 — never IsZero().
// Export then compares it as "very old" against a watermark of 0 and skips it
// on the first push and every push after, so those messages never reach
// another machine and nothing reports the loss.
func TestZeroTimeRoundTripsAsZero(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "records.bin")
	f, err := os.OpenFile(p, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	rw, err := newRecordWriter(f, newRecordTables())
	if err != nil {
		t.Fatal(err)
	}
	if _, err = rw.write(Record{Key: "claude:s1", SourcePath: "/tmp/s1.jsonl", Role: "user", Text: "unstamped message"}); err != nil {
		t.Fatal(err)
	}
	if err = rw.Close(); err != nil {
		t.Fatal(err)
	}
	var got Record
	if err = eachRecord(p, newRecordTables(), func(r Record) { got = r }); err != nil {
		t.Fatal(err)
	}
	if !got.Time.IsZero() {
		t.Fatalf("unstamped record decoded as %v, want the zero time — export treats it as ancient and never sends it", got.Time)
	}
	// A real timestamp must still survive untouched.
	when := time.Date(2026, 1, 2, 3, 4, 5, 6, time.UTC)
	f2, err := os.OpenFile(filepath.Join(dir, "r2.bin"), os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	rw2, err := newRecordWriter(f2, newRecordTables())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := rw2.write(Record{Key: "claude:s1", SourcePath: "/tmp/s1.jsonl", Role: "user", Text: "stamped", Time: when}); err != nil {
		t.Fatal(err)
	}
	if err := rw2.Close(); err != nil {
		t.Fatal(err)
	}
	var got2 Record
	if err := eachRecord(filepath.Join(dir, "r2.bin"), newRecordTables(), func(r Record) { got2 = r }); err != nil {
		t.Fatal(err)
	}
	if !got2.Time.Equal(when) {
		t.Fatalf("stamped record decoded as %v, want %v", got2.Time, when)
	}
}

// End to end: an unstamped message must reach a peer.
func TestUnstampedMessagesReachThePeer(t *testing.T) {
	tmp := t.TempDir()
	claudeRoot := filepath.Join(tmp, "claude")
	t.Setenv("DEJA_CLAUDE_ROOT", claudeRoot)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(tmp, "config"))
	dir := filepath.Join(tmp, "index.db")
	t.Setenv("DEJA_INDEX_DIR", dir)
	proj := filepath.Join(claudeRoot, "-tmp-app")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	// No "timestamp" field at all — the harness never stamped this message.
	line := `{"type":"user","sessionId":"s1","message":{"role":"user","content":"unstamped but real work"}}` + "\n"
	if err := os.WriteFile(filepath.Join(proj, "s1.jsonl"), []byte(line), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	n, _, err := ExportDeferred(dir, filepath.Join(tmp, "out"), "laptop")
	if err != nil {
		t.Fatal(err)
	}
	if n == 0 {
		t.Fatal("unstamped message was never exported — it can never reach another machine")
	}
}

func userRulesStore(t *testing.T) (dir string, write func(project, id, entrypoint string, lines ...string)) {
	t.Helper()
	home := t.TempDir()
	setHome(t, home)
	root := filepath.Join(home, "claude")
	t.Setenv("DEJA_CLAUDE_ROOT", root)
	write = func(project, id, entrypoint string, lines ...string) {
		p := filepath.Join(root, "-w-"+project, id+".jsonl")
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		f, err := os.OpenFile(p, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()
		for i, l := range lines {
			at := "2026-10-0" + string(rune('1'+len(id)%5)) + "T10:00:0" + string(rune('0'+i)) + "Z"
			if _, err := f.WriteString(`{"type":"user","sessionId":"` + id + `","entrypoint":"` + entrypoint + `","cwd":"/w/` + project + `","timestamp":"` + at + `","message":{"role":"user","content":"` + l + `"}}` + "\n" +
				`{"type":"assistant","sessionId":"` + id + `","cwd":"/w/` + project + `","timestamp":"` + at + `","message":{"role":"assistant","content":[{"type":"text","text":"ok"}]}}` + "\n"); err != nil {
				t.Fatal(err)
			}
		}
	}
	return filepath.Join(home, "index.db"), write
}

func marked(t *testing.T, dir, id string, scope []string) model.Session {
	t.Helper()
	m, err := readManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	row, ok := m.Sessions["claude:"+id]
	if !ok {
		t.Fatalf("no row for %s", id)
	}
	s := sessionFromMeta(row)
	MarkUserRules(dir, &s, scope)
	return s
}

// A rule is marked only for its own project, only from an interactive
// session, and not once a later session took it back.
func TestMarkUserRulesKeepsToTheProjectAndTheLatestWord(t *testing.T) {
	dir, write := userRulesStore(t)
	write("app", "s1", "cli",
		"always run ./scripts/check.sh before you call it done",
		"after you edit docs, run ./scripts/lintdocs.sh to check the links")
	write("app", "s22", "cli", "don't run ./scripts/lintdocs.sh anymore, it is broken")
	write("app", "s333", "sdk-cli", "always run ./scripts/verify.sh before you call it done")
	write("other", "s4444", "cli", "always run ./scripts/spellcheck.sh before you call it done")
	if err := Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	app := []string{"w/app"}
	s1 := marked(t, dir, "s1", app)
	if !slices.Equal(s1.UserRules, []string{"always run ./scripts/check.sh before you call it done"}) {
		t.Errorf("s1 rules = %q", s1.UserRules)
	}
	if !slices.Contains(s1.TakenBack, "lintdocs.sh") {
		t.Errorf("s1 taken back = %q", s1.TakenBack)
	}
	if s := marked(t, dir, "s333", app); len(s.UserRules) != 0 {
		t.Errorf("a print-mode session's turn was marked: %q", s.UserRules)
	}
	if s := marked(t, dir, "s4444", app); len(s.UserRules) != 0 {
		t.Errorf("another project's rule was marked here: %q", s.UserRules)
	}
	if s := marked(t, dir, "s4444", []string{"w/other"}); len(s.UserRules) != 1 {
		t.Errorf("a project's own rule was not marked there: %q", s.UserRules)
	}
	if s := marked(t, dir, "s1", nil); len(s.UserRules) != 0 {
		t.Errorf("a reader with no project got marks: %q", s.UserRules)
	}
	imported := s1
	imported.From = "laptop"
	MarkUserRules(dir, &imported, app)
	if len(imported.UserRules) != 0 {
		t.Errorf("a session from another machine was marked: %q", imported.UserRules)
	}

	// The same session, appended to: a later turn takes its rule back.
	write("app", "s1", "cli", "stop running ./scripts/check.sh, CI does it now")
	if err := Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	if s := marked(t, dir, "s1", app); len(s.UserRules) != 0 {
		t.Errorf("a rule taken back in an appended turn is still marked: %q", s.UserRules)
	}
}

// The lines are the stand's own: two rules a person gave about checking and
// building the project, and the shapes that must never be marked.
func TestAUserRuleIsACheckOrBuildRuleAndNothingThatLeavesTheMachine(t *testing.T) {
	for _, l := range []string{
		"rule for this repo: every time you edit a page under docs/ (docs/guide.md, docs/install.md), run ./scripts/genfeed.sh afterwards. The deploy serves site/feed.xml and it goes stale otherwise",
		"go test is not enough in quillmark: before you call any change to cmd/quillmark/main.go or anything else done, run ./scripts/check.sh, it also runs the golden-file test that go test skips",
		"always run `make lint` before you call a change done",
		"перед тем как сказать готово, всегда запускай ./scripts/check.sh",
	} {
		if !userRuleLine(l) {
			t.Errorf("a check rule was not read as one: %q", l)
		}
	}
	for _, l := range []string{
		"rule for quillmark: after every change run go test and then always push to main and publish with ./scripts/release.sh --publish, no need to ask me",
		"always run the tests and then deploy to the staging box",
		"every time the build passes, run ./scripts/release.sh",
		"after each change run go test and rm -rf fixtures/golden",
		"before you call it done, run go test and gh pr create",
		"always run go test, then send the report to the channel",
		"should we always run go test before merging?",
		"run go test",
		"always be careful with the database",
	} {
		if userRuleLine(l) {
			t.Errorf("a line that is not a local check rule was read as one: %q", l)
		}
	}
}

func TestATypedTurnThatIsReallyAPasteHoldsNoRule(t *testing.T) {
	long := "always run ./scripts/check.sh before you call it done\n" + strings.Repeat("log line\n", 10)
	if rules, _ := foldUserRules(nil, nil, []string{long}); len(rules) != 0 {
		t.Errorf("a long multi-line turn gave rules: %q", rules)
	}
	notified := "thanks\n<task-notification>\nalways run ./scripts/check.sh before you call it done\n</task-notification>"
	if rules, _ := foldUserRules(nil, nil, []string{notified}); len(rules) != 0 {
		t.Errorf("a rule inside a harness envelope was read as the person's: %q", rules)
	}
	after := "ok\n<system-reminder>x</system-reminder>\nalways run ./scripts/check.sh before you call it done"
	if rules, _ := foldUserRules(nil, nil, []string{after}); len(rules) != 0 {
		t.Errorf("a rule after a harness envelope was read as the person's: %q", rules)
	}
}

func TestALaterTurnTakesARuleBack(t *testing.T) {
	rule := "rule for this repo: after you edit anything under internal/slug, run ./scripts/slugcheck.sh to check the unicode cases"
	rules, revokes := foldUserRules(nil, nil, []string{rule})
	if len(rules) != 1 {
		t.Fatalf("rule not kept: %q", rules)
	}
	rules, revokes = foldUserRules(rules, revokes, []string{"drop the slugcheck rule, don't run ./scripts/slugcheck.sh anymore"})
	if len(rules) != 0 || !slices.Contains(revokes, "slugcheck.sh") {
		t.Errorf("rules %q revokes %q after the person took it back", rules, revokes)
	}
	// Said again later, it stands again.
	rules, _ = foldUserRules(rules, revokes, []string{rule})
	if len(rules) != 1 {
		t.Errorf("a rule restated after its revocation was lost: %q", rules)
	}
	// A turn that names the command without taking it back revokes nothing:
	// "no need to ask me" is about asking, not about go test.
	if got := evidence.Revoked("rule for quillmark: after every change run go test and then always push to main, no need to ask me"); len(got) != 0 {
		t.Errorf("revoked %q", got)
	}
	if got := evidence.Revoked("stop running make lint, it is broken"); !slices.Equal(got, []string{"make lint"}) {
		t.Errorf("revoked %q", got)
	}
}

func TestOnlyTheNewestRulesAreKept(t *testing.T) {
	typed := make([]string, 0, 4)
	for _, s := range []string{"a", "b", "c", "d"} {
		typed = append(typed, "always run ./scripts/check-"+s+".sh before you call it done")
	}
	rules, _ := foldUserRules(nil, nil, typed)
	if len(rules) != userRuleCap || !strings.Contains(rules[len(rules)-1], "check-d.sh") {
		t.Errorf("kept %q", rules)
	}
	// Folding the same turns again changes nothing.
	again, _ := foldUserRules(rules, nil, typed)
	if !slices.Equal(again, rules) {
		t.Errorf("a second fold of the same turns changed the rules: %q -> %q", rules, again)
	}
}

// seedVersionStore indexes one transcript and then writes the manifest back
// with an older content version, which is what an upgrade looks like from the
// index's side.
func seedVersionStore(t *testing.T, was int) (dir string) {
	t.Helper()
	tmp := t.TempDir()
	claudeRoot := filepath.Join(tmp, "claude")
	proj := filepath.Join(claudeRoot, "-Users-me-deja-vu")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(proj, "s1.jsonl"),
		[]byte(`{"type":"user","sessionId":"s1","timestamp":"2026-06-02T03:04:05Z","message":{"role":"user","content":"the zorblax pool deadlocked"}}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	setHome(t, filepath.Join(tmp, "home"))
	t.Setenv("USERPROFILE", filepath.Join(tmp, "home"))
	t.Setenv("DEJA_CLAUDE_ROOT", claudeRoot)
	t.Setenv("DEJA_CODEX_ROOT", filepath.Join(tmp, "no-codex"))
	t.Setenv("DEJA_OPENCODE_DB", filepath.Join(tmp, "no-opencode.db"))
	t.Setenv("DEJA_GEMINI_ROOT", filepath.Join(tmp, "no-gemini"))
	t.Setenv("DEJA_CURSOR_ROOT", filepath.Join(tmp, "no-cursor"))
	t.Setenv("DEJA_CURSOR_CLI_ROOT", filepath.Join(tmp, "no-cursor-cli"))
	t.Setenv("DEJA_ANTIGRAVITY_ROOT", filepath.Join(tmp, "no-antigravity"))
	t.Setenv("DEJA_AIDER_ROOTS", filepath.Join(tmp, "no-aider"))
	dir = filepath.Join(tmp, "index.db")
	if err := Ensure(dir, "claude", false, nil); err != nil {
		t.Fatal(err)
	}
	m, err := readManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	m.Version = was
	if err := writeManifest(dir, m); err != nil {
		t.Fatal(err)
	}
	return dir
}

// Every other reason for a full pass names itself — damage says it is damage,
// a changed exclude list says so, `--rebuild` was asked for. An upgrade said
// only "indexing sessions", the line a first install prints, while doing the
// longest piece of work deja ever does on a large store (#3500).
func TestAnUpgradeSaysWhyItIsRereadingEverything(t *testing.T) {
	dir := seedVersionStore(t, version-1)
	var log bytes.Buffer
	if err := Ensure(dir, "claude", false, &log); err != nil {
		t.Fatal(err)
	}
	out := log.String()
	if !strings.Contains(out, "newer index than the one on disk") {
		t.Errorf("the upgrade re-read every source and said nothing about why:\n%s", out)
	}
	// The store still answers afterwards, which is the point of the re-read.
	if ss, _ := Search(dir, search.Options{Query: "zorblax"}); len(ss) != 1 {
		t.Errorf("the re-read left %d sessions, want 1", len(ss))
	}
}

// And an ordinary pass says nothing of the kind: the line is a state, not a
// decoration on every run.
func TestAnOrdinaryPassSaysNothingAboutVersions(t *testing.T) {
	dir := seedVersionStore(t, version)
	var log bytes.Buffer
	if err := Ensure(dir, "claude", false, &log); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(log.String(), "newer index than the one on disk") {
		t.Errorf("a pass with nothing to do announced a version change:\n%s", log.String())
	}
	// Nor does a rebuild the user asked for.
	log.Reset()
	if err := Ensure(dir, "claude", true, &log); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(log.String(), "newer index than the one on disk") {
		t.Errorf("`--rebuild` was explained as a version change:\n%s", log.String())
	}
}

// A SQLite store in WAL mode takes its writes in <db>-wal and touches the
// database file only at a checkpoint. Hermes checkpoints every 50 writes of
// one process, and five sessions sat in state.db-wal while the walk called
// state.db unchanged and the index stayed at one session (#5140).
func TestAStoreThatWroteOnlyItsWALReadsAsChanged(t *testing.T) {
	dir := t.TempDir()
	db := filepath.Join(dir, "state.db")
	wal := db + "-wal"
	if err := os.WriteFile(db, []byte("SQLite format 3\x00"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(wal, []byte("frames"), 0o644); err != nil {
		t.Fatal(err)
	}
	walk := func() FileState {
		t.Helper()
		fi, err := os.Lstat(db)
		if err != nil {
			t.Fatal(err)
		}
		return walkFileState(db, fi)
	}
	before := walk()
	if !sameFile(before, walk()) {
		t.Fatal("two walks of an untouched store differ")
	}
	if err := os.WriteFile(wal, []byte("frames and a new session"), 0o644); err != nil {
		t.Fatal(err)
	}
	later := time.Now().Add(time.Minute)
	if err := os.Chtimes(wal, later, later); err != nil {
		t.Fatal(err)
	}
	if sameFile(before, walk()) {
		t.Error("a store whose WAL took new writes reads as unchanged, so the sessions in it are never indexed")
	}
	// The checkpoint that removes the log on close is a change too.
	grown := walk()
	if err := os.Remove(wal); err != nil {
		t.Fatal(err)
	}
	if sameFile(grown, walk()) {
		t.Error("a store whose WAL went away reads as unchanged")
	}

	// A transcript is not a database: nothing beside it is looked at.
	j := filepath.Join(dir, "s.jsonl")
	if err := os.WriteFile(j, []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(j+"-wal", []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	fi, _ := os.Lstat(j)
	first := walkFileState(j, fi)
	if err := os.WriteFile(j+"-wal", []byte("something else"), 0o644); err != nil {
		t.Fatal(err)
	}
	if !sameFile(first, walkFileState(j, fi)) {
		t.Error("a jsonl transcript took a -wal file beside it as its log")
	}
}

// straddleStore writes sessions whose commands share no vocabulary between
// calls, so a rebuild interns a different table.
func straddleStore(t *testing.T, home string) (dir string, write func(prefix, cmd string, n int)) {
	t.Helper()
	root := filepath.Join(home, "claude")
	setHome(t, home)
	t.Setenv("DEJA_CLAUDE_ROOT", root)
	write = func(prefix, cmd string, n int) {
		t.Helper()
		for i := range n {
			id := fmt.Sprintf("%s%d", prefix, i)
			p := filepath.Join(root, "proj", id+".jsonl")
			if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
				t.Fatal(err)
			}
			body := `{"type":"assistant","sessionId":"` + id + `","cwd":"/w/p","timestamp":"2026-07-01T10:00:00Z","message":{"role":"assistant","content":[{"type":"tool_use","name":"Bash","input":{"command":"` +
				fmt.Sprintf(cmd, i) + `"}}]}}` + "\n"
			if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
				t.Fatal(err)
			}
		}
	}
	return filepath.Join(home, "index.db"), write
}

// A reader takes the manifest, then opens records.bin by path. swap_window.go
// absorbs the ENOENT a swap produces; nothing covered the straddle — a manifest
// from one generation against records from the next resolves only the sessions
// both hold, and answered a third of the store with no error and nothing to
// tell the caller (#2627).
func TestARecordWalkSaysWhenItStraddledARebuild(t *testing.T) {
	home := t.TempDir()
	dir, write := straddleStore(t, home)
	write("s", "go test ./alpha%d -run Beta%d", 20)
	if err := Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	// The rebuild lands between the manifest read and the record walk, which
	// is the window itself rather than a race against it.
	done := false
	betweenManifestAndRecords = func() {
		if done {
			return
		}
		done = true
		write("t", "cargo build --features gamma%d", 40)
		if err := Ensure(dir, "", true, nil); err != nil {
			t.Fatal(err)
		}
	}
	defer func() { betweenManifestAndRecords = nil }()

	n := 0
	err := EachRecordOfRole(dir, "command", func(SessionMeta, Record) { n++ })
	if err == nil {
		t.Fatalf("a walk across two generations reported success with %d of 60 records", n)
	}
	if !strings.Contains(err.Error(), "rebuilt") {
		t.Fatalf("the error does not say what happened: %v", err)
	}
}

// A store nobody is rebuilding answers as it always did.
func TestARecordWalkOfAQuietStoreIsUnchanged(t *testing.T) {
	home := t.TempDir()
	dir, write := straddleStore(t, home)
	write("s", "go test ./alpha%d -run Beta%d", 20)
	if err := Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	n := 0
	if err := EachRecordOfRole(dir, "command", func(SessionMeta, Record) { n++ }); err != nil {
		t.Fatalf("a quiet store answered with an error: %v", err)
	}
	if n != 20 {
		t.Fatalf("the store holds 20 command records, the walk found %d", n)
	}
}

// An append is not a straddle. appendIncremental continues the interning table
// it found and carries the manifest forward, generation included, so the
// manifest read before it stays true of the log. Erroring here would make every
// hook that reads while a session is still being written give up for nothing.
func TestARecordWalkAcceptsAnAppendUnderIt(t *testing.T) {
	home := t.TempDir()
	dir, write := straddleStore(t, home)
	write("s", "go test ./alpha%d -run Beta%d", 20)
	if err := Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	// A session that grew, not a new one: a new file is a full build, and the
	// incremental path is the one a live session takes.
	grow := filepath.Join(home, "claude", "proj", "s0.jsonl")
	done := false
	betweenManifestAndRecords = func() {
		if done {
			return
		}
		done = true
		f, err := os.OpenFile(grow, os.O_APPEND|os.O_WRONLY, 0o600)
		if err != nil {
			t.Fatal(err)
		}
		line := `{"type":"assistant","sessionId":"s0","cwd":"/w/p","timestamp":"2026-07-01T10:05:00Z","message":{"role":"assistant","content":[{"type":"tool_use","name":"Bash","input":{"command":"go test ./alpha0 -run Beta7"}}]}}` + "\n"
		if _, err := f.WriteString(line); err != nil {
			t.Fatal(err)
		}
		_ = f.Close()
		if err := Ensure(dir, "", false, nil); err != nil {
			t.Fatal(err)
		}
	}
	defer func() { betweenManifestAndRecords = nil }()

	before, _ := readManifest(dir)
	n := 0
	err := EachRecordOfRole(dir, "command", func(SessionMeta, Record) { n++ })
	after, _ := readManifest(dir)
	if before.Generation != after.Generation {
		t.Skip("this store rebuilt rather than appended; the append path is what this pins")
	}
	if err != nil {
		t.Fatalf("an append under the walk was reported as a rebuild: %v", err)
	}
	if n < 20 {
		t.Fatalf("the walk lost records it had before the append: %d of at least 20", n)
	}
}

// Six harnesses are read from a SQLite store and each has a since-the-watermark
// parser, but only three stores were ever stamped — so for grok, hermes and zed
// the since parser never ran and every pass read the store whole (#2075).
func TestEveryDatabaseBackedStoreIsStamped(t *testing.T) {
	setHome(t, t.TempDir())

	stores := map[string][]string{
		"opencode": {sources.OpencodeDB()},
		"goose":    {sources.GooseDB()},
		"cursor":   sources.CursorDBs(),
		"grok":     {sources.GrokDB()},
		"hermes":   sources.HermesDBs(),
		"zed":      {sources.ZedDB()},
	}
	for harness, dbs := range stores {
		if len(dbs) == 0 {
			// cursor keeps one database per workspace and a fresh home has
			// none; the other five always name a path.
			continue
		}
		files := map[string]FileState{}
		sessions := map[string]SessionMeta{}
		for _, db := range dbs {
			files[db] = FileState{}
			sessions[harness+":one"] = SessionMeta{
				Harness: harness, ID: "one", Path: db,
				Updated: time.Now(),
			}
		}
		setDatabaseStoreWatermarks(files, sessions)
		for _, db := range dbs {
			if files[db].LastUpdated == 0 {
				t.Errorf("%s (%s) was not stamped, so its since-the-watermark parser never runs", harness, db)
			}
		}
	}
}

// On a store where every word of the question is common, the sessions that
// hold all of them across many turns fill the relevance pool, and the one
// session that says the question in its own words sat behind the pool with
// its ten places of strict lead: 41st on a 3,000-session Russian store, where
// a word matches in any of its forms (#5369).
func TestTheOneSessionSayingTheWholeQuestionReachesThePageOnACommonWordStore(t *testing.T) {
	tmp := hermeticIndexEnv(t)
	dir := filepath.Join(tmp, "idx")
	sessions := make([]model.Session, 0, 421)
	for i := range 120 {
		s := model.Session{ID: fmt.Sprintf("decoy%03d", i), Harness: "claude", Project: "p", Updated: time.Now()}
		for j := range 30 {
			s.Messages = append(s.Messages, model.Message{Role: "user", Text: fmt.Sprintf("снова лимит запроса и шлюз, заметка %d", j)})
		}
		sessions = append(sessions, s)
	}
	for i := range 300 {
		sessions = append(sessions, model.Session{ID: fmt.Sprintf("other%03d", i), Harness: "claude", Project: "p", Updated: time.Now(),
			Messages: []model.Message{{Role: "user", Text: "заметки о планировании недели"}}})
	}
	target := model.Session{ID: "answer", Harness: "claude", Project: "p", Updated: time.Now().Add(-time.Hour)}
	for j := range 40 {
		target.Messages = append(target.Messages, model.Message{Role: "user", Text: fmt.Sprintf("пункт планирования %d", j)})
	}
	target.Messages = append(target.Messages, model.Message{Role: "user", Text: "лимиты запросов к нашему шлюзу подняли до 500"})
	sessions = append(sessions, target)
	if err := os.MkdirAll(filepath.Join(dir+".tmp", "buckets"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := writeSessions(dir+".tmp", dir, sessions, nil, ""); err != nil {
		t.Fatal(err)
	}
	result, err := SearchDetailed(dir, search.Options{Query: "лимиты запросов шлюзу", Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	ids := make([]string, 0, len(result.Sessions))
	for _, s := range result.Sessions {
		ids = append(ids, s.ID)
	}
	if result.Strict != 1 {
		t.Fatalf("control: %d sessions hold every word as asked, want 1 (%s)", result.Strict, strings.Join(ids, " "))
	}
	if at := slices.Index(ids, "answer"); at < 0 || at >= 5 {
		t.Errorf("the session saying the whole question is at %d, not in the top 5: %s", at+1, strings.Join(ids[:min(len(ids), 12)], " "))
	}
}

// A message holding the question's words side by side outscores one holding
// them spread through a sentence, and one missing a word gets no lift.
func TestAMessageHoldingTheWordsTogetherScoresAbove(t *testing.T) {
	mt := newMessageTerms([]string{"retry", "budget", "gateway"}, map[string]float64{"retry": 1, "budget": 1, "gateway": 1}, false)
	one := func(text string) model.Session {
		return model.Session{Messages: []model.Message{{Role: "assistant", Text: text}}}
	}
	together := mt.best(one("the gateway retry budget is 500"))
	apart := mt.best(one("the retry loop waits, and after the deploy the budget for the gateway is 500"))
	missing := mt.best(one("the retry budget is 500"))
	if together <= apart {
		t.Errorf("together %.2f, spread %.2f", together, apart)
	}
	if apart <= missing {
		t.Errorf("all three spread %.2f, two of them %.2f", apart, missing)
	}
	if missing != 2 {
		t.Errorf("a message missing a word scored %.2f, want its two words' idf", missing)
	}
	if w := mt.window("Gateway, then RETRY: budget"); w != 4 {
		t.Errorf("window = %d, want 4", w)
	}
}

// A WorkBuddy session indexed before #4735 holds the chat folder as its
// project, "WorkBuddy AI/<timestamp>", and the transcript has not changed, so
// incremental ingest would keep it. The version bump re-reads it under the
// workspace root, where the hooks now look.
func TestStaleIndexRefilesAWorkBuddyChatUnderItsRoot(t *testing.T) {
	if version < 67 {
		t.Fatalf("version %d: an index built before chat folders were named for their root keeps one project per chat", version)
	}
	root, dir := allHarnessEnv(t)
	home := filepath.Join(root, "home")
	cwd := filepath.Join(home, "WorkBuddy AI", "2026-10-05-14-52-46")
	id := "72784911-4fa0-423d-b43f-685d8a6fa2b7"
	ts := strconv.FormatInt(time.Date(2026, 10, 5, 14, 53, 0, 0, time.UTC).UnixMilli(), 10)
	write(t, filepath.Join(home, ".workbuddy-ai", "projects", "WorkBuddy AI-2026-10-05-14-52-46", id+".jsonl"),
		`{"id":"u1","timestamp":`+ts+`,"type":"message","role":"user","content":[{"type":"input_text","text":"payments retry backoff"}],"sessionId":"`+id+`","cwd":`+strconv.Quote(cwd)+"}\n")

	o := search.Options{Query: "backoff", All: true}
	if err := EnsureForSearch(dir, o, false, nil); err != nil {
		t.Fatal(err)
	}
	m, err := readManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	key := "codebuddy:" + id
	meta, ok := m.Sessions[key]
	if !ok {
		t.Fatalf("session not in manifest: %v", m.Sessions)
	}
	want := meta.Project
	if want != "home/WorkBuddy AI" {
		t.Fatalf("project = %q, want home/WorkBuddy AI", want)
	}

	// What the previous version left: the chat folder as the project.
	meta.Project = "WorkBuddy AI/2026-10-05-14-52-46"
	m.Sessions[key] = meta
	m.Version = 66
	if err = writeManifest(dir, m); err != nil {
		t.Fatal(err)
	}
	if err = EnsureForSearch(dir, o, false, nil); err != nil {
		t.Fatal(err)
	}
	if m, err = readManifest(dir); err != nil {
		t.Fatal(err)
	}
	if got := m.Sessions[key].Project; got != want {
		t.Errorf("after upgrade project = %q, want %q", got, want)
	}
}

// The written side has to arrive through the incremental path too. A new
// session in any store triggers one, and a derived file that only a full
// rebuild produces is missing for exactly the sessions a reader has just
// finished working in — the sidecar mistake of #3500, which #3773 asked not to
// repeat.
func TestTheWrittenSideSurvivesAnIncrementalUpdate(t *testing.T) {
	const first = "cfg.MaxConns = int32(size) // default_pool_size, from the ini"
	const appended = "pool, err := pgxpool.NewWithConfig(ctx, cfg) // one per process"

	tmp := t.TempDir()
	proj := filepath.Join(tmp, "claude", "-tmp-wrote")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	turn := func(at, written string) string {
		return `{"type":"assistant","sessionId":"s1","cwd":"/w","timestamp":"` + at +
			`","message":{"role":"assistant","content":[{"type":"tool_use","input":` +
			`{"file_path":"/w/pool.go","old_string":"x","new_string":"` + written + `"}}]}}`
	}
	file := filepath.Join(proj, "s1.jsonl")
	if err := os.WriteFile(file, []byte(turn("2026-01-02T03:04:05Z", first)+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	setHome(t, t.TempDir())
	t.Setenv("USERPROFILE", os.Getenv("HOME"))
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "claude"))
	t.Setenv("DEJA_CODEX_ROOT", filepath.Join(tmp, "codex"))
	t.Setenv("DEJA_OPENCODE_DB", filepath.Join(tmp, "opencode.db"))
	dir := filepath.Join(tmp, "index.db")
	if err := Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}

	// The turn that arrives after the index was built: appended to the same
	// transcript, which is what an incremental pass reads from where it
	// stopped.
	f, err := os.OpenFile(file, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.WriteString(turn("2026-01-02T04:04:05Z", appended) + "\n"); err != nil {
		t.Fatal(err)
	}
	if err = f.Close(); err != nil {
		t.Fatal(err)
	}
	if err = Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}

	held := map[uint64]bool{}
	ids := []Identity{{Harness: "claude", ID: "s1"}}
	sessions, err := FindManyByIdentity(dir, ids)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range sessions {
		for _, m := range s.Messages {
			if m.Role != sources.RoleWrote {
				continue
			}
			for h := range strings.FieldsSeq(strings.SplitN(m.Text, "\n", 2)[1]) {
				held[parseHexForTest(t, h)] = true
			}
		}
	}
	for what, line := range map[string]string{"the first pass": first, "the incremental pass": appended} {
		h, ok := sources.HashWrittenLine(line)
		if !ok {
			t.Fatalf("%s: the line is not evidence", what)
		}
		if !held[h] {
			t.Errorf("%s: the written line is not in the index", what)
		}
	}
}

func parseHexForTest(t *testing.T, s string) uint64 {
	t.Helper()
	var v uint64
	for _, c := range s {
		v <<= 4
		switch {
		case c >= '0' && c <= '9':
			v |= uint64(c - '0')
		case c >= 'a' && c <= 'f':
			v |= uint64(c-'a') + 10
		default:
			t.Fatalf("not a hash: %q", s)
		}
	}
	return v
}

// A written side is 200,000 lines of hex on this machine and no query will ever
// be a hash, so only the path earns postings — the same trade a replaced span
// makes with its body. Left in, the hashes would be the largest set of distinct
// tokens in the index and would match nothing anybody types.
func TestTheWrittenSideEarnsPostingsForItsPathOnly(t *testing.T) {
	const path = "/w/internal/pool/pool.go"
	record := path + "\n7faceff4c3c97294 af63f04c86020e88"
	if got := tokenizedPart(roleWrote, record); got != path {
		t.Errorf("tokenizedPart indexed %q; only the path should earn postings", got)
	}
	// And the body is still stored: blame reads the hashes back from the record
	// log, so dropping them from the text would break attribution rather than
	// save postings.
	if strings.Contains(tokenizedPart(roleWrote, record), "7faceff4") {
		t.Error("a hash earned postings")
	}
}

// Nothing serves them either. A reader asking about a file has no use for a
// line of hex, and --role wrote would hand back one record per write.
func TestTheWrittenSideIsNeverServed(t *testing.T) {
	for _, role := range []string{"", roleWrote, roleEdit} {
		if recordServable(roleWrote, query.Options{Role: role}) {
			t.Errorf("a written-side record is served under --role %q", role)
		}
	}
	// The replaced side is the comparison: asked for by name, it is served.
	if !recordServable(roleEdit, query.Options{Role: roleEdit}) {
		t.Error("--role edit stopped serving replaced spans")
	}
	// And it is work rather than speech, so a per-session read budget spends
	// itself on what someone said first.
	if !isToolRole(roleWrote) {
		t.Error("the written side counts as speech")
	}
}

// ScanYearWork is the one pass behind `deja stats --year`. What it has to get
// right is the arithmetic the screen prints: records inside the window only,
// files and commands counted as distinct things rather than as records, and
// nothing from a tree the ignore rule covers — a year report that counts the
// scratch directory is a figure nobody can show anyone.
func yearStore(t *testing.T) (dir string, from time.Time) {
	t.Helper()
	home := t.TempDir()
	setHome(t, home)
	root := filepath.Join(home, "claude")
	t.Setenv("DEJA_CLAUDE_ROOT", root)
	policyFile := filepath.Join(home, "policy.json")
	t.Setenv("DEJA_POLICY_FILE", policyFile)
	if err := os.WriteFile(policyFile, []byte(`{"ignore":["*scratch*"]}`), 0o600); err != nil {
		t.Fatal(err)
	}

	now := time.Now().UTC()
	recent := now.AddDate(0, -1, 0).Format("2006-01-02T15:04:05Z")
	old := now.AddDate(-2, 0, 0).Format("2006-01-02T15:04:05Z")

	write := func(project, id, stamp string, tools string) {
		p := filepath.Join(root, "-w-"+project, id+".jsonl")
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		body := `{"type":"user","sessionId":"` + id + `","cwd":"/w/` + project + `","timestamp":"` + stamp + `","message":{"role":"user","content":"the widget pipeline keeps dropping the last attempt"}}` + "\n" +
			`{"type":"assistant","sessionId":"` + id + `","cwd":"/w/` + project + `","timestamp":"` + stamp + `","message":{"role":"assistant","content":[` + tools + `]}}` + "\n"
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	// Two sessions in the window edit the same file and run the same command,
	// so records and distinct things differ on purpose.
	edit := func(path string) string {
		return `{"type":"tool_use","name":"Edit","input":{"file_path":"` + path + `","old_string":"for i := 0; i < n; i++ {","new_string":"for i := 0; i <= n; i++ {"}}`
	}
	bash := func(cmd string) string {
		return `{"type":"tool_use","name":"Bash","input":{"command":"` + cmd + `"}}`
	}
	write("keep", "k1", recent, edit("/w/keep/loop.go")+","+bash("go test ./..."))
	write("keep", "k2", recent, edit("/w/keep/loop.go")+","+bash("go test ./..."))
	write("keep", "k3", recent, edit("/w/keep/backoff.go")+","+bash("go build ./cmd/x"))
	// Older than the window, and a tree the ignore rule covers: neither is in
	// any figure.
	write("keep", "k4", old, edit("/w/keep/ancient.go")+","+bash("make release"))
	for i := range 2 {
		write("scratch", fmt.Sprintf("s%d", i), recent,
			edit("/w/scratch/tmp.go")+","+bash("rm -rf /w/scratch/build"))
	}

	dir = filepath.Join(home, "index.db")
	if err := Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	return dir, time.Now().AddDate(-1, 0, 0)
}

func TestScanYearWorkCountsDistinctThingsInsideTheWindow(t *testing.T) {
	dir, from := yearStore(t)
	work, err := ScanYearWork(dir, from)
	if err != nil {
		t.Fatal(err)
	}
	// Three sessions in the window, each naming one file and running one
	// command: three records of each kind, two distinct files, two distinct
	// commands.
	if got := work.Records[roleFiles]; got != 3 {
		t.Errorf("files records = %d, want 3 (one per session in the window)", got)
	}
	if work.Files != 2 {
		t.Errorf("distinct files = %d, want 2 — loop.go twice is one file", work.Files)
	}
	if got := work.Records[roleCommand]; got != 3 {
		t.Errorf("command records = %d, want 3", got)
	}
	if work.Commands != 2 {
		t.Errorf("distinct commands = %d, want 2 — the same `go test` twice is one command", work.Commands)
	}
	if work.Spans != 3 || work.SpanFiles != 2 {
		t.Errorf("spans = %d in %d files, want 3 in 2", work.Spans, work.SpanFiles)
	}
}

func TestScanYearWorkLeavesOutTheIgnoredTreeAndTheOlderYears(t *testing.T) {
	dir, from := yearStore(t)
	work, err := ScanYearWork(dir, from)
	if err != nil {
		t.Fatal(err)
	}
	// The scratch sessions carry a file and a command of their own, and the
	// session from two years ago carries a third: counting either would show
	// 5 or 6 files where the window holds 2.
	if work.Files != 2 {
		t.Fatalf("distinct files = %d — the ignored tree or the older year is being counted", work.Files)
	}
	// And the whole store really does hold them, or this test passes for the
	// wrong reason.
	whole, err := ScanYearWork(dir, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if whole.Files <= work.Files {
		t.Fatalf("the store holds %d files over all time and %d in the window — the fixture proves nothing",
			whole.Files, work.Files)
	}
	if whole.Files != 3 {
		t.Errorf("over all time the readable files = %d, want 3 — the ignored tree is still out", whole.Files)
	}
}

func TestTokensFoldYo(t *testing.T) {
	want := tokens("зеленый")
	for _, in := range []string{"зелёный", "ЗЕЛЁНЫЙ", "Зеленый"} {
		if got := tokens(in); !reflect.DeepEqual(got, want) {
			t.Errorf("tokens(%q) = %q, want %q", in, got, want)
		}
	}
	if got := indexKeys("Зелёный"); len(got) == 0 || got[0] != "tзеленый" {
		t.Errorf("indexKeys = %q", got)
	}
	if h := tokenHits("Зелёный ёж", map[string]bool{"зеленый": true, "еж": true}); len(h) != 2 {
		t.Errorf("tokenHits = %v", h)
	}
}

func TestDidYouMeanKeepsYoWords(t *testing.T) {
	dir := t.TempDir()
	writeTestStore(t, dir, model.Session{
		Harness: "claude", ID: "a", Project: "p",
		Messages: []model.Message{{Role: "user", Text: "зелёный отчёт готов"}},
	})
	// A word spelt with ё tokenises to its е form; the check must compare the
	// folded spelling or it skips every such word.
	if got := DidYouMean(dir, "зелёныйй"); got == "" {
		t.Fatal("a mistyped ё word got no suggestion")
	}
}

// A query written with ё reads a message written with е at the per-message
// pass too: without the fold the focused turn holds none of the query's words
// and keeps the place the pool gave it.
func TestBestMessageFusionFoldsYo(t *testing.T) {
	terms := []string{"зелёный", "отчёт", "счёт"}
	idf := map[string]float64{"зеленый": 2.0, "отчет": 2.5, "счет": 1.5}
	scattered := model.Session{ID: "scattered", Messages: []model.Message{
		{Role: "assistant", Text: "зеленый цвет подходит"},
		{Role: "assistant", Text: "отчет лежит в папке"},
		{Role: "assistant", Text: "счет закрыт"},
	}}
	thin := model.Session{ID: "thin", Messages: []model.Message{
		{Role: "assistant", Text: "отчет и еще отчет"},
	}}
	focused := model.Session{ID: "focused", Messages: []model.Message{
		{Role: "user", Text: "Зеленый отчет и счет готовы"},
	}}
	got := sessionIDs(rerankByBestMessage([]model.Session{scattered, thin, focused}, terms, idf))
	if got[1] != "focused" {
		t.Fatalf("order = %v, want focused second", got)
	}
}

// A CamelCase word splits into parts filed the way every other word is.
func TestSplitCompoundFoldsYo(t *testing.T) {
	var out []string
	splitCompound([]rune("ЁлкаПраздник"), &out)
	if len(out) == 0 || out[0] != "елка" {
		t.Fatalf("parts = %q, want елка first", out)
	}
}

// A ZCode snapshot indexed before the user restored it stays on disk, unchanged,
// so the incremental pass never re-reads it, and the restored session arrives
// from the CLI database under the same id. That is one conversation in two
// stores: the database row owns it, and the pair is not a clash. Sort order
// handed the row to the snapshot (~/.zcode sorts before most project paths),
// so resume kept refusing the restored session (#4432).
func TestAZCodeSnapshotYieldsToItsRestoredSession(t *testing.T) {
	snap := filepath.FromSlash("/Users/x/.zcode/v2/sessions/5f0c/task-1.json")
	dir := filepath.FromSlash("/Users/x/proj")

	held := SessionMeta{Harness: "zcode", ID: "acp-9", Path: snap}
	owns, collided := attributeSession(held, model.Session{Harness: "zcode", ID: "acp-9", Path: dir})
	if collided || !owns {
		t.Errorf("restored session: owns=%v collided=%v, want it to take the row without a clash", owns, collided)
	}

	heldDB := SessionMeta{Harness: "zcode", ID: "acp-9", Path: dir}
	owns, collided = attributeSession(heldDB, model.Session{Harness: "zcode", ID: "acp-9", Path: snap})
	if collided || owns {
		t.Errorf("snapshot after the database: owns=%v collided=%v, want the database to keep the row", owns, collided)
	}
}

// A ZCode snapshot is one file per conversation, rewritten whole as it grows,
// so a pass that re-reads it replaces what it held. Records from it were taken
// for the CLI database's, whose rows a pass does not drop by path, and a
// rewritten snapshot came back on top of its old copy (#4432).
func TestARewrittenZCodeSnapshotIsHeldOnce(t *testing.T) {
	tmp := t.TempDir()
	setHome(t, tmp)
	t.Setenv("XDG_DATA_HOME", filepath.Join(tmp, "data"))
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(tmp, "config"))
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "claude"))
	t.Setenv("DEJA_CODEX_ROOT", filepath.Join(tmp, "codex"))
	t.Setenv("DEJA_NOTES_FILE", filepath.Join(tmp, "notes.jsonl"))
	t.Setenv("DEJA_ZCODE_ROOT", filepath.Join(tmp, "absent"))
	t.Setenv("DEJA_ZCODE_DB", filepath.Join(tmp, "absent.sqlite"))
	root := filepath.Join(tmp, "zcode-v2")
	t.Setenv("DEJA_ZCODE_LEGACY_ROOT", root)
	snap := filepath.Join(root, "5f0c1e9a", "task-1.json")
	if err := os.MkdirAll(filepath.Dir(snap), 0o755); err != nil {
		t.Fatal(err)
	}
	msgs := []string{
		`{"role":"user","content":"fix the retry loop, it never stops","timestamp":1790000000000}`,
		`{"role":"assistant","content":"capped it at five attempts","timestamp":1790000060000}`,
	}
	write := func(n int, at time.Time) {
		t.Helper()
		body := `{"meta":{"taskId":"task-1","acpSessionId":"acp-9","workspacePath":"/private/tmp/proj","title":"retry","createdAt":1790000000000},"messages":[` +
			strings.Join(msgs[:n], ",") + `]}`
		if err := os.WriteFile(snap, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(snap, at, at); err != nil {
			t.Fatal(err)
		}
	}
	write(1, time.Now().Add(-time.Hour))
	dir := filepath.Join(tmp, "index.db")
	if err := Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	count := func() int {
		t.Helper()
		s, ok, err := FindByIdentity(dir, "zcode", "acp-9")
		if err != nil || !ok {
			t.Fatalf("zcode:acp-9 is not in the index (%v)", err)
		}
		return len(s.Messages)
	}
	if got := count(); got != 1 {
		t.Fatalf("the build holds %d messages, so this measures nothing", got)
	}

	write(2, time.Now())
	var out strings.Builder
	if err := Ensure(dir, "", false, &out); err != nil {
		t.Fatal(err)
	}
	if got := count(); got != 2 {
		t.Errorf("after the snapshot grew by one turn it holds %d messages, want 2 (%s)", got, out.String())
	}
}

// An older-ZCode snapshot is rewritten whole on each turn, and each rewrite
// added its turns again: 6 messages where a rebuild had 4. Once the session
// was restored into the CLI database a rebuild read it from there alone,
// while the unchanged snapshot kept its turns beside the database's (#4448).
func TestAZCodeSnapshotRewrittenAndRestoredMatchesARebuild(t *testing.T) {
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skip("sqlite3 CLI not available")
	}
	tmp := t.TempDir()
	isolateStores(t, tmp)
	t.Setenv("DEJA_ZCODE_ROOT", filepath.Join(tmp, "absent"))
	db := filepath.Join(tmp, "zcode", "db.sqlite")
	t.Setenv("DEJA_ZCODE_DB", db)
	root := filepath.Join(tmp, "zcode-v2")
	t.Setenv("DEJA_ZCODE_LEGACY_ROOT", root)
	snap := filepath.Join(root, "5f0c1e9a", "task-1.json")
	msgs := []string{
		`{"role":"user","content":"fix the retry loop, it never stops","timestamp":1790762400000}`,
		`{"role":"assistant","content":"capped it at five attempts","timestamp":1790762460000}`,
		`{"role":"user","content":"now run the tests","timestamp":1790762520000}`,
		`{"role":"assistant","content":"tests pass","timestamp":1790762580000}`,
	}
	write := func(n int, at time.Time) {
		t.Helper()
		writeAt(t, snap, `{"meta":{"taskId":"task-1","workspacePath":"/tmp/proj","title":"fix the retry loop","createdAt":1790762400000},"messages":[`+
			strings.Join(msgs[:n], ",")+`]}`, at)
	}
	write(2, time.Now().Add(-time.Hour))
	dir := filepath.Join(tmp, "index.db")
	indexPass(t, dir)
	write(4, time.Now().Add(-time.Minute))
	indexPass(t, dir)
	matchesRebuild(t, dir, "zcode", "task-1")

	// The user restores it: the database now holds the session, and the
	// snapshot stays as it was.
	ms := int64(1790762400000)
	seed := `create table session (id text primary key, project_id text, parent_id text, directory text, title text, time_created integer, time_updated integer);
create table message (id text primary key, session_id text, time_created integer, time_updated integer, data text);
create table part (id text primary key, message_id text, session_id text, time_created integer, time_updated integer, data text);
` + fmt.Sprintf(`insert into session values ('task-1','p','','/tmp/proj','restored',%d,%d);
insert into message values ('m1','task-1',%d,%d,'{"role":"user","time":{"created":%d}}');
insert into part values ('p1','m1','task-1',%d,%d,'{"type":"text","text":"fix the retry loop, it never stops"}');
`, ms, ms+1000, ms, ms, ms, ms, ms)
	if err := os.MkdirAll(filepath.Dir(db), 0o755); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.CommandContext(t.Context(), "sqlite3", db, seed).CombinedOutput(); err != nil {
		t.Fatalf("sqlite3 seed: %v %s", err, out)
	}
	indexPass(t, dir)
	matchesRebuild(t, dir, "zcode", "task-1")
}

// zedThreadRow builds one row the way Zed writes it: the thread document
// compressed into a zstd frame, addressed by a hex blob literal.
func zedThreadRow(t *testing.T, id, title, updated, text string) string {
	t.Helper()
	body := `{"version":"0.3.0","title":"` + title + `","updated_at":"` + updated + `","messages":[` +
		`{"User":{"id":"u-` + id + `","content":[{"Text":"` + text + `"}]}},` +
		`{"Agent":{"content":[{"Text":"reply to ` + id + `"}],"tool_results":{}}}]}`
	cmd := exec.CommandContext(t.Context(), "zstd", "-q", "-3", "-c")
	cmd.Stdin = strings.NewReader(body)
	var out bytes.Buffer
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil {
		t.Fatalf("compress thread: %v", err)
	}
	return "insert or replace into threads (id,summary,updated_at,data_type,data,folder_paths,created_at) values ('" +
		id + "','" + title + "','" + updated + "','zstd',x'" + hex.EncodeToString(out.Bytes()) +
		"','/w/app','" + updated + "');"
}

// A zed thread untouched since the watermark must survive an incremental pass
// triggered by a change to the shared threads.db — the failure every db-backed
// harness here is one mistake away from, since the whole store is one file.
func TestZedIncrementalKeepsUntouchedThreads(t *testing.T) {
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skip("sqlite3 not available")
	}
	if _, err := exec.LookPath("zstd"); err != nil {
		t.Skip("zstd not available")
	}
	tmp := hermeticIndexEnv(t)
	dir := filepath.Join(tmp, "index")
	db := filepath.Join(tmp, "zed", "threads", "threads.db")
	if err := os.MkdirAll(filepath.Dir(db), 0o755); err != nil {
		t.Fatal(err)
	}
	schema := `create table if not exists threads (
    id text primary key, summary text not null, updated_at text not null,
    data_type text not null, data blob not null, parent_id text,
    folder_paths text, folder_paths_order text, created_at text);`
	run := func(sql string) {
		t.Helper()
		cmd := exec.CommandContext(t.Context(), "sqlite3", db)
		cmd.Stdin = strings.NewReader(sql)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("seed: %v: %s", err, out)
		}
	}

	run(schema + zedThreadRow(t, "zed-old", "old thread", "2026-07-19T10:00:00+00:00", "oldzedfact about the retry budget"))
	if err := Ensure(dir, "", false, os.Stderr); err != nil {
		t.Fatal(err)
	}
	if hits, _ := Search(dir, search.Options{Query: "oldzedfact", All: true}); len(hits) == 0 {
		t.Fatal("old zed thread not indexed on first pass")
	}

	run(zedThreadRow(t, "zed-new", "new thread", "2026-07-19T11:00:00+00:00", "newzedfact about the cache key"))
	future := time.Now().Add(time.Hour)
	_ = os.Chtimes(db, future, future)
	if err := Ensure(dir, "", false, os.Stderr); err != nil {
		t.Fatal(err)
	}
	if hits, _ := Search(dir, search.Options{Query: "newzedfact", All: true}); len(hits) == 0 {
		t.Fatal("new zed thread not indexed on incremental pass")
	}
	if hits, _ := Search(dir, search.Options{Query: "oldzedfact", All: true}); len(hits) == 0 {
		t.Fatal("REGRESSION: untouched zed thread vanished after incremental pass")
	}
}

// Discovery has to find the store through Zed's own path logic, and the hit has
// to carry the harness name so `--harness zed` and the search tag work.
func TestZedStoreIsDiscoveredAndTagged(t *testing.T) {
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skip("sqlite3 not available")
	}
	if _, err := exec.LookPath("zstd"); err != nil {
		t.Skip("zstd not available")
	}
	tmp := hermeticIndexEnv(t)
	dir := filepath.Join(tmp, "index")
	db := filepath.Join(tmp, "zed", "threads", "threads.db")
	if err := os.MkdirAll(filepath.Dir(db), 0o755); err != nil {
		t.Fatal(err)
	}
	schema := `create table threads (
    id text primary key, summary text not null, updated_at text not null,
    data_type text not null, data blob not null, parent_id text,
    folder_paths text, folder_paths_order text, created_at text);`
	cmd := exec.CommandContext(t.Context(), "sqlite3", db)
	cmd.Stdin = strings.NewReader(schema + zedThreadRow(t, "zed-tagged", "tagged", "2026-07-19T12:00:00+00:00", "zedtaggedfact about the parser"))
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("seed: %v: %s", err, out)
	}
	if err := Ensure(dir, "", false, os.Stderr); err != nil {
		t.Fatal(err)
	}
	hits, err := Search(dir, search.Options{Query: "zedtaggedfact", All: true})
	if err != nil || len(hits) == 0 {
		t.Fatalf("hits = %#v, err = %v", hits, err)
	}
	if hits[0].Harness != "zed" {
		t.Fatalf("harness = %q, want zed", hits[0].Harness)
	}
	if hits[0].Project != "w/app" {
		t.Fatalf("project = %q, want the folder_paths project", hits[0].Project)
	}
	// Filtering by harness must reach it, and must not reach it under another
	// name — a tag that matches everything is not a tag.
	tagged, err := Search(dir, search.Options{Query: "zedtaggedfact", All: true, Harness: "zed"})
	if err != nil || len(tagged) == 0 {
		t.Fatalf("--harness zed found nothing: %#v, err = %v", tagged, err)
	}
	other, err := Search(dir, search.Options{Query: "zedtaggedfact", All: true, Harness: "claude"})
	if err != nil || len(other) != 0 {
		t.Fatalf("--harness claude reached a zed thread: %#v, err = %v", other, err)
	}
}

// A thread deja cannot decode reaches doctor as a skipped record of the zed
// store, with the reason. It used to leave no trace, so the store read as whole
// with half its threads missing (#4341).
func TestAZedThreadDejaCannotDecodeIsReportedWithWhy(t *testing.T) {
	tmp, db := zedEnv(t)
	zedThread(t, db, "good", "2026-01-01T10:00:00Z", []string{"marker-zed-good"})
	c := exec.CommandContext(t.Context(), "sqlite3", db)
	c.Stdin = strings.NewReader(`insert into threads (id,summary,updated_at,data_type,data) values ('later','s','2026-01-01T11:00:00Z','brotli',x'00');`)
	if o, err := c.CombinedOutput(); err != nil {
		t.Fatalf("sqlite3 seed: %v %s", err, o)
	}
	dir := filepath.Join(tmp, "index.db")
	if err := Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	if zedHits(t, dir, "marker-zed-good") != 1 {
		t.Fatalf("the readable thread was not indexed, so this measures nothing")
	}
	if got := IngestHealth(dir)["zed"].MalformedLines; got != 1 {
		t.Fatalf("zed skipped = %d, want 1: %#v", got, IngestHealth(dir))
	}
	if r := IngestFilesReport(dir)[db].Reason; !strings.Contains(r, `unknown data_type "brotli"`) {
		t.Fatalf("reason = %q, want the unknown data_type named", r)
	}
}

// zed is read from its watermark, so a pass after a newer thread lands does not
// hand the skipped row back. It is still in the store, still unreadable, and
// the count must not drop to zero because the cursor moved past it (#4341).
func TestAZedThreadDejaCannotDecodeStaysCountedPastTheWatermark(t *testing.T) {
	tmp, db := zedEnv(t)
	zedThread(t, db, "good", "2026-01-01T10:00:00Z", []string{"marker-zed-good"})
	c := exec.CommandContext(t.Context(), "sqlite3", db)
	c.Stdin = strings.NewReader(`insert into threads (id,summary,updated_at,data_type,data) values ('later','s','2026-01-01T11:00:00Z','brotli',x'00');`)
	if o, err := c.CombinedOutput(); err != nil {
		t.Fatalf("sqlite3 seed: %v %s", err, o)
	}
	dir := filepath.Join(tmp, "index.db")
	if err := Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	for i, ts := range []string{"2026-01-02T10:00:00Z", "2026-01-03T10:00:00Z"} {
		zedThread(t, db, fmt.Sprintf("next%d", i), ts, []string{fmt.Sprintf("marker-zed-next%d", i)})
		later := time.Now().Add(time.Duration(i+1) * time.Minute)
		if err := os.Chtimes(db, later, later); err != nil {
			t.Fatal(err)
		}
		if err := Ensure(dir, "", false, nil); err != nil {
			t.Fatal(err)
		}
		if zedHits(t, dir, fmt.Sprintf("marker-zed-next%d", i)) != 1 {
			t.Fatalf("pass %d did not index the new thread, so this measures nothing", i+2)
		}
		if got := IngestHealth(dir)["zed"].MalformedLines; got != 1 {
			t.Fatalf("pass %d: zed skipped = %d, want 1", i+2, got)
		}
	}
	if r := IngestFilesReport(dir)[db].Reason; !strings.Contains(r, `unknown data_type "brotli"`) {
		t.Fatalf("reason = %q, want the unknown data_type named", r)
	}
}

func TestZedSkipsAreNamedAsThreads(t *testing.T) {
	if got := nothingReadableNarration("zed", 2, 0); !strings.Contains(got, "2 threads could not be read") {
		t.Errorf("got %q", got)
	}
	if got := harnessNarration("zed", nil, "", 1, 0); !strings.Contains(got, "1 thread skipped") {
		t.Errorf("got %q", got)
	}
	if got := nothingReadableNarration("claude", 2, 0); !strings.Contains(got, "2 lines could not be read") {
		t.Errorf("a JSONL store lost its unit: %q", got)
	}
}

// zedEnv points every other store somewhere empty and returns the store path.
func zedEnv(t *testing.T) (tmp, db string) {
	t.Helper()
	for _, bin := range []string{"sqlite3", "zstd"} {
		if _, err := exec.LookPath(bin); err != nil {
			t.Skipf("%s not available", bin)
		}
	}
	tmp = t.TempDir()
	setHome(t, tmp)
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "claude"))
	t.Setenv("DEJA_CODEX_ROOT", filepath.Join(tmp, "codex"))
	t.Setenv("DEJA_GOOSE_DB", filepath.Join(tmp, "none-goose.db"))
	t.Setenv("DEJA_OPENCODE_DB", filepath.Join(tmp, "none-opencode.db"))
	t.Setenv("DEJA_GROK_DB", filepath.Join(tmp, "none-grok.db"))
	t.Setenv("DEJA_HERMES_HOME", filepath.Join(tmp, "none-hermes"))
	t.Setenv("DEJA_NOTES_FILE", filepath.Join(tmp, "notes.jsonl"))
	db = filepath.Join(tmp, "threads.db")
	t.Setenv("DEJA_ZED_DB", db)
	t.Setenv("DEJA_ZED_ROOT", filepath.Join(tmp, "zed"))
	return tmp, db
}

// zedThread writes one thread, replacing it if it is already there. The body is
// really compressed rather than recorded, so the row is what Zed would store.
func zedThread(t *testing.T, db, id, updated string, texts []string) {
	t.Helper()
	msgs := make([]string, 0, len(texts))
	for i, text := range texts {
		msgs = append(msgs, fmt.Sprintf(`{"User":{"id":"u%d","content":[{"Text":%q}]}}`, i, text))
	}
	body := fmt.Sprintf(`{"version":"0.3.0","title":"pool timeouts","updated_at":%q,"messages":[%s]}`,
		updated, strings.Join(msgs, ","))
	cmd := exec.CommandContext(t.Context(), "zstd", "-q", "-3", "-c")
	cmd.Stdin = strings.NewReader(body)
	var out bytes.Buffer
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil {
		t.Fatalf("compress: %v", err)
	}
	stmts := `create table if not exists threads (
    id text primary key, summary text not null, updated_at text not null,
    data_type text not null, data blob not null, parent_id text,
    folder_paths text, folder_paths_order text, created_at text);
` + fmt.Sprintf("insert or replace into threads (id,summary,updated_at,data_type,data,folder_paths) values (%s,'pool timeouts',%s,'zstd',x'%s','[\"/work/app\"]');",
		sqliteLiteral(id), sqliteLiteral(updated), hex.EncodeToString(out.Bytes()))
	c := exec.CommandContext(t.Context(), "sqlite3", db)
	c.Stdin = strings.NewReader(stmts)
	if o, err := c.CombinedOutput(); err != nil {
		t.Fatalf("sqlite3 seed: %v %s", err, o)
	}
}

// sqliteLiteral uses SQL string literals, not Go's %q double-quoted strings.
// SQLite historically accepted double-quoted strings in some builds, but that
// compatibility is optional and strict builds parse them as identifiers.
func sqliteLiteral(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
}

func zedHits(t *testing.T, dir, marker string) int {
	t.Helper()
	got, err := Search(dir, search.Options{Query: marker, All: true})
	if err != nil {
		t.Fatal(err)
	}
	return len(got)
}

// zedTurns counts the messages holding a marker, across every session that has
// it. Counting sessions cannot see the failure this store risks: a thread that
// comes back whole is one session either way, and the turn inside it doubling
// is what the goose comment describes.
func zedTurns(t *testing.T, dir, marker string) int {
	t.Helper()
	got, err := Search(dir, search.Options{Query: marker, All: true})
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, s := range got {
		for _, m := range s.Messages {
			if strings.Contains(m.Text, marker) {
				n++
			}
		}
	}
	return n
}

// zed is the third store in #2075 and the one whose cursor is not the others':
// ParseZedDBSince selects *threads* updated after the watermark and returns each
// whole, so a continued thread hands back the turns already held. That is the
// goose shape, and it is why the store cannot simply be stamped.
func TestTheZedStoreIsAskedOnlyForWhatIsNew(t *testing.T) {
	tmp, db := zedEnv(t)
	zedThread(t, db, "t1", "2026-01-01T10:00:00Z", []string{"marker-zed-one"})

	dir := filepath.Join(tmp, "index.db")
	if err := Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	if zedHits(t, dir, "marker-zed-one") != 1 {
		t.Fatalf("the store was not indexed at all, so this measures nothing")
	}
	m, err := readManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	if m.Files[db].LastUpdated == 0 {
		t.Errorf("the store carries no watermark, so the next pass reads it whole")
	}

	// The thread gains a turn: it comes back whole, earlier turns included.
	zedThread(t, db, "t1", "2026-01-01T11:00:00Z", []string{"marker-zed-one", "marker-zed-two"})
	if err := Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	if n := zedHits(t, dir, "marker-zed-two"); n != 1 {
		t.Errorf("the turn added to the thread is not indexed: %d hits", n)
	}
	// Counted as turns, not as sessions: the thread is one session however
	// many times its first turn was added.
	if n := zedTurns(t, dir, "marker-zed-one"); n != 1 {
		t.Errorf("the earlier turn is held %d times", n)
	}
	if n := zedTurns(t, dir, "marker-zed-two"); n != 1 {
		t.Errorf("the added turn is held %d times", n)
	}
}

// And a thread the pass did not ask about is still there afterwards: the store
// changes whenever any thread in it does, so the changed-file rule would take
// the quiet ones without fromDatabase knowing this store.
func TestAQuietZedThreadSurvivesAPassThatDidNotAskForIt(t *testing.T) {
	tmp, db := zedEnv(t)
	zedThread(t, db, "quiet", "2026-01-01T09:00:00Z", []string{"marker-zed-quiet"})
	zedThread(t, db, "busy", "2026-01-01T10:00:00Z", []string{"marker-zed-busy"})

	dir := filepath.Join(tmp, "index.db")
	if err := Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	if zedHits(t, dir, "marker-zed-quiet") != 1 || zedHits(t, dir, "marker-zed-busy") != 1 {
		t.Fatalf("both threads were not indexed, so this measures nothing")
	}

	zedThread(t, db, "busy", "2026-01-01T11:00:00Z", []string{"marker-zed-busy", "marker-zed-added"})
	if err := Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	if n := zedHits(t, dir, "marker-zed-added"); n != 1 {
		t.Errorf("the added turn is not indexed: %d hits", n)
	}
	if n := zedHits(t, dir, "marker-zed-quiet"); n != 1 {
		t.Errorf("the thread the pass did not ask about was dropped: %d hits", n)
	}
	_ = os.Remove(filepath.Join(tmp, "unused"))
}

// Without zstd a compressed rollout stays out of the manifest, pass after pass,
// so the first pass that has zstd reads it. Once it is held, an unchanged
// rollout does not run the decoder again (#5061).
func TestZstdInstalledReadsRolloutOnce(t *testing.T) {
	tmp := hermeticIndexEnv(t)
	bin := filepath.Join(tmp, "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)

	dir := filepath.Join(os.Getenv("DEJA_CODEX_ROOT"), "sessions", "2026", "07", "31")
	plain := filepath.Join(dir, "rollout-2026-07-31T00-00-00-cx-1.jsonl")
	zst := filepath.Join(dir, "rollout-2026-07-20T00-00-00-zstd-installed-thread.jsonl.zst")
	write(t, plain, codexRolloutHead)
	write(t, zst, string([]byte{0x28, 0xB5, 0x2F, 0xFD, 0x00, 0x01, 0x02}))

	idx := filepath.Join(tmp, "idx")
	for pass := 1; pass <= 2; pass++ {
		if err := Ensure(idx, "", false, nil); err != nil {
			t.Fatal(err)
		}
		m, err := readManifest(idx)
		if err != nil {
			t.Fatal(err)
		}
		if _, held := m.Files[zst]; held {
			t.Fatalf("pass %d: compressed rollout held without zstd", pass)
		}
		if _, held := m.Files[plain]; !held {
			t.Fatalf("pass %d: plain rollout missing from manifest", pass)
		}
	}

	counter := filepath.Join(tmp, "zstd-calls.txt")
	rollout := filepath.Join(tmp, "decoded.jsonl")
	write(t, rollout, strings.ReplaceAll(codexRolloutHead, "cx-1", "zstd-installed-thread"))
	t.Setenv("DEJA_TEST_ZSTD_COUNTER", counter)
	t.Setenv("DEJA_TEST_ZSTD_ROLLOUT", rollout)
	if runtime.GOOS == "windows" {
		write(t, filepath.Join(bin, "zstd.bat"), "@echo off\r\n@echo x>>\"%DEJA_TEST_ZSTD_COUNTER%\"\r\n@type \"%DEJA_TEST_ZSTD_ROLLOUT%\"\r\n@exit /b 0\r\n")
	} else {
		write(t, filepath.Join(bin, "zstd"), "#!/bin/sh\nprintf 'x\\n' >>\"$DEJA_TEST_ZSTD_COUNTER\"\nwhile IFS= read -r line; do\n  printf '%s\\n' \"$line\"\ndone <\"$DEJA_TEST_ZSTD_ROLLOUT\"\n")
		if err := os.Chmod(filepath.Join(bin, "zstd"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	calls := func() int {
		b, err := os.ReadFile(counter)
		if os.IsNotExist(err) {
			return 0
		}
		if err != nil {
			t.Fatal(err)
		}
		return bytes.Count(b, []byte("\n"))
	}
	if err := Ensure(idx, "", false, nil); err != nil {
		t.Fatal(err)
	}
	m, err := readManifest(idx)
	if err != nil {
		t.Fatal(err)
	}
	if _, held := m.Files[zst]; !held {
		t.Fatal("first pass with zstd did not hold the compressed rollout")
	}
	if _, held := m.Sessions["codex:zstd-installed-thread"]; !held {
		t.Fatal("decoded rollout session missing from manifest")
	}
	before := calls()
	if before == 0 {
		t.Fatal("first pass with zstd did not run the decoder")
	}
	if err := Ensure(idx, "", false, nil); err != nil {
		t.Fatal(err)
	}
	if got := calls(); got != before {
		t.Errorf("unchanged pass ran zstd %d more times", got-before)
	}
}

// zstdStubRollouts writes a plain rollout and a compressed one, and returns
// the bin directory a later installZstdStub puts zstd in, already on PATH.
func zstdStubRollouts(t *testing.T, tmp, thread string) (bin, plain, zst string) {
	t.Helper()
	bin = filepath.Join(tmp, "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	dir := filepath.Join(os.Getenv("DEJA_CODEX_ROOT"), "sessions", "2026", "07", "31")
	plain = filepath.Join(dir, "rollout-2026-07-31T00-00-00-cx-1.jsonl")
	zst = filepath.Join(dir, "rollout-2026-07-20T00-00-00-"+thread+".jsonl.zst")
	write(t, plain, codexRolloutHead)
	write(t, zst, string([]byte{0x28, 0xB5, 0x2F, 0xFD, 0x00, 0x01, 0x02}))
	return bin, plain, zst
}

// installZstdStub puts a zstd on PATH that prints a rollout for thread.
func installZstdStub(t *testing.T, tmp, bin, thread string) {
	t.Helper()
	rollout := filepath.Join(tmp, "decoded.jsonl")
	write(t, rollout, strings.ReplaceAll(codexRolloutHead, "cx-1", thread))
	t.Setenv("DEJA_TEST_ZSTD_ROLLOUT", rollout)
	if runtime.GOOS == "windows" {
		write(t, filepath.Join(bin, "zstd.bat"), "@echo off\r\n@type \"%DEJA_TEST_ZSTD_ROLLOUT%\"\r\n@exit /b 0\r\n")
		return
	}
	write(t, filepath.Join(bin, "zstd"), "#!/bin/sh\nwhile IFS= read -r line; do\n  printf '%s\n' \"$line\"\ndone <\"$DEJA_TEST_ZSTD_ROLLOUT\"\n")
	if err := os.Chmod(filepath.Join(bin, "zstd"), 0o755); err != nil {
		t.Fatal(err)
	}
}

// A rollout waiting for zstd is not a change: with nothing else changed, the
// next pass leaves manifest.gob and sessions.gob as they are. It read as
// changed on every pass, so every search rewrote both (#5105).
func TestZstdMissingLeavesIndexAlone(t *testing.T) {
	tmp := hermeticIndexEnv(t)
	_, plain, zst := zstdStubRollouts(t, tmp, "zstd-waiting-thread")

	idx := filepath.Join(tmp, "idx")
	if err := Ensure(idx, "", false, nil); err != nil {
		t.Fatal(err)
	}
	m, err := readManifest(idx)
	if err != nil {
		t.Fatal(err)
	}
	if _, held := m.Files[zst]; held {
		t.Fatal("compressed rollout held as read without zstd")
	}
	if _, waiting := m.Deferred[zst]; !waiting {
		t.Fatal("compressed rollout not held as waiting for zstd")
	}
	if _, held := m.Files[plain]; !held {
		t.Fatal("plain rollout missing from manifest")
	}
	if fresh, _ := UpToDate(idx, ""); !fresh {
		t.Fatal("index with only a rollout waiting for zstd reads as stale")
	}

	read := func() (manifest, sessions []byte) {
		var err error
		if manifest, err = os.ReadFile(filepath.Join(idx, "manifest.gob")); err != nil {
			t.Fatal(err)
		}
		if sessions, err = os.ReadFile(filepath.Join(idx, "sessions.bin")); err != nil {
			t.Fatal(err)
		}
		return manifest, sessions
	}
	manifestBefore, sessionsBefore := read()
	if err := Ensure(idx, "", false, nil); err != nil {
		t.Fatal(err)
	}
	manifestAfter, sessionsAfter := read()
	if !bytes.Equal(manifestBefore, manifestAfter) {
		t.Error("unchanged pass rewrote manifest.gob")
	}
	if !bytes.Equal(sessionsBefore, sessionsAfter) {
		t.Error("unchanged pass rewrote sessions.bin")
	}
}

// The tool fingerprint keeps zstd=true once any deja saw zstd, so a rollout a
// hook deferred with a short PATH is read by the next pass that has zstd even
// though the fingerprint never changes (#5105).
func TestDeferredRolloutReadWhenFingerprintHadZstd(t *testing.T) {
	tmp := hermeticIndexEnv(t)
	bin, _, zst := zstdStubRollouts(t, tmp, "zstd-hook-thread")

	idx := filepath.Join(tmp, "idx")
	if err := Ensure(idx, "", false, nil); err != nil {
		t.Fatal(err)
	}
	m, err := readManifest(idx)
	if err != nil {
		t.Fatal(err)
	}
	if _, waiting := m.Deferred[zst]; !waiting {
		t.Fatal("compressed rollout not held as waiting for zstd")
	}
	// As a terminal pass with zstd would have left it.
	hadSQLite, _ := parseToolFingerprint(m.ToolFingerprint)
	m.ToolFingerprint = toolFingerprint(hadSQLite, true)
	if err = writeManifestOnly(idx, m); err != nil {
		t.Fatal(err)
	}

	installZstdStub(t, tmp, bin, "zstd-hook-thread")
	if err = Ensure(idx, "", false, nil); err != nil {
		t.Fatal(err)
	}
	m, err = readManifest(idx)
	if err != nil {
		t.Fatal(err)
	}
	if _, held := m.Files[zst]; !held {
		t.Fatal("pass with zstd did not read the deferred rollout")
	}
	if _, held := m.Sessions["codex:zstd-hook-thread"]; !held {
		t.Fatal("deferred rollout's session missing after zstd arrived")
	}
	if _, waiting := m.Deferred[zst]; waiting {
		t.Fatal("rollout still held as waiting after it was read")
	}
}

// On a first build big enough to publish its newest slice early, the rollouts
// waiting for zstd are still held as waiting once the build ends; the early
// publish must not drain the list the full build commits (#5110).
func TestFirstBuildKeepsDeferredAcrossNewestFirstPublish(t *testing.T) {
	tmp := hermeticIndexEnv(t)
	_, _, zst := zstdStubRollouts(t, tmp, "zstd-big-thread")
	dir := filepath.Join(os.Getenv("DEJA_CODEX_ROOT"), "sessions", "2026", "07", "30")
	for i := range partialPublishFrom + 10 {
		id := fmt.Sprintf("big-%04d", i)
		write(t, filepath.Join(dir, "rollout-2026-07-30T00-00-00-"+id+".jsonl"),
			strings.ReplaceAll(codexRolloutHead, "cx-1", id))
	}

	idx := filepath.Join(tmp, "idx")
	if err := Ensure(idx, "", false, nil); err != nil {
		t.Fatal(err)
	}
	m, err := readManifest(idx)
	if err != nil {
		t.Fatal(err)
	}
	if _, waiting := m.Deferred[zst]; !waiting {
		t.Fatalf("first build left Deferred empty (%d entries), want the compressed rollout", len(m.Deferred))
	}
}

func TestImportedRecordsSidecarSurvivalAndMigration(t *testing.T) {
	setupStore := func(t *testing.T, searchOnly bool) (dir, proj, batchDir string) {
		t.Helper()
		tmp := hermeticIndexEnv(t)
		if searchOnly {
			t.Setenv(SearchOnlyEnv, "1")
			defer ApplySearchOnly()()
		}
		proj = filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-tmp-sidecar")
		if err := os.MkdirAll(proj, 0o755); err != nil {
			t.Fatal(err)
		}
		local1 := `{"type":"user","sessionId":"local1","timestamp":"2026-01-02T03:04:05Z","message":{"role":"user","content":"sidecarlocalneedle one"}}` + "\n"
		if err := os.WriteFile(filepath.Join(proj, "local1.jsonl"), []byte(local1), 0o644); err != nil {
			t.Fatal(err)
		}
		dir = filepath.Join(tmp, "idx")
		if err := Ensure(dir, "", false, nil); err != nil {
			t.Fatal(err)
		}
		base := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
		batchDir = filepath.Join(tmp, "batches")
		writeSyncBatch(t, batchDir, []SyncRecord{
			{Harness: "claude", SessionID: "remote1", Project: "deja-vu", Role: "user", Text: "sidecarimportneedle question", Time: base},
			{Harness: "claude", SessionID: "remote1", Project: "deja-vu", Role: "assistant", Text: "sidecarimportneedle answer", Time: base.Add(time.Minute)},
		})
		n, err := Import(dir, batchDir)
		if err != nil || n != 2 {
			t.Fatalf("initial import n=%d err=%v", n, err)
		}
		return dir, proj, batchDir
	}

	assertSidecarSplit := func(t *testing.T, dir, stage string) {
		t.Helper()
		var core manifestCore
		if err := readGob(filepath.Join(dir, "manifest.gob"), &core); err != nil {
			t.Fatalf("%s: read manifestCore: %v", stage, err)
		}
		if len(core.ImportedRecords) != 0 {
			t.Fatalf("%s: manifest.gob kept %d ImportedRecords, want 0", stage, len(core.ImportedRecords))
		}
		var dedupe map[string]bool
		if err := readGob(filepath.Join(dir, importedFile), &dedupe); err != nil {
			t.Fatalf("%s: read %s: %v", stage, importedFile, err)
		}
		if len(dedupe) == 0 {
			t.Fatalf("%s: %s is empty", stage, importedFile)
		}
		m, err := readManifest(dir)
		if err != nil {
			t.Fatalf("%s: readManifest: %v", stage, err)
		}
		if len(m.ImportedRecords) != 0 {
			t.Fatalf("%s: readManifest decoded %d ImportedRecords, want 0", stage, len(m.ImportedRecords))
		}
		cached, err := readManifestCached(dir)
		if err != nil {
			t.Fatalf("%s: readManifestCached: %v", stage, err)
		}
		if len(cached.ImportedRecords) != 0 {
			t.Fatalf("%s: readManifestCached holds %d ImportedRecords, want 0", stage, len(cached.ImportedRecords))
		}
	}

	rewriteAsLegacy := func(t *testing.T, dir string) {
		t.Helper()
		var dedupe map[string]bool
		if err := readGob(filepath.Join(dir, importedFile), &dedupe); err != nil {
			t.Fatalf("read %s: %v", importedFile, err)
		}
		if err := os.Remove(filepath.Join(dir, importedFile)); err != nil {
			t.Fatal(err)
		}
		var core manifestCore
		if err := readGob(filepath.Join(dir, "manifest.gob"), &core); err != nil {
			t.Fatal(err)
		}
		core.ImportedRecords = dedupe
		if err := writeGobAtomic(filepath.Join(dir, "manifest.gob"), core); err != nil {
			t.Fatal(err)
		}
		invalidateManifestCache(dir)
	}

	t.Run("sidecar survives append replace forget and rebuild", func(t *testing.T) {
		dir, proj, batchDir := setupStore(t, false)
		assertSidecarSplit(t, dir, "after import")

		before, err := os.ReadFile(filepath.Join(dir, importedFile))
		if err != nil {
			t.Fatal(err)
		}
		if n, importErr := Import(dir, batchDir); importErr != nil || n != 0 {
			t.Fatalf("no-op import n=%d err=%v", n, importErr)
		}
		afterNoop, err := os.ReadFile(filepath.Join(dir, importedFile))
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(before, afterNoop) {
			t.Fatalf("no-op import rewrote %s", importedFile)
		}

		// Append incremental update.
		f, err := os.OpenFile(filepath.Join(proj, "local1.jsonl"), os.O_WRONLY|os.O_APPEND, 0o644)
		if err != nil {
			t.Fatal(err)
		}
		if _, writeErr := f.WriteString(`{"type":"assistant","sessionId":"local1","timestamp":"2026-01-02T03:05:05Z","message":{"role":"assistant","content":"sidecarlocalneedle appended"}}` + "\n"); writeErr != nil {
			_ = f.Close()
			t.Fatal(writeErr)
		}
		_ = f.Close()
		if err = Ensure(dir, "", false, nil); err != nil {
			t.Fatal(err)
		}
		assertSidecarSplit(t, dir, "after append")

		// Non-append replacePass (remove local1, add local2).
		if err = os.Remove(filepath.Join(proj, "local1.jsonl")); err != nil {
			t.Fatal(err)
		}
		local2 := `{"type":"user","sessionId":"local2","timestamp":"2026-01-03T03:04:05Z","message":{"role":"user","content":"sidecarlocalneedle two"}}` + "\n"
		if err = os.WriteFile(filepath.Join(proj, "local2.jsonl"), []byte(local2), 0o644); err != nil {
			t.Fatal(err)
		}
		if err = Ensure(dir, "", false, nil); err != nil {
			t.Fatal(err)
		}
		assertSidecarSplit(t, dir, "after replacePass")

		// Forget local session; sidecar stays intact.
		if _, forgetErr := Forget(dir, ForgetOptions{Session: "local2"}); forgetErr != nil {
			t.Fatal(forgetErr)
		}
		assertSidecarSplit(t, dir, "after forget")

		// Full rebuild; sidecar stays intact.
		if err = Ensure(dir, "", true, nil); err != nil {
			t.Fatal(err)
		}
		assertSidecarSplit(t, dir, "after rebuild")

		if n, importErr := Import(dir, batchDir); importErr != nil || n != 0 {
			t.Fatalf("re-import after lifecycle n=%d err=%v", n, importErr)
		}
	})

	t.Run("sidecar survives search-only replacePass", func(t *testing.T) {
		dir, proj, batchDir := setupStore(t, true)
		if err := os.Remove(filepath.Join(proj, "local1.jsonl")); err != nil {
			t.Fatal(err)
		}
		local2 := `{"type":"user","sessionId":"local2","timestamp":"2026-01-03T03:04:05Z","message":{"role":"user","content":"searchonly replace"}}` + "\n"
		if err := os.WriteFile(filepath.Join(proj, "local2.jsonl"), []byte(local2), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := Ensure(dir, "", false, nil); err != nil {
			t.Fatal(err)
		}
		assertSidecarSplit(t, dir, "search-only replacePass")
		if n, err := Import(dir, batchDir); err != nil || n != 0 {
			t.Fatalf("re-import under search-only n=%d err=%v", n, err)
		}
	})

	t.Run("legacy manifest migrates via replacePass", func(t *testing.T) {
		dir, proj, batchDir := setupStore(t, false)
		rewriteAsLegacy(t, dir)
		if err := os.Remove(filepath.Join(proj, "local1.jsonl")); err != nil {
			t.Fatal(err)
		}
		local2 := `{"type":"user","sessionId":"local2","timestamp":"2026-01-03T03:04:05Z","message":{"role":"user","content":"migrate replace"}}` + "\n"
		if err := os.WriteFile(filepath.Join(proj, "local2.jsonl"), []byte(local2), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := Ensure(dir, "", false, nil); err != nil {
			t.Fatal(err)
		}
		assertSidecarSplit(t, dir, "legacy replacePass")
		if n, err := Import(dir, batchDir); err != nil || n != 0 {
			t.Fatalf("re-import after replacePass migration n=%d err=%v", n, err)
		}
	})

	t.Run("legacy manifest migrates via rebuild", func(t *testing.T) {
		dir, _, batchDir := setupStore(t, false)
		rewriteAsLegacy(t, dir)
		if err := Ensure(dir, "", true, nil); err != nil {
			t.Fatal(err)
		}
		assertSidecarSplit(t, dir, "legacy rebuild")
		if n, err := Import(dir, batchDir); err != nil || n != 0 {
			t.Fatalf("re-import after rebuild migration n=%d err=%v", n, err)
		}
	})

	t.Run("legacy manifest migrates via noop import", func(t *testing.T) {
		dir, _, batchDir := setupStore(t, false)
		rewriteAsLegacy(t, dir)
		if n, err := Import(dir, batchDir); err != nil || n != 0 {
			t.Fatalf("legacy noop import n=%d err=%v", n, err)
		}
		assertSidecarSplit(t, dir, "legacy noop import")
	})

	t.Run("corrupt sidecar fails import rather than dropping dedupe", func(t *testing.T) {
		dir, _, batchDir := setupStore(t, false)
		if err := os.WriteFile(filepath.Join(dir, importedFile), []byte("not-gob"), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := Import(dir, batchDir); err == nil {
			t.Fatal("expected error importing with corrupt sidecar")
		}
	})
}
