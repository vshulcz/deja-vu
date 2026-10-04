package sources

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/model"
)

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
