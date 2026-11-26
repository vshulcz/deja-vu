package index

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/search"
	"github.com/vshulcz/deja-vu/internal/sources"
)

func execIndexSQLite(t *testing.T, db, sql string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(db), 0o755); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.CommandContext(t.Context(), "sqlite3", db, sql).CombinedOutput(); err != nil {
		t.Fatalf("sqlite3 %s: %v\n%s", db, err, out)
	}
}

func bumpFileTime(t *testing.T, path string, when time.Time) {
	t.Helper()
	if err := os.Chtimes(path, when, when); err != nil {
		t.Fatal(err)
	}
}

func seedDeltaCursorStore(t *testing.T, db string, count int, start time.Time) {
	t.Helper()
	var b strings.Builder
	b.WriteString("create table if not exists ItemTable (key text primary key, value text);\n")
	b.WriteString("create table if not exists cursorDiskKV (key text primary key, value text);\n")
	b.WriteString("insert or replace into ItemTable values('workbench.panel.output', '{\"open\":false}');\n")
	for i := 1; i <= count; i++ {
		ts1 := start.Add(time.Duration(i) * time.Second).UnixMilli()
		ts2 := start.Add(time.Duration(i)*time.Second + 500*time.Millisecond).UnixMilli()
		cid := fmt.Sprintf("cur-%02d", i)
		fmt.Fprintf(&b,
			"insert or replace into cursorDiskKV values('composerData:%s', json('{\"composerId\":\"%s\",\"name\":\"Cursor Session %02d\",\"createdAt\":%d,\"lastUpdatedAt\":%d,\"fullConversationHeadersOnly\":[{\"bubbleId\":\"b1\",\"type\":1},{\"bubbleId\":\"b2\",\"type\":2}]}'));\n",
			cid, cid, i, ts1, ts2,
		)
		fmt.Fprintf(&b,
			"insert or replace into cursorDiskKV values('bubbleId:%s:b1', json('{\"type\":1,\"text\":\"cursorseedneedle-%02d question\",\"timestamp\":%d,\"workspaceProjectDir\":\"/Users/alex/work/cursor-proj\"}'));\n",
			cid, i, ts1,
		)
		fmt.Fprintf(&b,
			"insert or replace into cursorDiskKV values('bubbleId:%s:b2', json('{\"type\":2,\"text\":\"cursorseedreply-%02d answer\",\"timestamp\":%d,\"workspaceProjectDir\":\"/Users/alex/work/cursor-proj\"}'));\n",
			cid, i, ts2,
		)
	}
	execIndexSQLite(t, db, b.String())
}

func TestSQLiteCursorCapturePersistenceAndItemTableZeroDecode(t *testing.T) {
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skip("sqlite3 CLI not available")
	}
	tmp := hermeticDBEnv(t)
	idxDir := filepath.Join(tmp, "index.db")

	cursorDB := filepath.Join(sources.CursorUserRoot(), "workspaceStorage", "ws1", "state.vscdb")
	baseTime := time.Now().Add(-20 * time.Minute).UTC().Truncate(time.Second)
	seedDeltaCursorStore(t, cursorDB, 5, baseTime)
	tick := time.Now().Add(-5 * time.Minute)
	bumpFileTime(t, cursorDB, tick)

	claudeFile := filepath.Join(sources.ClaudeRoot(), "-Users-alex-proj", "claude-1.jsonl")
	if err := os.MkdirAll(filepath.Dir(claudeFile), 0o755); err != nil {
		t.Fatal(err)
	}
	line1 := `{"type":"user","sessionId":"cl1","cwd":"/Users/alex/proj","timestamp":"2026-09-01T10:00:00Z","message":{"role":"user","content":"claudebaselineneedle"}}` + "\n"
	if err := os.WriteFile(claudeFile, []byte(line1), 0o644); err != nil {
		t.Fatal(err)
	}
	bumpFileTime(t, claudeFile, tick)

	if err := Ensure(idxDir, "", true, nil); err != nil {
		t.Fatalf("initial Ensure: %v", err)
	}
	m1, err := readManifest(idxDir)
	if err != nil {
		t.Fatal(err)
	}
	st1 := m1.Files[cursorDB]
	if st1.DBCursor == "" {
		t.Fatalf("m1.Files[%q].DBCursor is empty", cursorDB)
	}
	if st1.LastUpdated == 0 {
		t.Fatalf("m1.Files[%q].LastUpdated = 0, want > 0", cursorDB)
	}

	// Unrelated .jsonl append takes appendPass and preserves DB cursor across passes.
	tick = tick.Add(5 * time.Second)
	line2 := `{"type":"assistant","sessionId":"cl1","cwd":"/Users/alex/proj","timestamp":"2026-09-01T10:01:00Z","message":{"role":"assistant","content":"claudeappendneedle"}}` + "\n"
	if err = os.WriteFile(claudeFile, []byte(line1+line2), 0o644); err != nil {
		t.Fatal(err)
	}
	bumpFileTime(t, claudeFile, tick)

	if err = Ensure(idxDir, "", false, nil); err != nil {
		t.Fatalf("Ensure after claude append: %v", err)
	}
	m2, err := readManifest(idxDir)
	if err != nil {
		t.Fatal(err)
	}
	if m2.Files[cursorDB].DBCursor != st1.DBCursor {
		t.Fatalf("cursor changed across unrelated append: got %q, want %q", m2.Files[cursorDB].DBCursor, st1.DBCursor)
	}

	// ItemTable-only write: Cursor metadata changes DB mtime, delta query finds 0 rows.
	tick = tick.Add(5 * time.Second)
	execIndexSQLite(t, cursorDB, `insert or replace into ItemTable values('workbench.panel.output', '{"open":true}');`)
	bumpFileTime(t, cursorDB, tick)

	sources.ResetSQLiteDecodedRows()
	if err = Ensure(idxDir, "", false, nil); err != nil {
		t.Fatalf("Ensure after ItemTable write: %v", err)
	}
	if got := sources.SQLiteDecodedRows(); got != 0 {
		t.Fatalf("SQLiteDecodedRows after ItemTable-only write = %d, want 0", got)
	}
	m3, err := readManifest(idxDir)
	if err != nil {
		t.Fatal(err)
	}
	st3 := m3.Files[cursorDB]
	if st3.LastUpdated != m2.Files[cursorDB].LastUpdated {
		t.Fatalf("LastUpdated changed on ItemTable-only write: got %d, want %d", st3.LastUpdated, m2.Files[cursorDB].LastUpdated)
	}
	if st3.DBCursor == "" || st3.DBCursor != m2.Files[cursorDB].DBCursor {
		t.Fatalf("DBCursor after ItemTable-only write = %q, want non-empty preserved %q", st3.DBCursor, m2.Files[cursorDB].DBCursor)
	}
	if st3.MTime == m2.Files[cursorDB].MTime {
		t.Fatalf("MTime did not advance on ItemTable write: still %d", st3.MTime)
	}

	hits, err := Search(idxDir, search.Options{Query: "cursorseedneedle", Harness: "cursor", All: true})
	if err != nil || len(hits) != 5 {
		t.Fatalf("Cursor search hits = %d (err=%v), want 5", len(hits), err)
	}
}

func TestSQLiteEnsureIncrementalParityWithColdRebuild(t *testing.T) {
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skip("sqlite3 CLI not available")
	}
	tmp := hermeticDBEnv(t)
	incDir := filepath.Join(tmp, "inc.db")
	coldDir := filepath.Join(tmp, "cold.db")

	t0 := time.Now().Add(-20 * time.Minute).UTC().Truncate(time.Second)
	t1 := time.Now().Add(-1 * time.Minute).UTC().Truncate(time.Second)
	tick := time.Now().Add(-5 * time.Minute)

	// 1. Cursor globalStorage store
	cursorDB := filepath.Join(sources.CursorUserRoot(), "globalStorage", "state.vscdb")
	execIndexSQLite(t, cursorDB, fmt.Sprintf(`
create table cursorDiskKV (key text primary key, value text);
insert into cursorDiskKV values('composerData:cu-append', json('{"composerId":"cu-append","name":"Cursor Append Base","createdAt":%d,"lastUpdatedAt":%d,"fullConversationHeadersOnly":[{"bubbleId":"b1","type":1}]}'));
insert into cursorDiskKV values('bubbleId:cu-append:b1', json('{"type":1,"text":"cursorbaselineneedle turn one","timestamp":%d,"workspaceProjectDir":"/Users/alex/work/cur-proj"}'));
insert into cursorDiskKV values('composerData:cu-rename', json('{"composerId":"cu-rename","name":"Cursor Title Old","createdAt":%d,"lastUpdatedAt":%d,"fullConversationHeadersOnly":[{"bubbleId":"b1","type":1}]}'));
insert into cursorDiskKV values('bubbleId:cu-rename:b1', json('{"type":1,"text":"cursorrenameneedle body","timestamp":%d,"workspaceProjectDir":"/Users/alex/work/cur-proj"}'));
insert into cursorDiskKV values('composerData:cu-quiet', json('{"composerId":"cu-quiet","name":"Cursor Quiet","createdAt":%d,"lastUpdatedAt":%d,"fullConversationHeadersOnly":[{"bubbleId":"b1","type":1}]}'));
insert into cursorDiskKV values('bubbleId:cu-quiet:b1', json('{"type":1,"text":"cursorquietneedle body","timestamp":%d,"workspaceProjectDir":"/Users/alex/work/cur-proj"}'));
`,
		t0.UnixMilli(), t0.Add(10*time.Second).UnixMilli(), t0.Add(10*time.Second).UnixMilli(),
		t0.UnixMilli(), t0.Add(20*time.Second).UnixMilli(), t0.Add(20*time.Second).UnixMilli(),
		t0.UnixMilli(), t0.Add(5*time.Second).UnixMilli(), t0.Add(5*time.Second).UnixMilli(),
	))
	bumpFileTime(t, cursorDB, tick)

	// 2. OpenCode v1 SQLite store
	opencodeDB := sources.OpencodeDB()
	ocParentDir := filepath.Join(sources.Home(), "work", "oc-parent")
	ocOtherDir := filepath.Join(sources.Home(), "work", "oc-other")
	t0Ms := t0.UnixMilli()
	execIndexSQLite(t, opencodeDB, fmt.Sprintf(`
create table project(id text primary key, worktree text);
create table session(id text primary key, project_id text, parent_id text, directory text, title text, time_created integer, time_updated integer);
create table message(id text primary key, session_id text, time_created integer, time_updated integer, data text);
create table part(id text primary key, message_id text, session_id text, time_created integer, time_updated integer, data text);
insert into project values('proj-parent', %q), ('proj-other', %q);
insert into session values('oc-parent', 'proj-parent', null, %q, 'OpenCode Parent', %d, %d);
insert into message values('m-ocp', 'oc-parent', %d, %d, '{"role":"user"}');
insert into part values('p-ocp', 'm-ocp', 'oc-parent', %d, %d, '{"type":"text","text":"ocparentneedle question"}');
insert into session values('oc-append', 'proj-other', null, %q, 'OpenCode Append', %d, %d);
insert into message values('m-oca1', 'oc-append', %d, %d, '{"role":"user"}');
insert into part values('p-oca1', 'm-oca1', 'oc-append', %d, %d, '{"type":"text","text":"ocappendbaseneedle turn one"}');
insert into session values('oc-rename', 'proj-other', null, %q, 'OpenCode Old Title', %d, %d);
insert into message values('m-ocr1', 'oc-rename', %d, %d, '{"role":"user"}');
insert into part values('p-ocr1', 'm-ocr1', 'oc-rename', %d, %d, '{"type":"text","text":"ocrenameneedle body"}');
insert into session values('oc-stream', 'proj-other', null, %q, 'OpenCode Stream', %d, %d);
insert into message values('m-ocs1', 'oc-stream', %d, %d, '{"role":"assistant"}');
insert into part values('p-ocs1', 'm-ocs1', 'oc-stream', %d, %d, '{"type":"text","text":"ocstreambaseneedle"}');
`,
		ocParentDir, ocOtherDir,
		ocParentDir, t0Ms, t0Ms+5000, t0Ms+5000, t0Ms+5000, t0Ms+5000, t0Ms+5000,
		ocOtherDir, t0Ms, t0Ms+5000, t0Ms+5000, t0Ms+5000, t0Ms+5000, t0Ms+5000,
		ocOtherDir, t0Ms, t0Ms+5000, t0Ms+5000, t0Ms+5000, t0Ms+5000, t0Ms+5000,
		ocOtherDir, t0Ms, t0Ms+5000, t0Ms+5000, t0Ms+5000, t0Ms+5000, t0Ms+5000,
	))
	bumpFileTime(t, opencodeDB, tick)

	// 3. Goose SQLite store (including an empty session deleted before VACUUM)
	gooseDB := sources.GooseDB()
	t0Sec := t0.Unix()
	t0Str := t0.Add(5 * time.Second).Format(time.RFC3339)
	execIndexSQLite(t, gooseDB, fmt.Sprintf(`
create table sessions (id text primary key, name text, description text, working_dir text, created_at text, updated_at text);
create table messages (id integer primary key autoincrement, session_id text, role text, content_json text, created_timestamp integer);
insert into sessions values('gs-append', 'Goose Append', '', '/work/gs', %q, %q);
insert into messages(session_id, role, content_json, created_timestamp) values('gs-append', 'user', '[{"type":"text","text":"goosebaselineneedle turn one"}]', %d);
insert into sessions values('gs-rename', 'gs-rename', 'Goose Old Title', '/work/gs', %q, %q);
insert into messages(session_id, role, content_json, created_timestamp) values('gs-rename', 'user', '[{"type":"text","text":"gooserenameneedle body"}]', %d);
insert into sessions values('gs-quiet', 'Goose Quiet', '', '/work/gs', %q, %q);
insert into messages(session_id, role, content_json, created_timestamp) values('gs-quiet', 'user', '[{"type":"text","text":"goosequietneedle body"}]', %d);
insert into sessions values('gs-empty', 'Empty', '', '/work/gs', %q, %q);
`,
		t0Str, t0Str, t0Sec,
		t0Str, t0Str, t0Sec,
		t0Str, t0Str, t0Sec,
		t0Str, t0Str,
	))
	bumpFileTime(t, gooseDB, tick)

	if err := Ensure(incDir, "", true, nil); err != nil {
		t.Fatalf("initial Ensure: %v", err)
	}

	// Apply delta mutations across all three DBs.
	tick = tick.Add(10 * time.Second)
	t1Ms := t1.UnixMilli()

	execIndexSQLite(t, cursorDB, fmt.Sprintf(`
insert or replace into cursorDiskKV values('bubbleId:cu-append:b2', json('{"type":2,"text":"cursorappendneedle turn two","timestamp":%d,"workspaceProjectDir":"/Users/alex/work/cur-proj"}'));
insert or replace into cursorDiskKV values('composerData:cu-append', json('{"composerId":"cu-append","name":"Cursor Append Base","createdAt":%d,"lastUpdatedAt":%d,"fullConversationHeadersOnly":[{"bubbleId":"b1","type":1},{"bubbleId":"b2","type":2}]}'));
insert or replace into cursorDiskKV values('composerData:cu-rename', json('{"composerId":"cu-rename","name":"Cursor Title New","createdAt":%d,"lastUpdatedAt":%d,"fullConversationHeadersOnly":[{"bubbleId":"b1","type":1}]}'));
`,
		t1Ms, t0.UnixMilli(), t1Ms,
		t0.UnixMilli(), t1Ms+5000,
	))
	bumpFileTime(t, cursorDB, tick)

	execIndexSQLite(t, opencodeDB, fmt.Sprintf(`
insert into message values('m-oca2', 'oc-append', %d, %d, '{"role":"assistant"}');
insert into part values('p-oca2', 'm-oca2', 'oc-append', %d, %d, '{"type":"text","text":"ocappendneedle turn two"}');
update session set time_updated = %d where id = 'oc-append';
insert into session values('oc-child', 'proj-parent', 'oc-parent', '', 'OpenCode Child Agent', %d, %d);
insert into message values('m-occ1', 'oc-child', %d, %d, '{"role":"user"}');
insert into part values('p-occ1', 'm-occ1', 'oc-child', %d, %d, '{"type":"text","text":"ocsubagentneedle task"}');
insert into session values('oc-nodir', '', null, '', 'OpenCode No Dir', %d, %d);
insert into message values('m-ocn1', 'oc-nodir', %d, %d, '{"role":"user"}');
insert into part values('p-ocn1', 'm-ocn1', 'oc-nodir', %d, %d, '{"type":"text","text":"ocnodirneedle standalone"}');
update session set title = 'OpenCode New Title', time_updated = %d where id = 'oc-rename';
update part set data = '{"type":"text","text":"ocstreambaseneedle ocstreamedneedle"}', time_updated = %d where id = 'p-ocs1';
`,
		t1Ms, t1Ms, t1Ms, t1Ms, t1Ms,
		t1Ms, t1Ms, t1Ms, t1Ms, t1Ms, t1Ms,
		t1Ms, t1Ms, t1Ms, t1Ms, t1Ms, t1Ms,
		t1Ms, t1Ms,
	))
	bumpFileTime(t, opencodeDB, tick)

	t1Str := t1.Format(time.RFC3339)
	t1RenameStr := t1.Add(5 * time.Second).Format(time.RFC3339)
	t1PostVacStr := t1.Add(10 * time.Second).Format(time.RFC3339)
	execIndexSQLite(t, gooseDB, fmt.Sprintf(`
insert into messages(session_id, role, content_json, created_timestamp) values('gs-append', 'assistant', '[{"type":"text","text":"gooseappendneedle reply"}]', %d);
update sessions set updated_at = %q where id = 'gs-append';
update sessions set description = 'Goose New Title', updated_at = %q where id = 'gs-rename';
delete from sessions where id = 'gs-empty';
vacuum;
insert into sessions values('gs-postvac', 'Goose Post Vacuum', '', '/work/gs', %q, %q);
insert into messages(session_id, role, content_json, created_timestamp) values('gs-postvac', 'user', '[{"type":"text","text":"goosepostvacuumneedle prompt"}]', %d);
`,
		t1.Unix(), t1Str, t1RenameStr,
		t1PostVacStr, t1PostVacStr, t1.Add(10*time.Second).Unix(),
	))
	bumpFileTime(t, gooseDB, tick)

	sources.ResetSQLiteDecodedRows()
	if err := Ensure(incDir, "", false, nil); err != nil {
		t.Fatalf("incremental Ensure: %v", err)
	}
	if got := sources.SQLiteDecodedRows(); got == 0 {
		t.Fatalf("SQLiteDecodedRows after SQLite delta updates = 0, want > 0")
	}

	if err := Ensure(coldDir, "", true, nil); err != nil {
		t.Fatalf("cold Rebuild: %v", err)
	}

	incM, err := readManifest(incDir)
	if err != nil {
		t.Fatal(err)
	}
	coldM, err := readManifest(coldDir)
	if err != nil {
		t.Fatal(err)
	}

	if len(incM.Sessions) != len(coldM.Sessions) {
		incKeys := make([]string, 0, len(incM.Sessions))
		coldKeys := make([]string, 0, len(coldM.Sessions))
		for k := range incM.Sessions {
			incKeys = append(incKeys, k)
		}
		for k := range coldM.Sessions {
			coldKeys = append(coldKeys, k)
		}
		slices.Sort(incKeys)
		slices.Sort(coldKeys)
		t.Fatalf("session count mismatch: inc=%d (%v) cold=%d (%v)",
			len(incM.Sessions), incKeys, len(coldM.Sessions), coldKeys)
	}
	for k, coldMeta := range coldM.Sessions {
		incMeta, ok := incM.Sessions[k]
		if !ok {
			t.Fatalf("inc missing session %q", k)
		}
		if incMeta.Title != coldMeta.Title {
			t.Errorf("%s Title = %q, want %q", k, incMeta.Title, coldMeta.Title)
		}
		if incMeta.Project != coldMeta.Project {
			t.Errorf("%s Project = %q, want %q", k, incMeta.Project, coldMeta.Project)
		}
		if incMeta.Path != coldMeta.Path {
			t.Errorf("%s Path = %q, want %q", k, incMeta.Path, coldMeta.Path)
		}
		if incMeta.Kind != coldMeta.Kind || incMeta.Parent != coldMeta.Parent {
			t.Errorf("%s lineage = (%q,%q), want (%q,%q)", k, incMeta.Kind, incMeta.Parent, coldMeta.Kind, coldMeta.Parent)
		}
		if !incMeta.Updated.Equal(coldMeta.Updated) {
			t.Errorf("%s Updated = %v, want %v", k, incMeta.Updated, coldMeta.Updated)
		}
	}

	childMeta := incM.Sessions["opencode:oc-child"]
	if childMeta.Kind != "subagent" || childMeta.Parent != "oc-parent" || childMeta.Project != "work/oc-parent" || childMeta.Path != ocParentDir {
		t.Fatalf("opencode child metadata = %+v, want kind=subagent parent=oc-parent project=work/oc-parent path=%q", childMeta, ocParentDir)
	}
	if nodirMeta := incM.Sessions["opencode:oc-nodir"]; nodirMeta.Path != opencodeDB {
		t.Fatalf("opencode directory-less Path = %q, want db fallback %q", nodirMeta.Path, opencodeDB)
	}
	if got := incM.Sessions["cursor:cu-rename"].Title; got != "Cursor Title New" {
		t.Errorf("cursor rename Title = %q, want Cursor Title New", got)
	}
	if got := incM.Sessions["opencode:oc-rename"].Title; got != "OpenCode New Title" {
		t.Errorf("opencode rename Title = %q, want OpenCode New Title", got)
	}
	if got := incM.Sessions["goose:gs-rename"].Title; got != "Goose New Title" {
		t.Errorf("goose rename Title = %q, want Goose New Title", got)
	}

	needles := []string{
		"cursorbaselineneedle",
		"cursorappendneedle",
		"cursorrenameneedle",
		"cursorquietneedle",
		"ocparentneedle",
		"ocappendbaseneedle",
		"ocappendneedle",
		"ocsubagentneedle",
		"ocnodirneedle",
		"ocrenameneedle",
		"ocstreambaseneedle",
		"ocstreamedneedle",
		"goosebaselineneedle",
		"gooseappendneedle",
		"gooserenameneedle",
		"goosequietneedle",
		"goosepostvacuumneedle",
	}
	hitKeys := func(dir, q string) []string {
		ss, serr := Search(dir, search.Options{Query: q, All: true})
		if serr != nil {
			t.Fatalf("Search(%s, %q): %v", dir, q, serr)
		}
		out := make([]string, len(ss))
		for i, s := range ss {
			out[i] = s.Key()
		}
		slices.Sort(out)
		return out
	}
	for _, needle := range needles {
		gotInc := hitKeys(incDir, needle)
		gotCold := hitKeys(coldDir, needle)
		if len(gotInc) == 0 {
			t.Errorf("needle %q missing from incremental index", needle)
		}
		if !slices.Equal(gotInc, gotCold) {
			t.Errorf("needle %q hits differ: inc=%v cold=%v", needle, gotInc, gotCold)
		}
	}
}

func TestSQLiteEnsureUnreadableStorePreservesCursorAndRecovers(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("chmod 0000 is not enforced on windows or under root")
	}
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skip("sqlite3 CLI not available")
	}
	tmp := hermeticDBEnv(t)
	idxDir := filepath.Join(tmp, "index.db")

	cursorDB := filepath.Join(sources.CursorUserRoot(), "globalStorage", "state.vscdb")
	baseTime := time.Now().Add(-20 * time.Minute).UTC().Truncate(time.Second)
	seedDeltaCursorStore(t, cursorDB, 2, baseTime)
	tick := time.Now().Add(-5 * time.Minute)
	bumpFileTime(t, cursorDB, tick)

	claudeFile := filepath.Join(sources.ClaudeRoot(), "-Users-alex-proj", "claude-1.jsonl")
	if err := os.MkdirAll(filepath.Dir(claudeFile), 0o755); err != nil {
		t.Fatal(err)
	}
	origClaude := `{"type":"user","sessionId":"cl1","cwd":"/Users/alex/proj","timestamp":"2026-09-01T10:00:00Z","message":{"role":"user","content":"claudepreserveneedle"}}` + "\n"
	if err := os.WriteFile(claudeFile, []byte(origClaude), 0o644); err != nil {
		t.Fatal(err)
	}
	bumpFileTime(t, claudeFile, tick)

	if err := Ensure(idxDir, "", true, nil); err != nil {
		t.Fatalf("initial Ensure: %v", err)
	}
	mBefore, err := readManifest(idxDir)
	if err != nil {
		t.Fatal(err)
	}
	before := mBefore.Files[cursorDB]
	if before.DBCursor == "" || before.LastUpdated == 0 {
		t.Fatalf("initial FileState missing cursor/watermark: %+v", before)
	}

	// Mutate cursorDB, lock down permissions, and rewrite Claude transcript to drive replacePass.
	tick = tick.Add(10 * time.Second)
	t1 := time.Now().Add(-1 * time.Minute).UTC().Truncate(time.Second)
	execIndexSQLite(t, cursorDB, fmt.Sprintf(`
insert or replace into cursorDiskKV values('bubbleId:cur-01:b3', json('{"type":1,"text":"recoveryneedle arrived while locked","timestamp":%d,"workspaceProjectDir":"/Users/alex/work/cursor-proj"}'));
insert or replace into cursorDiskKV values('composerData:cur-01', json('{"composerId":"cur-01","name":"Cursor Session 01","createdAt":%d,"lastUpdatedAt":%d,"fullConversationHeadersOnly":[{"bubbleId":"b1","type":1},{"bubbleId":"b2","type":2},{"bubbleId":"b3","type":1}]}'));
`, t1.UnixMilli(), baseTime.Add(time.Second).UnixMilli(), t1.UnixMilli()))
	bumpFileTime(t, cursorDB, tick)

	t.Cleanup(func() { _ = os.Chmod(cursorDB, 0o644) })
	if err = os.Chmod(cursorDB, 0o000); err != nil {
		t.Fatal(err)
	}
	rewrittenClaude := `{"type":"user","sessionId":"cl1","cwd":"/Users/alex/proj","timestamp":"2026-09-01T10:00:00Z","message":{"role":"user","content":"clauderewrittenneedle"}}` + "\n"
	if err = os.WriteFile(claudeFile, []byte(rewrittenClaude), 0o644); err != nil {
		t.Fatal(err)
	}
	bumpFileTime(t, claudeFile, tick)

	_ = Ensure(idxDir, "", false, nil)
	mMid, err := readManifest(idxDir)
	if err != nil {
		t.Fatal(err)
	}
	mid := mMid.Files[cursorDB]
	if mid.LastUpdated != before.LastUpdated || mid.DBCursor != before.DBCursor {
		t.Fatalf("unreadable DB lost state: mid=(%d,%q) before=(%d,%q)", mid.LastUpdated, mid.DBCursor, before.LastUpdated, before.DBCursor)
	}
	hitsMid, err := Search(idxDir, search.Options{Query: "cursorseedneedle", Harness: "cursor", All: true})
	if err != nil || len(hitsMid) != 2 {
		t.Fatalf("baseline cursor sessions after unreadable skip = %d (err=%v), want 2", len(hitsMid), err)
	}

	if err = os.Chmod(cursorDB, 0o644); err != nil {
		t.Fatal(err)
	}
	if err = Ensure(idxDir, "", false, nil); err != nil {
		t.Fatalf("recovery Ensure: %v", err)
	}
	hitsAfter, err := Search(idxDir, search.Options{Query: "recoveryneedle", Harness: "cursor", All: true})
	if err != nil || len(hitsAfter) != 1 {
		t.Fatalf("recoveryneedle hits = %d (err=%v), want 1", len(hitsAfter), err)
	}
	mAfter, err := readManifest(idxDir)
	if err != nil {
		t.Fatal(err)
	}
	if mAfter.Files[cursorDB].DBCursor == "" || mAfter.Files[cursorDB].DBCursor == before.DBCursor {
		t.Fatalf("recovery DBCursor = %q, want updated != %q", mAfter.Files[cursorDB].DBCursor, before.DBCursor)
	}
}
