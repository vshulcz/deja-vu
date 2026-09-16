package sources

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Cherry Studio writes Claude Code transcripts from a desktop app, and appends
// the same API call three or four times as the stream progresses: a new uuid
// each time, the same requestId and message id, the text growing. Read plainly
// that is one reply stored three times in prefixes — measured on this fixture
// before the collapse: "the advisory", "the advisory lock was", "the advisory
// lock was never released" (#3644).
const cherrySnapshotTranscript = `{"type":"user","sessionId":"cs-1","timestamp":"2026-07-17T09:00:00.000Z","cwd":"/work/api","message":{"role":"user","content":"why does the migration hang"}}
{"type":"assistant","uuid":"u1","requestId":"req-1","sessionId":"cs-1","timestamp":"2026-07-17T09:00:01.000Z","message":{"id":"msg-1","role":"assistant","content":[{"type":"text","text":"the advisory"}]}}
{"type":"assistant","uuid":"u2","requestId":"req-1","sessionId":"cs-1","timestamp":"2026-07-17T09:00:01.500Z","message":{"id":"msg-1","role":"assistant","content":[{"type":"text","text":"the advisory lock was"}]}}
{"type":"assistant","uuid":"u3","requestId":"req-1","sessionId":"cs-1","timestamp":"2026-07-17T09:00:02.000Z","message":{"id":"msg-1","role":"assistant","content":[{"type":"text","text":"the advisory lock was never released"}]}}
{"type":"assistant","uuid":"u4","requestId":"req-2","sessionId":"cs-1","timestamp":"2026-07-17T09:00:05.000Z","message":{"id":"msg-2","role":"assistant","content":[{"type":"text","text":"rerun the migration with a lock timeout"}]}}
`

func writeCherryStore(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	root := filepath.Join(home, "CherryStudio", "Data", "Agents", ".claude", "projects", "-work-api")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "cs-1.jsonl"), []byte(cherrySnapshotTranscript), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_CHERRYSTUDIO_ROOTS", filepath.Dir(root))
	return root
}

func TestCherryStudioCollapsesAStreamingRun(t *testing.T) {
	writeCherryStore(t)

	files := CherryStudioSessionFiles()
	if len(files) != 1 {
		t.Fatalf("files = %v", files)
	}
	ss := LoadCherryStudio()
	if len(ss) != 1 {
		t.Fatalf("sessions = %d, want 1", len(ss))
	}
	s := ss[0]
	if s.Harness != "cherrystudio" {
		t.Errorf("harness = %q — a Cherry Studio session must not report as Claude Code", s.Harness)
	}
	var assistant []string
	for _, m := range s.Messages {
		if m.Role == "assistant" {
			assistant = append(assistant, m.Text)
		}
	}
	if len(assistant) != 2 {
		t.Fatalf("assistant messages = %d %q, want one per API call", len(assistant), assistant)
	}
	if assistant[0] != "the advisory lock was never released" {
		t.Errorf("the collapsed reply is %q, want the complete snapshot", assistant[0])
	}
	if assistant[1] != "rerun the migration with a lock timeout" {
		t.Errorf("the second call was folded into the first: %q", assistant[1])
	}
	// The user turn survives the collapse.
	if len(s.Messages) < 3 || s.Messages[0].Role != "user" {
		t.Errorf("the question is missing: %+v", s.Messages)
	}
}

// The same file read through the stock Claude parser keeps every snapshot,
// which is what makes the collapse a property of this store rather than a
// change to Claude Code's own reader.
func TestClaudeItselfIsUnchangedByTheCollapse(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "cs-1.jsonl")
	if err := os.WriteFile(p, []byte(cherrySnapshotTranscript), 0o644); err != nil {
		t.Fatal(err)
	}
	ss, err := ParseClaudeFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if len(ss) != 1 {
		t.Fatalf("sessions = %d", len(ss))
	}
	n := 0
	for _, m := range ss[0].Messages {
		if m.Role == "assistant" {
			n++
		}
	}
	if n != 4 {
		t.Errorf("the stock reader kept %d assistant records, want all four", n)
	}
	if ss[0].Harness != "claude" {
		t.Errorf("harness = %q, want claude", ss[0].Harness)
	}
}

// A stock Claude transcript must not be claimed by this harness, and a Cherry
// Studio one must not be claimed by Claude's kind.
func TestCherryStudioAndClaudeDoNotClaimEachOther(t *testing.T) {
	root := writeCherryStore(t)
	cherry := filepath.Join(root, "cs-1.jsonl")

	stockHome := t.TempDir()
	stock := filepath.Join(stockHome, ".claude", "projects", "-work-api")
	if err := os.MkdirAll(stock, 0o755); err != nil {
		t.Fatal(err)
	}
	stockFile := filepath.Join(stock, "s-1.jsonl")
	if err := os.WriteFile(stockFile, []byte(cherrySnapshotTranscript), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_CLAUDE_ROOT", stock)

	kinds := map[string]string{}
	for _, h := range Registry() {
		for _, k := range h.Kinds {
			for _, p := range []string{cherry, stockFile} {
				if k.Match(p) {
					kinds[p] += h.Name + ":" + k.Name + " "
				}
			}
		}
	}
	if !strings.Contains(kinds[cherry], "cherrystudio") {
		t.Errorf("the Cherry Studio transcript is claimed by %q", kinds[cherry])
	}
	if strings.Contains(kinds[stockFile], "cherrystudio") {
		t.Errorf("a stock Claude transcript is claimed by cherrystudio: %q", kinds[stockFile])
	}
}
