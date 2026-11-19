package index

import (
	"bytes"
	"encoding/json"
	"fmt"
	"github.com/vshulcz/deja-vu/internal/query"
	"github.com/vshulcz/deja-vu/internal/search"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"testing"
	"time"
)

// The line the replacement-grade incremental pass prints is on the path every
// user hits: the first search or blame after a day's work prints it above the
// answer. It read "incremental index changed_files=306 removed_files=0
// sessions=1789" — a log line, in a stream where every other line is a
// sentence. This pins the register, not the exact wording.
func TestTheIncrementalPassSpeaksInASentence(t *testing.T) {
	tmp := t.TempDir()
	setHome(t, tmp)
	root := filepath.Join(tmp, "claude", "projects", "-work-pool")
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "claude", "projects"))
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}

	write := func(name, body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(root, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	session := func(id, text string) string {
		return `{"type":"user","sessionId":"` + id + `","timestamp":"2026-09-01T10:00:00Z","message":{"role":"user","content":"` + text + `"}}
{"type":"assistant","sessionId":"` + id + `","timestamp":"2026-09-01T10:01:00Z","message":{"role":"assistant","content":"the pool retires connections before the proxy does"}}
`
	}

	write("aaa1.jsonl", session("aaa1", "connections die five minutes after a deploy"))
	dir := filepath.Join(tmp, "index.db")
	if err := Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}

	// Rewritten rather than appended to: the tail cannot be resumed, so the
	// next pass is replacement-grade, which is the one that prints this line.
	write("aaa1.jsonl", session("aaa1", "connections die four minutes after a deploy"))
	write("bbb2.jsonl", session("bbb2", "the reindex job runs out of memory at eighty percent"))
	var progress bytes.Buffer
	if err := Ensure(dir, "", false, &progress); err != nil {
		t.Fatal(err)
	}
	out := progress.String()

	for _, leak := range []string{"changed_files=", "removed_files=", "sessions="} {
		if strings.Contains(out, leak) {
			t.Errorf("the pass printed %q, which is a log line rather than something a reader was meant to see:\n%s", leak, out)
		}
	}
	if !strings.Contains(out, "transcript") {
		t.Errorf("the pass said nothing about the transcripts it re-read:\n%s", out)
	}
	// Whatever it says, it says it once and about a real count: zero is left
	// out rather than printed as "0 gone".
	if strings.Contains(out, " 0 ") {
		t.Errorf("the pass printed a zero count:\n%s", out)
	}
}

// Every way an index is brought up to date must leave what a forced rebuild
// of the same sources leaves: sessions, records, postings and sidecars.

// twoWayEnv is a hermetic claude store plus the index dir.
type twoWayEnv struct {
	t    *testing.T
	tmp  string
	root string
}

func newTwoWayEnv(t *testing.T) *twoWayEnv {
	t.Helper()
	tmp := t.TempDir()
	claudeRoot := filepath.Join(tmp, "claude")
	if err := os.MkdirAll(claudeRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	setHome(t, filepath.Join(tmp, "home"))
	t.Setenv("DEJA_CLAUDE_ROOT", claudeRoot)
	t.Setenv("DEJA_CODEX_ROOT", filepath.Join(tmp, "no-codex"))
	t.Setenv("DEJA_OPENCODE_DB", filepath.Join(tmp, "no-opencode.db"))
	t.Setenv("DEJA_GEMINI_ROOT", filepath.Join(tmp, "no-gemini"))
	t.Setenv("DEJA_CURSOR_ROOT", filepath.Join(tmp, "no-cursor"))
	t.Setenv("DEJA_CURSOR_CLI_ROOT", filepath.Join(tmp, "no-cursor-cli"))
	t.Setenv("DEJA_ANTIGRAVITY_ROOT", filepath.Join(tmp, "no-antigravity"))
	t.Setenv("DEJA_AIDER_ROOTS", filepath.Join(tmp, "no-aider"))
	t.Setenv("DEJA_NOTES_FILE", filepath.Join(tmp, "notes.jsonl"))
	return &twoWayEnv{t: t, tmp: tmp, root: claudeRoot}
}

func (h *twoWayEnv) path(project, sid string) string {
	return filepath.Join(h.root, "-Users-me-"+project, sid+".jsonl")
}

func (h *twoWayEnv) put(project, sid, body string) {
	h.t.Helper()
	p := h.path(project, sid)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		h.t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		h.t.Fatal(err)
	}
}

func (h *twoWayEnv) add(project, sid, body string) {
	h.t.Helper()
	f, err := os.OpenFile(h.path(project, sid), os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		h.t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	if _, err := f.WriteString(body); err != nil {
		h.t.Fatal(err)
	}
}

// mustJSON is json.Marshal for fixtures, which always encode.
func mustJSON(v any) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return b
}

func hjson(v any) string {
	b := mustJSON(v)
	return string(b) + "\n"
}

func hts(day, minute int) string { return fmt.Sprintf("2026-06-%02dT10:%02d:00Z", day, minute) }

func hPrompt(project, sid string, day, minute int, text string) string {
	return hjson(map[string]any{"type": "user", "sessionId": sid, "timestamp": hts(day, minute), "cwd": "/Users/me/" + project,
		"message": map[string]any{"role": "user", "content": text}})
}

func hSay(project, sid string, day, minute int, text string) string {
	return hjson(map[string]any{"type": "assistant", "sessionId": sid, "timestamp": hts(day, minute), "cwd": "/Users/me/" + project,
		"message": map[string]any{"role": "assistant", "content": []any{map[string]any{"type": "text", "text": text}}}})
}

func hBash(project, sid string, day, minute int, id, cmd, out string, failed bool) string {
	a := map[string]any{"type": "assistant", "sessionId": sid, "timestamp": hts(day, minute), "cwd": "/Users/me/" + project,
		"message": map[string]any{"role": "assistant", "content": []any{map[string]any{"type": "tool_use", "id": id, "name": "Bash", "input": map[string]any{"command": cmd}}}}}
	r := map[string]any{"type": "user", "sessionId": sid, "timestamp": hts(day, minute), "cwd": "/Users/me/" + project,
		"message": map[string]any{"role": "user", "content": []any{map[string]any{"type": "tool_result", "tool_use_id": id, "is_error": failed, "content": out}}}}
	return hjson(a) + hjson(r)
}

func hEdit(project, sid string, day, minute int, id, file, old, updated string) string {
	a := map[string]any{"type": "assistant", "sessionId": sid, "timestamp": hts(day, minute), "cwd": "/Users/me/" + project,
		"message": map[string]any{"role": "assistant", "content": []any{map[string]any{"type": "tool_use", "id": id, "name": "Edit",
			"input": map[string]any{"file_path": "/Users/me/" + project + "/" + file, "old_string": old, "new_string": updated}}}}}
	r := map[string]any{"type": "user", "sessionId": sid, "timestamp": hts(day, minute), "cwd": "/Users/me/" + project,
		"message": map[string]any{"role": "user", "content": []any{map[string]any{"type": "tool_result", "tool_use_id": id, "content": "The file has been updated."}}}}
	return hjson(a) + hjson(r)
}

// richSession has a failure, a remedy and a pass, so fixes, commands,
// command failures and session facts all have something to say about it.
func richSession(project, sid string, day int, topic string) string {
	return hPrompt(project, sid, day, 0, "the "+topic+" build fails with undefined symbol, please fix") +
		hSay(project, sid, day, 1, "Looking at the "+topic+" package now.") +
		hBash(project, sid, day, 2, sid+"a", "go build ./...", "# example.com/app\n./main.go:12:2: undefined: zorbleFrobnicate\nexit status 1", true) +
		hEdit(project, sid, day, 3, sid+"e", "main.go", "zorbleFrobnicate()", "frobnicate()") +
		hBash(project, sid, day, 4, sid+"b", "go mod tidy", "go: downloading example.com/frob v1.2.3", false) +
		hBash(project, sid, day, 5, sid+"c", "go build ./...", "", false) +
		hBash(project, sid, day, 6, sid+"d", "npm run lint", "npm ERR! missing script: lint", true) +
		hSay(project, sid, day, 7, "The "+topic+" build passes now; lint script is missing.")
}

// snap is everything two builds of the same sources must agree on.
type snap struct {
	sessions map[string]string
	records  []string
	postings map[string]string
	sidecars map[string]string
}

func takeSnap(t *testing.T, dir string) snap {
	t.Helper()
	m, err := readManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	s := snap{sessions: map[string]string{}, postings: map[string]string{}, sidecars: map[string]string{}}
	byOrd := map[uint32]string{}
	for k, meta := range m.Sessions {
		byOrd[meta.Ord] = k
		meta.Ord = 0
		b := mustJSON(meta)
		s.sessions[k] = inUTC(string(b))
	}
	recs, err := ReadRecords(dir)
	if err != nil {
		t.Fatal(err)
	}
	byOff := map[int64]Record{}
	for _, r := range recs {
		byOff[r.Offset] = r.Record
		s.records = append(s.records, fmt.Sprintf("%s|%s|%s|%s|%q", r.Record.Key, r.Record.Role, r.Record.Time.UTC().Format("2006-01-02T15:04:05.000"), r.Record.SourcePath, r.Record.Text))
	}
	sort.Strings(s.records)
	checkRecordOffsets(t, dir, m, recs)
	files, _ := filepath.Glob(filepath.Join(dir, "buckets", "*.bin"))
	for _, f := range files {
		data, err := readBucket(f)
		if err != nil {
			t.Fatal(err)
		}
		for tok, ps := range data {
			if ps = liveOnly(ps, m.DeadRecords); len(ps) == 0 {
				continue
			}
			var parts []string
			for _, p := range ps {
				r, ok := byOff[p.Off]
				who := byOrd[p.Sid]
				if ok && r.Key != who {
					who += "!rec=" + r.Key
				}
				parts = append(parts, fmt.Sprintf("%s/%s/%v", who, r.Role, p.Tool))
				if !ok {
					parts[len(parts)-1] += "/dangling"
				}
			}
			sort.Strings(parts)
			s.postings[tok] = strings.Join(parts, ",")
		}
	}
	add := func(name string, v any) {
		b := mustJSON(v)
		s.sidecars[name] = inUTC(string(b))
	}
	add("compactions", m.Compactions)
	add("friction texts", checkFrictionTexts(t, dir, m))
	var kept []string
	for p, f := range m.Files {
		if f.Kept {
			kept = append(kept, filepath.Base(p))
		}
	}
	sort.Strings(kept)
	add("kept files", kept)
	fx := ReadFixes(dir)
	fxs := make([]string, 0, len(fx))
	for _, p := range fx {
		b := mustJSON(p)
		fxs = append(fxs, string(b))
	}
	sort.Strings(fxs)
	add("fixes", fxs)
	raw, _ := readFixRaw(dir)
	rws := make([]string, 0, len(raw))
	for _, p := range raw {
		b := mustJSON(p)
		rws = append(rws, string(b))
	}
	sort.Strings(rws)
	add("fixraw", rws)
	cmds := ReadCommands(dir)
	cs := make([]string, 0, len(cmds))
	for _, c := range cmds {
		b := mustJSON(c)
		cs = append(cs, string(b))
	}
	sort.Strings(cs)
	add("commands", cs)
	cf := ReadCommandFails(dir)
	cfs := make([]string, 0, len(cf))
	for _, c := range cf {
		b := mustJSON(c)
		cfs = append(cfs, string(b))
	}
	sort.Strings(cfs)
	add("commandfails", cfs)
	// cooccur.gob is carried, not rebuilt, by every incremental path, and
	// carrySidecars documents that staleness; it is left out on purpose.
	add("sessionfacts", ReadSessionFacts(dir))
	return s
}

func diffSnaps(t *testing.T, label string, got, want snap) int {
	t.Helper()
	n := 0
	report := func(format string, args ...any) {
		n++
		if n <= 25 {
			t.Errorf(label+": "+format, args...)
		}
	}
	for k, w := range want.sessions {
		if g, ok := got.sessions[k]; !ok {
			report("session %s missing from incremental", k)
		} else if g != w {
			report("session %s meta differs:\n  incr    %s\n  rebuild %s", k, g, w)
		}
	}
	for k := range got.sessions {
		if _, ok := want.sessions[k]; !ok {
			report("session %s only in incremental", k)
		}
	}
	gr, wr := map[string]int{}, map[string]int{}
	for _, r := range got.records {
		gr[r]++
	}
	for _, r := range want.records {
		wr[r]++
	}
	for r, c := range wr {
		if gr[r] != c {
			report("record x%d incr vs x%d rebuild: %s", gr[r], c, r)
		}
	}
	for r, c := range gr {
		if _, ok := wr[r]; !ok {
			report("record x%d only in incremental: %s", c, r)
		}
	}
	var toks []string
	for tok := range want.postings {
		toks = append(toks, tok)
	}
	for tok := range got.postings {
		if _, ok := want.postings[tok]; !ok {
			toks = append(toks, tok)
		}
	}
	sort.Strings(toks)
	for _, tok := range toks {
		if got.postings[tok] != want.postings[tok] {
			report("posting %q:\n  incr    %s\n  rebuild %s", tok, got.postings[tok], want.postings[tok])
		}
	}
	for name, w := range want.sidecars {
		if got.sidecars[name] != w {
			report("sidecar %s:\n  incr    %s\n  rebuild %s", name, got.sidecars[name], w)
		}
	}
	return n
}

var instantRE = regexp.MustCompile(`\d{4}-\d\d-\d\dT\d\d:\d\d:\d\d(\.\d+)?(Z|[+-]\d\d:\d\d)`)

// inUTC rewrites every RFC 3339 instant to UTC, so one instant read back in
// two zones is not a difference.
func inUTC(s string) string {
	return instantRE.ReplaceAllStringFunc(s, func(v string) string {
		tm, err := time.Parse(time.RFC3339Nano, v)
		if err != nil {
			return v
		}
		return tm.UTC().Format(time.RFC3339Nano)
	})
}

func copyDir(t *testing.T, from, to string) {
	t.Helper()
	if out, err := exec.CommandContext(t.Context(), "cp", "-R", from, to).CombinedOutput(); err != nil {
		t.Fatalf("cp: %v %s", err, out)
	}
}

// twoWays runs step on the store, then brings index A up to date
// incrementally and index B (a copy taken before step) by a forced rebuild.
func (h *twoWayEnv) twoWays(label string, seed func(), step func()) {
	t := h.t
	t.Helper()
	seed()
	a := filepath.Join(h.tmp, "a.db")
	if err := Ensure(a, "claude", false, nil); err != nil {
		t.Fatal(err)
	}
	b := filepath.Join(h.tmp, "b.db")
	copyDir(t, a, b)
	step()
	if err := Ensure(a, "claude", false, nil); err != nil {
		t.Fatal(err)
	}
	if err := Ensure(b, "claude", true, nil); err != nil {
		t.Fatal(err)
	}
	diffSnaps(t, label, takeSnap(t, a), takeSnap(t, b))
}

func TestAppendOnlyMatchesRebuild(t *testing.T) {
	h := newTwoWayEnv(t)
	h.twoWays("append", func() {
		h.put("app", "s1", richSession("app", "s1", 2, "pool"))
		h.put("app", "s2", hPrompt("app", "s2", 3, 0, "first question about caching"))
	}, func() {
		h.add("app", "s2", hBash("app", "s2", 3, 1, "s2a", "go build ./...", "# example.com/app\n./main.go:12:2: undefined: zorbleFrobnicate\nexit status 1", true)+
			hBash("app", "s2", 3, 2, "s2b", "go mod tidy", "ok", false)+
			hBash("app", "s2", 3, 3, "s2c", "go build ./...", "", false)+
			hBash("app", "s2", 3, 4, "s2d", "npm run lint", "npm ERR! missing script: lint", true))
		h.put("svc", "s3", richSession("svc", "s3", 4, "cache"))
	})
}

func TestRewriteMatchesRebuild(t *testing.T) {
	h := newTwoWayEnv(t)
	h.twoWays("rewrite", func() {
		h.put("app", "s1", richSession("app", "s1", 2, "pool"))
		h.put("app", "s2", richSession("app", "s2", 3, "queue"))
	}, func() {
		h.put("app", "s2", richSession("app", "s2", 3, "worker"))
		h.put("svc", "s3", richSession("svc", "s3", 4, "cache"))
	})
}

// `deja search --rebuild` goes through rebuildForSearch, `deja index --force`
// through rebuildWithTombstones. Both are full builds of the same stores.
func TestSearchRebuildMatchesIndexRebuild(t *testing.T) {
	h := newTwoWayEnv(t)
	h.put("app", "s1", richSession("app", "s1", 2, "pool"))
	h.put("app", "s2", richSession("app", "s2", 3, "queue"))
	h.put("svc", "s3", richSession("svc", "s3", 4, "cache"))
	a := filepath.Join(h.tmp, "a.db")
	if err := Ensure(a, "", false, nil); err != nil {
		t.Fatal(err)
	}
	// The client deleted one transcript; the index keeps it.
	if err := os.Remove(h.path("app", "s2")); err != nil {
		t.Fatal(err)
	}
	if err := Ensure(a, "", false, nil); err != nil {
		t.Fatal(err)
	}
	if !HasSession(a, "s2") {
		t.Fatal("setup: incremental pass dropped the deleted transcript's session")
	}
	b := filepath.Join(h.tmp, "b.db")
	copyDir(t, a, b)
	if err := EnsureForSearch(a, query.Options{All: true}, true, nil); err != nil {
		t.Fatal(err)
	}
	if err := Ensure(b, "", true, nil); err != nil {
		t.Fatal(err)
	}
	diffSnaps(t, "search-rebuild vs index-rebuild", takeSnap(t, a), takeSnap(t, b))
}

// forget then unforget of one session brings back what was there.
func TestForgetUnforgetRoundTrip(t *testing.T) {
	h := newTwoWayEnv(t)
	h.put("app", "s1", richSession("app", "s1", 2, "pool"))
	h.put("app", "s2", richSession("app", "s2", 3, "queue"))
	h.put("svc", "s3", richSession("svc", "s3", 4, "cache"))
	a := filepath.Join(h.tmp, "a.db")
	if err := Ensure(a, "", false, nil); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(h.path("svc", "s3")); err != nil {
		t.Fatal(err)
	}
	if err := Ensure(a, "", false, nil); err != nil {
		t.Fatal(err)
	}
	b := filepath.Join(h.tmp, "b.db")
	copyDir(t, a, b)
	if _, err := Forget(a, ForgetOptions{Session: "s2"}); err != nil {
		t.Fatal(err)
	}
	if n, err := Unforget(a, "s2", nil); err != nil || n != 1 {
		t.Fatalf("unforget: %d %v", n, err)
	}
	if err := Ensure(b, "", true, nil); err != nil {
		t.Fatal(err)
	}
	diffSnaps(t, "forget+unforget vs rebuild", takeSnap(t, a), takeSnap(t, b))
}

// importParitySnaps returns an index holding an imported batch as the import
// left it, and the same index rebuilt.
func importParitySnaps(t *testing.T) (snap, snap) {
	t.Helper()
	tmp := t.TempDir()
	setHome(t, filepath.Join(tmp, "home"))
	t.Setenv("DEJA_CODEX_ROOT", filepath.Join(tmp, "no-codex"))
	t.Setenv("DEJA_OPENCODE_DB", filepath.Join(tmp, "no-opencode.db"))
	t.Setenv("DEJA_GEMINI_ROOT", filepath.Join(tmp, "no-gemini"))
	t.Setenv("DEJA_CURSOR_ROOT", filepath.Join(tmp, "no-cursor"))
	t.Setenv("DEJA_CURSOR_CLI_ROOT", filepath.Join(tmp, "no-cursor-cli"))
	t.Setenv("DEJA_AIDER_ROOTS", filepath.Join(tmp, "no-aider"))
	mk := func(name string) *twoWayEnv {
		root := filepath.Join(tmp, name)
		t.Setenv("XDG_CONFIG_HOME", filepath.Join(tmp, "config-"+name))
		t.Setenv("DEJA_CLAUDE_ROOT", root)
		t.Setenv("DEJA_NOTES_FILE", filepath.Join(tmp, "notes-"+name+".jsonl"))
		return &twoWayEnv{t: t, tmp: tmp, root: root}
	}
	src := mk("src")
	src.put("app", "s1", richSession("app", "s1", 2, "pool"))
	src.put("app", "s2", richSession("app", "s2", 3, "queue"))
	srcDir := filepath.Join(tmp, "src.db")
	if err := Ensure(srcDir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	batch := filepath.Join(tmp, "batch")
	if err := os.MkdirAll(batch, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := Export(srcDir, batch); err != nil {
		t.Fatal(err)
	}
	dst := mk("dst")
	dst.put("svc", "s3", richSession("svc", "s3", 4, "cache"))
	a := filepath.Join(tmp, "a.db")
	if err := Ensure(a, "", false, nil); err != nil {
		t.Fatal(err)
	}
	if n, err := Import(a, batch); err != nil || n == 0 {
		t.Fatalf("import: %d %v", n, err)
	}
	b := filepath.Join(tmp, "b.db")
	copyDir(t, a, b)
	if err := Ensure(b, "", true, nil); err != nil {
		t.Fatal(err)
	}
	return takeSnap(t, a), takeSnap(t, b)
}

// The manifest row an import writes for a peer's session matches the one a
// rebuild of the same index writes for it.
func TestImportSessionMetaMatchesRebuild(t *testing.T) {
	got, want := importParitySnaps(t)
	got.sidecars, want.sidecars = nil, nil
	diffSnaps(t, "import vs rebuild", got, want)
}

// The sidecars after an import cover the imported sessions the way a rebuild's
// do.
func TestImportSidecarsMatchRebuild(t *testing.T) {
	got, want := importParitySnaps(t)
	got.sessions, want.sessions = nil, nil
	diffSnaps(t, "import vs rebuild", got, want)
}

func cursorSQL(t *testing.T, db, sql string) {
	t.Helper()
	if out, err := exec.CommandContext(t.Context(), "sqlite3", db, sql).CombinedOutput(); err != nil {
		t.Fatalf("sqlite3: %v: %s", err, out)
	}
}

// A database store is re-read whole on every change.
func TestCursorStoreMatchesRebuild(t *testing.T) {
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skip("no sqlite3")
	}
	h := newTwoWayEnv(t)
	cur := filepath.Join(h.tmp, "cursor-user")
	t.Setenv("DEJA_CURSOR_ROOT", cur)
	db := filepath.Join(cur, "globalStorage", "state.vscdb")
	if err := os.MkdirAll(filepath.Dir(db), 0o755); err != nil {
		t.Fatal(err)
	}
	cursorSQL(t, db, `create table cursorDiskKV (key text primary key, value text);
insert into cursorDiskKV values
 ('composerData:cu1', json('{"composerId":"cu1","name":"pool tuning","createdAt":1780394400000,"lastUpdatedAt":1780394700000}')),
 ('bubbleId:cu1:b1', json('{"type":1,"text":"why does the pool drop connections after four minutes","timestamp":1780394401000,"workspaceProjectDir":"/Users/me/app"}')),
 ('bubbleId:cu1:b2', json('{"type":2,"text":"the proxy retires idle connections; set MaxConnLifetime below it","timestamp":1780394402000}')),
 ('composerData:cu3', json('{"composerId":"cu3","name":"cache ttl","createdAt":1780567200000,"lastUpdatedAt":1780567300000}')),
 ('bubbleId:cu3:b1', json('{"type":1,"text":"what ttl should the cache use","timestamp":1780567201000,"workspaceProjectDir":"/Users/me/svc"}'));`)
	a := filepath.Join(h.tmp, "a.db")
	if err := Ensure(a, "cursor", false, nil); err != nil {
		t.Fatal(err)
	}
	b := filepath.Join(h.tmp, "b.db")
	copyDir(t, a, b)
	cursorSQL(t, db, `insert into cursorDiskKV values
 ('bubbleId:cu1:b3', json('{"type":1,"text":"and the retry budget for the queue worker","timestamp":1780653600000}')),
 ('composerData:cu2', json('{"composerId":"cu2","name":"queue worker","createdAt":1780653700000,"lastUpdatedAt":1780653800000}')),
 ('bubbleId:cu2:b1', json('{"type":1,"text":"the queue worker stalls on shutdown","timestamp":1780653701000,"workspaceProjectDir":"/Users/me/app"}'));
update cursorDiskKV set value = json('{"composerId":"cu1","name":"pool tuning","createdAt":1780394400000,"lastUpdatedAt":1780653600000}') where key = 'composerData:cu1';
delete from cursorDiskKV where key like '%cu3%';`)
	if err := Ensure(a, "cursor", false, nil); err != nil {
		t.Fatal(err)
	}
	if err := Ensure(b, "cursor", true, nil); err != nil {
		t.Fatal(err)
	}
	diffSnaps(t, "cursor", takeSnap(t, a), takeSnap(t, b))
}

// The turns a harness compacted out of its file survive `deja index --force`
// (#4795); `deja search --rebuild` is the same full build.
func TestSearchRebuildKeepsCompactedAwayTurns(t *testing.T) {
	tmp := hermeticIndexEnv(t)
	root := filepath.Join(tmp, "continue")
	t.Setenv("DEJA_CONTINUE_ROOT", root)
	p := filepath.Join(root, "sessions", "s1.json")
	a := filepath.Join(tmp, "a.db")
	stamp := time.Now().Add(-time.Hour)
	put := func(history string) {
		t.Helper()
		write(t, p, `{"sessionId":"s1","title":"t","workspaceDirectory":"/tmp/proj","history":[`+history+`]}`)
		stamp = stamp.Add(time.Minute)
		if err := os.Chtimes(p, stamp, stamp); err != nil {
			t.Fatal(err)
		}
		if err := Ensure(a, "", false, nil); err != nil {
			t.Fatal(err)
		}
	}
	put(`{"message":{"role":"user","content":"why does zanzibarflux break the build"}},{"message":{"role":"assistant","content":"quokkaplex is stale"}}`)
	put(`{"message":{"role":"assistant","content":"SUMMARY-ONE the build was fixed"},"conversationSummary":"SUMMARY-ONE the build was fixed"}`)
	b := filepath.Join(tmp, "b.db")
	copyDir(t, a, b)
	if err := EnsureForSearch(a, query.Options{All: true}, true, nil); err != nil {
		t.Fatal(err)
	}
	if err := Ensure(b, "", true, nil); err != nil {
		t.Fatal(err)
	}
	diffSnaps(t, "search-rebuild vs index-rebuild", takeSnap(t, a), takeSnap(t, b))
}

// An index left by an older deja version is rebuilt by whichever command runs
// first. A search-side command must not lose what `deja index` would keep.
func TestVersionUpgradeKeepsDeletedTranscripts(t *testing.T) {
	h := newTwoWayEnv(t)
	h.put("app", "s1", richSession("app", "s1", 2, "pool"))
	h.put("app", "s2", richSession("app", "s2", 3, "queue"))
	a := filepath.Join(h.tmp, "a.db")
	if err := Ensure(a, "", false, nil); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(h.path("app", "s2")); err != nil {
		t.Fatal(err)
	}
	if err := Ensure(a, "", false, nil); err != nil {
		t.Fatal(err)
	}
	m, err := readManifest(a)
	if err != nil {
		t.Fatal(err)
	}
	m.Version = version - 1
	if err := writeManifestOnly(a, m); err != nil {
		t.Fatal(err)
	}
	invalidateManifestCache(a)
	b := filepath.Join(h.tmp, "b.db")
	copyDir(t, a, b)
	if err := EnsureForSearch(a, query.Options{All: true}, false, nil); err != nil {
		t.Fatal(err)
	}
	if err := Ensure(b, "", false, nil); err != nil {
		t.Fatal(err)
	}
	if !HasSession(b, "s2") {
		t.Fatal("setup: deja index dropped the kept session too")
	}
	if !HasSession(a, "s2") {
		t.Errorf("after a version upgrade the search path dropped kept session s2; deja index kept it")
	}
}

func TestRenameMatchesRebuild(t *testing.T) {
	h := newTwoWayEnv(t)
	h.twoWays("rename", func() {
		h.put("app", "s1", richSession("app", "s1", 2, "pool"))
		h.put("app", "s2", richSession("app", "s2", 3, "queue"))
	}, func() {
		to := h.path("app2", "s2")
		if err := os.MkdirAll(filepath.Dir(to), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Rename(h.path("app", "s2"), to); err != nil {
			t.Fatal(err)
		}
	})
}

// `codex archive` moves a rollout from sessions/ to archived_sessions/.
func TestCodexArchiveMatchesRebuild(t *testing.T) {
	h := newTwoWayEnv(t)
	codex := filepath.Join(h.tmp, "codex")
	t.Setenv("DEJA_CODEX_ROOT", codex)
	rollout := func(id, day, text string) string {
		return hjson(map[string]any{"type": "session_meta", "timestamp": "2026-06-" + day + "T10:00:00Z", "payload": map[string]any{"id": id, "session_id": id, "cwd": "/Users/me/app"}}) +
			hjson(map[string]any{"timestamp": "2026-06-" + day + "T10:00:01Z", "type": "response_item", "payload": map[string]any{"type": "message", "role": "user", "content": []any{map[string]any{"type": "input_text", "text": text}}}}) +
			hjson(map[string]any{"timestamp": "2026-06-" + day + "T10:00:02Z", "type": "response_item", "payload": map[string]any{"type": "function_call", "name": "shell", "call_id": "c1", "arguments": `{"command":["bash","-lc","go build ./..."]}`}}) +
			hjson(map[string]any{"timestamp": "2026-06-" + day + "T10:00:03Z", "type": "response_item", "payload": map[string]any{"type": "function_call_output", "call_id": "c1", "output": "./main.go:12:2: undefined: zorbleFrobnicate\nexit status 1"}})
	}
	live := filepath.Join(codex, "sessions", "2026", "06", "02", "rollout-2026-06-02T10-00-00-x1.jsonl")
	write(t, live, rollout("x1", "02", "why does the codex build fail"))
	write(t, filepath.Join(codex, "sessions", "2026", "06", "03", "rollout-2026-06-03T10-00-00-x2.jsonl"), rollout("x2", "03", "second codex session"))
	a := filepath.Join(h.tmp, "a.db")
	if err := Ensure(a, "codex", false, nil); err != nil {
		t.Fatal(err)
	}
	b := filepath.Join(h.tmp, "b.db")
	copyDir(t, a, b)
	arch := filepath.Join(codex, "archived_sessions", filepath.Base(live))
	if err := os.MkdirAll(filepath.Dir(arch), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(live, arch); err != nil {
		t.Fatal(err)
	}
	if err := Ensure(a, "codex", false, nil); err != nil {
		t.Fatal(err)
	}
	if err := Ensure(b, "codex", true, nil); err != nil {
		t.Fatal(err)
	}
	diffSnaps(t, "codex archive", takeSnap(t, a), takeSnap(t, b))
}

// The project directory itself renamed, the way a moved checkout shows up.
func TestProjectDirRenameMatchesRebuild(t *testing.T) {
	h := newTwoWayEnv(t)
	h.twoWays("dir rename", func() {
		h.put("app", "s1", richSession("app", "s1", 2, "pool"))
		h.put("app", "s2", richSession("app", "s2", 3, "queue"))
		h.put("svc", "s3", richSession("svc", "s3", 4, "cache"))
	}, func() {
		if err := os.Rename(filepath.Dir(h.path("app", "s1")), filepath.Dir(h.path("app2", "s1"))); err != nil {
			t.Fatal(err)
		}
	})
}

// A session whose transcript was deleted, then a new transcript appended to
// the same project in the same pass.
func TestDeleteAndAppendMatchesRebuild(t *testing.T) {
	h := newTwoWayEnv(t)
	h.twoWays("delete+append", func() {
		h.put("app", "s1", richSession("app", "s1", 2, "pool"))
		h.put("app", "s2", richSession("app", "s2", 3, "queue"))
	}, func() {
		if err := os.Remove(h.path("app", "s2")); err != nil {
			t.Fatal(err)
		}
		h.add("app", "s1", hPrompt("app", "s1", 2, 30, "one more question about the pool"))
	})
}

func TestDeleteMatchesRebuild(t *testing.T) {
	h := newTwoWayEnv(t)
	h.twoWays("delete", func() {
		h.put("app", "s1", richSession("app", "s1", 2, "pool"))
		h.put("app", "s2", richSession("app", "s2", 3, "queue"))
		h.put("svc", "s3", richSession("svc", "s3", 4, "cache"))
	}, func() {
		if err := os.Remove(h.path("app", "s2")); err != nil {
			t.Fatal(err)
		}
	})
}

// A line the store wrote twice, the copy arriving in a later append: the
// update wrote a second record and stopped counting at the copy, where a
// rebuild keeps one record and counts the line once.
func TestARepeatedLineMatchesRebuild(t *testing.T) {
	h := newTwoWayEnv(t)
	h.twoWays("repeated line", func() {
		h.put("app", "s1", hPrompt("app", "s1", 2, 0, "why does the pool fail")+hSay("app", "s1", 2, 0, "Looking at it."))
	}, func() {
		h.add("app", "s1", hSay("app", "s1", 2, 0, "The pool needs a fix.")+hSay("app", "s1", 2, 0, "Looking at it."))
	})
}

// The clause that names what a build could not read (#1993) lives in
// harnessNarration, which only a full pass calls. After the first build every
// run a person sees is the append path, and that one counted the loss into the
// manifest and said nothing (#2007).
func TestAnIncrementalRunSaysWhatItCouldNotRead(t *testing.T) {
	tmp := t.TempDir()
	setHome(t, tmp)
	t.Setenv("USERPROFILE", tmp)
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "claude"))
	t.Setenv("DEJA_CODEX_ROOT", filepath.Join(tmp, "codex"))
	t.Setenv("DEJA_OPENCODE_DB", filepath.Join(tmp, "none.db"))
	t.Setenv("DEJA_NOTES_FILE", filepath.Join(tmp, "notes.jsonl"))
	proj := filepath.Join(tmp, "claude", "-tmp-app")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(proj, "s1.jsonl")
	if err := os.WriteFile(path, []byte(
		`{"type":"user","sessionId":"s1","cwd":"/tmp/app","timestamp":"2026-01-02T03:04:05Z","message":{"role":"user","content":"why does pgbouncer time out"}}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(tmp, "index.db")
	if err := Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}

	// Two more turns, one of them carrying a raw escape inside the JSON string,
	// which makes the line invalid JSON.
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.WriteString(
		`{"type":"user","sessionId":"s1","cwd":"/tmp/app","timestamp":"2026-01-02T03:04:06Z","message":{"role":"user","content":"the pool ` + "\x1b" + `[31mtimed out"}}` + "\n" +
			`{"type":"assistant","sessionId":"s1","cwd":"/tmp/app","timestamp":"2026-01-02T03:04:07Z","message":{"role":"assistant","content":"raised it to 40"}}` + "\n"); err != nil {
		t.Fatal(err)
	}
	if err = f.Close(); err != nil {
		t.Fatal(err)
	}

	var out strings.Builder
	if err = Ensure(dir, "", false, &out); err != nil {
		t.Fatal(err)
	}
	said := out.String()

	// The premise: this has to be the append path, not a rebuild that happens
	// to narrate. Without it the case would pass on the line #1993 already
	// covers.
	if !strings.Contains(said, "updated 1 file") {
		t.Fatalf("this did not take the append path, so it does not measure what it is about:\n%s", said)
	}
	m, err := readManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got := m.IngestHealth["claude"].MalformedLines; got != 1 {
		t.Fatalf("the pass counted %d unreadable lines, so there is nothing for the run to report", got)
	}

	if !strings.Contains(said, "— 1 line skipped") {
		t.Errorf("the run that read the loss never mentioned it:\n%s", said)
	}
}

// A chmod changes no size or mtime and a directory that cannot be listed adds
// no file, so the incremental pass called the index up to date while two paths
// were unreadable; only a rebuild said so (#5393).
func TestIncrementalPassReportsPathsItCannotRead(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("file permissions do not deny reads here")
	}
	root, dir := allHarnessEnv(t)
	locked := filepath.Join(root, "claude", "-tmp-p1", "a.jsonl")
	write(t, locked, claudeLine("a1", "2026-01-02T03:04:05Z", "lockneedle one"))
	write(t, filepath.Join(root, "claude", "-tmp-p1", "b.jsonl"),
		claudeLine("b1", "2026-01-02T04:04:05Z", "lockneedle two"))
	if err := Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	newDir := filepath.Join(root, "claude", "-tmp-p2")
	write(t, filepath.Join(newDir, "c.jsonl"), claudeLine("c1", "2026-01-03T03:04:05Z", "lockneedle three"))
	for _, p := range []string{locked, newDir} {
		if err := os.Chmod(p, 0); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() { _ = os.Chmod(newDir, 0o755); _ = os.Chmod(locked, 0o644) })

	if fresh, _ := UpToDate(dir, ""); fresh {
		t.Fatal("the index is up to date with two paths it cannot read")
	}
	if err := Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	if got := IngestHealth(dir)["claude"].FailedFiles; got != 2 {
		t.Fatalf("an incremental pass counted %d unreadable paths, want 2: %+v", got, IngestHealth(dir))
	}
	if fresh, n := UpToDate(dir, ""); !fresh || n != 2 {
		t.Fatalf("once recorded the index is current: fresh=%v sessions=%d", fresh, n)
	}
	// The sessions already read from the locked file stay searchable.
	if !hasHit5393(t, dir, "lockneedle one") {
		t.Error("the locked transcript's indexed session left recall")
	}

	if err := os.Chmod(newDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(locked, 0o644); err != nil {
		t.Fatal(err)
	}
	if fresh, _ := UpToDate(dir, ""); fresh {
		t.Fatal("readable again, and the index still holds the old errors")
	}
	if err := EnsureForSearch(dir, search.Options{Query: "lockneedle", All: true}, false, nil); err != nil {
		t.Fatal(err)
	}
	if got := IngestHealth(dir)["claude"].FailedFiles; got != 0 {
		t.Fatalf("readable again, still %d unreadable: %+v", got, IngestFilesReport(dir))
	}
	if fresh, n := UpToDate(dir, ""); !fresh || n != 3 {
		t.Fatalf("after the paths open again: fresh=%v sessions=%d, want 3", fresh, n)
	}
}

func hasHit5393(t *testing.T, dir, q string) bool {
	t.Helper()
	res, err := Search(dir, search.Options{Query: q, All: true})
	if err != nil {
		t.Fatal(err)
	}
	return len(res) > 0
}

// A directory locked over transcripts already indexed: the pass keeps their
// sessions, names them as unreadable, and is then up to date, instead of
// running again on every search (#5393).
func TestALockedDirectoryOverIndexedTranscriptsSettles(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("directory permissions do not deny reads here")
	}
	root, dir := allHarnessEnv(t)
	proj := filepath.Join(root, "claude", "-tmp-p1")
	write(t, filepath.Join(proj, "a.jsonl"), claudeLine("a1", "2026-01-02T03:04:05Z", "settleneedle"))
	if err := Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(proj, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(proj, 0o755) })
	if err := Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	if got := IngestHealth(dir)["claude"].FailedFiles; got == 0 {
		t.Fatalf("the locked directory went unnamed: %+v", IngestFilesReport(dir))
	}
	if fresh, n := UpToDate(dir, ""); !fresh || n != 1 {
		t.Fatalf("after the pass that named it: fresh=%v sessions=%d", fresh, n)
	}
	if stale, err := EnsureForSearchStale(dir, search.Options{Query: "settleneedle", All: true}, nil); err != nil || stale {
		t.Fatalf("a search hands the same pass to a warmup every time: stale=%v err=%v", stale, err)
	}
	if !hasHit5393(t, dir, "settleneedle") {
		t.Error("the sessions behind the locked directory left recall")
	}
}

// A parse error on a file that has not changed is still the file's. Only the
// walk's own refusals clear when a later walk reads the path (#5393).
func TestAParseErrorOnAnUnchangedFileStands(t *testing.T) {
	root, dir := allHarnessEnv(t)
	write(t, filepath.Join(root, "claude", "-tmp-p1", "a.jsonl"), claudeLine("a1", "2026-01-02T03:04:05Z", "standneedle"))
	if err := Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	m, err := readManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(root, "claude", "-tmp-p1", "a.jsonl")
	m.IngestFiles = map[string]FileIngest{p: {Error: "decode: unexpected end of JSON input"}}
	if err := writeManifestOnly(dir, m); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := Ensure(dir, "", false, nil); err != nil {
			t.Fatal(err)
		}
	}
	if got := IngestFilesReport(dir)[p].Error; got == "" {
		t.Fatal("a parse error on an unchanged file was cleared by a walk that only statted it")
	}
	if fresh, _ := UpToDate(dir, ""); !fresh {
		t.Fatal("a parse error alone keeps the index from being up to date")
	}
}

// A locked transcript deleted afterwards has nothing left to report.
func TestALockedTranscriptDeletedStopsBeingReported(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("file permissions do not deny reads here")
	}
	root, dir := allHarnessEnv(t)
	p := filepath.Join(root, "claude", "-tmp-p1", "a.jsonl")
	write(t, p, claudeLine("a1", "2026-01-02T03:04:05Z", "goneneedle"))
	write(t, filepath.Join(root, "claude", "-tmp-p1", "b.jsonl"), claudeLine("b1", "2026-01-02T03:04:05Z", "goneneedle too"))
	if err := Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(p, 0); err != nil {
		t.Fatal(err)
	}
	if err := Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	if got := IngestHealth(dir)["claude"].FailedFiles; got != 1 {
		t.Fatalf("locked: %d unreadable, want 1", got)
	}
	if err := os.Remove(p); err != nil {
		t.Fatal(err)
	}
	if err := Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	if got := IngestHealth(dir)["claude"].FailedFiles; got != 0 {
		t.Fatalf("deleted, and still %d unreadable: %+v", got, IngestFilesReport(dir))
	}
	if fresh, _ := UpToDate(dir, ""); !fresh {
		t.Fatal("not up to date after the deletion was recorded")
	}
}

// A file that failed to parse and was then deleted has nothing left to
// report; kept, it failed every later `deja index` with exit status 3 (#5396).
func TestAParseErrorOnADeletedFileClears(t *testing.T) {
	root, dir := allHarnessEnv(t)
	write(t, filepath.Join(root, "claude", "-tmp-p1", "a.jsonl"), claudeLine("a1", "2026-01-02T03:04:05Z", "clearneedle"))
	if err := Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	m, err := readManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	gonePath := filepath.Join(root, "claude", "-tmp-p1", "b.jsonl")
	m.IngestFiles = map[string]FileIngest{gonePath: {Error: "decode: unexpected end of JSON input"}}
	if err := writeManifestOnly(dir, m); err != nil {
		t.Fatal(err)
	}
	if err := Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	if got := IngestHealth(dir)["claude"].FailedFiles; got != 0 {
		t.Fatalf("a deleted file is still reported unreadable: %+v", IngestFilesReport(dir))
	}
}
