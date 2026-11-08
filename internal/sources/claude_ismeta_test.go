package sources

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/model"
)

// Claude Code marks the user-role records it writes itself — a loaded skill's
// body, a prompt a cron job re-fired, the /fork notice, an image placeholder —
// with isMeta: true. They were indexed as the person's words (#3267).
func TestClaudeIsMetaRecordsAreNotThePersons(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "s1.jsonl")
	body := `{"type":"user","sessionId":"s1","timestamp":"2026-08-10T10:00:00Z","isMeta":true,"message":{"role":"user","content":"# /loop — schedule a recurring or self-paced prompt\n\nParse the input below into [interval] <prompt…> and schedule it."}}` + "\n" +
		`{"type":"user","sessionId":"s1","timestamp":"2026-08-10T10:00:01Z","message":{"role":"user","content":"/loop 1m follow the night protocol"}}` + "\n" +
		`{"type":"assistant","sessionId":"s1","timestamp":"2026-08-10T10:00:02Z","message":{"role":"assistant","content":[{"type":"text","text":"scheduled"}]}}` + "\n" +
		`{"type":"user","sessionId":"s1","timestamp":"2026-08-10T10:01:00Z","isMeta":true,"userType":"external","message":{"role":"user","content":"The fork runs as its own separate session — nothing it does arrives in this conversation."}}` + "\n" +
		`{"type":"user","sessionId":"s1","timestamp":"2026-08-10T10:02:00Z","message":{"role":"user","content":"why does the pool test still fail"}}` + "\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	for name, parse := range map[string]func(string, int64) ([]model.Session, error){
		"typed":   parseClaudeTypedFromOffset,
		"generic": parseClaudeGenericFromOffset,
	} {
		ss, err := parse(path, 0)
		if err != nil || len(ss) != 1 {
			t.Fatalf("%s: %v, %d sessions", name, err, len(ss))
		}
		want := []string{"/loop 1m follow the night protocol", "scheduled", "why does the pool test still fail"}
		if len(ss[0].Messages) != len(want) {
			t.Errorf("%s: messages = %+v", name, ss[0].Messages)
			continue
		}
		for i, m := range ss[0].Messages {
			if m.Text != want[i] {
				t.Errorf("%s: message %d = %q, want %q", name, i, m.Text, want[i])
			}
		}
	}
}

// A subagent report delivered to the parent session with isMeta: true and
// origin.kind == "peer" is indexed as an assistant conclusion with the frame
// line stripped, rather than dropped or treated as user speech (#5985).
func TestClaudeSubagentHandbackReportIsIndexedAsAssistant(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "p1.jsonl")
	body := `{"type":"user","sessionId":"p1","timestamp":"2026-08-10T10:00:00Z","message":{"role":"user","content":"find why regex failed"}}` + "\n" +
		`{"type":"assistant","sessionId":"p1","timestamp":"2026-08-10T10:00:01Z","message":{"role":"assistant","content":[{"type":"text","text":"spawning research agent"}]}}` + "\n" +
		`{"type":"user","sessionId":"p1","timestamp":"2026-08-10T10:00:05Z","isMeta":true,"origin":{"kind":"peer","from":"agent-r1","senderTaskId":"agent-r1","name":"general-purpose","body":"[Subagent hand-back] agent-r1 (general-purpose). The report follows:\n  Root cause: regex failed on unescaped slashes in target pattern."},"message":{"role":"user","content":"[Subagent hand-back] agent-r1 (general-purpose). The report follows:\n  Root cause: regex failed on unescaped slashes in target pattern."}}` + "\n" +
		`{"type":"user","sessionId":"p1","timestamp":"2026-08-10T10:00:07Z","isMeta":true,"origin":{"kind":"peer","from":"agent-r2","body":"[Subagent hand-back] agent-r2\n  Secondary finding from origin body only."}}` + "\n" +
		`{"type":"user","sessionId":"p1","timestamp":"2026-08-10T10:00:08Z","isMeta":true,"origin":{"kind":"peer","from":"agent-r3","body":"ordinary peer ping"}}` + "\n" +
		`{"type":"user","sessionId":"p1","timestamp":"2026-08-10T10:00:10Z","isMeta":true,"message":{"role":"user","content":"# /fork notice - fork runs separate"}}` + "\n" +
		`{"type":"assistant","sessionId":"p1","timestamp":"2026-08-10T10:00:15Z","message":{"role":"assistant","content":[{"type":"text","text":"Applying the fix to escape slashes."}]}}` + "\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	for name, parse := range map[string]func(string, int64) ([]model.Session, error){
		"typed":   parseClaudeTypedFromOffset,
		"generic": parseClaudeGenericFromOffset,
	} {
		ss, err := parse(path, 0)
		if err != nil || len(ss) != 1 {
			t.Fatalf("%s: %v, %d sessions", name, err, len(ss))
		}
		s := ss[0]
		if len(s.Messages) != 5 {
			t.Fatalf("%s: want 5 messages, got %d: %+v", name, len(s.Messages), s.Messages)
		}
		report := s.Messages[2]
		if report.Role != "assistant" {
			t.Errorf("%s: subagent report role = %q, want assistant", name, report.Role)
		}
		wantReport := "Root cause: regex failed on unescaped slashes in target pattern."
		if report.Text != wantReport {
			t.Errorf("%s: subagent report text = %q, want %q", name, report.Text, wantReport)
		}
		if s.Messages[3].Role != "assistant" || s.Messages[3].Text != "Secondary finding from origin body only." {
			t.Errorf("%s: second report = %+v", name, s.Messages[3])
		}
		for _, typed := range s.Typed {
			if strings.Contains(typed, "unescaped slashes") {
				t.Errorf("%s: subagent report leaked into s.Typed: %q", name, typed)
			}
		}
	}
}
