package sources

import (
	"bytes"
	"encoding/json"
	"fmt"
	"github.com/vshulcz/deja-vu/internal/model"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

// The since clauses normalise both sides with strftime's %f, which is
// milliseconds, while the readers parse RFC3339Nano. A message stamped inside
// the watermark's own millisecond compared as not-after it and was skipped for
// good — and the two sides do not even agree on which millisecond a stamp is
// in, because Go truncates where strftime rounds (#2155).
func TestAMessageInsideTheWatermarksMillisecondIsNotLost(t *testing.T) {
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skip("sqlite3 not installed")
	}
	db := filepath.Join(t.TempDir(), "grok.db")
	schema := `
CREATE TABLE workspaces (id TEXT PRIMARY KEY, canonical_path TEXT);
CREATE TABLE sessions (id TEXT PRIMARY KEY, workspace_id TEXT, title TEXT, cwd_last TEXT, created_at TEXT);
CREATE TABLE messages (session_id TEXT, seq INTEGER, role TEXT, message_json TEXT, created_at TEXT);
INSERT INTO sessions VALUES ('s1','w1','t','/work/api','2026-07-27T10:00:00.0000000Z');
INSERT INTO messages VALUES ('s1',0,'user','{"role":"user","content":"first message"}','2026-07-27T10:00:00.8121000Z');
INSERT INTO messages VALUES ('s1',1,'user','{"role":"user","content":"second message"}','2026-07-27T10:00:00.8129000Z');
`
	cmd := exec.CommandContext(t.Context(), "sqlite3", db)
	cmd.Stdin = stringsReader(schema)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("seed: %v: %s", err, out)
	}
	read := func(cut string) []string {
		at, err := time.Parse(time.RFC3339Nano, cut)
		if err != nil {
			t.Fatal(err)
		}
		ss, err := ParseGrokDBSince(db, at)
		if err != nil {
			t.Fatal(err)
		}
		var texts []string
		for _, s := range ss {
			for _, m := range s.Messages {
				texts = append(texts, m.Text)
			}
		}
		return texts
	}
	// A watermark before both messages must leave neither behind.
	if got := read("2026-07-27T10:00:00.8120000Z"); len(got) != 2 {
		t.Errorf("watermark before both returned %v, want both messages", got)
	}
	// One stamped between them keeps at least the later one — the earlier may
	// come back too, which costs a message being offered twice and is the side
	// of the error worth being on.
	if got := read("2026-07-27T10:00:00.8125000Z"); !strings.Contains(strings.Join(got, " "), "second message") {
		t.Errorf("watermark between the two returned %v, want at least the later message", got)
	}
	// And it is still a filter: a watermark a second later returns nothing.
	if got := read("2026-07-27T10:00:01.0000000Z"); len(got) != 0 {
		t.Errorf("watermark after both returned %v, want none", got)
	}
}

// parseTimeAny takes a stamp in either unit, so a since clause may not assume
// one. opencode's compared everything against milliseconds, which puts every
// seconds-stamped row below the watermark for ever: the store is read once and
// then never updated again, silently (#2064).
func TestOpencodeSinceTakesASecondsStampedRow(t *testing.T) {
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skip("sqlite3 CLI not available")
	}
	db := filepath.Join(t.TempDir(), "opencode.db")
	// The turn is newer than the watermark. Written in seconds, which is what
	// unixGuess's lower branch exists for.
	newer := time.Date(2026, 3, 2, 12, 0, 0, 0, time.UTC)
	watermark := newer.Add(-time.Hour)
	stmts := fmt.Sprintf(`
create table session (id text primary key, directory text, time_created integer, time_updated integer);
create table message (id text primary key, session_id text, data text, time_created integer);
create table part (id text primary key, message_id text, data text);
insert into session values ('s1','/tmp/app',%[1]d,%[1]d);
insert into message values ('m1','s1','{"role":"user"}',%[1]d);
insert into part values ('p1','m1',json_object('type','text','text','the autovacuum thread stalls'));
`, newer.Unix())
	if out, err := exec.CommandContext(t.Context(), "sqlite3", db, stmts).CombinedOutput(); err != nil {
		t.Fatalf("sqlite3 seed: %v %s", err, out)
	}
	// The premise: a full read does take this row, so a since clause that
	// drops it is losing something the reader understands.
	whole, err := ParseOpencodeDBSince(db, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if len(whole) != 1 {
		t.Fatalf("the fixture is not readable at all, so this measures nothing: %d sessions", len(whole))
	}

	ss, err := ParseOpencodeDBSince(db, watermark)
	if err != nil {
		t.Fatal(err)
	}
	if len(ss) != 1 {
		t.Fatalf("a turn stamped %s in seconds did not come back for a watermark of %s: %d sessions",
			newer.Format(time.RFC3339), watermark.Format(time.RFC3339), len(ss))
	}
}

// The other half: a millisecond store must still be filtered, or the watermark
// buys nothing on the multi-GB stores it exists for.
func TestOpencodeSinceStillFiltersAMillisecondStore(t *testing.T) {
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skip("sqlite3 CLI not available")
	}
	db := filepath.Join(t.TempDir(), "opencode.db")
	older := time.Date(2026, 3, 2, 12, 0, 0, 0, time.UTC)
	watermark := older.Add(time.Hour)
	stmts := fmt.Sprintf(`
create table session (id text primary key, directory text, time_created integer, time_updated integer);
create table message (id text primary key, session_id text, data text, time_created integer);
create table part (id text primary key, message_id text, data text);
insert into session values ('s1','/tmp/app',%[1]d,%[1]d);
insert into message values ('m1','s1','{"role":"user"}',%[1]d);
insert into part values ('p1','m1',json_object('type','text','text','the autovacuum thread stalls'));
`, older.UnixMilli())
	if out, err := exec.CommandContext(t.Context(), "sqlite3", db, stmts).CombinedOutput(); err != nil {
		t.Fatalf("sqlite3 seed: %v %s", err, out)
	}
	if whole, err := ParseOpencodeDBSince(db, time.Time{}); err != nil || len(whole) != 1 {
		t.Fatalf("the fixture is not readable at all: %d sessions err=%v", len(whole), err)
	}
	ss, err := ParseOpencodeDBSince(db, watermark)
	if err != nil {
		t.Fatal(err)
	}
	if len(ss) != 0 {
		t.Errorf("a turn older than the watermark came back anyway: %d sessions", len(ss))
	}
}

// An index run narrates every store it read; staying silent about the one it
// could not made an empty deja look like an empty history (#794). The note
// must stay off on a machine that never used the harness, where it would be
// noise about a tool nobody needs.
func TestSkipReasonNamesTheMissingToolOnlyWhenTheStoreIsThere(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, ".local", "share"))
	// Cursor's Windows root resolves through %APPDATA%; without this a
	// developer machine with real Cursor chats fails the no-store premise.
	t.Setenv("APPDATA", filepath.Join(home, "AppData", "Roaming"))
	t.Setenv("PATH", home) // no sqlite3 on it
	if SQLite3Available() {
		t.Skip("sqlite3 still resolvable")
	}

	for _, h := range []string{"opencode", "cursor", "grok", "hermes", "goose", "claude"} {
		if got := SkipReason(h); got != "" {
			t.Errorf("%s with no store on disk: %q, want no note", h, got)
		}
	}

	db := OpencodeDB()
	if err := os.MkdirAll(filepath.Dir(db), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(db, []byte("SQLite format 3\x00"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := SkipReason("opencode"); got != "sqlite3 CLI not found" {
		t.Errorf("opencode with a store and no CLI: %q", got)
	}
	// A harness deja reads without the CLI is never explained away by it.
	if got := SkipReason("claude"); got != "" {
		t.Errorf("claude: %q, want no note", got)
	}
}

// With the CLI present there is nothing to explain, store or no store.
func TestSkipReasonSilentWhenTheToolIsThere(t *testing.T) {
	if !SQLite3Available() {
		t.Skip("no sqlite3 on this machine")
	}
	for _, h := range []string{"opencode", "cursor", "grok", "hermes", "goose"} {
		if got := SkipReason(h); got != "" {
			t.Errorf("%s: %q, want no note", h, got)
		}
	}
}

func TestParseClaudeFile(t *testing.T) {
	ss, err := ParseClaudeFile(filepath.Join("..", "..", "fixtures", "synthetic", "claude", "project", "session.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if len(ss) != 1 || ss[0].ID != "claude-abc" || len(ss[0].Messages) != 2 {
		t.Fatalf("bad claude parse: %#v", ss)
	}
	if ss[0].Messages[1].Text != "The frobnicator bug is in parser.go" {
		t.Fatalf("bad text: %q", ss[0].Messages[1].Text)
	}
}

// A UTF-8 BOM on the first line of a transcript made its JSON invalid, so the
// opening turn was dropped while the rest of the session indexed. Some Windows
// editors write the mark; deja strips it.
func TestParseSkipsAUTF8BOMOnTheFirstLine(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bom.jsonl")
	body := []byte("\xef\xbb\xbf" +
		`{"type":"user","sessionId":"bom","timestamp":"2026-01-02T03:04:05Z","message":{"role":"user","content":"the very first turn"}}` + "\n" +
		`{"type":"assistant","sessionId":"bom","timestamp":"2026-01-02T03:05:05Z","message":{"role":"assistant","content":"the reply"}}` + "\n")
	if err := os.WriteFile(path, body, 0o644); err != nil {
		t.Fatal(err)
	}
	ss, err := ParseClaudeFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(ss) != 1 || len(ss[0].Messages) != 2 {
		t.Fatalf("BOM dropped a turn: %#v", ss)
	}
	if ss[0].Messages[0].Text != "the very first turn" {
		t.Fatalf("first turn = %q, want the BOM-prefixed opening turn", ss[0].Messages[0].Text)
	}
}

func TestLoadersOffsetsAndUtilityVariants(t *testing.T) {
	tmp := t.TempDir()
	claudeRoot := filepath.Join(tmp, "claude")
	claudeProj := filepath.Join(claudeRoot, "-tmp-demo-project")
	if err := os.MkdirAll(claudeProj, 0o755); err != nil {
		t.Fatal(err)
	}
	claudeFile := filepath.Join(claudeProj, "c.jsonl")
	first := `{"type":"user","sessionId":"c","timestamp":"2026-01-02T03:04:05Z","message":{"role":"user","content":[{"text":"first"}]}}` + "\n"
	second := `{"type":"assistant","sessionId":"c","timestamp":"2026-01-02T03:05:05Z","message":{"role":"assistant","content":[{"content":"second"}]}}` + "\n"
	if err := os.WriteFile(claudeFile, []byte(first+second), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", t.TempDir())
	t.Setenv("USERPROFILE", os.Getenv("HOME"))
	t.Setenv("DEJA_CLAUDE_ROOT", claudeRoot)
	if got := LoadClaude(); len(got) != 1 || len(got[0].Messages) != 2 {
		t.Fatalf("LoadClaude=%#v", got)
	}
	if got, err := ParseClaudeFileFromOffset(claudeFile, int64(len(first))); err != nil || len(got) != 1 || got[0].Messages[0].Text != "second" {
		t.Fatalf("claude offset=%#v %v", got, err)
	}

	codexRoot := filepath.Join(tmp, "codex")
	rollDir := filepath.Join(codexRoot, "sessions", "2026", "01", "02")
	if err := os.MkdirAll(rollDir, 0o755); err != nil {
		t.Fatal(err)
	}
	roll := filepath.Join(rollDir, "rollout-2026-01-02T03-04-05-r.jsonl")
	r1 := `{"timestamp":"2026-01-02T03:04:05Z","type":"session_meta","payload":{"session_id":"r","cwd":"/tmp/demo"}}` + "\n"
	r2 := `{"timestamp":"2026-01-02T03:05:05Z","payload":{"role":"assistant","content":"roll text"}}` + "\n"
	if err := os.WriteFile(roll, []byte(r1+r2), 0o644); err != nil {
		t.Fatal(err)
	}
	hist := filepath.Join(codexRoot, "history.jsonl")
	if err := os.WriteFile(hist, []byte(`{"session_id":"h","text":"history text","ts":1767337445}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_CODEX_ROOT", codexRoot)
	if got := LoadCodex(); len(got) != 2 {
		t.Fatalf("LoadCodex=%#v", got)
	}
	if got, err := ParseCodexRolloutFromOffset(roll, int64(len(r1))); err != nil || len(got) != 1 || got[0].Messages[0].Text != "roll text" {
		t.Fatalf("roll offset=%#v %v", got, err)
	}
	if got, err := ParseCodexHistoryFromOffset(hist, 0); err != nil || len(got) != 1 {
		t.Fatalf("hist=%#v %v", got, err)
	}

	for _, v := range []any{"2026-01-02T03:04:05Z", float64(1767337445000), json.Number("1767337445"), nil} {
		_ = parseTimeAny(v)
	}
	if textFromContent([]any{map[string]any{"text": "a"}, map[string]any{"content": "b"}}) != "a\nb" {
		t.Fatalf("textFromContent variant failed")
	}
	if projectName("") != "-" || projectName("/a/b") != "a/b" {
		t.Fatalf("projectName failed")
	}
	count := 0
	if err := scanJSONL(hist, func(map[string]any) { count++ }); err != nil || count != 1 {
		t.Fatalf("scanJSONL count=%d err=%v", count, err)
	}
}

func TestOpencodeSQLiteFixtureAndLoaders(t *testing.T) {
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skip("sqlite3 not installed")
	}
	tmp := t.TempDir()
	db := filepath.Join(tmp, "opencode.db")
	script := `create table session(id text, directory text, time_created any, time_updated any);
create table message(id text, session_id text, time_created any, data text);
create table part(id text, message_id text, data text);
insert into session values('s1','/tmp/proj','2026-01-02T03:00:00Z','2026-01-03T03:00:00Z');
insert into session values('s2','/tmp/other','2026-01-01T03:00:00Z','2026-01-01T03:00:00Z');
insert into message values('m1','s1',1767409200000,'{"role":"user","time":{"created":"2026-01-02T03:00:00Z"}}');
insert into message values('m2','s1',1767409260000,'{"role":"assistant"}');
insert into message values('m3','s2',1767322800000,'{"role":"user"}');
insert into part values('p1','m1','{"type":"text","text":"opencode needle","time":{"start":"2026-01-02T03:00:00Z"}}');
insert into part values('p2','m2','{"type":"text","text":"assistant answer","time":{"start":1767409260000}}');
insert into part values('p3','m3','{"type":"text","text":"other text","time":{"start":"2026-01-01T03:00:00Z"}}');`
	cmd := exec.CommandContext(t.Context(), "sqlite3", db, script)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("sqlite setup: %v %s", err, out)
	}
	t.Setenv("HOME", t.TempDir())
	t.Setenv("USERPROFILE", os.Getenv("HOME"))
	t.Setenv("DEJA_OPENCODE_DB", db)
	ss, err := ParseOpencodeDB(db)
	if err != nil || len(ss) != 2 {
		t.Fatalf("ParseOpencodeDB len=%d err=%v %#v", len(ss), err, ss)
	}
	ss, err = ParseOpencodeDBWhere(db, " and s.id like 's1%'", 1)
	if err != nil || len(ss) != 1 || ss[0].ID != "s1" || len(ss[0].Messages) != 1 {
		t.Fatalf("where/limit=%#v err=%v", ss, err)
	}
	ss, err = ParseOpencodeDBSince(db, time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC))
	if err != nil || len(ss) == 0 {
		t.Fatalf("since=%#v err=%v", ss, err)
	}
	if got := LoadOpencodePrefix("s1"); len(got) != 1 {
		t.Fatalf("prefix=%#v", got)
	}
	if got := LoadOpencodeMatching("needle"); len(got) != 1 {
		t.Fatalf("matching=%#v", got)
	}
	if got := LoadOpencodeRecent(1); len(got) == 0 {
		t.Fatalf("recent=%#v", got)
	}
	if s, m, err := OpencodeCounts(); err != nil || s != 2 || m != 3 {
		t.Fatalf("counts=%d %d %v", s, m, err)
	}
	if got := str("x"); got != "x" {
		t.Fatalf("str=%q", got)
	}
	if parseNestedTime(map[string]any{"time": map[string]any{"created": "2026-01-02T03:00:00Z"}}, "time", "created").IsZero() {
		t.Fatal("parseNestedTime zero")
	}
}

func TestParseClaudeProjectFromEncodedDirectory(t *testing.T) {
	tmp := t.TempDir()
	dir := filepath.Join(tmp, "-Users-me-deja-vu")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, "session.jsonl")
	line := `{"type":"user","sessionId":"s1","cwd":"/wrong/project","timestamp":"2026-01-02T03:04:05Z","message":{"role":"user","content":"hello"}}` + "\n"
	if err := os.WriteFile(p, []byte(line), 0o644); err != nil {
		t.Fatal(err)
	}
	ss, err := ParseClaudeFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if len(ss) != 1 || ss[0].Project != "deja/vu" {
		t.Fatalf("project came from wrong source: %#v", ss)
	}
}

func TestParseClaudeProjectFromNestedSubagentPath(t *testing.T) {
	tmp := t.TempDir()
	root := filepath.Join(tmp, "claude")
	project := filepath.Join(root, "-Users-me-deja-vu")
	dir := filepath.Join(project, "a7fa", "subagents")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, "7ca3b9dd0928.jsonl")
	line := `{"type":"user","sessionId":"sub1","timestamp":"2026-01-02T03:04:05Z","message":{"role":"user","content":"hello"}}` + "\n"
	if err := os.WriteFile(p, []byte(line), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", t.TempDir())
	t.Setenv("USERPROFILE", os.Getenv("HOME"))
	t.Setenv("DEJA_CLAUDE_ROOT", root)
	ss, err := ParseClaudeFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if len(ss) != 1 || ss[0].Project != "deja/vu" {
		t.Fatalf("project came from id path segment: %#v", ss)
	}
}

func TestParseCodexRollout(t *testing.T) {
	p := filepath.Join("..", "..", "fixtures", "synthetic", "codex", "sessions", "2026", "01", "02", "rollout-2026-01-02T03-04-05-codex-abc.jsonl")
	ss, err := ParseCodexRollout(p)
	if err != nil {
		t.Fatal(err)
	}
	if len(ss) != 1 || ss[0].ID != "codex-abc" || len(ss[0].Messages) != 2 {
		t.Fatalf("bad codex parse: %#v", ss)
	}
}

func TestParseCodexHistory(t *testing.T) {
	ss, err := ParseCodexHistory(filepath.Join("..", "..", "fixtures", "synthetic", "codex", "history.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if len(ss) != 1 || ss[0].ID != "hist-abc" || ss[0].Messages[0].Text != "history needle" {
		t.Fatalf("bad history: %#v", ss)
	}
}

func TestParseFilesKeepsSortedPathOrder(t *testing.T) {
	files := []string{"/tmp/c.jsonl", "/tmp/a.jsonl", "/tmp/b.jsonl"}
	ss := parseFiles(files, func(p string) ([]model.Session, error) {
		return []model.Session{{ID: filepath.Base(p)}}, nil
	})
	got := []string{ss[0].ID, ss[1].ID, ss[2].ID}
	want := []string{"a.jsonl", "b.jsonl", "c.jsonl"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("order = %v, want %v", got, want)
	}
}

func TestScanJSONLSkipsMalformedLines(t *testing.T) {
	tmp := t.TempDir()
	p := filepath.Join(tmp, "bad.jsonl")
	data := strings.Join([]string{
		`{"type":"user","sessionId":"s1","timestamp":"2026-01-02T03:04:05Z","message":{"role":"user","content":"before bad"}}`,
		`{"type":"user",`,
		`{"type":"assistant","sessionId":"s1","timestamp":"2026-01-02T03:05:05Z","message":{"role":"assistant","content":"after bad"}}`,
	}, "\n") + "\n"
	if err := os.WriteFile(p, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
	ss, err := ParseClaudeFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if len(ss) != 1 || len(ss[0].Messages) != 2 || ss[0].Messages[1].Text != "after bad" {
		t.Fatalf("bad malformed-line parse: %#v", ss)
	}
	// Tolerated loss must leave evidence: the skip is counted, not silent.
	malformed, _ := DiagSnapshot()
	if malformed[p] != 1 {
		t.Fatalf("malformed counter = %#v, want 1 for %s", malformed, p)
	}
}

func TestSQLQuoteDoublesSingleQuotes(t *testing.T) {
	in := `x' or 1=1 --`
	if got := sqlEscape(in); got != `x'' or 1=1 --` {
		t.Fatalf("sqlEscape(%q) = %q", in, got)
	}
}

// stubSQLite3 puts a shell script named sqlite3 alone on PATH and returns its
// path. The probe cache is reset on both sides so neighbouring tests see the
// real binary again.
func stubSQLite3(t *testing.T, body string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("shell-script stubs")
	}
	dir := t.TempDir()
	p := filepath.Join(dir, "sqlite3")
	if err := os.WriteFile(p, []byte("#!/bin/sh\n"+body+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	ResetSQLite3Probe()
	t.Cleanup(ResetSQLite3Probe)
	return p
}

// withOpencodeStore gives the test a home holding an opencode database file,
// so SkipReason has a store to explain.
func withOpencodeStore(t *testing.T) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, ".local", "share"))
	t.Setenv("DEJA_OPENCODE_DB", "")
	db := OpencodeDB()
	if err := os.MkdirAll(filepath.Dir(db), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(db, []byte("SQLite format 3\x00"), 0o644); err != nil {
		t.Fatal(err)
	}
}

// A sqlite3 on PATH that does not answer read every store as empty with exit
// 0, which deja could not tell from an empty database.
func TestSQLite3ProbeCatchesABinaryThatDoesNotAnswer(t *testing.T) {
	resolved, realErr := exec.LookPath("sqlite3")
	cases := []struct {
		name, body, want string
		needsReal        bool
	}{
		{"first-arg wrapper", `exec ` + resolved + ` "$1"`, "did not answer a probe query (no output)", true},
		{"silent stub", "exit 0", "did not answer a probe query (no output)", false},
		{"garbage", "echo hello", `answered a probe query with "hello"`, false},
		{"failing", "echo 'Error: no such function: json_object' >&2; exit 1", "failed a probe query (exit status 1: Error: no such function: json_object)", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if c.needsReal && realErr != nil {
				t.Skip("no real sqlite3 to wrap")
			}
			withOpencodeStore(t)
			stub := stubSQLite3(t, c.body)
			want := "sqlite3 at " + stub + " " + c.want
			if got := SQLite3Problem(); got != want {
				t.Fatalf("problem = %q, want %q", got, want)
			}
			if SQLite3Available() || !SQLite3Broken() {
				t.Error("a broken sqlite3 counts as available")
			}
			if got := SkipReason("opencode"); got != want {
				t.Errorf("opencode skip reason = %q, want %q", got, want)
			}
			if got := SkipReason("claude"); got != "" {
				t.Errorf("claude skip reason = %q, want none", got)
			}
		})
	}
}

func TestSQLite3ProbeAbsentAndWorking(t *testing.T) {
	withOpencodeStore(t)
	resolved, err := exec.LookPath("sqlite3")

	t.Setenv("PATH", t.TempDir())
	ResetSQLite3Probe()
	t.Cleanup(ResetSQLite3Probe)
	if got := SQLite3Problem(); got != SQLite3NotFound {
		t.Errorf("absent: problem = %q", got)
	}
	if SQLite3Broken() {
		t.Error("absent reported as broken")
	}
	if got := SkipReason("opencode"); got != SQLite3NotFound {
		t.Errorf("absent: skip reason = %q", got)
	}

	if err != nil {
		t.Skip("no real sqlite3")
	}
	t.Setenv("PATH", filepath.Dir(resolved))
	if got := SQLite3Problem(); got != "" {
		t.Errorf("real sqlite3: problem = %q", got)
	}
	if got := SkipReason("opencode"); got != "" {
		t.Errorf("real sqlite3: skip reason = %q", got)
	}
}

// One probe per binary: a second call must not start another process.
func TestSQLite3ProbeRunsOncePerBinary(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell-script stubs")
	}
	count := filepath.Join(t.TempDir(), "count")
	stubSQLite3(t, "echo x >> "+count)
	for range 3 {
		SQLite3Available()
		SkipReason("opencode")
	}
	b, _ := os.ReadFile(count)
	// Once with `.progress` and, since this one did not answer, once without.
	if n := strings.Count(string(b), "x"); n != 2 {
		t.Errorf("ran the probe %d times, want 2 for one binary", n)
	}
}

func TestZedSkipReasonCarriesTheSQLite3Problem(t *testing.T) {
	root := t.TempDir()
	t.Setenv("DEJA_ZED_ROOT", root)
	if err := os.MkdirAll(filepath.Dir(ZedDB()), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(ZedDB(), []byte("SQLite format 3\x00"), 0o644); err != nil {
		t.Fatal(err)
	}
	stub := stubSQLite3(t, "exit 0")
	got := SkipReason("zed")
	if !strings.HasPrefix(got, "sqlite3 at "+stub+" did not answer") {
		t.Errorf("zed skip reason = %q", got)
	}
}

// SQLite types values, not columns, so a driver that binds a message body as
// bytes writes a BLOB into a column declared TEXT. json_object refuses one
// outright — "JSON cannot hold BLOB values" — and that fails the whole query,
// which takes a harness out of recall while doctor still calls its store
// healthy. The body columns are cast, and this is the store that proves it.
func TestAStoreThatKeepsItsBodiesAsBlobsStillReads(t *testing.T) {
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skip("sqlite3 not installed")
	}
	tmp := t.TempDir()

	t.Run("hermes", func(t *testing.T) {
		db := filepath.Join(tmp, "hermes.db")
		writeStore(t, db, `create table messages(id integer primary key, session_id text, role text, content text, timestamp real);
insert into messages(session_id,role,content,timestamp) values('h1','user',cast('blob store needle' as blob),1767409200);`)
		ss, err := ParseHermesDB(db)
		if err != nil || len(ss) != 1 {
			t.Fatalf("len=%d err=%v", len(ss), err)
		}
		if !strings.Contains(ss[0].Messages[0].Text, "needle") {
			t.Fatalf("session = %#v", ss[0])
		}
	})

	t.Run("goose", func(t *testing.T) {
		db := filepath.Join(tmp, "goose.db")
		writeStore(t, db, `create table sessions(id text, working_dir text, description text, created_at text, updated_at text);
create table messages(id integer primary key, session_id text, role text, content_json text, created_timestamp integer);
insert into sessions values('g1','/w','a goose session','2026-01-02T03:00:00Z','2026-01-02T04:00:00Z');
insert into messages(session_id,role,content_json,created_timestamp) values('g1','user',
  cast('[{"type":"text","text":"blob store needle"}]' as blob),1767409200);`)
		ss, err := parseGooseDBWhere(db, "")
		if err != nil || len(ss) != 1 {
			t.Fatalf("len=%d err=%v", len(ss), err)
		}
		if !strings.Contains(ss[0].Messages[0].Text, "needle") {
			t.Fatalf("session = %#v", ss[0])
		}
	})
}

func writeStore(t *testing.T, db, script string) {
	t.Helper()
	if out, err := exec.CommandContext(t.Context(), "sqlite3", db, script).CombinedOutput(); err != nil {
		t.Fatalf("sqlite setup: %v %s", err, out)
	}
}

// The sqlite3 shell's -json output mode is quadratic in the characters it
// escapes, and a transcript is mostly quotes and backslashes: one long tool
// output was enough to make `deja index` look like it had hung (#3553). Rows
// come back through json_object instead, and this pins it — a reader that
// reaches for -json again reintroduces the hang on the one store big enough to
// show it, which is never the one in a fixture.
func TestNoReaderAsksSqlite3ToFormatTheJSON(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		name := e.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		b, err := os.ReadFile(filepath.Join(".", name))
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(b), `"-json"`) {
			t.Errorf("%s passes -json to sqlite3: build the row with json_object instead, see sqliteRows", name)
		}
	}
}

func walDB(t *testing.T, dir, name string) string {
	t.Helper()
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skip("no sqlite3")
	}
	db := filepath.Join(dir, name)
	mk := exec.CommandContext(t.Context(), "sqlite3", db,
		"pragma journal_mode=wal; create table t(x); insert into t values(42);")
	if out, err := mk.CombinedOutput(); err != nil {
		t.Fatalf("fixture: %v: %s", err, out)
	}
	// The resting state: SQLite removes both sidecars when the last connection
	// closes cleanly, and some builds leave empty ones behind.
	_ = os.Remove(db + "-wal")
	_ = os.Remove(db + "-shm")
	return db
}

// A WAL store with no sidecar is the state every one of these databases sits in
// when its agent is not running, and Apple's bundled sqlite3 cannot open it
// read-only. deja shells out to whatever sqlite3 is on PATH, which on a stock
// Mac is that one (#1642).
func TestAWalStoreAtRestIsReadThroughImmutable(t *testing.T) {
	db := walDB(t, t.TempDir(), "store.db")
	target := sqliteTarget(db)
	if !strings.Contains(target, "immutable=1") {
		t.Fatalf("a WAL store with no sidecar was handed over as %q", target)
	}
	out, err := exec.CommandContext(t.Context(), "sqlite3", "-readonly", target, "select x from t").CombinedOutput()
	if err != nil {
		t.Fatalf("the store did not read: %v: %s", err, out)
	}
	if strings.TrimSpace(string(out)) != "42" {
		t.Errorf("read %q, want the row back", out)
	}
}

// With a sidecar there may be frames that have not reached the file, and
// immutable would hide them. The plain path is what goes.
func TestAStoreWithAWalSidecarIsLeftAlone(t *testing.T) {
	dir := t.TempDir()
	db := walDB(t, dir, "live.db")
	if err := os.WriteFile(db+"-wal", []byte("frames"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := sqliteTarget(db); got != db {
		t.Errorf("a live store was declared immutable: %q", got)
	}
}

// A rollback-journal database opens read-only without a sidecar, so nothing is
// claimed about it.
func TestARollbackJournalStoreIsLeftAlone(t *testing.T) {
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skip("no sqlite3")
	}
	db := filepath.Join(t.TempDir(), "plain.db")
	mk := exec.CommandContext(t.Context(), "sqlite3", db, "create table t(x); insert into t values(1);")
	if out, err := mk.CombinedOutput(); err != nil {
		t.Fatalf("fixture: %v: %s", err, out)
	}
	if got := sqliteTarget(db); got != db {
		t.Errorf("a rollback-journal store was declared immutable: %q", got)
	}
}

// Neither a missing file nor something that is not a database is claimed to be
// anything: the caller's own error is the better message.
func TestAMissingOrForeignFileIsLeftAlone(t *testing.T) {
	dir := t.TempDir()
	missing := filepath.Join(dir, "nope.db")
	if got := sqliteTarget(missing); got != missing {
		t.Errorf("a missing file was rewritten: %q", got)
	}
	foreign := filepath.Join(dir, "notes.txt")
	if err := os.WriteFile(foreign, []byte("this is not a database at all, truly"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := sqliteTarget(foreign); got != foreign {
		t.Errorf("a text file was rewritten: %q", got)
	}
}

// A store under a directory with a space or a question mark still reaches
// sqlite3 whole: the URI form escapes per segment.
func TestAPathWithAwkwardCharactersSurvivesTheURI(t *testing.T) {
	// A name Windows accepts: `?` is not a filename character there, so the
	// old fixture could not be created at all and the test failed before it
	// measured anything. `#` needs the same per-segment escaping a `?` does,
	// and the `?` case is kept where it can exist (#2808).
	name := "My Store#v2"
	if runtime.GOOS != "windows" {
		name += "?raw"
	}
	dir := filepath.Join(t.TempDir(), name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	db := walDB(t, dir, "store.db")
	target := sqliteTarget(db)
	if strings.Contains(target, " ") || strings.Contains(target, "#v2") {
		t.Fatalf("the path went out unescaped: %q", target)
	}
	out, err := exec.CommandContext(t.Context(), "sqlite3", "-readonly", target, "select x from t").CombinedOutput()
	if err != nil {
		t.Fatalf("the store did not read: %v: %s", err, out)
	}
	if strings.TrimSpace(string(out)) != "42" {
		t.Errorf("read %q, want the row back", out)
	}
}

// #3553 hung for 13m54s inside a single sqlite3 child with 0.75s of CPU in deja
// itself, and nothing in the tree set a deadline — `grep -rn
// 'context.WithTimeout|CommandContext'` found none. Every read against a SQLite
// store now carries one (#3555).
func TestStoreReadBudget(t *testing.T) {
	for _, c := range []struct {
		set  string
		want time.Duration
	}{
		{"", 10 * time.Minute},
		{"90s", 90 * time.Second},
		{"2m", 2 * time.Minute},
		// Off, for someone who would rather wait than lose a store.
		{"0", 0},
		{"-5s", 0},
		// Garbage falls back rather than turning the cap off: a typo in an
		// environment variable must not quietly remove the bound.
		{"soon", 10 * time.Minute},
		{"10", 10 * time.Minute},
	} {
		t.Setenv("DEJA_STORE_TIMEOUT", c.set)
		if got := storeReadBudget(); got != c.want {
			t.Errorf("DEJA_STORE_TIMEOUT=%q gives %v, want %v", c.set, got, c.want)
		}
	}
}

// A fast read is not cut by a budget shorter than it, since the bound is on
// standing still rather than on the clock, and the lines `.progress` adds to
// keep a long query moving never reach the caller (#5517).
func TestASQLiteReadUnderTheBudgetAnswersWithoutProgressLines(t *testing.T) {
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skip("sqlite3 not installed")
	}
	ResetSQLite3Probe()
	if !sqlite3Progress() {
		t.Skip("this sqlite3 has no .progress")
	}
	db := filepath.Join(t.TempDir(), "store.db")
	if out, err := exec.CommandContext(t.Context(), "sqlite3", db, "create table t(a text); insert into t values('one');").CombinedOutput(); err != nil {
		t.Fatalf("building the fixture: %v: %s", err, out)
	}
	// 1s, not less: on a loaded macOS runner sqlite3 can take longer than
	// 250ms to print its first progress line, and the test failed on that.
	t.Setenv("DEJA_STORE_TIMEOUT", "1s")
	// No row until the end, and long past the budget: the progress lines
	// are all that moves.
	q := "with recursive c(x) as (select 1 union all select x+1 from c where x<6000000) select count(*) from c;"
	started := time.Now()
	out, err := sqliteOutput(db, q)
	if err != nil {
		t.Fatalf("a read that kept moving failed after %s: %v", time.Since(started).Round(time.Millisecond), err)
	}
	if got := strings.TrimSpace(string(out)); got != "6000000" {
		t.Errorf("answer = %q, want 6000000", clip(got))
	}
	if time.Since(started) < time.Second {
		t.Log("the query beat the budget here, so only the filter was checked")
	}
	cmd, stop := sqliteReadCmd(db, "select a from t")
	defer stop()
	if !slices.Contains(cmd.Args, sqliteProgressArg) {
		t.Errorf("the read does not ask for progress: %q", cmd.Args)
	}
}

func clip(s string) string {
	if len(s) > 120 {
		return s[:60] + "…" + s[len(s)-60:]
	}
	return s
}

func names(hs []Harness) []string {
	out := make([]string, 0, len(hs))
	for _, h := range hs {
		out = append(out, h.Name)
	}
	return out
}

func TestDejaStoresNarrowsTheRegistry(t *testing.T) {
	full := len(Registry())
	if full < 30 {
		t.Fatalf("the registry has %d harnesses; this test is reading the wrong thing", full)
	}

	t.Setenv(StoresEnv, "claude,codex")
	got := names(Registry())
	if len(got) != 2 || got[0] != "claude" || got[1] != "codex" {
		t.Fatalf("DEJA_STORES=claude,codex left %v", got)
	}
	// Load order is the registry's own, not the order they were written in the
	// variable: the index walks stores in that order and the path matches rely
	// on it staying deterministic.
	t.Setenv(StoresEnv, "codex, claude")
	if got := names(Registry()); len(got) != 2 || got[0] != "claude" {
		t.Fatalf("order came from the variable, not the registry: %v", got)
	}

	// What deja can read is not what it is reading now. The count in the
	// documentation, the set --harness accepts and the names it suggests all
	// come from the full list; narrowing one run must not shrink any of them.
	if n := len(AllHarnesses()); n != full {
		t.Errorf("AllHarnesses is %d under a narrowed selection; it should stay %d", n, full)
	}
	if !IsKnownHarness("zed") {
		t.Error("--harness zed is rejected while DEJA_STORES silences zed; a known store stays a known name")
	}
	if len(HarnessNames()) != full {
		t.Error("HarnessNames shrank with the selection")
	}
}

func TestDejaStoresKnowsTheNotesStoreByTheNamePeopleSee(t *testing.T) {
	// Every surface calls it "notes"; only the registry entry is called "deja".
	// This is the store a loop over the published registry misses, which is why
	// the variable exists (#3802).
	t.Setenv(StoresEnv, "notes")
	if got := names(Registry()); len(got) != 1 || got[0] != "deja" {
		t.Fatalf("DEJA_STORES=notes left %v", got)
	}
	if StoreSilenced("deja") {
		t.Error("the notes store is silenced under DEJA_STORES=notes")
	}
	if !StoreSilenced("claude") {
		t.Error("claude is not silenced under DEJA_STORES=notes")
	}
	if err := StoresSelectionError(); err != nil {
		t.Errorf("notes is rejected as a name: %v", err)
	}
}

func TestDejaStoresUnsetReadsEverything(t *testing.T) {
	t.Setenv(StoresEnv, "")
	if len(Registry()) != len(AllHarnesses()) {
		t.Error("an empty DEJA_STORES narrowed the registry; empty means the default, which is everything")
	}
	if StoreSilenced("claude") {
		t.Error("a store is silenced with no selection in force")
	}
	if _, ok := StoresSelected(); ok {
		t.Error("an empty variable reports a selection in force")
	}
	// A variable holding only separators is the same mistake as an empty one,
	// and reading it as "no stores" would index nothing at all.
	t.Setenv(StoresEnv, " , ")
	if len(Registry()) != len(AllHarnesses()) {
		t.Error("DEJA_STORES=\" , \" silenced every store")
	}
}

func TestDejaStoresRefusesANameThatIsNotAStore(t *testing.T) {
	t.Setenv(StoresEnv, "claude,claud")
	err := StoresSelectionError()
	if err == nil {
		t.Fatal("a typo in DEJA_STORES is accepted; it silences every store and the run looks like a machine with no history")
	}
	if !strings.Contains(err.Error(), "claud,") && !strings.Contains(err.Error(), "claud ") {
		t.Errorf("the error does not name the typo: %v", err)
	}
	// And it says what the names are, because nobody can guess thirty-five of
	// them — under the name each is printed by, not the registry's own.
	if !strings.Contains(err.Error(), "notes") {
		t.Errorf("the error lists the registry's internal name for the notes store, or none at all: %v", err)
	}

	t.Setenv(StoresEnv, "claude,notes")
	if err := StoresSelectionError(); err != nil {
		t.Errorf("a correct selection is refused: %v", err)
	}
}

func TestDejaStoresReportsWhatItSelected(t *testing.T) {
	t.Setenv(StoresEnv, "notes,claude")
	got, ok := StoresSelected()
	if !ok {
		t.Fatal("a selection is in force and StoresSelected says otherwise")
	}
	if strings.Join(got, ",") != "claude,deja" {
		t.Errorf("StoresSelected returned %v", got)
	}
}

// A child run that edits files is cut to its task and its answer like any
// other (#3009), but the edits are what blame, attribution and restore read.
// Dropping them with the prose meant blame never named the subagent that
// changed a file: 1,668 subagent edits on one machine, none indexed (#4163).
// The cut keeps the change records and still drops the reading in between.
func TestASubagentKeepsItsEditsWhenItsMiddleIsCut(t *testing.T) {
	sub := filepath.Join(t.TempDir(), "proj", "parent-1", "subagents")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	rec := func(role string, content any, minute int) string {
		b, err := json.Marshal(map[string]any{
			"type": role, "sessionId": "parent-1", "isSidechain": true, "agentId": "child-1",
			"requestId": fmt.Sprintf("req-%d", minute),
			"timestamp": fmt.Sprintf("2026-08-02T10:%02d:00Z", minute),
			"message":   map[string]any{"role": role, "content": content},
		})
		if err != nil {
			t.Fatal(err)
		}
		return string(b) + "\n"
	}
	body := rec("user", "move Recommended out of idea.go", 0)
	var bodySb35 strings.Builder
	for i := 11; i <= 15; i++ {
		bodySb35.WriteString(rec("assistant", []any{
			map[string]any{"type": "tool_use", "id": fmt.Sprintf("r%d", i), "name": "Read", "input": map[string]any{"file_path": fmt.Sprintf("/repo/only-read-%d.go", i)}},
		}, i))
	}
	body += bodySb35.String()
	var bodySb40 strings.Builder
	for i := 16; i <= 25; i++ {
		bodySb40.WriteString(rec("assistant", []any{
			map[string]any{"type": "text", "text": "reading another file"},
			map[string]any{"type": "tool_use", "id": fmt.Sprintf("t%d", i), "name": "Edit", "input": map[string]any{
				"file_path": "/repo/idea.go", "old_string": fmt.Sprintf("old line %d", i), "new_string": fmt.Sprintf("\tSource Source // set to USER for every idea published from bot %d", i)}},
			map[string]any{"type": "tool_use", "id": fmt.Sprintf("m%d", i), "name": "Read", "input": map[string]any{"file_path": fmt.Sprintf("/repo/only-read-beside-%d.go", i)}},
			map[string]any{"type": "tool_use", "id": fmt.Sprintf("b%d", i), "name": "Bash", "input": map[string]any{"command": fmt.Sprintf("go test ./... # %d", i)}},
		}, i))
		bodySb40.WriteString(rec("user", []any{map[string]any{"type": "tool_result", "tool_use_id": fmt.Sprintf("b%d", i), "content": "ok  repo 0.1s"}}, i))
	}
	body += bodySb40.String()
	body += rec("assistant", "moved Recommended into recommended.go; tests pass", 30)
	path := filepath.Join(sub, "agent-child-1.jsonl")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	ss, err := ParseClaudeFile(path)
	if err != nil || len(ss) != 1 {
		t.Fatalf("sessions = %d, err %v", len(ss), err)
	}
	count := map[string]int{}
	var prose []string
	for _, m := range ss[0].Messages {
		count[m.Role]++
		if m.Role == "user" || m.Role == "assistant" {
			prose = append(prose, m.Text)
		}
	}
	if count[RoleEdit] != 10 {
		t.Errorf("edit records = %d, want all 10 the child made", count[RoleEdit])
	}
	if count[RoleWrote] != 10 {
		t.Errorf("wrote records = %d, want all 10", count[RoleWrote])
	}
	if count[RoleFiles] == 0 {
		t.Error("the files the child touched are gone")
	}
	for _, m := range ss[0].Messages {
		if m.Role == RoleFiles && strings.Contains(m.Text, "only-read-") {
			t.Errorf("a file the child only read came back: %q", m.Text)
		}
	}
	if n := strings.Count(strings.Join(prose, "\n"), "reading another file"); n > SubagentTailKept {
		t.Errorf("the middle's prose came back with the edits: %d turns", n)
	}
	// The commands stay, and `ok  repo 0.1s` is go test's own verdict (#5678).
	if count[RoleCommand] != 10 {
		t.Errorf("command records = %d, want all 10 the child ran", count[RoleCommand])
	}
}

// A child run's commands and the outputs that failed are what `fix` mines a
// failure and its remedy from. Cut with the prose, 1482 subagent sessions on one
// machine held no command record, and October's failure-and-fix episodes were
// nearly all in child runs (#5678).
func TestASubagentKeepsItsCommandsAndTheOutputsThatFailed(t *testing.T) {
	old := ChildOutputFailed
	ChildOutputFailed = func(s string) bool { return strings.Contains(s, "ERROR:") }
	t.Cleanup(func() { ChildOutputFailed = old })
	sub := filepath.Join(t.TempDir(), "proj", "parent-1", "subagents")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	minute := 0
	rec := func(role string, content any) string {
		minute++
		b, err := json.Marshal(map[string]any{
			"type": role, "sessionId": "parent-1", "isSidechain": true, "agentId": "child-1",
			"timestamp": fmt.Sprintf("2026-10-20T10:%02d:00Z", minute),
			"message":   map[string]any{"role": role, "content": content},
		})
		if err != nil {
			t.Fatal(err)
		}
		return string(b) + "\n"
	}
	var body strings.Builder
	body.WriteString(rec("user", "install vllm on the build box"))
	bash := func(id, cmd, out string, failed bool) {
		body.WriteString(rec("assistant", []any{
			map[string]any{"type": "text", "text": "running it"},
			map[string]any{"type": "tool_use", "id": id, "name": "Bash", "input": map[string]any{"command": cmd}},
		}))
		body.WriteString(rec("user", []any{map[string]any{"type": "tool_result", "tool_use_id": id, "content": out, "is_error": failed}}))
	}
	read := func(id, path, out string) {
		body.WriteString(rec("assistant", []any{
			map[string]any{"type": "tool_use", "id": id, "name": "Read", "input": map[string]any{"file_path": path}},
		}))
		body.WriteString(rec("user", []any{map[string]any{"type": "tool_result", "tool_use_id": id, "content": out}}))
	}
	bash("b1", "git status", "nothing to commit, working tree clean", false)
	read("r1", "/repo/notes.txt", "ERROR: an old log line someone pasted into notes")
	bash("b2", "pip3 install vllm==0.6.3", "Collecting vllm==0.6.3\n  Downloading index page\nERROR: No matching distribution found for vllm==0.6.3", true)
	bash("b3", "pip3 install --index-url https://mirror.example.internal/simple vllm==0.6.3", "Successfully installed vllm-0.6.3", false)
	bash("b4", "go test ./...", "--- FAIL: TestX (0.00s)\nFAIL\trepo\t0.1s", true)
	bash("b5", "go test ./...", "ok  \trepo\t0.1s", false)
	for i := range 3 {
		body.WriteString(rec("assistant", fmt.Sprintf("closing turn %d", i)))
	}
	body.WriteString(rec("assistant", "vllm comes from the mirror; tests pass"))
	path := filepath.Join(sub, "agent-child-1.jsonl")
	if err := os.WriteFile(path, []byte(body.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	ss, err := ParseClaudeFile(path)
	if err != nil || len(ss) != 1 {
		t.Fatalf("sessions = %d, err %v", len(ss), err)
	}
	var cmds, outs []string
	for _, m := range ss[0].Messages {
		switch m.Role {
		case RoleCommand:
			cmds = append(cmds, m.Text)
		case RoleToolOutput:
			outs = append(outs, m.Text)
		case "user", "assistant":
			if m.Text == "running it" {
				t.Error("the middle's prose came back with the commands")
			}
		}
	}
	joined := strings.Join(cmds, "\n")
	for _, want := range []string{"$ pip3 install vllm==0.6.3", "mirror.example.internal/simple vllm==0.6.3  → exit 0", "git status"} {
		if !strings.Contains(joined, want) {
			t.Errorf("command %q is missing from the cut run:\n%s", want, joined)
		}
	}
	gotOuts := strings.Join(outs, "\n|\n")
	for _, want := range []string{"No matching distribution", "--- FAIL: TestX", "ok  \trepo\t0.1s"} {
		if !strings.Contains(gotOuts, want) {
			t.Errorf("output %q is missing:\n%s", want, gotOuts)
		}
	}
	for _, gone := range []string{"working tree clean", "Successfully installed", "old log line", "Downloading index page"} {
		if strings.Contains(gotOuts, gone) {
			t.Errorf("output %q came back with the failures", gone)
		}
	}
	if again := KeepSubagentTail(ss[0].Messages); len(again) != len(ss[0].Messages) {
		t.Errorf("a second cut changed the run: %d records, then %d", len(ss[0].Messages), len(again))
	}
}

// switchCase is one reader's sub-agent run: setup writes the store and returns
// the path the registry parses, child is the run's session id, and long says
// the run has enough turns that the default cut shows.
type switchCase struct {
	kind  string
	child string
	long  bool
	// fork marks a control: a session the person went on in, which the
	// switch leaves whole.
	fork  bool
	setup func(t *testing.T) string
}

const switchTurns = 10

func switchJSONL(line func(i int) string) string {
	var b strings.Builder
	for i := range switchTurns {
		b.WriteString(line(i))
		b.WriteString("\n")
	}
	return b.String()
}

func writeSwitchFile(t *testing.T, p, body string) string {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func switchSQLite(t *testing.T, name, script string) string {
	t.Helper()
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skip("sqlite3 not installed")
	}
	db := filepath.Join(t.TempDir(), name)
	if out, err := exec.CommandContext(t.Context(), "sqlite3", db, script).CombinedOutput(); err != nil {
		t.Fatalf("sqlite setup: %v %s", err, out)
	}
	return db
}

func switchCases() []switchCase {
	return []switchCase{
		{kind: "gemini", child: "24b39ab6-b48a-48e4-80f9-0ba7bb24acc6", long: true, setup: func(t *testing.T) string {
			t.Helper()
			_, chats := geminiTree(t)
			head := `{"sessionId":"24b39ab6-b48a-48e4-80f9-0ba7bb24acc6","projectHash":"abc","startTime":"2026-10-17T21:48:40.460Z","lastUpdated":"2026-10-17T21:48:40.460Z","kind":"subagent","directories":["/w/p"]}` + "\n"
			body := switchJSONL(func(i int) string {
				typ := "user"
				content := fmt.Sprintf(`[{"text":"zebrafish step %d"}]`, i)
				if i%2 == 1 {
					typ, content = "gemini", fmt.Sprintf(`"zebrafish answer %d"`, i)
				}
				return fmt.Sprintf(`{"id":"m%d","timestamp":"2026-10-17T21:48:%02d.000Z","type":%q,"content":%s,"model":"gemini-3"}`, i, 41+i, typ, content)
			})
			return writeSwitchFile(t, filepath.Join(chats, "f7b7a064-d884-4582-a228-e95a8c1ac4f3", "24b39ab6-b48a-48e4-80f9-0ba7bb24acc6.jsonl"), head+body)
		}},
		{kind: "codex", child: "child", long: true, setup: func(t *testing.T) string {
			t.Helper()
			head := `{"timestamp":"2026-10-02T07:01:00Z","type":"session_meta","payload":{"id":"child","cwd":"/w","source":{"subagent":{"thread_spawn":{"parent_thread_id":"parent","depth":1}}}}}` + "\n"
			body := switchJSONL(func(i int) string {
				role, typ := "user", "input_text"
				if i%2 == 1 {
					role, typ = "assistant", "output_text"
				}
				return fmt.Sprintf(`{"timestamp":"2026-10-02T07:01:%02dZ","type":"response_item","payload":{"type":"message","role":%q,"content":[{"type":%q,"text":"zebrafish turn %d"}]}}`, i+1, role, typ, i)
			})
			return writeSwitchFile(t, filepath.Join(t.TempDir(), "rollout-2026-10-02T07-01-00-child.jsonl"), head+body)
		}},
		{kind: "openclaw", child: "kid", long: true, setup: func(t *testing.T) string {
			t.Helper()
			dir := filepath.Join(t.TempDir(), "main", "sessions")
			writeSwitchFile(t, filepath.Join(dir, "sessions.json"), `{"agent:main:main":{"sessionId":"p"},"agent:main:subagent:1":{"sessionId":"kid","spawnedBy":"agent:main:main"}}`)
			head := `{"type":"session","version":3,"id":"kid","timestamp":"2026-07-20T09:00:00Z","cwd":"/work/app"}` + "\n"
			body := switchJSONL(func(i int) string {
				role := "user"
				if i%2 == 1 {
					role = "assistant"
				}
				return fmt.Sprintf(`{"type":"message","id":"m%d","timestamp":"2026-07-20T09:00:%02dZ","message":{"role":%q,"content":"zebrafish turn %d"}}`, i, i+1, role, i)
			})
			return writeSwitchFile(t, filepath.Join(dir, "kid.jsonl"), head+body)
		}},
		{kind: "pi", child: "kid", long: true, setup: func(t *testing.T) string {
			t.Helper()
			head := `{"type":"session","version":3,"id":"kid","timestamp":"2026-07-20T09:00:00Z","cwd":"/work/app","rlmDepth":1,"parentSession":"/work/p.jsonl"}` + "\n"
			body := switchJSONL(func(i int) string {
				role := "user"
				if i%2 == 1 {
					role = "assistant"
				}
				return fmt.Sprintf(`{"type":"message","id":"m%d","timestamp":"2026-07-20T09:00:%02dZ","message":{"role":%q,"content":"zebrafish turn %d"}}`, i, i+1, role, i)
			})
			return writeSwitchFile(t, filepath.Join(t.TempDir(), "kid.jsonl"), head+body)
		}},
		{kind: "kiro-cli", child: "e012b5f2-0f80-45a3-a1f6-eccc522bb35e", long: true, setup: func(t *testing.T) string {
			t.Helper()
			dir := t.TempDir()
			id := "e012b5f2-0f80-45a3-a1f6-eccc522bb35e"
			writeSwitchFile(t, filepath.Join(dir, id+".json"), `{"session_id":"`+id+`","cwd":"/w/app","parent_session_id":"136cf72a-585d-4047-997f-d37a0d0f194a","session_created_reason":"subagent"}`)
			body := switchJSONL(func(i int) string {
				return fmt.Sprintf(`{"version":"v1","kind":"Prompt","data":{"message_id":"m%d","content":[{"kind":"text","data":"zebrafish step %d"}],"meta":{"timestamp":%d}}}`, i, i, 1792281752+i)
			})
			return writeSwitchFile(t, filepath.Join(dir, id+".jsonl"), body)
		}},
		{kind: "codewhale", child: "child", long: true, fork: true, setup: func(t *testing.T) string {
			t.Helper()
			root := t.TempDir()
			t.Setenv("DEJA_CODEWHALE_ROOT", root)
			msgs := make([]string, 0, switchTurns)
			for i := range switchTurns {
				role := "user"
				if i%2 == 1 {
					role = "assistant"
				}
				msgs = append(msgs, fmt.Sprintf(`{"role":%q,"content":[{"type":"text","text":"zebrafish turn %d"}]}`, role, i))
			}
			return writeSwitchFile(t, filepath.Join(root, "child.json"), `{"metadata":{"id":"child","title":"fork","created_at":"2026-09-19T11:00:00Z","updated_at":"2026-09-19T11:01:00Z","workspace":"/w/fork","parent_session_id":"parent-1"},"messages":[`+strings.Join(msgs, ",")+`]}`)
		}},
		{kind: "hermes", child: "s-sub", long: true, setup: func(t *testing.T) string {
			t.Helper()
			rows := make([]string, 0, switchTurns)
			for i := range switchTurns {
				role := "user"
				if i%2 == 1 {
					role = "assistant"
				}
				rows = append(rows, fmt.Sprintf("('s-sub','%s','zebrafish turn %d',%d)", role, i, 1785000010+i))
			}
			return writeHermesDB(t, filepath.Join(t.TempDir(), "p"), `
				CREATE TABLE sessions (id TEXT PRIMARY KEY, source TEXT, parent_session_id TEXT, cwd TEXT);
				INSERT INTO sessions VALUES ('s-cli','cli',NULL,'/w/app'),('s-sub','subagent','s-cli','/w/app');
				INSERT INTO messages (session_id,role,content,timestamp) VALUES
				 ('s-cli','user','plan the migration',1785000000),`+strings.Join(rows, ",")+`;`)
		}},
		{kind: "deepseek", child: "286f51ca-3e5c-437d-8e6b-c9d5cfb5e4d3", setup: func(t *testing.T) string {
			t.Helper()
			child := strings.Replace(deepSeekLog,
				`"id":"session-eaf5c9ac-0e47-4d2f-b982-8bae306062d1","createdAt":1787320263519,"cwd":"/work/pgbouncer-lab","delegationDepth":0}`,
				`"id":"286f51ca-3e5c-437d-8e6b-c9d5cfb5e4d3","createdAt":1787320263519,"cwd":"/work/pgbouncer-lab","parentSession":"session-c559c777-a703-4cfa-9f2e-f4b9ebf675cc","origin":"subagent","delegationDepth":1}`, 1)
			return writeDeepSeekSession(t, t.TempDir(), "child", child, false)
		}},
		{kind: "grok", child: "c1", setup: func(t *testing.T) string {
			t.Helper()
			root := t.TempDir()
			t.Setenv("DEJA_GROK_ROOT", root)
			at := time.Now().UTC().Add(-time.Minute).Format(time.RFC3339Nano)
			turn := `{"timestamp":1,"method":"session/update","params":{"update":{"sessionUpdate":"user_message_chunk","content":{"type":"text","text":"zebrafish"}}}}` + "\n"
			return grokLineageSession(t, root, "c1", `{"info":{"id":"c1","cwd":"/work/ledger"},"session_kind":"subagent","created_at":"`+at+`"}`, turn)
		}},
		{kind: "crush", child: "child", setup: func(t *testing.T) string {
			t.Helper()
			sql := "insert into sessions values ('parent',null,'top',1,0,0,0.0,1784282400,1784282400,null,null);\n"
			sql += "insert into sessions values ('child','parent','sub',1,0,0,0.0,1784282401,1784282401,null,null);\n"
			sql += crushInsert(t, "m1", "parent", "user", 1784282400, []any{map[string]any{"type": "text", "data": map[string]any{"text": "delegate it"}}})
			sql += crushInsert(t, "m2", "child", "assistant", 1784282401, []any{map[string]any{"type": "text", "data": map[string]any{"text": "zebrafish"}}})
			return crushStore(t, "demo", sql)
		}},
		{kind: "opencode", child: "ses_child", setup: func(t *testing.T) string {
			t.Helper()
			return switchSQLite(t, "opencode.db", `create table session(id text, parent_id text, directory text, title text, time_created any, time_updated any);
create table message(id text, session_id text, time_created any, data text);
create table part(id text, message_id text, data text);
insert into session values('ses_parent',NULL,'/w','top','2026-01-02T03:00:00Z','2026-01-03T03:00:00Z');
insert into session values('ses_child','ses_parent','/w','sub (@general subagent)','2026-01-02T03:10:00Z','2026-01-02T03:20:00Z');
insert into message values('m1','ses_parent',1767409200000,'{"role":"user","agent":"build"}');
insert into part values('p1','m1','{"type":"text","text":"review the screenshots","time":{"start":"2026-01-02T03:00:00Z"}}');
insert into message values('m2','ses_child',1767409800000,'{"role":"user","agent":"general"}');
insert into part values('p2','m2','{"type":"text","text":"zebrafish defects","time":{"start":"2026-01-02T03:10:00Z"}}');`)
		}},
		{kind: "goose-db", child: "20261018_2", setup: func(t *testing.T) string {
			t.Helper()
			return switchSQLite(t, "sessions.db", gooseLineageStore)
		}},
		{kind: "devin", child: "registry-devin-sub:ag-worker", setup: func(t *testing.T) string {
			t.Helper()
			devinHome(t)
			db, _ := devinTestDB(t, devinFixtureSQL(t))
			return db
		}},
	}
}

// parseThroughRegistry parses p the way an index pass does: through the
// registry's kind, which is where the switch is applied.
func parseThroughRegistry(t *testing.T, kind, p string) []model.Session {
	t.Helper()
	var found *FileKind
	for _, h := range Registry() {
		for i, k := range h.Kinds {
			if k.Name != kind {
				continue
			}
			if found == nil || k.Match(p) {
				found = &h.Kinds[i]
			}
		}
	}
	if found == nil {
		t.Fatalf("no kind %s", kind)
	}
	ss, err := found.Parse(p, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", p, err)
	}
	return ss
}

// DEJA_INCLUDE_SUBAGENTS=0 left out the sub-agents of eight readers and kept
// the other twelve, and the default read some of them whole (#5301). Every
// reader that marks a spawned run now gets the same switch: =0 leaves it out,
// unset keeps its task, edits and last turns, =1 keeps all of it.
func TestSubagentSwitchMeansTheSameInEveryReader(t *testing.T) {
	for _, c := range switchCases() {
		t.Run(c.kind, func(t *testing.T) {
			t.Setenv("DEJA_INCLUDE_SUBAGENTS", "")
			p := c.setup(t)
			count := func(mode string) int {
				t.Setenv("DEJA_INCLUDE_SUBAGENTS", mode)
				for _, s := range parseThroughRegistry(t, c.kind, p) {
					if s.ID == c.child {
						if model.SpawnedKind(s.Kind) == c.fork {
							t.Fatalf("%s read with kind %q", s.ID, s.Kind)
						}
						return len(s.Messages)
					}
				}
				return -1
			}
			whole, cut, off := count("1"), count(""), count("0")
			if c.fork {
				if whole != switchTurns || cut != whole || off != whole {
					t.Errorf("a fork is not a sub-agent: =1 %d, default %d, =0 %d messages, want %d each", whole, cut, off, switchTurns)
				}
				return
			}
			if whole <= 0 {
				t.Fatalf("=1: the sub-agent run is not read")
			}
			if cut <= 0 {
				t.Fatalf("default: the sub-agent run is not read")
			}
			if off != -1 {
				t.Errorf("=0: the sub-agent run is still read, %d messages", off)
			}
			if c.long && (cut >= whole || cut > SubagentTailKept+1) {
				t.Errorf("default kept %d of %d messages, want the task and the last %d turns", cut, whole, SubagentTailKept)
			}
		})
	}
}

func TestApplySubagentSwitchLeavesOtherSessionsAlone(t *testing.T) {
	msgs := make([]model.Message, 9)
	for i := range msgs {
		msgs[i] = model.Message{Role: "user", Text: strconv.Itoa(i)}
	}
	in := []model.Session{
		{ID: "top", Messages: msgs},
		{ID: "fork", Kind: "fork", Messages: msgs},
		{ID: "child", Kind: "subagent", Messages: msgs},
	}
	byID := func(ss []model.Session) map[string]int {
		out := map[string]int{}
		for _, s := range ss {
			out[s.ID] = len(s.Messages)
		}
		return out
	}
	t.Setenv("DEJA_INCLUDE_SUBAGENTS", "0")
	if got := byID(ApplySubagentSwitch(in)); len(got) != 2 || got["top"] != 9 || got["fork"] != 9 {
		t.Errorf("=0: %v, want top and fork whole and no child", got)
	}
	t.Setenv("DEJA_INCLUDE_SUBAGENTS", "")
	if got := byID(ApplySubagentSwitch(in)); got["top"] != 9 || got["fork"] != 9 || got["child"] != SubagentTailKept+1 {
		t.Errorf("default: %v", got)
	}
	if len(in[2].Messages) != 9 {
		t.Errorf("the switch changed the caller's slice: %d", len(in[2].Messages))
	}
	t.Setenv("DEJA_INCLUDE_SUBAGENTS", "1")
	if got := byID(ApplySubagentSwitch(in)); got["child"] != 9 {
		t.Errorf("=1: %v", got)
	}
}

// A child run is where the reading and the searching happen and where the
// answer is said; the parent keeps the launch and a summary. Measured on a real
// store, those files held a quarter of the machine's Claude messages and 387
// assistant turns that read like a settled answer, and none of it was in the
// index (#3009). They come in as the task and the answer; the middle, which is
// the volume, stays out.
func TestASubagentIsReadAsItsTaskAndItsAnswer(t *testing.T) {
	tmp := t.TempDir()
	sub := filepath.Join(tmp, "proj", "subagents")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	line := func(role, text string, minute int) string {
		b, err := json.Marshal(map[string]any{
			"type": role, "sessionId": "parent-1", "isSidechain": true, "agentId": "child-1",
			"timestamp": "2026-08-02T10:" + fmt.Sprintf("%02d", minute) + ":00Z",
			"message":   map[string]any{"role": role, "content": text},
		})
		if err != nil {
			t.Fatal(err)
		}
		return string(b) + "\n"
	}
	body := line("user", "find where the retry budget is set", 0)
	var bodySb35 strings.Builder
	for i := 1; i <= 20; i++ {
		bodySb35.WriteString(line("assistant", "reading another file, nothing here yet", i))
	}
	body += bodySb35.String()
	body += line("assistant", "the retry budget is set in config/retry.go, five attempts", 21)
	path := filepath.Join(sub, "child-1.jsonl")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	ss, err := ParseClaudeFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(ss) != 1 {
		t.Fatalf("sessions = %d, want the child as one", len(ss))
	}
	s := ss[0]
	if len(s.Messages) > SubagentTailKept+1 {
		t.Fatalf("the whole child run was indexed: %d messages", len(s.Messages))
	}
	joined := strings.Join(func() []string {
		out := make([]string, 0, len(s.Messages))
		for _, m := range s.Messages {
			out = append(out, m.Text)
		}
		return out
	}(), "\n")
	if !strings.Contains(joined, "find where the retry budget is set") {
		t.Fatalf("the task it was handed is missing:\n%s", joined)
	}
	if !strings.Contains(joined, "config/retry.go") {
		t.Fatalf("the answer it came back with is missing:\n%s", joined)
	}
	if strings.Count(joined, "nothing here yet") > SubagentTailKept {
		t.Fatalf("the middle came in with it:\n%s", joined)
	}

	// The whole run is still one variable away.
	t.Setenv("DEJA_INCLUDE_SUBAGENTS", "1")
	full, err := ParseClaudeFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(full) != 1 || len(full[0].Messages) < 20 {
		t.Fatalf("the opt-in did not take the whole run: %d messages", len(full[0].Messages))
	}
}

// A session that is not a child run keeps every turn — the cut is for the files
// the parent already summarises, not for transcripts in general.
func TestAnOrdinarySessionIsNotCut(t *testing.T) {
	tmp := t.TempDir()
	proj := filepath.Join(tmp, "proj")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	var body string
	var bodySb92 strings.Builder
	for i := range 12 {
		bodySb92.WriteString(`{"type":"user","sessionId":"s1","timestamp":"2026-08-02T10:00:0` +
			string(rune('0'+i%10)) + `Z","message":{"role":"user","content":"turn ` +
			string(rune('a'+i)) + `"}}` + "\n")
	}
	body += bodySb92.String()
	path := filepath.Join(proj, "s1.jsonl")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	ss, err := ParseClaudeFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(ss) != 1 || len(ss[0].Messages) != 12 {
		t.Fatalf("an ordinary session lost turns: %#v", ss)
	}
}

// Claude Code writes its compaction summary as a user record marked
// isCompactSummary. Read as the person's words it named every topic a
// marathon touched; it is filed under the summary role, as opencode's and
// Hermes's already were (#3384).
func TestClaudeCompactSummaryIsNotTheUser(t *testing.T) {
	lines := []string{
		`{"type":"user","sessionId":"c1","timestamp":"2026-10-01T10:00:00Z","message":{"role":"user","content":"why does the exporter drop every third retry"}}`,
		`{"type":"assistant","sessionId":"c1","timestamp":"2026-10-01T10:01:00Z","message":{"role":"assistant","content":[{"type":"text","text":"the backoff counts from zero"}]}}`,
		`{"type":"user","sessionId":"c1","timestamp":"2026-10-01T11:00:00Z","isCompactSummary":true,"isVisibleInTranscriptOnly":true,"message":{"role":"user","content":"This session is being continued from a previous conversation that ran out of context.\n\nSummary:\n1. Primary Request and Intent:\n   Fix the exporter retry budget."}}`,
		`{"type":"user","sessionId":"c1","timestamp":"2026-10-01T11:01:00Z","message":{"role":"user","content":"continue"}}`,
	}
	path := filepath.Join(t.TempDir(), "c1.jsonl")
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for name, parse := range map[string]func(string, int64) ([]model.Session, error){
		"typed":   parseClaudeTypedFromOffset,
		"generic": parseClaudeGenericFromOffset,
	} {
		ss, err := parse(path, 0)
		if err != nil || len(ss) != 1 {
			t.Fatalf("%s: len=%d err=%v", name, len(ss), err)
		}
		var roles []string
		for _, m := range ss[0].Messages {
			roles = append(roles, m.Role)
		}
		want := []string{"user", "assistant", RoleSummary, "user"}
		if strings.Join(roles, ",") != strings.Join(want, ",") {
			t.Errorf("%s: roles = %v, want %v", name, roles, want)
		}
	}
}

// Gemini's compression hands the model its own state snapshot as a user turn.
func TestGeminiStateSnapshotIsNotTheUser(t *testing.T) {
	path, _ := writeGeminiChat(t, geminiHeader, geminiAsk, geminiSaid, geminiSummary, geminiAck)
	ss, err := ParseGeminiFile(path)
	if err != nil || len(ss) != 1 {
		t.Fatalf("parse: %v %d", err, len(ss))
	}
	found := false
	for _, m := range ss[0].Messages {
		if strings.Contains(m.Text, "<state_snapshot>") {
			found = true
			if m.Role != RoleSummary {
				t.Errorf("snapshot filed as %q, want %q", m.Role, RoleSummary)
			}
		}
		if m.Text == "fix the parser test" && m.Role != "user" {
			t.Errorf("the person's question became %q", m.Role)
		}
	}
	if !found {
		t.Fatal("the snapshot was dropped rather than filed under its own role")
	}
}

// A store reached through a symlink is the store: people put ~/.claude in a
// dotfiles repo, on an external disk, in a synced folder. Walking the link
// itself found nothing and every surface called the store found and empty
// (#1744).
func TestASymlinkedStoreRootIsWalked(t *testing.T) {
	tmp := t.TempDir()
	resolved := filepath.Join(tmp, "real", "proj")
	if err := os.MkdirAll(resolved, 0o755); err != nil {
		t.Fatal(err)
	}
	rec := `{"type":"user","sessionId":"s1","cwd":"/w","timestamp":"2026-08-07T01:00:00Z","message":{"role":"user","content":"quibbleknot"}}` + "\n"
	if err := os.WriteFile(filepath.Join(resolved, "a.jsonl"), []byte(rec), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(tmp, "link")
	if err := os.Symlink(filepath.Join(tmp, "real"), link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	direct := walkFiles(filepath.Join(tmp, "real"), func(p string) bool { return filepath.Ext(p) == ".jsonl" })
	if len(direct) != 1 {
		t.Fatalf("the fixture is wrong: the real path yielded %v", direct)
	}
	through := walkFiles(link, func(p string) bool { return filepath.Ext(p) == ".jsonl" })
	if len(through) != 1 {
		t.Errorf("a symlinked store root yielded %v, while its real path yielded %v", through, direct)
	}
}

// A link inside a store still is not followed — that is what keeps a FIFO from
// hanging the build and a link from walking out of the store — and a loop does
// not stop the walk.
func TestLinksInsideAStoreAreStillSkipped(t *testing.T) {
	tmp := t.TempDir()
	store := filepath.Join(tmp, "store", "proj")
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	rec := `{"type":"user","sessionId":"s1","cwd":"/w","timestamp":"2026-08-07T01:00:00Z","message":{"role":"user","content":"quibbleknot"}}` + "\n"
	if err := os.WriteFile(filepath.Join(store, "a.jsonl"), []byte(rec), 0o644); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(tmp, "outside.jsonl")
	if err := os.WriteFile(outside, []byte(rec), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(store, "linked.jsonl")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	loop := filepath.Join(tmp, "store", "loop")
	if err := os.MkdirAll(loop, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(loop, filepath.Join(loop, "self")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	got := walkFiles(filepath.Join(tmp, "store"), func(p string) bool { return filepath.Ext(p) == ".jsonl" })
	if len(got) != 1 {
		t.Errorf("walk returned %v, want only the real file", got)
	}
}

// The paths that come back are the ones the user configured. Everything
// downstream keys on them — the kind predicates test strings.HasPrefix against
// the root, the manifest records them, the incremental pass looks them up — and
// two spellings of one file lose the file: the first attempt at #1744 walked
// the target and reported its paths, and the next incremental run indexed the
// store back down to nothing.
func TestWalkReportsPathsUnderTheConfiguredRoot(t *testing.T) {
	tmp := t.TempDir()
	resolved := filepath.Join(tmp, "real", "proj")
	if err := os.MkdirAll(resolved, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(resolved, "a.jsonl"), []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(tmp, "link")
	if err := os.Symlink(filepath.Join(tmp, "real"), link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	got := walkFiles(link, func(p string) bool { return filepath.Ext(p) == ".jsonl" })
	if len(got) != 1 {
		t.Fatalf("walk returned %v", got)
	}
	if want := filepath.Join(link, "proj", "a.jsonl"); got[0] != want {
		t.Errorf("walk reported %q, want %q — the configured root", got[0], want)
	}
	if _, err := os.Stat(got[0]); err != nil {
		t.Errorf("the reported path does not open: %v", err)
	}
}

// cursor and gemini walk a path built under their configured root, so a link
// *at* that path — ~/.cursor/projects pointing elsewhere, a chats directory
// moved off the disk — is the case they have to follow. A link higher up was
// never broken for them: the walk starts below it and the path resolves on the
// way in.
func TestCursorAndGeminiFollowASymlinkedWalkRoot(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", filepath.Join(tmp, "home"))
	t.Setenv("USERPROFILE", filepath.Join(tmp, "home"))

	cursorRoot := filepath.Join(tmp, "cursor")
	if err := os.MkdirAll(cursorRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	realProjects := filepath.Join(tmp, "projects-elsewhere", "p", "agent-transcripts")
	if err := os.MkdirAll(realProjects, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(realProjects, "t.jsonl"), []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(tmp, "projects-elsewhere"), filepath.Join(cursorRoot, "projects")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	t.Setenv("DEJA_CURSOR_CLI_ROOT", cursorRoot)
	got := CursorTranscripts()
	want := filepath.Join(cursorRoot, "projects", "p", "agent-transcripts", "t.jsonl")
	if len(got) != 1 || got[0] != want {
		t.Errorf("cursor through a symlinked projects directory: %v, want [%s]", got, want)
	}

	geminiRoot := filepath.Join(tmp, "gemini")
	if err := os.MkdirAll(filepath.Join(geminiRoot, "tmp", "hash"), 0o755); err != nil {
		t.Fatal(err)
	}
	realChats := filepath.Join(tmp, "chats-elsewhere")
	if err := os.MkdirAll(realChats, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(realChats, "c.json"), []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(realChats, filepath.Join(geminiRoot, "tmp", "hash", "chats")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	t.Setenv("DEJA_GEMINI_ROOT", geminiRoot)
	gots := GeminiChatFiles()
	wantG := filepath.Join(geminiRoot, "tmp", "hash", "chats", "c.json")
	if len(gots) != 1 || gots[0] != wantG {
		t.Errorf("gemini through a symlinked chats directory: %v, want [%s]", gots, wantG)
	}
}

// A tag is a short handle someone searches for. A control byte in one is not a
// tag anyone meant to write, and it reached notes.jsonl and every surface that
// reads it (#1810).
func TestNormalizeTagsDropsControlBytes(t *testing.T) {
	got := NormalizeTags([]string{"ok", "red\x1b[31mALERT\x1b[0m\rrewound", "two\nlines"})
	for _, tag := range got {
		if strings.ContainsAny(tag, "\x1b\r\n") {
			t.Errorf("a stored tag carries a control byte: %q", tag)
		}
	}
	if len(got) != 3 {
		t.Fatalf("tags were dropped rather than cleaned: %q", got)
	}
	if got[0] != "ok" {
		t.Errorf("an ordinary tag was altered: %q", got[0])
	}
	if !strings.Contains(got[1], "alert") {
		t.Errorf("the word the user typed is gone: %q", got[1])
	}
}

// The count has been capped at 8 since tags landed; one tag's length was not
// bounded at all, so a 400-character tag was stored and printed whole.
func TestNormalizeTagsBoundsOneTag(t *testing.T) {
	got := NormalizeTags([]string{strings.Repeat("w", 400)})
	if len(got) != 1 {
		t.Fatalf("the tag was dropped: %q", got)
	}
	if len(got[0]) > maxTagLen {
		t.Errorf("one tag is %d bytes, cap is %d", len(got[0]), maxTagLen)
	}
	// A tag that fits is untouched, so the bound is not paid by ordinary use.
	if same := NormalizeTags([]string{"retry-budget"}); same[0] != "retry-budget" {
		t.Errorf("an ordinary tag was cut: %q", same[0])
	}
}

// A tag with a space in it was never a handle anything could match — the tags
// are folded into "#tag" tokens in the indexed text, and half a tag matches
// nothing. Folding the space to a hyphen makes it one, and makes it the same
// handle as the hyphenated spelling someone else typed.
func TestATagWithASpaceBecomesOneHandle(t *testing.T) {
	got := NormalizeTags([]string{"retry budget", "retry-budget"})
	if len(got) != 1 || got[0] != "retry-budget" {
		t.Errorf("the two spellings did not fold into one handle: %q", got)
	}
	// Multibyte tags survive the cut as valid text, not as half a character.
	long := NormalizeTags([]string{strings.Repeat("резервирование", 8)})[0]
	if !utf8.ValidString(long) {
		t.Errorf("the cut split a character: %q", long)
	}
	if len(long) > maxTagLen {
		t.Errorf("a Cyrillic tag is %d bytes, cap is %d", len(long), maxTagLen)
	}
}

// parseClaudeGenericFromOffset is the previous implementation, kept as the
// reference the typed parser is proved against — including on a real store,
// where the shapes nobody thought of live.
func parseClaudeGenericFromOffset(path string, offset int64) ([]model.Session, error) {
	s := model.Session{Harness: "claude", ID: strings.TrimSuffix(filepath.Base(path), ".jsonl"), Project: claudeProjectNameFor(path), Path: path}
	// Where each Bash call's record landed, so the result that arrives in a
	// later record can stamp its outcome onto it.
	commandAt := map[string][]int{}
	forkedFrom := ""
	err := scanJSONLFromOffset(path, offset, func(m map[string]any) {
		typ, _ := m["type"].(string)
		if ff, ok := m["forkedFrom"].(map[string]any); ok && forkedFrom == "" {
			forkedFrom, _ = ff["sessionId"].(string)
		}
		if typ != "user" && typ != "assistant" {
			return
		}
		origin, _ := m["origin"].(map[string]any)
		kind, _ := origin["kind"].(string)
		isPeer := false
		if meta, _ := m["isMeta"].(bool); meta && typ == "user" {
			if kind == "peer" {
				isPeer = true
			} else {
				// The harness's own user-role record (#3267); the typed parser
				// skips it the same way.
				return
			}
		}
		side, _ := m["isSidechain"].(bool)
		agent, _ := m["agentId"].(string)
		if side && agent != "" {
			s.ID = agent
			s.Kind = "sidechain"
			s.Parent, _ = m["sessionId"].(string)
			if name, _ := m["attributionAgent"].(string); name != "" {
				s.Agent = name
			}
		} else if id, _ := m["sessionId"].(string); id != "" {
			s.ID = id
		}
		t := parseTimeAny(m["timestamp"])
		s.Touch(t)
		role := typ
		txt := ""
		if msg, ok := m["message"].(map[string]any); ok {
			if r, _ := msg["role"].(string); r != "" {
				role = r
			}
			var toolOut bool
			txt, toolOut = textFromContentKind(msg["content"])
			if toolOut {
				role = RoleToolOutput
			}
		}
		if isPeer {
			body, _ := origin["body"].(string)
			if txt == "" {
				txt = body
			}
			if !isClaudeSubagentReport(body, txt) {
				return
			}
			role = "assistant"
			txt = stripSubagentFrame(txt)
		}
		if summary, _ := m["isCompactSummary"].(bool); summary && typ == "user" && role == "user" {
			role = RoleSummary
		}
		if role == "user" {
			txt = UnwrapPastedContent(txt)
		}
		if txt != "" {
			s.Messages = append(s.Messages, model.Message{Role: role, Text: txt, Time: t})
		}
		if msg, ok := m["message"].(map[string]any); ok {
			if IndexToolPaths() {
				if p := toolPathsFromContent(msg["content"]); p != "" {
					s.Messages = append(s.Messages, model.Message{Role: RoleFiles, Text: p, Time: t})
				}
			}
			if IndexEdits() {
				for _, e := range editSpansFromContent(msg["content"]) {
					s.Messages = append(s.Messages, model.Message{Role: RoleEdit, Text: e, Time: t})
				}
			}
			if IndexWrites() {
				for _, w := range wroteRecordsFromContent(msg["content"]) {
					s.Messages = append(s.Messages, model.Message{Role: RoleWrote, Text: w, Time: t})
				}
			}
			if IndexCommands() {
				for _, cmd := range commandCallsFromContent(msg["content"]) {
					if cmd.ID != "" {
						// One call can carry a batch of commands, and the
						// filter drops some of them, so the indexes under one
						// id are neither one nor contiguous.
						commandAt[cmd.ID] = append(commandAt[cmd.ID], len(s.Messages))
					}
					s.Messages = append(s.Messages, model.Message{Role: RoleCommand, Text: cmd.Text, Time: t})
				}
				// The same stamp the typed parser writes, from the same
				// fields.
				for _, res := range toolOutcomesFromContent(msg["content"]) {
					if _, ok := commandAt[res.ID]; !ok || !res.Known {
						continue
					}
					commandExits(commandAt).stamp(s.Messages, res.ID, "", res.Code)
					delete(commandAt, res.ID)
				}
			}
		}
	})
	if len(s.Messages) == 0 {
		return nil, err
	}
	markClaudeBranch(&s, forkedFrom)
	// The same cut the typed parser makes: this is the reference it is proved
	// against, and a difference here reads as a parser bug (#3009).
	if IsSubagentPath(path) && !SubagentsWhole() {
		s.Messages = KeepSubagentTail(s.Messages)
	}
	resolveDirProject(&s)
	return []model.Session{s}, err
}

// claudeText joins the text a message carries. Items that are not objects, or
// objects with neither key, are skipped rather than failing the line — a
// transcript mixing shapes must not cost the whole message.
func claudeText(raw json.RawMessage) string {
	txt, _ := claudeTextKind(raw)
	return txt
}

// claudeTextKind joins a message's text and reports whether what it joined came
// from tool results rather than from speech. A message mixing both counts as
// speech: the person said something, and the tool output rode along with it.
func claudeTextKind(raw json.RawMessage) (string, bool) {
	return claudeTextKindIn(splitClaudeContent(raw))
}

// claudeToolPaths returns the distinct file paths a message's tool calls name,
// one per line, or "" when it names none.
func claudeToolPaths(raw json.RawMessage) string { return claudeToolPathsIn(splitClaudeContent(raw)) }

// claudeEditSpans returns "path\nreplaced bytes" for every edit in a turn,
// including the sub-edits of a MultiEdit call.
func claudeEditSpans(raw json.RawMessage) []string { return claudeEditSpansIn(splitClaudeContent(raw)) }

// claudeWroteRecords returns "path\n<line hashes>" for every write in a turn:
// the new side of an Edit, of each sub-edit of a MultiEdit, and the whole
// content of a Write. The mirror of claudeEditSpans, which takes the old side.
func claudeWroteRecords(raw json.RawMessage) []string {
	return claudeWroteRecordsIn(splitClaudeContent(raw))
}

// Most update lines are large tool events. Filter them before JSON decoding so
// indexing cost is dominated by reading the log rather than materializing tool
// payloads that will be discarded.
func scanGrokUpdates(path string, fn func(grokUpdateEvent)) error {
	return scanGrokUpdatesFrom(path, 0, fn)
}

// notesParses counts the parses that actually read the file, for the test that
// pins the memo.
func notesParses() int {
	notesMemo.Lock()
	defer notesMemo.Unlock()
	return notesMemo.parses
}

func parseNestedTime(m map[string]any, k, sub string) time.Time {
	if x, ok := m[k].(map[string]any); ok {
		return parseTimeAny(x[sub])
	}
	return time.Time{}
}

// opencodeV2 reports whether the conversation in a store is kept the 2.x way.
func opencodeV2(db string) bool { return opencodeSchemaOf(db).v2 }

// toolPathsFromContent is claudeToolPaths for the reference parser, which walks
// decoded maps rather than raw JSON. The two must agree or the differential
// test in #502 stops meaning anything.
func toolPathsFromContent(v any) string { return toolPathsIn(v, claudeDialect) }

// editSpansFromContent is claudeEditSpans for the reference parser.
func editSpansFromContent(v any) []string { return editSpansIn(v, claudeDialect) }

// wroteRecordsFromContent is claudeWroteRecords for the reference parser: the
// written side of the same calls editSpansIn reads the replaced side of.
func wroteRecordsFromContent(v any) []string { return wroteRecordsIn(v, claudeDialect) }

// commandsFromContent is claudeCommands for the reference parser.
func commandsFromContent(v any) []string { return commandsIn(v, claudeDialect) }

// commandCallsFromContent is commandsFromContent with the call id each command
// will be answered under, so the reference parser can stamp an outcome the same
// way the typed one does. The two must agree or the differential test in #502
// stops meaning anything.
func commandCallsFromContent(v any) []claudeCommand { return commandCallsIn(v, claudeDialect) }

// toolOutcomesFromContent is claudeToolOutcomes for the reference parser.
func toolOutcomesFromContent(v any) []claudeToolOutcome {
	items, ok := v.([]any)
	if !ok {
		return nil
	}
	var out []claudeToolOutcome
	for _, it := range items {
		m, ok := it.(map[string]any)
		if !ok {
			continue
		}
		if t, _ := m["type"].(string); t != "tool_result" {
			continue
		}
		id, _ := m["tool_use_id"].(string)
		if id == "" {
			continue
		}
		bad, _ := m["is_error"].(bool)
		out = append(out, claudeOutcome(id, bad, m["content"]))
	}
	return out
}

func ReadGeminiCompaction(path, nativeSessionID string) (CompactionTranscript, bool, error) {
	return ReadGeminiCompactionIfNew(path, nativeSessionID, nil)
}

func ReadZCodeCompaction(sessionID string) (CompactionTranscript, bool, error) {
	return ReadZCodeCompactionIfNew(sessionID, nil)
}

func ReadCodeWhaleCompaction(workspace string, since time.Time) (CompactionTranscript, bool, error) {
	return ReadCodeWhaleCompactionIfNew(workspace, since, nil)
}

func GooseTurnFailure(sessionID string) string {
	return GooseTurn(sessionID).Failure
}

func ReadJunieCompaction(sessionID string) (CompactionTranscript, bool, error) {
	return ReadJunieCompactionFromTurnIfNew(sessionID, JunieTurn(sessionID), nil)
}

func LifecycleKeys(m map[string]Lifecycle) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// AppendPromotedTagged appends a promoted note with no source time.
func AppendPromotedTagged(project, title, text, session, state string, tags []string, now time.Time) error {
	return AppendPromotedSourced(project, title, text, session, state, tags, time.Time{}, now)
}

// StaleCandidate pairs an accepted promoted note with a file it cites that was
// modified in a newer session after the note was recorded.
type StaleCandidate struct {
	Note      PromotedNote
	File      string
	TouchedAt time.Time
}

// StaleCandidateNotes returns accepted notes whose cited files were touched in
// sessions strictly newer than the note's timestamp.
func StaleCandidateNotes(notes []PromotedNote, fileLastTouched map[string]time.Time) []StaleCandidate {
	if len(notes) == 0 || len(fileLastTouched) == 0 {
		return nil
	}
	normTouched := make(map[string]time.Time, len(fileLastTouched)*2)
	for path, t := range fileLastTouched {
		norm := strings.ToLower(filepath.ToSlash(path))
		if t.After(normTouched[norm]) {
			normTouched[norm] = t
		}
		base := filepath.Base(norm)
		if t.After(normTouched[base]) {
			normTouched[base] = t
		}
	}
	var out []StaleCandidate
	for _, n := range notes {
		if n.State != "accepted" || n.At.IsZero() {
			continue
		}
		for _, ref := range NoteFileRefs(n) {
			key := strings.ToLower(filepath.ToSlash(ref))
			touchedAt := normTouched[key]
			if touchedAt.IsZero() {
				touchedAt = normTouched[filepath.Base(key)]
			}
			if touchedAt.After(n.At) {
				out = append(out, StaleCandidate{Note: n, File: ref, TouchedAt: touchedAt})
				break
			}
		}
	}
	return out
}

// LoadOpencodeMatching reads the OpenCode sessions whose text contains q.
func LoadOpencodeMatching(q string) []model.Session {
	where := fmt.Sprintf(" and lower(p.data) like '%%%s%%' escape '\\'", sqlEscape(sqlLikeEscape(strings.ToLower(q))))
	ss, _ := ParseOpencodeDBWhere(OpencodeDB(), where, 5000)
	return ss
}

// LoadOpencodeSince reads the OpenCode sessions that changed after t.
func LoadOpencodeSince(t time.Time) []model.Session {
	ss, _ := ParseOpencodeDBSince(OpencodeDB(), t)
	return ss
}

// PiProjectDirBase returns the encoded project dir name for a transcript
// path, e.g. "--Users-x-projects-app--" for .../sessions/--Users-x-projects-app--/s.jsonl.
func PiProjectDirBase(path string) string {
	dir := projectDir(PiRoot(), path)
	base := filepath.Base(dir)
	if base == "" || base == "." || base == string(filepath.Separator) {
		return ""
	}
	return base
}

// AllHarnesses is every harness deja knows how to read, whatever DEJA_STORES
// says. The set `--harness` accepts, and the number the documentation counts:
// silencing a store for one run does not make deja a tool that reads fewer
// harnesses.
func AllHarnesses() []Harness { return allHarnesses() }

// HomeRoots returns Home() followed by any additional readable home
// directories listed in DEJA_HOME_ROOTS (separated by filepath.ListSeparator,
// e.g. "/home/alice:/home/bob" on shared multi-user servers). Relative paths
// and unreadable directories are skipped so a restrictive peer home never
// fails the caller's pass (#5759, #5773).
func HomeRoots() []string {
	var out []string
	if h := Home(); h != "" {
		out = append(out, filepath.Clean(h))
	}
	for _, h := range extraHomeRoots() {
		if !slices.Contains(out, h) {
			out = append(out, h)
		}
	}
	return out
}

func PromotedNoteID(sourceSession string) string {
	return model.PromotedNoteID(sourceSession)
}

// A message that carries only tool results is not the user speaking, however
// the harness files it. Measured before this existed: 32,239 of 40,313 records
// under the user role — 80% — were tool output (#559).
func TestToolResultOnlyMessageIsLabelled(t *testing.T) {
	raw := json.RawMessage(`[{"type":"tool_result","content":"ok  github.com/x  1.2s"}]`)
	txt, isTool := claudeTextKind(raw)
	if txt != "ok  github.com/x  1.2s" {
		t.Fatalf("text = %q", txt)
	}
	if !isTool {
		t.Fatal("a tool_result-only message must be labelled as tool output")
	}
}

// Speech wins a mixed message: the person said something and the output rode
// along with it, so attributing the whole turn to a tool would lose the words.
func TestMixedMessageStaysSpeech(t *testing.T) {
	raw := json.RawMessage(`[
	  {"type":"text","text":"here is what I ran"},
	  {"type":"tool_result","content":"exit status 1"}
	]`)
	if _, isTool := claudeTextKind(raw); isTool {
		t.Fatal("a message containing speech must not be labelled tool output")
	}
}

func TestPlainAndEmptyShapesAreSpeech(t *testing.T) {
	for _, raw := range []string{`"just a sentence"`, `[]`, `null`, `[{"type":"text","text":"hi"}]`} {
		if _, isTool := claudeTextKind(json.RawMessage(raw)); isTool {
			t.Fatalf("%s was labelled tool output", raw)
		}
	}
}

// An MCP tool's result arrives as an array of content blocks, not a string —
// {"type":"tool_result","content":[{"type":"text","text":…}]} — and both
// parsers dropped the item whole: the text never reached the index and the
// label never applied. Measured on one real store: 76 records lost that way.
func TestToolResultContentBlocksAreKept(t *testing.T) {
	line := `[{"type":"tool_result","tool_use_id":"t1","content":[{"type":"text","text":"3 tests failed"},{"type":"image","source":{"type":"base64"}},{"type":"text","text":"in auth_test.go"}]}]`
	txt, isTool := claudeTextKind(json.RawMessage(line))
	if txt != "3 tests failed\nin auth_test.go" {
		t.Fatalf("text = %q", txt)
	}
	if !isTool {
		t.Fatal("a block-array tool result must be labelled as tool output")
	}
	var v any
	if err := json.Unmarshal([]byte(line), &v); err != nil {
		t.Fatal(err)
	}
	gtxt, gTool := textFromContentKind(v)
	if gtxt != txt || gTool != isTool {
		t.Fatalf("generic (%q, %v) disagrees with typed (%q, %v)", gtxt, gTool, txt, isTool)
	}
}

// Speech still wins a mixed message when the tool result is block-shaped.
func TestMixedMessageWithBlockResultStaysSpeech(t *testing.T) {
	line := `[{"type":"text","text":"ran the suite"},{"type":"tool_result","content":[{"type":"text","text":"ok"}]}]`
	txt, isTool := claudeTextKind(json.RawMessage(line))
	if isTool {
		t.Fatal("a message containing speech must not be labelled tool output")
	}
	if txt != "ran the suite\nok" {
		t.Fatalf("text = %q", txt)
	}
}

// An image-only result has nothing worth indexing and must stay empty rather
// than become a blank record — but it is still a tool result for the label.
func TestImageOnlyBlockResultStaysEmpty(t *testing.T) {
	txt, isTool := claudeTextKind(json.RawMessage(`[{"type":"tool_result","content":[{"type":"image","source":{"type":"base64","data":"AAAA"}}]}]`))
	if txt != "" {
		t.Fatalf("text = %q, want empty", txt)
	}
	if !isTool {
		t.Fatal("still a tool result, even with nothing to keep")
	}
}

// The content field takes junk shapes too — numbers, objects, bare strings in
// the block list, broken nesting. They must cost nothing, and the two parsers
// must read them identically.
func TestContentBlockJunkIsTolerated(t *testing.T) {
	cases := []struct {
		raw  string
		want string
	}{
		{`123`, ""},
		{`{"nested":"object"}`, ""},
		{`["a bare string in the blocks"]`, ""},
		{`[{"type":"text","text":123}]`, ""},
		{`[{"type":"text"},{"type":"text","text":"kept"}]`, "kept"},
		{`[not json`, ""},
		{`"unterminated`, ""},
		{`"plain"`, "plain"},
	}
	for _, c := range cases {
		if got := claudeContentText(json.RawMessage(c.raw)); got != c.want {
			t.Errorf("typed claudeContentText(%s) = %q, want %q", c.raw, got, c.want)
		}
		var v any
		if json.Unmarshal([]byte(c.raw), &v) != nil {
			v = nil // undecodable content never reaches the generic parser as a value
		}
		if got := contentText(v); got != c.want {
			t.Errorf("generic contentText(%s) = %q, want %q", c.raw, got, c.want)
		}
	}
}

// The reference parser must agree with the typed one, or the differential test
// in #502 stops meaning anything.
func TestBothParsersAgreeOnTheLabel(t *testing.T) {
	var v any
	if err := json.Unmarshal([]byte(`[{"type":"tool_result","content":"boom"}]`), &v); err != nil {
		t.Fatal(err)
	}
	_, generic := textFromContentKind(v)
	_, typed := claudeTextKind(json.RawMessage(`[{"type":"tool_result","content":"boom"}]`))
	if generic != typed || !generic {
		t.Fatalf("generic=%v typed=%v, want both true", generic, typed)
	}
}

// vocabCheck asserts that every want is among the records of ss and no
// unwanted one is.
func vocabCheck(t *testing.T, ss []model.Session, want, unwanted []string) {
	t.Helper()
	got := map[string]bool{}
	var all []string
	for _, s := range ss {
		for _, m := range s.Messages {
			got[m.Role+": "+m.Text] = true
			all = append(all, fmt.Sprintf("%s: %q", m.Role, m.Text))
		}
	}
	bad := false
	for _, w := range want {
		if !got[w] {
			bad = true
			t.Errorf("missing %q", w)
		}
	}
	for _, u := range unwanted {
		if got[u] {
			bad = true
			t.Errorf("unexpected %q", u)
		}
	}
	if bad {
		t.Logf("got:\n  %s", strings.Join(all, "\n  "))
	}
}

// Amp 0.0.1774959077, the last build with a local thread store: shell_command
// {command, workdir} and apply_patch {patchText} beside Bash and edit_file. A
// patch whose run was rejected changed nothing (#4527).
func TestAmpShellCommandAndApplyPatch(t *testing.T) {
	use := func(id, name string, in any) string {
		return vocabJSON(map[string]any{"type": "tool_use", "id": id, "name": name, "complete": true, "input": in})
	}
	res := func(id, status string) string {
		return vocabJSON(map[string]any{"type": "tool_result", "toolUseID": id, "run": map[string]any{"status": status, "result": map[string]any{"output": "ok", "exitCode": 0}}})
	}
	relPatch := "*** Begin Patch\n*** Update File: notes.go\n@@\n-func oldNotes() {}\n+func notes() error { return errRetry }\n*** End Patch"
	refused := "*** Begin Patch\n*** Update File: /tmp/proj/refused.go\n@@\n-func kept() {}\n+func refusedChange() error { return nil }\n*** End Patch"
	msgs := []string{
		`{"role":"user","content":[{"type":"text","text":"fix the retry loop"}],"meta":{"sentAt":1774950001000}}`,
		`{"role":"assistant","content":[` + use("t1", "shell_command", map[string]any{"command": "go test ./...", "workdir": "/tmp/proj"}) + `],"usage":{"timestamp":"2026-03-31T09:40:05Z"}}`,
		`{"role":"user","content":[` + res("t1", "done") + `]}`,
		`{"role":"assistant","content":[` + use("t2", "apply_patch", map[string]any{"patchText": patch}) + `,` + use("t3", "apply_patch", map[string]any{"patchText": relPatch}) + `]}`,
		`{"role":"user","content":[` + res("t2", "done") + `,` + res("t3", "done") + `]}`,
		`{"role":"assistant","content":[` + use("t4", "apply_patch", map[string]any{"patchText": refused}) + `]}`,
		`{"role":"user","content":[` + res("t4", "rejected-by-user") + `]}`,
		`{"role":"assistant","content":[` + use("t5", "create_file", map[string]any{"path": "/tmp/proj/refused2.go", "content": "func refusedCreate() error { return nil }"}) + `,` +
			use("t6", "edit_file", map[string]any{"path": "/tmp/proj/refused3.go", "old_str": "func keptToo() {}", "new_str": "func refusedEdit() error { return nil }"}) + `]}`,
		`{"role":"user","content":[` + res("t5", "rejected-by-user") + `,` + res("t6", "cancelled") + `]}`,
	}
	body := `{"v":7,"id":"T-0f3c","created":1774950000000,"title":"Fix the retry loop","env":{"initial":{"trees":[{"uri":"file:///tmp/proj"}]}},"messages":[` + strings.Join(msgs, ",") + `]}`
	p := vocabWrite(t, filepath.Join(t.TempDir(), "T-0f3c.json"), body)
	vocabCheck(t, vocabParse(t, ParseAmpFile, p), append([]string{
		vocabCmd("$ go test ./...  → exit 0"),
		vocabFiles("/tmp/proj/notes.go"),
		vocabEdit("/tmp/proj/notes.go", "func oldNotes() {}"),
		vocabWrote("/tmp/proj/notes.go", "func notes() error { return errRetry }"),
	}, patchWants...), []string{
		vocabEdit("/tmp/proj/refused.go", "func kept() {}"),
		vocabWrote("/tmp/proj/refused.go", "func refusedChange() error { return nil }"),
		vocabWrote("/tmp/proj/refused2.go", "func refusedCreate() error { return nil }"),
		vocabEdit("/tmp/proj/refused3.go", "func keptToo() {}"),
		vocabWrote("/tmp/proj/refused3.go", "func refusedEdit() error { return nil }"),
	})
}

// Antigravity 2.4.2: write_to_file {TargetFile, CodeContent} runs as a
// CODE_ACTION step that says "Created file" and carries no diff. A step that
// failed names no file and writes nothing (#4528).
func TestAntigravityWriteToFileWrote(t *testing.T) {
	q := func(s string) string { return vocabJSON(s) } // args are JSON-in-JSON on disk
	planner := func(file, content string) string {
		return vocabJSON(map[string]any{"step_index": 1, "source": "MODEL", "type": "PLANNER_RESPONSE", "status": "DONE", "created_at": "2026-09-30T10:00:05Z", "content": "",
			"tool_calls": []any{map[string]any{"name": "write_to_file", "args": map[string]any{"TargetFile": q(file), "CodeContent": q(content), "Overwrite": false}}}})
	}
	step := func(status, content string) string {
		return vocabJSON(map[string]any{"step_index": 2, "source": "MODEL", "type": "CODE_ACTION", "status": status, "created_at": "2026-09-30T10:00:09Z", "content": content})
	}
	failed := "func neverWritten() error { return nil }"
	p := vocabWrite(t, filepath.Join(t.TempDir(), "brain", "b0c1d2e3", ".system_generated", "logs", "transcript.jsonl"),
		`{"step_index":0,"source":"USER_EXPLICIT","type":"USER_INPUT","status":"DONE","created_at":"2026-09-30T10:00:00Z","content":"<USER_REQUEST>\nfix the retry loop\n</USER_REQUEST>"}`,
		planner("/tmp/proj/jitter.go", jitter),
		step("DONE", "Created At: now\nCompleted At: now\n\nCreated file file:///tmp/proj/jitter.go"),
		planner("/tmp/proj/never.go", failed),
		step("ERROR", "Created At: now\nCompleted At: now\n\nEncountered error in step execution: error executing cascade"),
	)
	vocabCheck(t, vocabParse(t, ParseAntigravityFile, p), []string{
		vocabFiles("/tmp/proj/jitter.go"),
		vocabWrote("/tmp/proj/jitter.go", jitter),
	}, []string{
		vocabWrote("/tmp/proj/never.go", failed),
	})
}

// A write_to_file whose step failed, retried on the same file: the step that
// finishes is the retry's, so the failed call's content is not what the file
// was given (#4528).
func TestAntigravityWriteToFileRetryAfterError(t *testing.T) {
	q := func(s string) string { return vocabJSON(s) }
	planner := func(content string) string {
		return vocabJSON(map[string]any{"source": "MODEL", "type": "PLANNER_RESPONSE", "status": "DONE", "created_at": "2026-09-30T10:00:05Z", "content": "",
			"tool_calls": []any{map[string]any{"name": "write_to_file", "args": map[string]any{"TargetFile": q("/tmp/proj/jitter.go"), "CodeContent": q(content)}}}})
	}
	step := func(status, content string) string {
		return vocabJSON(map[string]any{"source": "MODEL", "type": "CODE_ACTION", "status": status, "created_at": "2026-09-30T10:00:09Z", "content": content})
	}
	failed := "func firstAttemptNeverLanded() error { return nil }"
	p := vocabWrite(t, filepath.Join(t.TempDir(), "brain", "b0c1d2e4", ".system_generated", "logs", "transcript.jsonl"),
		`{"source":"USER_EXPLICIT","type":"USER_INPUT","status":"DONE","created_at":"2026-09-30T10:00:00Z","content":"<USER_REQUEST>\nadd jitter\n</USER_REQUEST>"}`,
		planner(failed),
		step("ERROR", "Created At: now\nCompleted At: now\n\nEncountered error in step execution: error executing cascade"),
		planner(jitter),
		step("DONE", "Created At: now\nCompleted At: now\n\nCreated file file:///tmp/proj/jitter.go"),
	)
	vocabCheck(t, vocabParse(t, ParseAntigravityFile, p), []string{
		vocabWrote("/tmp/proj/jitter.go", jitter),
	}, []string{
		vocabWrote("/tmp/proj/jitter.go", failed),
	})

	// A failed write_to_file, then a replace on the same file whose step has
	// no diff block: that step created nothing, so it does not give the
	// failed call's content to the file.
	replace := vocabJSON(map[string]any{"source": "MODEL", "type": "PLANNER_RESPONSE", "status": "DONE", "created_at": "2026-09-30T10:00:05Z", "content": "",
		"tool_calls": []any{map[string]any{"name": "replace_file_content", "args": map[string]any{"TargetFile": q("/tmp/proj/jitter.go"), "TargetContent": q("a"), "ReplacementContent": q("b")}}}})
	p = vocabWrite(t, filepath.Join(t.TempDir(), "brain", "b0c1d2e5", ".system_generated", "logs", "transcript.jsonl"),
		`{"source":"USER_EXPLICIT","type":"USER_INPUT","status":"DONE","created_at":"2026-09-30T10:00:00Z","content":"<USER_REQUEST>\nadd jitter\n</USER_REQUEST>"}`,
		planner(failed),
		step("ERROR", "Created At: now\nCompleted At: now\n\nEncountered error in step execution: error executing cascade"),
		replace,
		step("DONE", "Created At: now\nCompleted At: now\n\nThe following changes were made by the replace_file_content tool to: file:///tmp/proj/jitter.go"),
	)
	vocabCheck(t, vocabParse(t, ParseAntigravityFile, p), nil, []string{
		vocabWrote("/tmp/proj/jitter.go", failed),
	})
}

// Continue's IDE agent edits with edit_existing_file {filepath, changes}: the
// new code with the untouched stretches elided. The elision lines are not
// written lines, and a canceled call wrote nothing (#4529).
func TestContinueEditExistingFileWrote(t *testing.T) {
	st := func(id, status string, args map[string]any) string {
		return vocabJSON(map[string]any{"toolCall": map[string]any{"id": id, "type": "function", "function": map[string]any{"name": "edit_existing_file", "arguments": vocabJSON(args)}},
			"status": status, "parsedArgs": args, "output": []any{}})
	}
	changes := "// ... existing code ...\n" + newLoop + "\n\t# ... rest of code ...\n<!-- … unchanged markup … -->"
	canceled := "func canceledChange() error { return nil }"
	pending := "func neverAccepted() error { return nil }"
	body := `{"sessionId":"s1","title":"fix the retry loop","workspaceDirectory":"/tmp/proj","history":[` +
		`{"message":{"role":"user","content":"fix the retry loop"},"contextItems":[]},` +
		`{"message":{"role":"assistant","content":"","toolCalls":[]},"contextItems":[],"toolCallStates":[` +
		st("c1", "done", map[string]any{"filepath": "retry.go", "changes": changes}) + "," +
		st("c2", "canceled", map[string]any{"filepath": "other.go", "changes": canceled}) + "," +
		// Still awaiting approval, or a diff not yet accepted, when the
		// session was saved (core ToolStatus generated, calling).
		st("c3", "generated", map[string]any{"filepath": "pending.go", "changes": pending}) + "," +
		st("c4", "calling", map[string]any{"filepath": "pending.go", "content": pending}) + `]}]}`
	p := vocabWrite(t, filepath.Join(t.TempDir(), "sessions", "s1.json"), body)
	vocabCheck(t, vocabParse(t, ParseContinueFile, p), []string{
		vocabFiles("retry.go\nother.go\npending.go"),
		vocabWrote("retry.go", newLoop),
	}, []string{
		vocabWrote("retry.go", changes),
		vocabWrote("other.go", canceled),
		vocabWrote("pending.go", pending),
	})
}

func TestWithoutElisions(t *testing.T) {
	for _, keep := range []string{"...", "\tpass  # ...", "x = ...", "foo(...args)", "return a..b"} {
		if got := withoutElisions(keep); got != keep {
			t.Errorf("withoutElisions(%q) = %q, want it kept", keep, got)
		}
	}
	for _, drop := range []string{"// ... existing code ...", "  # ... rest of code ...", "/* ... */ ", "<!-- … unchanged … -->", "{/* ... existing JSX ... */}", "... existing code ..."} {
		if got := withoutElisions(drop); got != "" {
			t.Errorf("withoutElisions(%q) = %q, want it dropped", drop, got)
		}
	}
}

// rooTask parses one Roo task under workspace /tmp/proj whose assistant turn
// makes the given calls.
func rooTask(t *testing.T, parse func(string) ([]model.Session, error), calls ...string) []model.Session {
	t.Helper()
	task := filepath.Join(t.TempDir(), "tasks", "1788845325718")
	vocabWrite(t, filepath.Join(task, "history_item.json"), `{"id":"1788845325718","ts":1788845325718,"task":"fix the retry loop","workspace":"/tmp/proj"}`)
	p := vocabWrite(t, filepath.Join(task, "api_conversation_history.json"),
		`[{"role":"user","content":[{"type":"text","text":"<task>\nfix the retry loop\n</task>"}]},{"role":"assistant","content":[`+strings.Join(calls, ",")+`]}]`)
	return vocabParse(t, parse, p)
}

func rooUse(name string, in any) string {
	return vocabJSON(map[string]any{"type": "tool_use", "id": "t-" + name, "name": name, "input": in})
}

// roo-cli 0.1.17 offers MiniMax models search_and_replace, an alias of edit,
// and keeps the alias in history with edit's arguments (#4531).
func TestRooSearchAndReplaceAlias(t *testing.T) {
	ss := rooTask(t, ParseRooTask, rooUse("search_and_replace", map[string]any{"file_path": "retry.go", "old_string": oldLoop, "new_string": newLoop}))
	vocabCheck(t, ss, []string{
		vocabFiles("/tmp/proj/retry.go"),
		vocabEdit("/tmp/proj/retry.go", oldLoop),
		vocabWrote("/tmp/proj/retry.go", newLoop),
	}, nil)
}

// Roo's read_file still accepts files[{path, line_ranges}] and stores it as
// {files: [{path, lineRanges}], _legacyFormat: true} (#4531).
func TestRooLegacyReadFileList(t *testing.T) {
	ss := rooTask(t, ParseRooTask, rooUse("read_file", map[string]any{
		"files": []any{map[string]any{"path": "retry.go", "lineRanges": []any{map[string]any{"start": 1, "end": 20}}}, map[string]any{"path": "jitter.go"}}, "_legacyFormat": true}))
	vocabCheck(t, ss, []string{vocabFiles("/tmp/proj/retry.go\n/tmp/proj/jitter.go")}, nil)
}

// Crush v0.97.1: lsp_replace_symbol {symbol, file_path, replacement, action}
// writes replacement in place of the symbol, or before or after it; a delete
// writes nothing (#4533).
func TestCrushReplaceSymbolWrote(t *testing.T) {
	replaced := "func backoffJitter(attempt int) time.Duration { return time.Duration(rand.Int63n(int64(attempt+1))) }"
	deleted := "func legacyRetry() error { return errGiveUp }"
	call := func(id, input string) string {
		return crushInsert(t, "a_"+id, "s1", "assistant", 1784282401, []any{
			map[string]any{"type": "tool_call", "data": map[string]any{"id": "c_" + id, "name": "lsp_replace_symbol", "input": input, "finished": true}}})
	}
	sql := "insert into sessions values ('s1',null,'retry',2,0,0,0.0,1784282405,1784282400,null,null);\n" +
		call("1", vocabJSON(map[string]any{"symbol": "backoffJitter", "file_path": "/tmp/proj/jitter.go", "replacement": replaced, "action": "replace"})) +
		call("2", vocabJSON(map[string]any{"symbol": "legacyRetry", "file_path": "/tmp/proj/retry.go", "replacement": deleted, "action": "delete"}))
	ss, err := ParseCrushDB(crushStore(t, "proj", sql))
	if err != nil {
		t.Fatal(err)
	}
	vocabCheck(t, ss, []string{
		vocabFiles("/tmp/proj/jitter.go"),
		vocabWrote("/tmp/proj/jitter.go", replaced),
		vocabFiles("/tmp/proj/retry.go"),
	}, []string{
		vocabWrote("/tmp/proj/retry.go", deleted),
	})
}

// Kilo CLI 7.8.3 in kilo.db: background_process {action, command, workdir},
// notebook_edit {path, action, kind, source} and notebook_read {path}. Only
// start and monitor run a command, and only insert and replace write a cell;
// a notebook path may be relative to the session directory (#4534).
func TestKiloDBBackgroundProcessAndNotebooks(t *testing.T) {
	part := func(id, tool, status string, input any) string {
		data := fmt.Sprintf(`{"type":"tool","tool":%q,"callID":%q,"state":{"status":%q,"input":%s,"output":"ok","time":{"start":1790000002000}}}`, tool, id, status, vocabJSON(input))
		return fmt.Sprintf("insert into part values('%s','m1',%s);\n", id, sqlQuote(data))
	}
	cell := "retries = compute_backoff_with_jitter(attempt, base=0.5)"
	refused := "retries = refused_change(attempt)"
	db := vocabSQL(t, `create table session(id text primary key, parent_id text, directory text, title text, time_created integer, time_updated integer);
create table message(id text, session_id text, time_created integer, data text);
create table part(id text, message_id text, data text);
insert into session values('ses_1',null,'/tmp/proj','fix the retry loop',1790000000000,1790000100000);
insert into message values('m1','ses_1',1790000001000,'{"role":"assistant","time":{"created":1790000001000}}');
`+part("p1", "background_process", "completed", map[string]any{"action": "start", "command": "go run ./cmd/retryd", "workdir": "/tmp/proj"})+
		part("p2", "background_process", "completed", map[string]any{"action": "stop", "id": "bgp_01", "command": "go run ./cmd/stopped"})+
		part("p3", "notebook_edit", "completed", map[string]any{"path": "/tmp/proj/retry.ipynb", "action": "replace", "kind": "code", "index": 0, "source": cell})+
		part("p4", "notebook_read", "completed", map[string]any{"path": "notes/read.ipynb"})+
		part("p5", "notebook_edit", "error", map[string]any{"path": "/tmp/proj/retry.ipynb", "action": "insert", "kind": "code", "index": 1, "source": refused}))
	ss, err := ParseKiloDB(db)
	if err != nil {
		t.Fatal(err)
	}
	vocabCheck(t, ss, []string{
		vocabCmd("$ go run ./cmd/retryd"),
		vocabFiles("/tmp/proj/retry.ipynb"),
		vocabWrote("/tmp/proj/retry.ipynb", cell),
		vocabFiles("/tmp/proj/notes/read.ipynb"),
	}, []string{
		vocabCmd("$ go run ./cmd/stopped"),
		vocabWrote("/tmp/proj/retry.ipynb", refused),
	})
}

// Kilo Code (kilocode-legacy v5.16.2) task files: search_and_replace
// {path, operations[{search, replace}]}, fast_edit_file {target_file,
// instructions, code_edit}, write_file (the alias it keeps in history),
// delete_file {path} and generate_image {prompt, path} (#4535).
func TestKiloTaskToolVocabulary(t *testing.T) {
	second := "func backoffJitter(attempt int) time.Duration { return time.Duration(attempt) }"
	ss := rooTask(t, ParseKiloTask,
		rooUse("search_and_replace", map[string]any{"path": "retry.go", "operations": []any{
			map[string]any{"search": oldLoop, "replace": newLoop},
			map[string]any{"search": jitter, "replace": second}}}),
		rooUse("fast_edit_file", map[string]any{"target_file": "backoff.go", "instructions": "cap the retry loop",
			"code_edit": "// ... existing code ...\n" + newLoop + "\n// ... existing code ..."}),
		rooUse("write_file", map[string]any{"path": "jitter.go", "content": jitter}),
		rooUse("delete_file", map[string]any{"path": "old_retry.go"}),
		rooUse("generate_image", map[string]any{"prompt": "a retry diagram", "path": "retry.png"}),
	)
	vocabCheck(t, ss, []string{
		vocabFiles("/tmp/proj/retry.go\n/tmp/proj/backoff.go\n/tmp/proj/jitter.go\n/tmp/proj/old_retry.go\n/tmp/proj/retry.png"),
		vocabEdit("/tmp/proj/retry.go", oldLoop),
		vocabWrote("/tmp/proj/retry.go", newLoop),
		vocabEdit("/tmp/proj/retry.go", jitter),
		vocabWrote("/tmp/proj/retry.go", second),
		vocabWrote("/tmp/proj/backoff.go", newLoop),
		vocabWrote("/tmp/proj/jitter.go", jitter),
	}, nil)
}

// cwSaved parses one saved CodeWhale session, workspace /tmp/proj,
// whose assistant turn makes each call and gets result back (#4538).
func cwSaved(t *testing.T, result string, isErr bool, calls ...map[string]any) []model.Session {
	t.Helper()
	uses := make([]any, 0, len(calls))
	results := make([]any, 0, len(calls))
	for i, c := range calls {
		id := fmt.Sprintf("call_%d", i)
		uses = append(uses, map[string]any{"type": "tool_use", "id": id, "name": c["name"], "input": c["input"]})
		results = append(results, map[string]any{"type": "tool_result", "tool_use_id": id, "content": result, "is_error": isErr})
	}
	doc := map[string]any{
		"schema_version": 1,
		"metadata":       map[string]any{"id": "cw-1", "title": "fix the retry loop", "created_at": "2026-10-01T10:00:00Z", "updated_at": "2026-10-01T10:05:00Z", "workspace": "/tmp/proj"},
		"messages": []any{
			map[string]any{"role": "user", "content": []any{map[string]any{"type": "text", "text": "fix the retry loop"}}},
			map[string]any{"role": "assistant", "content": uses},
			map[string]any{"role": "user", "content": results},
		},
	}
	return vocabParse(t, ParseCodeWhaleFile, vocabWrite(t, filepath.Join(t.TempDir(), "cw-1.json"), vocabJSON(doc)))
}

// CodeWhale 0.10.1 runs commands through terminal/run {command, session} and
// task_shell_start {command, cwd} beside bash (#4538).
func TestCodeWhaleTerminalAndTaskShellCommands(t *testing.T) {
	ss := cwSaved(t, "ok", false,
		map[string]any{"name": "terminal/run", "input": map[string]any{"command": "go test ./...", "session": "term-1"}},
		map[string]any{"name": "task_shell_start", "input": map[string]any{"command": "go test -race ./..."}},
	)
	vocabCheck(t, ss, []string{vocabCmd("$ go test ./..."), vocabCmd("$ go test -race ./...")}, nil)
}

// CodeWhale 0.10.1's apply_patch takes a unified diff under patch, retargeted
// by path when set, or whole files under replace[] {path, content}. Paths are
// relative to the workspace, and a call that came back as an error changed
// nothing (#4538).
func TestCodeWhaleApplyPatch(t *testing.T) {
	diff := "--- a/retry.go\n+++ b/retry.go\n@@ -1,3 +1,3 @@\n func retry() {\n-" + oldLoop + "\n+" + newLoop + "\n }\n"
	ss := cwSaved(t, "applied", false,
		map[string]any{"name": "apply_patch", "input": map[string]any{"path": "backoff.go", "patch": diff}},
		map[string]any{"name": "apply_patch", "input": map[string]any{"patch": diff}},
		map[string]any{"name": "apply_patch", "input": map[string]any{"replace": []any{map[string]any{"path": "/tmp/proj/jitter.go", "content": jitter}}}},
	)
	vocabCheck(t, ss, []string{
		vocabFiles("/tmp/proj/backoff.go"),
		vocabEdit("/tmp/proj/backoff.go", oldLoop),
		vocabWrote("/tmp/proj/backoff.go", newLoop),
		vocabFiles("/tmp/proj/retry.go"),
		vocabEdit("/tmp/proj/retry.go", oldLoop),
		vocabWrote("/tmp/proj/retry.go", newLoop),
		vocabFiles("/tmp/proj/jitter.go"),
		vocabWrote("/tmp/proj/jitter.go", jitter),
	}, nil)

	refused := cwSaved(t, "Error: patch did not apply", true,
		map[string]any{"name": "apply_patch", "input": map[string]any{"patch": diff}})
	vocabCheck(t, refused, nil, []string{
		vocabEdit("/tmp/proj/retry.go", oldLoop),
		vocabWrote("/tmp/proj/retry.go", newLoop),
	})

	// file_path and filePath are folded onto path before the patch runs
	// (file.rs PATH_ALIASES), so they retarget it too.
	for _, key := range []string{"file_path", "filePath"} {
		aliased := cwSaved(t, "applied", false,
			map[string]any{"name": "apply_patch", "input": map[string]any{key: "backoff.go", "patch": diff}})
		vocabCheck(t, aliased, []string{
			vocabFiles("/tmp/proj/backoff.go"),
			vocabWrote("/tmp/proj/backoff.go", newLoop),
		}, []string{
			vocabFiles("/tmp/proj/retry.go"),
			vocabWrote("/tmp/proj/retry.go", newLoop),
		})
	}
}

func TestUnifiedPatchReadsHunksByCount(t *testing.T) {
	// The removed SQL comment reads "--- old" with its diff marker, and is not
	// the next file's header; the second file has a bare @@.
	unified := "diff --git a/q.sql b/q.sql\n--- a/q.sql\t2026-06-26 10:00:00 +0000\n+++ b/q.sql\n@@ -1,2 +1,2 @@\n--- old\n+select retries from attempts;\n context\n" +
		"--- /dev/null\n+++ b/new.go\n@@\n+" + jitter + "\n"
	files, spans, wrote := unifiedPatch(unified, "", func(p string) string { return "/w/" + p })
	if strings.Join(files, ",") != "/w/q.sql,/w/new.go" {
		t.Errorf("files = %q", files)
	}
	if len(spans) != 1 || spans[0] != "/w/q.sql\n-- old" {
		t.Errorf("spans = %q", spans)
	}
	want := []string{WroteRecord("/w/q.sql", "select retries from attempts;"), WroteRecord("/w/new.go", jitter)}
	if strings.Join(wrote, "|") != strings.Join(want, "|") {
		t.Errorf("wrote = %q, want %q", wrote, want)
	}
}

// A model miscounts hunks, and CodeWhale applies the patch anyway: a hunk ends
// at the next @@, at diff, or at a --- that a +++ follows, whatever its
// header promised (apply_patch.rs parse_hunk_header).
func TestUnifiedPatchOvercountedHunk(t *testing.T) {
	unified := "--- a/a.go\n+++ b/a.go\n@@ -1,6 +1,6 @@\n-func oldA() {}\n+func newA() error { return nil }\n" +
		"@@ -9,6 +9,6 @@\n-func oldA2() {}\n+func newA2() error { return nil }\n" +
		"--- a/b.go\n+++ b/b.go\n@@ -1,6 +1,6 @@\n-func oldB() {}\n+func newB() error { return nil }\n" +
		"diff --git a/c.go b/c.go\n--- a/c.go\n+++ b/c.go\n@@ -1 +1 @@\n-func oldC() {}\n+func newC() error { return nil }\n"
	files, spans, wrote := unifiedPatch(unified, "", nil)
	if got := strings.Join(files, ","); got != "a.go,b.go,c.go" {
		t.Errorf("files = %q", got)
	}
	wantSpans := []string{"a.go\nfunc oldA() {}", "a.go\nfunc oldA2() {}", "b.go\nfunc oldB() {}", "c.go\nfunc oldC() {}"}
	if strings.Join(spans, "|") != strings.Join(wantSpans, "|") {
		t.Errorf("spans = %q, want %q", spans, wantSpans)
	}
	wantWrote := []string{
		WroteRecord("a.go", "func newA() error { return nil }\nfunc newA2() error { return nil }"),
		WroteRecord("b.go", "func newB() error { return nil }"),
		WroteRecord("c.go", "func newC() error { return nil }"),
	}
	if strings.Join(wrote, "|") != strings.Join(wantWrote, "|") {
		t.Errorf("wrote = %q, want %q", wrote, wantWrote)
	}
}

// command-code 1.74.0: shell_command and monitor_command take {command,
// args[]}, powershell {command}, and read_file's paths may hold globs, which
// name no file the session touched (#4540).
func TestCommandCodeShellArgsAndReadGlobs(t *testing.T) {
	root := t.TempDir()
	t.Setenv("DEJA_COMMANDCODE_ROOT", root)
	n := 0
	line := func(role string, content []any) string {
		n++
		return vocabJSON(map[string]any{"type": "message", "id": fmt.Sprintf("m%d", n), "timestamp": fmt.Sprintf("2026-10-01T10:00:%02d.000Z", n), "message": map[string]any{"role": role, "content": content}})
	}
	use := func(name string, in any) string {
		return line("assistant", []any{map[string]any{"type": "tool_use", "id": "call_" + name, "name": name, "input": in}})
	}
	p := vocabWrite(t, filepath.Join(root, "tmp-proj", "cc-1.jsonl"),
		vocabJSON(map[string]any{"type": "session", "version": 3, "id": "cc-1", "timestamp": "2026-10-01T10:00:00.000Z", "cwd": "/tmp/proj"}),
		line("user", []any{map[string]any{"type": "text", "text": "fix the retry loop"}}),
		use("shell_command", map[string]any{"command": "go", "args": []any{"test", "./retry/..."}}),
		use("powershell", map[string]any{"command": "go vet ./..."}),
		use("monitor_command", map[string]any{"command": "go", "args": []any{"run", "./cmd/retryd", "--watch"}}),
		use("read_file", map[string]any{"paths": []any{"/tmp/proj/retry.go", "/tmp/proj/**/*_test.go"}}),
	)
	vocabCheck(t, vocabParse(t, ParseCommandCodeFile, p), []string{
		vocabCmd("$ go test ./retry/..."),
		vocabCmd("$ go vet ./..."),
		vocabCmd("$ go run ./cmd/retryd --watch"),
		vocabFiles("/tmp/proj/retry.go"),
	}, []string{
		vocabFiles("/tmp/proj/retry.go\n/tmp/proj/**/*_test.go"),
	})
}

// Reasonix 1.39.6: notebook_edit {path, new_source, edit_mode}, delete_range
// {path, start_anchor, end_anchor} whose result is a unified diff,
// delete_symbol {path, name, kind} and move_file {source_path,
// destination_path}. delete_symbol returns the unified diff of what it cut
// too (internal/tools/builtin/delete_symbol.go, #595). A delete_range that
// failed returns an error, not a diff (#4541).
func TestReasonixFileTools(t *testing.T) {
	cell := "retries = compute_backoff_with_jitter(attempt, base=0.5)"
	dropped := "dropped = legacy_cell(attempt)"
	rangeDiff := "--- a/retry.go\n+++ b/retry.go\n@@\n-func legacyRetry() error {\n-\treturn errGiveUp\n-}\n"
	symbolDiff := "--- a/symbols.go\n+++ b/symbols.go\n@@ -3,4 +3,1 @@\n package retry\n-func legacyBackoff(attempt int) time.Duration {\n-\treturn time.Duration(attempt) * time.Second\n-}\n"
	calls := []any{
		toolCall("c1", "notebook_edit", map[string]any{"path": "/tmp/proj/retry.ipynb", "cell_number": 0, "new_source": cell, "edit_mode": "replace"}),
		toolCall("c2", "notebook_edit", map[string]any{"path": "/tmp/proj/old.ipynb", "cell_number": 1, "new_source": dropped, "edit_mode": "delete"}),
		toolCall("c3", "delete_range", map[string]any{"path": "/tmp/proj/retry.go", "start_anchor": "func legacyRetry() error {", "end_anchor": "}"}),
		toolCall("c4", "delete_range", map[string]any{"path": "/tmp/proj/backoff.go", "start_anchor": "func gone() {", "end_anchor": "}"}),
		toolCall("c5", "delete_symbol", map[string]any{"path": "/tmp/proj/symbols.go", "name": "legacyBackoff", "kind": "func"}),
		toolCall("c6", "move_file", map[string]any{"source_path": "/tmp/proj/jitter.go", "destination_path": "/tmp/proj/backoff/jitter.go"}),
	}
	results := []string{"edited cell 0", "deleted cell 1", rangeDiff, "start_anchor not found in /tmp/proj/backoff.go", symbolDiff, "moved"}
	want := []string{
		vocabWrote("/tmp/proj/retry.ipynb", cell),
		vocabEdit("/tmp/proj/retry.go", "func legacyRetry() error {\n\treturn errGiveUp\n}"),
		vocabEdit("/tmp/proj/symbols.go", "func legacyBackoff(attempt int) time.Duration {\n\treturn time.Duration(attempt) * time.Second\n}"),
	}
	unwanted := []string{vocabWrote("/tmp/proj/old.ipynb", dropped)}
	// v1 puts all the calls' files in one record and v4 one per call, so the
	// files are checked as a set.
	files := []string{"/tmp/proj/retry.ipynb", "/tmp/proj/old.ipynb", "/tmp/proj/retry.go", "/tmp/proj/backoff.go",
		"/tmp/proj/symbols.go", "/tmp/proj/jitter.go", "/tmp/proj/backoff/jitter.go"}
	check := func(t *testing.T, ss []model.Session) {
		t.Helper()
		vocabCheck(t, ss, want, unwanted)
		got := map[string]bool{}
		for _, p := range vocabRoles(ss, RoleFiles) {
			for f := range strings.SplitSeq(p, "\n") {
				got[f] = true
			}
		}
		for _, f := range files {
			if !got[f] {
				t.Errorf("no files record for %s", f)
			}
		}
	}

	t.Run("v1 jsonl", func(t *testing.T) {
		root := t.TempDir()
		clearReasonixEnv(t, root)
		t.Setenv("DEJA_REASONIX_ROOT", root)
		p := filepath.Join(root, "projects", "-tmp-proj", "sessions", "20261001-100000.000000000-deepseek-chat.jsonl")
		lines := make([]any, 0, 2+len(results))
		lines = append(lines,
			map[string]any{"role": "user", "content": "fix the retry loop"},
			map[string]any{"role": "assistant", "content": "", "tool_calls": calls},
		)
		for i, r := range results {
			lines = append(lines, map[string]any{"role": "tool", "tool_call_id": fmt.Sprintf("c%d", i+1), "content": r})
		}
		writeJSONLines(t, p, lines...)
		writeJSONDoc(t, p+".meta", map[string]any{"workspace_root": "/tmp/proj", "created_at": "2026-10-01T10:00:00Z"})
		check(t, vocabParse(t, ParseReasonixFile, p))
	})
	t.Run("v4 events.frames", func(t *testing.T) {
		root := t.TempDir()
		clearReasonixEnv(t, root)
		t.Setenv("DEJA_REASONIX_ROOT", root)
		id := "839559938275e9a3ebde5a804aa24edd"
		t0 := time.Date(2026, 10, 1, 10, 0, 0, 0, time.UTC)
		l := newRxLog(t, filepath.Join(root, "projects", "-tmp-proj", "sessions-v4", id), id, t0, nil)
		l.batch(t0.Add(time.Second), rxMsg(map[string]any{"role": "user", "id": "m1", "origin": "user", "content": "fix the retry loop"}))
		l.batch(t0.Add(2*time.Second), rxMsg(map[string]any{"role": "assistant", "id": "m2", "content": "", "tool_calls": calls}))
		for i, r := range results {
			l.batch(t0.Add(time.Duration(3+i)*time.Second), rxMsg(map[string]any{"role": "tool", "id": fmt.Sprintf("r%d", i), "tool_call_id": fmt.Sprintf("c%d", i+1), "content": r}))
		}
		check(t, vocabParse(t, ParseReasonixFile, l.save()))
	})
}

// A rename in the apply_patch format is "*** Update File: a" then "*** Move
// to: b", and its hunks are written to b (codex-rs/apply-patch parser.rs
// change_move). Every reader of the format filed them under a, a file that no
// longer exists, so blame of the line in b found nothing (#595).
func TestApplyPatchMoveFilesTheHunksUnderTheNewName(t *testing.T) {
	const (
		from    = "/tmp/proj/retry.go"
		to      = "/tmp/proj/backoff/retry.go"
		removed = "func retryForever(fn func() error) error {"
		added   = "func retryCapped(fn func() error, maxAttempts int) error {"
	)
	move := "*** Begin Patch\n*** Update File: " + from + "\n*** Move to: " + to + "\n@@\n-" + removed + "\n+" + added + "\n*** End Patch"
	want := []string{vocabEdit(to, removed), vocabWrote(to, added)}
	unwanted := []string{vocabEdit(from, removed), vocabWrote(from, added)}
	cases := []struct {
		name  string
		parse func(t *testing.T) []model.Session
		files bool
	}{
		{"codex", vocabCodexPatch(move), true},
		{"copilot", vocabCopilot(move), true},
		{"openclaw", vocabOpenClaw(move), true},
		{"cline-sdk", vocabClineSDK(move), true},
		{"cline-vscode", vocabClineVSCode(move), true},
		{"opencode", vocabOpencodePatch(move), false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ss := c.parse(t)
			vocabCheck(t, ss, want, unwanted)
			if !c.files {
				return
			}
			got := map[string]bool{}
			for _, rec := range vocabRoles(ss, RoleFiles) {
				for f := range strings.SplitSeq(rec, "\n") {
					got[f] = true
				}
			}
			for _, f := range []string{from, to} {
				if !got[f] {
					t.Errorf("no files record names %s", f)
				}
			}
		})
	}
}

// Codex 0.149: apply_patch as a custom tool call, the patch its input.
func vocabCodexPatch(patch string) func(t *testing.T) []model.Session {
	return func(t *testing.T) []model.Session {
		t.Helper()
		rec := func(typ string, payload any) string {
			return vocabJSON(map[string]any{"timestamp": "2026-10-01T10:00:01.000Z", "type": typ, "payload": payload})
		}
		p := vocabWrite(t, filepath.Join(t.TempDir(), "rollout-2026-10-01T10-00-00-019a0000-1111-7222-8333-444455556667.jsonl"),
			rec("session_meta", map[string]any{"id": "019a0000-1111-7222-8333-444455556667", "cwd": "/tmp/proj"}),
			rec("response_item", map[string]any{"type": "message", "role": "user", "content": []any{map[string]any{"type": "input_text", "text": "move the retry loop"}}}),
			rec("response_item", map[string]any{"type": "custom_tool_call", "name": "apply_patch", "call_id": "c1", "input": patch}),
		)
		return vocabParse(t, ParseCodexRollout, p)
	}
}

// opencode 1.18: apply_patch {patchText}.
func vocabOpencodePatch(patch string) func(t *testing.T) []model.Session {
	return func(t *testing.T) []model.Session {
		t.Helper()
		data := fmt.Sprintf(`{"type":"tool","tool":"apply_patch","callID":"p1","state":{"status":"completed","input":%s,"output":"ok","time":{"start":1790000002000}}}`,
			vocabJSON(map[string]any{"patchText": patch}))
		db := vocabSQL(t, `create table session(id text primary key, parent_id text, directory text, title text, time_created integer, time_updated integer);
create table message(id text, session_id text, time_created integer, data text);
create table part(id text, message_id text, data text);
insert into session values('ses_1',null,'/tmp/proj','move the retry loop',1790000000000,1790000100000);
insert into message values('m0','ses_1',1790000000000,'{"role":"user","time":{"created":1790000000000}}');
insert into part values('p0','m0','{"type":"text","text":"move the retry loop"}');
insert into message values('m1','ses_1',1790000001000,'{"role":"assistant","time":{"created":1790000001000}}');
insert into part values('p1','m1',`+sqlQuote(data)+`);
`)
		return vocabParse(t, ParseOpencodeDB, db)
	}
}

// vocabHas reports whether any session carries the record.
func vocabHas(ss []model.Session, want string) bool {
	for _, s := range ss {
		for _, m := range s.Messages {
			if m.Role+": "+m.Text == want {
				return true
			}
		}
	}
	return false
}

// vocabRoles lists every record of the given role, for a failure message.
func vocabRoles(ss []model.Session, role string) []string {
	var out []string
	for _, s := range ss {
		for _, m := range s.Messages {
			if m.Role == role {
				out = append(out, m.Text)
			}
		}
	}
	return out
}

// A NotebookEdit in delete mode still carries new_source, and Claude Code
// discards it: no line of it reaches the notebook, so it is no wrote record
// (#4489).
func TestClaudeNotebookDeleteWritesNothing(t *testing.T) {
	const gone = "retries = compute_backoff_with_jitter(attempt, base=0.5)\n"
	for name, parse := range map[string]func(string) ([]model.Session, error){
		"claude":    ParseClaudeFile,
		"reference": func(p string) ([]model.Session, error) { return parseClaudeGenericFromOffset(p, 0) },
	} {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			t.Setenv("DEJA_CLAUDE_ROOT", root)
			p := vocabWrite(t, filepath.Join(root, "-tmp-proj", "c1.jsonl"),
				`{"type":"user","sessionId":"c1","timestamp":"2026-10-01T10:00:00Z","cwd":"/tmp/proj","message":{"role":"user","content":"drop the old cell"}}`,
				vocabJSON(map[string]any{"type": "assistant", "sessionId": "c1", "timestamp": "2026-10-01T10:00:01Z", "cwd": "/tmp/proj",
					"message": map[string]any{"role": "assistant", "content": []any{map[string]any{"type": "tool_use", "id": "n1", "name": "NotebookEdit",
						"input": map[string]any{"notebook_path": "/tmp/proj/retry.ipynb", "cell_id": "c1", "new_source": gone, "edit_mode": "delete"}}}}}),
			)
			ss := vocabParse(t, parse, p)
			if !vocabHas(ss, vocabFiles("/tmp/proj/retry.ipynb")) {
				t.Errorf("the deleted cell's notebook is not a files record: %q", vocabRoles(ss, RoleFiles))
			}
			if w := vocabRoles(ss, RoleWrote); len(w) > 0 {
				t.Errorf("a deleted cell left wrote records %q", w)
			}
		})
	}
}

// Only copilot_readFile names in its message the file it opened. getErrors
// over the workspace lists every file with a problem, getChangedFiles the
// repository root, createDirectory a directory and memory a file of the
// extension's own — none a file the session touched (#4492).
func TestCopilotChatMessageURIsOnlyForReadFile(t *testing.T) {
	uri := func(paths ...string) map[string]any {
		out := map[string]any{}
		for _, p := range paths {
			out["file://"+p] = map[string]any{"$mid": 1, "path": p, "scheme": "file"}
		}
		return out
	}
	part := func(id, tool, msg string, uris map[string]any) any {
		return map[string]any{"kind": "toolInvocationSerialized", "toolId": tool, "toolCallId": id, "isComplete": true,
			"pastTenseMessage": map[string]any{"value": msg, "uris": uris}}
	}
	p := vocabWrite(t, filepath.Join(t.TempDir(), "chatSessions", "5c0ffee0-0000-4000-8000-000000000001.jsonl"),
		vocabJSON(map[string]any{"kind": 0, "v": map[string]any{"version": 3, "sessionId": "5c0ffee0-0000-4000-8000-000000000001", "creationDate": 1790000000000, "requests": []any{}}}),
		vocabJSON(map[string]any{"kind": 2, "k": []any{"requests"}, "v": []any{map[string]any{
			"requestId": "r1", "timestamp": 1790000001000, "message": map[string]any{"text": "fix the retry loop"},
			"response": []any{
				part("c1", "copilot_getErrors", "Checked workspace, 3 problems found in [](file:///tmp/proj/a.go), [](file:///tmp/proj/b.go)", uri("/tmp/proj/a.go", "/tmp/proj/b.go")),
				part("c2", "copilot_getChangedFiles", "Read changed files in [](file:///tmp/proj)", uri("/tmp/proj")),
				part("c3", "copilot_createDirectory", "Created [](file:///tmp/proj/pkg)", uri("/tmp/proj/pkg")),
				part("c4", "copilot_memory", "Read memory [](file:///tmp/storage/memory.md)", uri("/tmp/storage/memory.md")),
				part("c5", "copilot_readFile", "Read [](file:///tmp/proj/retry.go)", uri("/tmp/proj/retry.go")),
				map[string]any{"value": "The retry loop never stops."},
			}}}}),
	)
	ss := vocabParse(t, ParseCopilotChatFile, p)
	if got := vocabRoles(ss, RoleFiles); len(got) != 1 || got[0] != "/tmp/proj/retry.go" {
		t.Errorf("files records = %q, want only the file readFile opened", got)
	}
}

// A patch names files relative to where it runs: Copilot CLI and Cline resolve
// `*** Update File: retry.go` against the session's directory, and the record
// has to say /tmp/proj/retry.go for blame and restore to find it (#4491,
// #4503).
const relPatch = "*** Begin Patch\n*** Update File: retry.go\n@@\n-" + oldLoop + "\n+" + newLoop + "\n*** End Patch"

var relPatchWants = []string{
	vocabFiles("/tmp/proj/retry.go"),
	vocabEdit("/tmp/proj/retry.go", oldLoop),
	vocabWrote("/tmp/proj/retry.go", newLoop),
}

func TestRelativePatchPathsResolveAgainstTheSession(t *testing.T) {
	for name, parse := range map[string]func(t *testing.T) []model.Session{
		"copilot": vocabCopilot(relPatch),
		"copilot from an offset": func(t *testing.T) []model.Session {
			t.Helper()
			ss := vocabCopilot(relPatch)(t)
			path := ss[0].Path
			// Past session.start, the way an incremental index reads on.
			raw, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			off := int64(bytes.IndexByte(raw, '\n') + 1)
			out, err := ParseCopilotFileFromOffset(path, off)
			if err != nil {
				t.Fatal(err)
			}
			return out
		},
		"cline-sdk": func(t *testing.T) []model.Session {
			t.Helper()
			dir := filepath.Join(t.TempDir(), "1790877871095_abcdf")
			vocabWrite(t, filepath.Join(dir, "1790877871095_abcdf.json"), `{"session_id":"1790877871095_abcdf","cwd":"/tmp/proj"}`)
			p := vocabWrite(t, filepath.Join(dir, "1790877871095_abcdf.messages.json"), vocabJSON(map[string]any{"messages": []any{
				map[string]any{"role": "user", "content": []any{map[string]any{"type": "text", "text": "fix the retry loop"}}},
				map[string]any{"role": "assistant", "content": []any{map[string]any{"type": "tool_use", "id": "c1", "name": "apply_patch", "input": map[string]any{"input": relPatch}}}},
			}}))
			return vocabParse(t, ParseClineFile, p)
		},
	} {
		t.Run(name, func(t *testing.T) {
			ss := parse(t)
			for _, w := range relPatchWants {
				if !vocabHas(ss, w) {
					t.Errorf("missing %q; files %q, edits %q", w, vocabRoles(ss, RoleFiles), vocabRoles(ss, RoleEdit))
				}
			}
		})
	}
}

// Cline matches its markers as whole lines of three or more of a character —
// /^[-]{3,} SEARCH>?$/, /^[=]{3,}$/, /^[+]{3,} REPLACE>?$/ and Roo's < and >
// spellings — so a model's `--- SEARCH` is a block and an indented `=======`
// inside the replaced code is not a marker (#4504).
func TestClineDiffMarkersAsClineReadsThem(t *testing.T) {
	cases := []struct {
		name, diff        string
		replaced, written []string
	}{
		{"canonical", "------- SEARCH\na\n=======\nb\n+++++++ REPLACE", []string{"a"}, []string{"b"}},
		{"roo's form", "<<<<<<< SEARCH\na\n=======\nb\n>>>>>>> REPLACE", []string{"a"}, []string{"b"}},
		{"three and eight", "--- SEARCH\na\n===\nb\n++++++++ REPLACE", []string{"a"}, []string{"b"}},
		{"trailing >", "------- SEARCH>\na\n=======\nb\n+++++++ REPLACE>", []string{"a"}, []string{"b"}},
		{"two blocks", "------- SEARCH\na\n=======\nb\n+++++++ REPLACE\n\n------- SEARCH\nc\n=======\nd\n+++++++ REPLACE", []string{"a", "c"}, []string{"b", "d"}},
		{"crlf", "------- SEARCH\r\na\r\n=======\r\nb\r\n+++++++ REPLACE\r\n", []string{"a\r"}, []string{"b\r"}},
		{"indented rule in the code", "------- SEARCH\nx := 1\n    =======\ny := 2\n=======\nz := 3\n+++++++ REPLACE", []string{"x := 1\n    =======\ny := 2"}, []string{"z := 3"}},
		{"roo line numbers", "<<<<<<< SEARCH\n:start_line:3\n-------\na\n=======\nb\n>>>>>>> REPLACE", []string{"a"}, []string{"b"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			replaced, written := rooDiffSides(c.diff, clineMarkers)
			if strings.Join(replaced, "|") != strings.Join(c.replaced, "|") || strings.Join(written, "|") != strings.Join(c.written, "|") {
				t.Errorf("sides = %q / %q, want %q / %q", replaced, written, c.replaced, c.written)
			}
		})
	}
}

// Roo's apply_diff takes exactly seven, so a `===` rule in the replaced text
// stays text there (#4504).
func TestRooDiffMarkersStayExact(t *testing.T) {
	replaced, written := rooDiffSides("<<<<<<< SEARCH\nTitle\n===\n=======\nHeading\n===\n>>>>>>> REPLACE", rooMarkers)
	if len(replaced) != 1 || replaced[0] != "Title\n===" || len(written) != 1 || written[0] != "Heading\n===" {
		t.Errorf("sides = %q / %q", replaced, written)
	}
}

// The v3 engine persists a tool_call line each time the action changes state
// — awaiting approval, running, then how it ended — under one id and one
// start time. Every line was read as the call made again, and a denied write
// as a write (#4506).
func TestKiroIDECallsOnceAndOnlyWhenRun(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "f045c81011ce49e2", "sess_00000000-0000-4000-8000-0000000000ab")
	line := func(id, name, status string, args any) string {
		return vocabJSON(map[string]any{"id": id + "-call", "timestamp": "2026-10-01T10:00:01.000Z", "payload": map[string]any{
			"type": "tool_call", "toolCallId": id, "toolName": name, "args": args, "status": status, "kind": "edit", "executionId": "e1", "actionType": name}})
	}
	edit := map[string]any{"path": "/tmp/proj/retry.go", "oldStr": oldLoop, "newStr": newLoop}
	denied := map[string]any{"path": "/tmp/proj/secret.go", "text": jitter}
	failed := map[string]any{"path": "/tmp/proj/failed.go", "oldStr": "func stale() {}", "newStr": "func fresh() { return nil }"}
	p := vocabWrite(t, filepath.Join(dir, "messages.jsonl"),
		`{"id":"u1","timestamp":"2026-10-01T10:00:00.000Z","payload":{"type":"user","content":"fix the retry loop"}}`,
		line("a1", "str_replace", "awaiting_approval", edit),
		line("a1", "str_replace", "executing", edit),
		line("a1", "str_replace", "completed", edit),
		line("a2", "fs_write", "awaiting_approval", denied),
		line("a2", "fs_write", "denied", denied),
		line("a3", "str_replace", "executing", failed),
		line("a3", "str_replace", "failed", failed),
	)
	ss := vocabParse(t, ParseKiroIDEFile, p)
	if got := vocabRoles(ss, RoleEdit); len(got) != 1 || got[0] != "/tmp/proj/retry.go\n"+oldLoop {
		t.Errorf("edit records = %q, want the one completed str_replace once", got)
	}
	if got := vocabRoles(ss, RoleWrote); len(got) != 1 || got[0] != WroteRecord("/tmp/proj/retry.go", newLoop) {
		t.Errorf("wrote records = %q, want the one completed str_replace once", got)
	}
	for _, f := range vocabRoles(ss, RoleFiles) {
		if strings.Contains(f, "secret.go") {
			t.Errorf("a denied write left a files record %q", f)
		}
	}
}

// grok-dev stores a call whose arguments were not valid JSON with input as
// the raw string, and names files relative to the session's cwd. The string
// cost every other call in its message; the relative path matched no file
// (#4498).
func TestGrokDBRawInputAndRelativePaths(t *testing.T) {
	msg := map[string]any{"role": "assistant", "content": []any{
		map[string]any{"type": "tool-call", "toolCallId": "c0", "toolName": "bash", "input": `{"command": "go test`},
		map[string]any{"type": "tool-call", "toolCallId": "c1", "toolName": "edit_file", "input": map[string]any{"path": "retry.go", "old_string": oldLoop, "new_string": newLoop}},
	}}
	db := vocabSQL(t, `CREATE TABLE sessions (id TEXT PRIMARY KEY, workspace_id TEXT, title TEXT, cwd_last TEXT, created_at TEXT);
CREATE TABLE messages (session_id TEXT, seq INTEGER, role TEXT, message_json TEXT, created_at TEXT);
INSERT INTO sessions VALUES ('gd2','w1','fix the retry loop','/tmp/proj','2026-09-20T10:00:00.000Z');
INSERT INTO messages VALUES ('gd2',0,'user','{"role":"user","content":"fix the retry loop"}','2026-09-20T10:00:00.000Z');
INSERT INTO messages VALUES ('gd2',1,'assistant',`+sqlQuote(vocabJSON(msg))+`,'2026-09-20T10:00:01.000Z');`)
	ss := vocabParse(t, func(db string) ([]model.Session, error) { return ParseGrokDBSince(db, time.Time{}) }, db)
	for _, w := range relPatchWants {
		if !vocabHas(ss, w) {
			t.Errorf("missing %q; files %q, edits %q", w, vocabRoles(ss, RoleFiles), vocabRoles(ss, RoleEdit))
		}
	}
}

// read_many_files takes paths relative to the project and directories beside
// files ("docs/"): a directory is not a file the session read, and a relative
// path is under the directory the session ran in (#4494).
func TestGeminiReadManyFilesDirectoriesAndRelativePaths(t *testing.T) {
	_, chats := geminiTree(t)
	vocabWrite(t, filepath.Join(filepath.Dir(chats), ".project_root"), "/tmp/proj")
	call := map[string]any{"id": "rm1", "name": "read_many_files", "status": "success",
		"args": map[string]any{"include": []any{"docs/", "src/a.go", "/abs/b.go", "src/**/*.go"}}}
	p := vocabWrite(t, filepath.Join(chats, "session-2026-10-02T10-00-sess-v-2.jsonl"),
		`{"sessionId":"sess-v-2","projectHash":"abc","startTime":"2026-10-02T10:00:00.000Z","lastUpdated":"2026-10-02T10:01:00.000Z","kind":"main"}`,
		`{"id":"u1","timestamp":"2026-10-02T10:00:01.000Z","type":"user","content":[{"text":"read the docs"}]}`,
		`{"id":"g1","timestamp":"2026-10-02T10:00:02.000Z","type":"gemini","content":"","model":"luna","toolCalls":[`+vocabJSON(call)+`]}`,
	)
	ss := vocabParse(t, ParseGeminiFile, p)
	if got := vocabRoles(ss, RoleFiles); len(got) != 1 || got[0] != "/tmp/proj/src/a.go\n/abs/b.go" {
		t.Errorf("files records = %q, want the two files, the relative one under the project", got)
	}
}

// The glob filter is opt-in per dialect: a list of plain paths keeps a Next.js
// route file named with brackets (#4494). Command Code opts in, since 1.74
// expands any entry with *, ?, [ or { as a glob (isGlobPattern, #4540).
func TestPathListKeepsBracketedFiles(t *testing.T) {
	got := toolPathStrings(map[string]any{"paths": []any{"/tmp/proj/app/[id]/page.tsx"}}, toolDialect{pathListKey: "paths"})
	if len(got) != 1 || got[0] != "/tmp/proj/app/[id]/page.tsx" {
		t.Errorf("paths = %q, want the bracketed route file", got)
	}
}

const (
	oldLoop = "\tfor {"
	newLoop = "\tfor attempt := 0; attempt < maxAttempts; attempt++ {"
	jitter  = "func backoffJitter(attempt int) time.Duration { return 0 }"
)

// patch is one apply_patch body in the format codex, Copilot CLI, OpenClaw and
// Cline share, and patchWants what every reader must make of it.
var (
	patch      = "*** Begin Patch\n*** Update File: /tmp/proj/retry.go\n@@\n-" + oldLoop + "\n+" + newLoop + "\n*** Add File: /tmp/proj/jitter.go\n+" + jitter + "\n*** End Patch"
	patchWants = []string{
		vocabFiles("/tmp/proj/retry.go\n/tmp/proj/jitter.go"),
		vocabEdit("/tmp/proj/retry.go", oldLoop),
		vocabWrote("/tmp/proj/retry.go", newLoop),
		vocabWrote("/tmp/proj/jitter.go", jitter),
	}
)

// Each row is tool calls the way a harness's own client writes them, with the
// tool names and argument keys read off that client's registry, and the work
// records they must yield. A reader that knew the transport but not the
// vocabulary kept the calls as bare output: no command, no file, no edit and
// nothing for blame to attribute (#4489–#4506).
func TestToolVocabularyAcrossReaders(t *testing.T) {
	cases := []struct {
		name  string
		parse func(t *testing.T) []model.Session
		want  []string
	}{
		{"claude PowerShell and NotebookEdit", vocabClaude(ParseClaudeFile), []string{
			vocabCmd("$ go test ./..."),
			vocabFiles("/tmp/proj/retry.ipynb"),
			vocabWrote("/tmp/proj/retry.ipynb", "retries = compute_backoff_with_jitter(attempt, base=0.5)\n"),
		}},
		{"claude reference parser", vocabClaude(func(p string) ([]model.Session, error) { return parseClaudeGenericFromOffset(p, 0) }), []string{
			vocabCmd("$ go test ./..."),
			vocabFiles("/tmp/proj/retry.ipynb"),
			vocabWrote("/tmp/proj/retry.ipynb", "retries = compute_backoff_with_jitter(attempt, base=0.5)\n"),
		}},
		{"codex shell_command", vocabCodex, []string{
			vocabCmd("$ go test ./...  → exit 1"),
		}},
		{"copilot view and apply_patch", vocabCopilot(patch), append([]string{
			vocabFiles("/tmp/proj/view.go"),
		}, patchWants...)},
		{"copilot-chat readFile", vocabCopilotChat, []string{
			vocabFiles("/tmp/proj/retry.go"),
		}},
		{"gemini read_many_files", vocabGemini, []string{
			vocabFiles("/tmp/proj/many_a.go\n/tmp/proj/many_b.go"),
		}},
		{"opencode 1.x edit and write", vocabOpencodeV1, []string{
			vocabEdit("/tmp/proj/retry.go", oldLoop),
			vocabWrote("/tmp/proj/retry.go", newLoop),
			vocabWrote("/tmp/proj/backoff.go", jitter),
		}},
		{"opencode 2.x write", vocabOpencodeV2, []string{
			vocabWrote("/tmp/proj/backoff.go", jitter),
		}},
		{"grok search_replace and write", vocabGrok, []string{
			vocabFiles("/tmp/proj/retry.go"),
			vocabEdit("/tmp/proj/retry.go", oldLoop),
			vocabWrote("/tmp/proj/retry.go", newLoop),
			vocabFiles("/tmp/proj/jitter.go"),
			vocabWrote("/tmp/proj/jitter.go", jitter),
		}},
		{"grok-dev grok.db", vocabGrokDB, []string{
			vocabCmd("$ go test ./..."),
			RoleToolOutput + ": ./retry.go:12:5: undefined: backoffJitter",
			vocabFiles("/tmp/proj/read.go"),
			vocabFiles("/tmp/proj/retry.go"),
			vocabEdit("/tmp/proj/retry.go", oldLoop),
			vocabWrote("/tmp/proj/retry.go", newLoop),
			vocabWrote("/tmp/proj/jitter.go", jitter),
		}},
		{"openclaw apply_patch", vocabOpenClaw(patch), patchWants},
		{"cline-sdk editor and apply_patch", vocabClineSDK(patch), append([]string{
			vocabFiles("/tmp/proj/jitter.go"),
			vocabWrote("/tmp/proj/jitter.go", jitter),
			vocabEdit("/tmp/proj/retry.go", oldLoop),
		}, patchWants...)},
		{"cline-vscode replace_in_file and apply_patch", vocabClineVSCode(patch), append([]string{
			vocabEdit("/tmp/proj/backoff.go", "for retries := 0; ; retries++ {"),
			vocabWrote("/tmp/proj/backoff.go", "for attempt := 0; attempt < maxAttempts; attempt++ {"),
		}, patchWants...)},
		{"kiro-ide tool calls", vocabKiroIDE, []string{
			vocabCmd("$ go test ./retry"),
			vocabFiles("/tmp/proj/retry.go"),
			vocabEdit("/tmp/proj/retry.go", oldLoop),
			vocabWrote("/tmp/proj/retry.go", newLoop),
			vocabFiles("/tmp/proj/jitter.go"),
			vocabWrote("/tmp/proj/jitter.go", jitter),
			vocabWrote("/tmp/proj/notes.md", "retries are capped at maxAttempts with a jittered backoff"),
			vocabFiles("/tmp/proj/read.go"),
			vocabFiles("/tmp/proj/old.go"),
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ss := c.parse(t)
			got := map[string]bool{}
			var all []string
			for _, s := range ss {
				for _, m := range s.Messages {
					got[m.Role+": "+m.Text] = true
					all = append(all, fmt.Sprintf("%s: %q", m.Role, m.Text))
				}
			}
			missing := false
			for _, w := range c.want {
				if !got[w] {
					missing = true
					t.Errorf("missing %q", w)
				}
			}
			if missing {
				t.Logf("got:\n  %s", strings.Join(all, "\n  "))
			}
		})
	}
}

func vocabCmd(s string) string   { return RoleCommand + ": " + s }
func vocabFiles(s string) string { return RoleFiles + ": " + s }
func vocabEdit(path, span string) string {
	return RoleEdit + ": " + path + "\n" + span
}
func vocabWrote(path, text string) string { return RoleWrote + ": " + WroteRecord(path, text) }

func vocabJSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return string(b)
}

func mustJSON(tb testing.TB, v any) []byte {
	tb.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		tb.Fatal(err)
	}
	return b
}

func vocabWrite(t *testing.T, path string, lines ...string) string {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func vocabParse(t *testing.T, parse func(string) ([]model.Session, error), path string) []model.Session {
	t.Helper()
	ss, err := parse(path)
	if err != nil {
		t.Fatal(err)
	}
	return ss
}

func vocabSQL(t *testing.T, script string) string {
	t.Helper()
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skip("sqlite3 not installed")
	}
	db := filepath.Join(t.TempDir(), "store.db")
	if out, err := exec.CommandContext(t.Context(), "sqlite3", db, script).CombinedOutput(); err != nil {
		t.Fatalf("sqlite setup: %v %s", err, out)
	}
	return db
}

// Claude Code 2.1.287: PowerShell {command}, NotebookEdit {notebook_path,
// cell_id, new_source, edit_mode} (#4489).
func vocabClaude(parse func(string) ([]model.Session, error)) func(t *testing.T) []model.Session {
	return func(t *testing.T) []model.Session {
		t.Helper()
		root := t.TempDir()
		t.Setenv("DEJA_CLAUDE_ROOT", root)
		line := func(content any) string {
			return vocabJSON(map[string]any{"type": "assistant", "sessionId": "c1", "timestamp": "2026-10-01T10:00:00Z", "cwd": "/tmp/proj",
				"message": map[string]any{"role": "assistant", "content": content}})
		}
		p := vocabWrite(t, filepath.Join(root, "-tmp-proj", "c1.jsonl"),
			`{"type":"user","sessionId":"c1","timestamp":"2026-10-01T10:00:00Z","cwd":"/tmp/proj","message":{"role":"user","content":"fix the retry loop"}}`,
			line([]any{map[string]any{"type": "tool_use", "id": "p1", "name": "PowerShell", "input": map[string]any{"command": "go test ./..."}}}),
			line([]any{map[string]any{"type": "tool_use", "id": "n1", "name": "NotebookEdit", "input": map[string]any{
				"notebook_path": "/tmp/proj/retry.ipynb", "cell_id": "c1", "new_source": "retries = compute_backoff_with_jitter(attempt, base=0.5)\n", "edit_mode": "replace"}}}),
		)
		return vocabParse(t, parse, p)
	}
}

// Codex 0.149: shell_command {command, workdir}, the shell whenever unified
// exec is off (#4490).
func vocabCodex(t *testing.T) []model.Session {
	t.Helper()
	rec := func(typ string, payload any) string {
		return vocabJSON(map[string]any{"timestamp": "2026-10-01T10:00:01.000Z", "type": typ, "payload": payload})
	}
	p := vocabWrite(t, filepath.Join(t.TempDir(), "rollout-2026-10-01T10-00-00-019a0000-1111-7222-8333-444455556666.jsonl"),
		rec("session_meta", map[string]any{"id": "019a0000-1111-7222-8333-444455556666", "cwd": "/tmp/proj"}),
		rec("response_item", map[string]any{"type": "message", "role": "user", "content": []any{map[string]any{"type": "input_text", "text": "fix the retry loop"}}}),
		rec("response_item", map[string]any{"type": "function_call", "name": "shell_command", "call_id": "c1",
			"arguments": vocabJSON(map[string]any{"command": "go test ./...", "workdir": "/tmp/proj"})}),
		rec("response_item", map[string]any{"type": "function_call_output", "call_id": "c1",
			"output": "Exit code: 1\nWall time: 0.4 seconds\nOutput:\n./retry.go:12:5: undefined: backoffJitter\nFAIL"}),
	)
	return vocabParse(t, ParseCodexRollout, p)
}

// Copilot CLI 1.0.79: view {path}, and with a GPT model apply_patch, whose
// arguments are the patch string itself (#4491).
func vocabCopilot(patch string) func(t *testing.T) []model.Session {
	return func(t *testing.T) []model.Session {
		t.Helper()
		ev := func(name string, args any) string {
			return vocabJSON(map[string]any{"type": "tool.execution_start", "timestamp": "2026-10-01T10:00:01.000Z",
				"data": map[string]any{"toolCallId": name, "toolName": name, "arguments": args}})
		}
		p := vocabWrite(t, filepath.Join(t.TempDir(), "c0c0c0c0", "events.jsonl"),
			vocabJSON(map[string]any{"type": "session.start", "data": map[string]any{"sessionId": "c0c0c0c0", "startTime": "2026-10-01T10:00:00.000Z", "context": map[string]any{"cwd": "/tmp/proj"}}}),
			`{"type":"user.message","timestamp":"2026-10-01T10:00:00.500Z","data":{"content":"fix the retry loop"}}`,
			ev("view", map[string]any{"path": "/tmp/proj/view.go"}),
			ev("apply_patch", patch),
		)
		return vocabParse(t, ParseCopilotFile, p)
	}
}

// Copilot Chat 0.64.1: copilot_readFile keeps its target only in the
// message uris (#4492).
func vocabCopilotChat(t *testing.T) []model.Session {
	t.Helper()
	uris := map[string]any{"file:///tmp/proj/retry.go": map[string]any{"$mid": 1, "path": "/tmp/proj/retry.go", "scheme": "file"}}
	p := vocabWrite(t, filepath.Join(t.TempDir(), "chatSessions", "5c0ffee0-0000-4000-8000-000000000000.jsonl"),
		vocabJSON(map[string]any{"kind": 0, "v": map[string]any{"version": 3, "sessionId": "5c0ffee0-0000-4000-8000-000000000000", "creationDate": 1790000000000, "requests": []any{}}}),
		vocabJSON(map[string]any{"kind": 2, "k": []any{"requests"}, "v": []any{map[string]any{
			"requestId": "r1", "timestamp": 1790000001000, "message": map[string]any{"text": "fix the retry loop"},
			"response": []any{
				map[string]any{"kind": "toolInvocationSerialized", "toolId": "copilot_readFile", "toolCallId": "c1", "isComplete": true,
					"invocationMessage": map[string]any{"value": "Reading [](file:///tmp/proj/retry.go)", "uris": uris},
					"pastTenseMessage":  map[string]any{"value": "Read [](file:///tmp/proj/retry.go)", "uris": uris}},
				map[string]any{"value": "The retry loop never stops."},
			}}}}),
	)
	return vocabParse(t, ParseCopilotChatFile, p)
}

// Gemini CLI 0.60.0: read_many_files {include: [paths or globs]} (#4494).
func vocabGemini(t *testing.T) []model.Session {
	t.Helper()
	_, chats := geminiTree(t)
	call := map[string]any{"id": "rm1", "name": "read_many_files", "status": "success",
		"args": map[string]any{"include": []any{"/tmp/proj/many_a.go", "/tmp/proj/many_b.go", "src/**/*.go"}}}
	p := vocabWrite(t, filepath.Join(chats, "session-2026-10-02T10-00-sess-v-1.jsonl"),
		`{"sessionId":"sess-v-1","projectHash":"abc","startTime":"2026-10-02T10:00:00.000Z","lastUpdated":"2026-10-02T10:01:00.000Z","kind":"main"}`,
		`{"id":"u1","timestamp":"2026-10-02T10:00:01.000Z","type":"user","content":[{"text":"fix the retry loop"}]}`,
		`{"id":"g1","timestamp":"2026-10-02T10:00:02.000Z","type":"gemini","content":"","model":"luna","toolCalls":[`+vocabJSON(call)+`]}`,
	)
	return vocabParse(t, ParseGeminiFile, p)
}

// opencode 1.18: edit {filePath, oldString, newString}, write {filePath,
// content} (#4495).
func vocabOpencodeV1(t *testing.T) []model.Session {
	t.Helper()
	part := func(id, tool string, input any) string {
		// Spelled out rather than marshalled: opencode writes type and tool
		// first, and the reader's cheap gate looks for them there.
		data := fmt.Sprintf(`{"type":"tool","tool":%q,"callID":%q,"state":{"status":"completed","input":%s,"output":"ok","time":{"start":1790000002000}}}`, tool, id, vocabJSON(input))
		return fmt.Sprintf("insert into part values('%s','m1',%s);\n", id, sqlQuote(data))
	}
	db := vocabSQL(t, `create table session(id text primary key, parent_id text, directory text, title text, time_created integer, time_updated integer);
create table message(id text, session_id text, time_created integer, data text);
create table part(id text, message_id text, data text);
insert into session values('ses_1',null,'/tmp/proj','fix the retry loop',1790000000000,1790000100000);
insert into message values('m0','ses_1',1790000000000,'{"role":"user","time":{"created":1790000000000}}');
insert into part values('p0','m0','{"type":"text","text":"fix the retry loop"}');
insert into message values('m1','ses_1',1790000001000,'{"role":"assistant","time":{"created":1790000001000}}');
`+part("p1", "edit", map[string]any{"filePath": "/tmp/proj/retry.go", "oldString": "\tfor {", "newString": "\tfor attempt := 0; attempt < maxAttempts; attempt++ {"})+
		part("p2", "write", map[string]any{"filePath": "/tmp/proj/backoff.go", "content": "func backoffJitter(attempt int) time.Duration { return 0 }"}))
	return vocabParse(t, ParseOpencodeDB, db)
}

// opencode 2.0.22: write {path, content} (#4495).
func vocabOpencodeV2(t *testing.T) []model.Session {
	t.Helper()
	content := []any{map[string]any{"type": "tool", "id": "c1", "name": "write", "time": map[string]any{"start": 1790000002000},
		"state": map[string]any{"status": "completed", "input": map[string]any{"path": "/tmp/proj/backoff.go", "content": "func backoffJitter(attempt int) time.Duration { return 0 }"}}}}
	db := vocabSQL(t, `create table session_v2(id text primary key, project_id text, parent_id text, directory text, title text, time_created integer, time_updated integer);
create table session_message(id text primary key, session_id text, type text, seq integer, time_created integer, time_updated integer, data text);
insert into session_v2 values('s1','p1',null,'/tmp/proj','fix the retry loop',1790000000000,1790000100000);
insert into session_message values('m1','s1','user',1,1790000000000,1790000000000,'{"time":{"created":1790000000000},"text":"fix the retry loop"}');
insert into session_message values('m2','s1','assistant',2,1790000001000,1790000001000,`+sqlQuote(vocabJSON(map[string]any{"time": map[string]any{"created": 1790000001000}, "content": content}))+`);`)
	return vocabParse(t, ParseOpencodeDB, db)
}

// Grok Build 1.0.41: search_replace {file_path, old_string, new_string},
// write {file_path, content} (#4497).
func vocabGrok(t *testing.T) []model.Session {
	t.Helper()
	call := func(id, name string, in any) string {
		return vocabJSON(map[string]any{"timestamp": 1790874400002, "params": map[string]any{"update": map[string]any{
			"sessionUpdate": "tool_call", "toolCallId": id, "title": name, "rawInput": in,
			"_meta": map[string]any{"x.ai/tool": map[string]any{"name": name}}}}})
	}
	dir := filepath.Join(t.TempDir(), "sessions", "%2Ftmp%2Fproj", "01a0f900-0000-7000-8000-000000000001")
	vocabWrite(t, filepath.Join(dir, "summary.json"), `{"info":{"id":"01a0f900-0000-7000-8000-000000000001","cwd":"/tmp/proj"}}`)
	p := vocabWrite(t, filepath.Join(dir, "updates.jsonl"),
		`{"timestamp":1790874400001,"params":{"update":{"sessionUpdate":"user_message_chunk","content":{"type":"text","text":"fix the retry loop"}}}}`,
		call("c2", "search_replace", map[string]any{"file_path": "/tmp/proj/retry.go", "old_string": "\tfor {", "new_string": "\tfor attempt := 0; attempt < maxAttempts; attempt++ {"}),
		call("c3", "write", map[string]any{"file_path": "/tmp/proj/jitter.go", "content": "func backoffJitter(attempt int) time.Duration { return 0 }"}),
	)
	return vocabParse(t, ParseGrokFile, p)
}

// grok-dev 1.1.7: AI SDK tool-call and tool-result parts in grok.db, bash
// {command}, read_file/write_file/edit_file {path, …} (#4498).
func vocabGrokDB(t *testing.T) []model.Session {
	t.Helper()
	var b strings.Builder
	b.WriteString(`CREATE TABLE sessions (id TEXT PRIMARY KEY, workspace_id TEXT, title TEXT, cwd_last TEXT, created_at TEXT);
CREATE TABLE messages (session_id TEXT, seq INTEGER, role TEXT, message_json TEXT, created_at TEXT);
INSERT INTO sessions VALUES ('gd1','w1','fix the retry loop','/tmp/proj','2026-09-20T10:00:00.000Z');
`)
	seq := 0
	add := func(role string, msg any) {
		fmt.Fprintf(&b, "INSERT INTO messages VALUES ('gd1',%d,'%s',%s,'2026-09-20T10:00:%02d.000Z');\n", seq, role, sqlQuote(vocabJSON(msg)), seq)
		seq++
	}
	add("user", map[string]any{"role": "user", "content": "fix the retry loop"})
	for i, c := range []struct {
		name    string
		in, out map[string]any
	}{
		{"bash", map[string]any{"command": "go test ./..."}, map[string]any{"success": false, "error": "./retry.go:12:5: undefined: backoffJitter"}},
		{"read_file", map[string]any{"path": "/tmp/proj/read.go"}, map[string]any{"success": true, "output": "package proj"}},
		{"edit_file", map[string]any{"path": "/tmp/proj/retry.go", "old_string": "\tfor {", "new_string": "\tfor attempt := 0; attempt < maxAttempts; attempt++ {"}, map[string]any{"success": true, "output": "Edited /tmp/proj/retry.go"}},
		{"write_file", map[string]any{"path": "/tmp/proj/jitter.go", "content": "func backoffJitter(attempt int) time.Duration { return 0 }"}, map[string]any{"success": true, "output": "Wrote /tmp/proj/jitter.go"}},
	} {
		id := fmt.Sprintf("c%d", i)
		add("assistant", map[string]any{"role": "assistant", "content": []any{map[string]any{"type": "tool-call", "toolCallId": id, "toolName": c.name, "input": c.in}}})
		add("tool", map[string]any{"role": "tool", "content": []any{map[string]any{"type": "tool-result", "toolCallId": id, "toolName": c.name, "output": map[string]any{"type": "json", "value": c.out}}}})
	}
	return vocabParse(t, func(db string) ([]model.Session, error) { return ParseGrokDBSince(db, time.Time{}) }, vocabSQL(t, b.String()))
}

// OpenClaw 2026.7.1: apply_patch {input}, on for every model (#4500).
func vocabOpenClaw(patch string) func(t *testing.T) []model.Session {
	return func(t *testing.T) []model.Session {
		t.Helper()
		p := vocabWrite(t, filepath.Join(t.TempDir(), "agents", "main", "sessions", "oc-s1.jsonl"),
			`{"type":"session","version":3,"id":"oc-s1","timestamp":"2026-09-20T10:00:00.000Z","cwd":"/tmp/proj"}`,
			`{"type":"message","id":"u1","timestamp":"2026-09-20T10:00:00.000Z","message":{"role":"user","content":[{"type":"text","text":"fix the retry loop"}]}}`,
			vocabJSON(map[string]any{"type": "message", "id": "a1", "timestamp": "2026-09-20T10:00:01.000Z", "message": map[string]any{"role": "assistant",
				"content": []any{map[string]any{"type": "toolCall", "id": "c1", "name": "apply_patch", "arguments": map[string]any{"input": patch}}}}}),
			`{"type":"message","id":"r1","timestamp":"2026-09-20T10:00:02.000Z","message":{"role":"toolResult","toolCallId":"c1","toolName":"apply_patch","content":[{"type":"text","text":"Success. Updated the following files:\nM /tmp/proj/retry.go"}],"details":{},"isError":false}}`,
		)
		return vocabParse(t, ParseOpenClawFile, p)
	}
}

// Cline CLI 3.0.67 (@cline/core 0.0.89): editor {path, old_text?, new_text},
// apply_patch {input} (#4503).
func vocabClineSDK(patch string) func(t *testing.T) []model.Session {
	return func(t *testing.T) []model.Session {
		t.Helper()
		use := func(id, name string, in any) any {
			return map[string]any{"role": "assistant", "content": []any{map[string]any{"type": "tool_use", "id": id, "name": name, "input": in}}}
		}
		doc := map[string]any{"messages": []any{
			map[string]any{"role": "user", "content": []any{map[string]any{"type": "text", "text": "fix the retry loop"}}},
			use("c1", "editor", map[string]any{"path": "/tmp/proj/jitter.go", "new_text": "func backoffJitter(attempt int) time.Duration { return 0 }"}),
			use("c2", "editor", map[string]any{"path": "/tmp/proj/retry.go", "old_text": "\tfor {", "new_text": "\tfor attempt := 0; attempt < maxAttempts; attempt++ {"}),
			use("c3", "apply_patch", map[string]any{"input": patch}),
		}}
		p := vocabWrite(t, filepath.Join(t.TempDir(), "1790877871094_abcde", "1790877871094_abcde.messages.json"), vocabJSON(doc))
		return vocabParse(t, ParseClineFile, p)
	}
}

// Cline extension 3.81: replace_in_file {path, diff} with Cline's own
// markers, apply_patch {input} (#4504).
func vocabClineVSCode(patch string) func(t *testing.T) []model.Session {
	return func(t *testing.T) []model.Session {
		t.Helper()
		use := func(id, name string, in any) any {
			return map[string]any{"role": "assistant", "content": []any{map[string]any{"type": "tool_use", "id": id, "name": name, "input": in}}}
		}
		doc := []any{
			map[string]any{"role": "user", "content": []any{map[string]any{"type": "text", "text": "fix the retry loop"}}},
			use("t1", "replace_in_file", map[string]any{"path": "/tmp/proj/backoff.go",
				"diff": "------- SEARCH\nfor retries := 0; ; retries++ {\n=======\nfor attempt := 0; attempt < maxAttempts; attempt++ {\n+++++++ REPLACE"}),
			use("t2", "apply_patch", map[string]any{"input": patch}),
		}
		p := vocabWrite(t, filepath.Join(t.TempDir(), "tasks", "1790000000000", "api_conversation_history.json"), vocabJSON(doc))
		return vocabParse(t, ParseClineFile, p)
	}
}

// kiro-cli --v3 and the Kiro IDE (@kiro/agent 0.66.0): tool_call records with
// toolName and args (#4506).
func vocabKiroIDE(t *testing.T) []model.Session {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "f045c81011ce49e2", "sess_00000000-0000-4000-8000-0000000000aa")
	n := 0
	rec := func(payload any) string {
		n++
		return vocabJSON(map[string]any{"id": fmt.Sprintf("r%d", n), "timestamp": fmt.Sprintf("2026-10-01T10:00:%02d.000Z", n), "payload": payload})
	}
	call := func(id, name string, args any) string {
		return rec(map[string]any{"type": "tool_call", "toolCallId": id, "toolName": name, "args": args, "status": "completed"})
	}
	p := vocabWrite(t, filepath.Join(dir, "messages.jsonl"),
		rec(map[string]any{"type": "user", "content": "fix the retry loop"}),
		call("c1", "execute_bash", map[string]any{"command": "go test ./retry", "cwd": "/tmp/proj"}),
		rec(map[string]any{"type": "tool_result", "toolCallId": "c1", "content": "Output: ./retry.go:12:5: undefined: backoffJitter  Exit Code: 1", "success": false}),
		call("c2", "str_replace", map[string]any{"path": "/tmp/proj/retry.go", "oldStr": "\tfor {", "newStr": "\tfor attempt := 0; attempt < maxAttempts; attempt++ {"}),
		call("c3", "fs_write", map[string]any{"path": "/tmp/proj/jitter.go", "text": "func backoffJitter(attempt int) time.Duration { return 0 }"}),
		call("c4", "fs_append", map[string]any{"path": "/tmp/proj/notes.md", "text": "retries are capped at maxAttempts with a jittered backoff"}),
		call("c5", "read_file", map[string]any{"path": "/tmp/proj/read.go"}),
		call("c6", "delete_file", map[string]any{"targetFile": "/tmp/proj/old.go"}),
	)
	return vocabParse(t, ParseKiroIDEFile, p)
}

// A pass that lands while the client is appending sees the last line cut
// short with no newline yet. That is the client still writing, not a broken
// line, and the next pass reads it whole; counting it put "1 line skipped,
// deja could not read it" on an ordinary update (#4276). A line that did end
// is still counted, so the note keeps its meaning.
func TestALineTheClientIsStillWritingIsNotCalledUnreadable(t *testing.T) {
	dir := t.TempDir()
	whole := `{"type":"user","sessionId":"s1","uuid":"u1","timestamp":"2026-10-01T10:00:00Z","message":{"role":"user","content":"tune the backoff jitter"}}` + "\n"
	cut := `{"type":"assistant","sessionId":"s1","uuid":"u2","timestamp":"2026-10-01T10:00:05Z","message":{"role":"assi`
	for _, tc := range []struct {
		name  string
		parse func(string) error
	}{
		{"generic", func(p string) error { return scanJSONLFromOffset(p, 0, func(map[string]any) {}) }},
		{"claude", func(p string) error { _, err := ParseClaudeFile(p); return err }},
	} {
		growing := filepath.Join(dir, tc.name+"-growing.jsonl")
		broken := filepath.Join(dir, tc.name+"-broken.jsonl")
		if err := os.WriteFile(growing, []byte(whole+cut), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(broken, []byte(whole+cut+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		for _, p := range []string{growing, broken} {
			if err := tc.parse(p); err != nil {
				t.Fatal(err)
			}
		}
		got := DiagMalformedCounts()
		if got[growing] != 0 {
			t.Errorf("%s: the half-written last line was counted unreadable %d time(s)", tc.name, got[growing])
		}
		if got[broken] != 1 {
			t.Errorf("%s: a cut line that ended in a newline counted %d time(s), want 1", tc.name, got[broken])
		}
	}
}

// The parsers used to cut a message to 64 KiB with a raw byte slice before the
// index ever redacted it. That split multibyte runes and, worse, capped below
// the index's own redaction boundary, so a secret past 64 KiB was either dropped
// or, when it straddled the cut, half-stored before redaction could see it.
func TestCapParsedMessageKeepsRunesAndReach(t *testing.T) {
	// A rune that lands astride the cap must not be split into invalid UTF-8.
	pad := strings.Repeat("a", maxParsedMessage-1)
	got := capParsedMessage(pad + "€aaaa") // '€' starts at maxParsedMessage-1
	if !utf8.ValidString(got) {
		t.Fatalf("cap produced invalid UTF-8 at the boundary")
	}

	// Text past 64 KiB but within the cap survives, so ingest can redact it
	// whole instead of the parser truncating it away first.
	secret := "ghp_" + strings.Repeat("Z", 36)
	msg := strings.Repeat("x", 70*1024) + secret
	kept := capParsedMessage(msg)
	if !strings.Contains(kept, secret) {
		t.Fatalf("cap dropped content past 64 KiB that redaction still needs to see")
	}

	// Below the cap the text is returned untouched.
	if capParsedMessage("short") != "short" {
		t.Fatalf("cap altered a message under the limit")
	}
}

// A PRoot directory entry can report a symlink type for a regular file,
// while its Info result still has the underlying regular mode.
type misreportedWalkEntry struct{ os.DirEntry }

func (misreportedWalkEntry) Type() fs.FileMode { return fs.ModeSymlink }

func TestWalkEntryAcceptsAMisreportedRegularFile(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "session.v4.jsonl.zstd")
	if err := os.WriteFile(target, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !isRegularWalkEntry(misreportedWalkEntry{entries[0]}) {
		t.Fatal("a reported symlink with regular file information should be discovered")
	}

	linkDir := t.TempDir()
	err = os.Symlink(target, filepath.Join(linkDir, "link.jsonl"))
	if err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}
	linkEntries, err := os.ReadDir(linkDir)
	if err != nil {
		t.Fatal(err)
	}
	if isRegularWalkEntry(linkEntries[0]) {
		t.Fatal("a real symlink to a regular file must stay excluded")
	}
}
