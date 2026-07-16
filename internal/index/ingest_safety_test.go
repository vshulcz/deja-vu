package index

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/vshulcz/deja-vu/internal/search"
)

func writeClaudeLine(t *testing.T, path, sid, text string, partial bool) {
	t.Helper()
	line := `{"type":"user","sessionId":"` + sid + `","timestamp":"2026-01-02T03:04:05Z","message":{"role":"user","content":"` + text + `"}}`
	if !partial {
		line += "\n"
	} else {
		line = line[:len(line)/2] // torn mid-write
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(line); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
}

// A session file caught mid-write must not lose the torn line: the next
// index pass picks it up exactly once.
func TestTornTailLineIndexedOnce(t *testing.T) {
	tmp := t.TempDir()
	claudeRoot := filepath.Join(tmp, "claude")
	proj := filepath.Join(claudeRoot, "-tmp-torn")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(proj, "t1.jsonl")
	writeClaudeLine(t, file, "t1", "first tornneedle message", false)
	// torn second line: writer got interrupted mid-json
	writeClaudeLine(t, file, "t1", "second tornneedle message", true)
	t.Setenv("HOME", t.TempDir())
	t.Setenv("USERPROFILE", os.Getenv("HOME"))
	t.Setenv("DEJA_CLAUDE_ROOT", claudeRoot)
	t.Setenv("DEJA_CODEX_ROOT", filepath.Join(tmp, "codex"))
	t.Setenv("DEJA_OPENCODE_DB", filepath.Join(tmp, "opencode.db"))
	dir := filepath.Join(tmp, "index.db")
	o := search.Options{Query: "tornneedle", All: true}
	if err := EnsureForSearch(dir, o, false, nil); err != nil {
		t.Fatal(err)
	}
	ss, err := Search(dir, o)
	if err != nil {
		t.Fatal(err)
	}
	if len(ss) != 1 || len(ss[0].Messages) != 1 {
		t.Fatalf("initial index: %#v", ss)
	}

	// writer finishes the line: complete the torn json and append a newline
	full := `{"type":"user","sessionId":"t1","timestamp":"2026-01-02T03:06:05Z","message":{"role":"user","content":"second tornneedle message"}}`
	b, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	safe := 0
	for i := len(b) - 1; i >= 0; i-- {
		if b[i] == '\n' {
			safe = i + 1
			break
		}
	}
	if err := os.WriteFile(file, append(b[:safe], []byte(full+"\n")...), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := EnsureForSearch(dir, o, false, nil); err != nil {
		t.Fatal(err)
	}
	ss, err = Search(dir, o)
	if err != nil {
		t.Fatal(err)
	}
	if len(ss) != 1 || len(ss[0].Messages) != 2 {
		t.Fatalf("after completing torn line: want 2 messages exactly once, got %#v", ss)
	}
}

// Subagent transcripts are skipped by default and included with the env flag.
func TestSubagentTranscriptsSkipped(t *testing.T) {
	tmp := t.TempDir()
	claudeRoot := filepath.Join(tmp, "claude")
	proj := filepath.Join(claudeRoot, "-tmp-sub")
	sub := filepath.Join(proj, "subagents")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	main := `{"type":"user","sessionId":"m1","timestamp":"2026-01-02T03:04:05Z","message":{"role":"user","content":"mainneedle question"}}` + "\n"
	agent := `{"type":"user","sessionId":"a1","timestamp":"2026-01-02T03:04:06Z","message":{"role":"user","content":"subneedle question"}}` + "\n"
	if err := os.WriteFile(filepath.Join(proj, "m1.jsonl"), []byte(main), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sub, "a1.jsonl"), []byte(agent), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", t.TempDir())
	t.Setenv("USERPROFILE", os.Getenv("HOME"))
	t.Setenv("DEJA_CLAUDE_ROOT", claudeRoot)
	t.Setenv("DEJA_CODEX_ROOT", filepath.Join(tmp, "codex"))
	t.Setenv("DEJA_OPENCODE_DB", filepath.Join(tmp, "opencode.db"))
	dir := filepath.Join(tmp, "index.db")
	if err := EnsureForSearch(dir, search.Options{Query: "subneedle"}, false, nil); err != nil {
		t.Fatal(err)
	}
	if ss, err := Search(dir, search.Options{Query: "subneedle", All: true}); err != nil || len(ss) != 0 {
		t.Fatalf("subagent indexed by default: %#v err=%v", ss, err)
	}
	if ss, err := Search(dir, search.Options{Query: "mainneedle", All: true}); err != nil || len(ss) != 1 {
		t.Fatalf("main session missing: %#v err=%v", ss, err)
	}

	t.Setenv("DEJA_INCLUDE_SUBAGENTS", "1")
	dir2 := filepath.Join(tmp, "index2.db")
	if err := EnsureForSearch(dir2, search.Options{Query: "subneedle"}, false, nil); err != nil {
		t.Fatal(err)
	}
	if ss, err := Search(dir2, search.Options{Query: "subneedle", All: true}); err != nil || len(ss) != 1 {
		t.Fatalf("opt-in did not index subagent: %#v err=%v", ss, err)
	}
}
