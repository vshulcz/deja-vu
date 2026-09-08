package sources

import (
	"os"
	"path/filepath"
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
