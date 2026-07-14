package sources

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
)

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
	if projectName("") != "-" || projectName("/a/b") != "b" {
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
	cmd := exec.Command("sqlite3", db, script)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("sqlite setup: %v %s", err, out)
	}
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
	dir := filepath.Join(tmp, "-Users-shulcz-deja-vu")
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
	if len(ss) != 1 || ss[0].Project != filepath.Join("deja", "vu") {
		t.Fatalf("project came from wrong source: %#v", ss)
	}
}

func TestParseClaudeProjectFromNestedSubagentPath(t *testing.T) {
	tmp := t.TempDir()
	root := filepath.Join(tmp, "claude")
	project := filepath.Join(root, "-Users-shulcz-deja-vu")
	dir := filepath.Join(project, "a7fa", "subagents")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, "7ca3b9dd0928.jsonl")
	line := `{"type":"user","sessionId":"sub1","timestamp":"2026-01-02T03:04:05Z","message":{"role":"user","content":"hello"}}` + "\n"
	if err := os.WriteFile(p, []byte(line), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_CLAUDE_ROOT", root)
	ss, err := ParseClaudeFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if len(ss) != 1 || ss[0].Project != filepath.Join("deja", "vu") {
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
}

func TestSQLQuoteDoublesSingleQuotes(t *testing.T) {
	in := `x' or 1=1 --`
	if got := sqlQuote(in); got != `x'' or 1=1 --` {
		t.Fatalf("sqlQuote(%q) = %q", in, got)
	}
}
