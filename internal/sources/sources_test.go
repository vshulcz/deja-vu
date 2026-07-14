package sources

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
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
