package sources

import (
	"encoding/json"
	"errors"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func storeSessionText(tr CompactionTranscript) string {
	var b strings.Builder
	for _, m := range tr.Session.Messages {
		b.WriteString(m.Role + ": " + m.Text + "\n")
	}
	return b.String()
}

// opencode and Kilo CLI keep every message in SQLite and filter the compacted
// ones out only of what they send the model, so the session read by id still
// has the turns being summarised.
func TestCompactionStoreReadsOneSessionByID(t *testing.T) {
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skip("sqlite3 not installed")
	}
	for _, harness := range []string{"opencode", "kilocode"} {
		db := filepath.Join(t.TempDir(), harness+".db")
		script := `create table session(id text, directory text, time_created any, time_updated any);
create table message(id text, session_id text, time_created any, data text);
create table part(id text, message_id text, data text);
insert into session values('ses_a','/w/proj','2026-01-02T03:00:00Z','2026-01-03T03:00:00Z');
insert into session values('ses_b','/w/other','2026-01-02T03:00:00Z','2026-01-03T03:00:00Z');
insert into message values('m1','ses_a',1767409200000,'{"role":"user"}');
insert into part values('p1','m1','{"type":"text","text":"fix the parser test"}');
insert into message values('m2','ses_a',1767409201000,'{"role":"assistant"}');
insert into part values('p2','m2','{"type":"tool","tool":"bash","state":{"input":{"command":"go test ./parser/..."},"output":"--- FAIL: TestParseSeed\nwant 3, got 4","metadata":{"exit":1}},"time":{"start":"2026-01-02T03:00:01Z"}}');
insert into message values('m3','ses_b',1767409202000,'{"role":"user"}');
insert into part values('p3','m3','{"type":"text","text":"someone else"}');`
		if out, err := exec.Command("sqlite3", db, script).CombinedOutput(); err != nil {
			t.Fatalf("sqlite setup: %v %s", err, out)
		}
		t.Setenv("DEJA_OPENCODE_DB", db)
		t.Setenv("DEJA_KILO_DB", db)
		tr, err := ReadCompactionStore(harness, "ses_a")
		if err != nil {
			t.Fatalf("%s: %v", harness, err)
		}
		text := storeSessionText(tr)
		if tr.Harness != harness || tr.Workspace != "/w/proj" || !strings.Contains(text, "go test ./parser/...  → exit 1") || strings.Contains(text, "someone else") {
			t.Fatalf("%s: harness %q workspace %q\n%s", harness, tr.Harness, tr.Workspace, text)
		}
		if _, err := ReadCompactionStore(harness, "ses_missing"); !errors.Is(err, ErrTranscriptIdentity) {
			t.Fatalf("%s: a session the store lacks: %v", harness, err)
		}
	}
	if _, err := ReadCompactionStore("claude", "ses_a"); !errors.Is(err, ErrUnsupportedCompactionTranscript) {
		t.Fatalf("a harness without a store: %v", err)
	}
}

// Hermes hands its memory provider the turns it compresses, in the shape it
// keeps in state.db; they read as the store's rows do.
func TestCompactionMessagesReadHermesTurns(t *testing.T) {
	result, _ := json.Marshal(map[string]any{"output": "--- FAIL: TestParseSeed\nwant 3, got 4\nFAIL", "exit_code": 1})
	msgs, _ := json.Marshal([]map[string]any{
		{"role": "user", "content": "fix the parser test"},
		{"role": "assistant", "content": "", "tool_calls": []any{map[string]any{"id": "call_1", "type": "function", "function": map[string]any{"name": "terminal", "arguments": `{"command":"go test ./parser/..."}`}}}},
		{"role": "tool", "tool_call_id": "call_1", "content": string(result)},
		{"role": "assistant", "content": []any{map[string]any{"type": "text", "text": "It fails: want 3, got 4."}}},
	})
	tr, err := ReadCompactionMessages("hermes", "h-1", "/w/proj", msgs)
	if err != nil {
		t.Fatal(err)
	}
	text := storeSessionText(tr)
	for _, want := range []string{"user: fix the parser test", "go test ./parser/...  → exit 1", "want 3, got 4", "assistant: It fails"} {
		if !strings.Contains(text, want) {
			t.Errorf("hermes turns lack %q:\n%s", want, text)
		}
	}
	if tr.Session.ID != "h-1" || tr.Workspace != "/w/proj" {
		t.Fatalf("identity: %q %q", tr.Session.ID, tr.Workspace)
	}
	if _, err := ReadCompactionMessages("hermes", "h-1", "/w/proj", json.RawMessage(`[]`)); err == nil {
		t.Fatal("no turns made a capture")
	}
	if _, err := ReadCompactionMessages("opencode", "h-1", "/w/proj", msgs); err == nil {
		t.Fatal("turns from a host that does not send them were read")
	}
}
