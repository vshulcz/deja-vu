package index

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
)

// A session of nothing but tool output — a hook run, an agent driven entirely
// by another program — has no user turn and no assistant turn. It listed as an
// empty bracket in `deja last` while `stats` printed a dash for the same
// session: two screens, two answers, neither of them saying what the session
// was.
func TestToolOnlySessionIsNamedAfterItsOutput(t *testing.T) {
	at := time.Date(2026, 8, 1, 10, 0, 0, 0, time.UTC)
	s := model.Session{ID: "toolonly", Harness: "claude", Project: "proj", Updated: at, Messages: []model.Message{
		{Role: roleToolOutput, Text: "npm ERR! ENOENT missing package.json", Time: at},
		{Role: roleToolOutput, Text: "exit status 1", Time: at.Add(time.Minute)},
	}}
	meta := metaForSession(s)
	if meta.Title != "tool output: npm ERR! ENOENT missing package.json" {
		t.Errorf("title = %q, want it named after the first output", meta.Title)
	}
	// The mark belongs to the assistant's own words; tool output is neither
	// the reader's question nor the agent's, and "agent: npm ERR!" would be a
	// third wrong answer.
	if meta.AgentTitle {
		t.Error("tool output was marked as the agent's own words")
	}
}

// The title is derived again on the receiving machine, from the record stream
// alone. A tool-only session that lists by name here must not list as a bare
// id there.
func TestImportNamesAToolOnlySessionTheSameWay(t *testing.T) {
	dir := t.TempDir()
	exp := filepath.Join(dir, "export")
	if err := os.MkdirAll(exp, 0o755); err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 8, 1, 10, 0, 0, 0, time.UTC)
	var batch strings.Builder
	for i, text := range []string{"npm ERR! ENOENT missing package.json", "exit status 1"} {
		b, err := json.Marshal(SyncRecord{Harness: "claude", SessionID: "toolonly", Project: "proj",
			Role: roleToolOutput, Text: text, Time: at.Add(time.Duration(i) * time.Minute)})
		if err != nil {
			t.Fatal(err)
		}
		batch.WriteString(string(b) + "\n")
	}
	if err := os.WriteFile(filepath.Join(exp, "deja-sync-x.jsonl"), []byte(batch.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Import(filepath.Join(dir, "index"), exp); err != nil {
		t.Fatal(err)
	}
	ss, err := Recent(filepath.Join(dir, "index"), 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(ss) != 1 {
		t.Fatalf("imported %d sessions, want 1", len(ss))
	}
	if ss[0].Title != "tool output: npm ERR! ENOENT missing package.json" {
		t.Errorf("imported title = %q, want the same name the local path gives", ss[0].Title)
	}
	if ss[0].AgentTitle {
		t.Error("imported tool output was marked as the agent's own words")
	}
}

// Speech still wins wherever it exists, on both paths: #636 was titles that
// were stack traces, and that must not come back through the fallback.
func TestSpeechStillOutranksToolOutputOnImport(t *testing.T) {
	dir := t.TempDir()
	exp := filepath.Join(dir, "export")
	if err := os.MkdirAll(exp, 0o755); err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 8, 1, 10, 0, 0, 0, time.UTC)
	var batch strings.Builder
	for i, r := range []struct{ role, text string }{
		{roleToolOutput, "panic: runtime error"},
		{"assistant", "looking at the pool"},
		{"user", "the pool starves under load"},
	} {
		b, err := json.Marshal(SyncRecord{Harness: "claude", SessionID: "mixed", Project: "proj",
			Role: r.role, Text: r.text, Time: at.Add(time.Duration(i) * time.Minute)})
		if err != nil {
			t.Fatal(err)
		}
		batch.WriteString(string(b) + "\n")
	}
	if err := os.WriteFile(filepath.Join(exp, "deja-sync-x.jsonl"), []byte(batch.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Import(filepath.Join(dir, "index"), exp); err != nil {
		t.Fatal(err)
	}
	ss, err := Recent(filepath.Join(dir, "index"), 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(ss) != 1 {
		t.Fatalf("imported %d sessions, want 1", len(ss))
	}
	if ss[0].Title != "the pool starves under load" {
		t.Errorf("imported title = %q, want the user turn", ss[0].Title)
	}
}
