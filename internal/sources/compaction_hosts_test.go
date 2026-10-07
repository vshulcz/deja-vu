package sources

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A ZCode compaction adds a compact_summary message and keeps the turns before
// it in the CLI database (3.14.4 stand). The capture is the session as it
// stood before the newest summary, and the same summary keeps its fingerprint.
func TestZCodeCompactionReadsTheTurnsBeforeTheSummary(t *testing.T) {
	if !SQLite3Available() {
		t.Skip("sqlite3 CLI not installed")
	}
	home := t.TempDir()
	db := filepath.Join(home, "db.sqlite")
	t.Setenv("DEJA_ZCODE_DB", db)
	t.Setenv("DEJA_ZCODE_ROOT", filepath.Join(home, "absent"))
	schema := `create table session (id text primary key, directory text, time_created integer, time_updated integer);
create table message (id text primary key, session_id text, time_created integer, data text);
create table part (id text primary key, message_id text, data text);
insert into session values ('sess_1', '/work/app', 1790000000000, 1790000090000);
insert into message values ('m1', 'sess_1', 1790000001000, '{"role":"user","semantics":{"kind":"user_prompt"}}');
insert into message values ('m2', 'sess_1', 1790000002000, '{"role":"assistant","semantics":{"kind":"assistant_response"}}');
insert into message values ('m3', 'sess_1', 1790000050000, '{"role":"user","summary":{"title":"Compact summary"},"semantics":{"kind":"compact_summary"}}');
insert into message values ('m4', 'sess_1', 1790000060000, '{"role":"user","semantics":{"kind":"user_prompt"}}');
insert into part values ('p1', 'm1', '{"type":"text","text":"fix the parser test"}');
insert into part values ('p2', 'm2', '{"type":"text","text":"the parser test fails on escapes"}');
insert into part values ('p3', 'm3', '{"type":"text","text":"summary of the work so far","synthetic":true}');
insert into part values ('p4', 'm4', '{"type":"text","text":"carry on after the summary"}');
`
	if out, err := exec.Command("sqlite3", db, schema).CombinedOutput(); err != nil {
		t.Fatalf("sqlite3 seed: %v %s", err, out)
	}
	got, found, err := ReadZCodeCompaction("sess_1")
	if err != nil || !found {
		t.Fatalf("found=%v err=%v", found, err)
	}
	var texts []string
	for _, m := range got.Session.Messages {
		texts = append(texts, m.Text)
	}
	all := strings.Join(texts, "\n")
	if !strings.Contains(all, "fix the parser test") || strings.Contains(all, "carry on after the summary") {
		t.Errorf("captured %q, want the turns before the summary only", all)
	}
	if got.Workspace != "/work/app" {
		t.Errorf("workspace %q", got.Workspace)
	}
	again, _, _ := ReadZCodeCompaction("sess_1")
	if again.Fingerprint != got.Fingerprint {
		t.Error("the same summary read twice changed its fingerprint")
	}
	if _, found, _ := ReadZCodeCompaction("sess_other"); found {
		t.Error("a session with no summary was reported compacted")
	}
}

func TestZCodeHookTranscriptIsTheTempFile(t *testing.T) {
	if !ZCodeHookTranscript(filepath.Join(os.TempDir(), "zcode-claude-hook-8rQtnE", "transcript.jsonl")) {
		t.Error("ZCode's hook transcript not recognised")
	}
	if ZCodeHookTranscript(filepath.Join("work", "projects", "x.jsonl")) || ZCodeHookTranscript("") {
		t.Error("another transcript taken for ZCode's")
	}
}

// CodeWhale saves the history before a compaction and the summary beside it
// once written; the hooks' session id is not the saved one, so the session is
// the one in the workspace with a summary written since the hook session
// began, and a history with no summary is a failed attempt.
func TestCodeWhaleCompactionReadsTheSavedHistory(t *testing.T) {
	root := filepath.Join(t.TempDir(), "sessions")
	t.Setenv("DEJA_CODEWHALE_ROOT", root)
	t.Setenv("CODEWHALE_HOME", filepath.Join(root, "..", "home"))
	work := t.TempDir()
	write := func(p, body string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(root, "s1.json"), `{"metadata":{"id":"s1","workspace":`+quoteJSON(work)+`,"created_at":"2026-10-07T10:00:00Z"},"messages":[]}`)
	write(filepath.Join(root, "s2.json"), `{"metadata":{"id":"s2","workspace":"/elsewhere","created_at":"2026-10-07T10:00:00Z"},"messages":[]}`)
	arts := filepath.Join(root, "s1", "artifacts")
	write(filepath.Join(arts, "context-transfer-a.json"), `[{"role":"user","content":[{"type":"text","text":"fix the tokenizer test"}]},{"role":"assistant","content":[{"type":"text","text":"looking"}]}]`)
	write(filepath.Join(root, "s2", "artifacts", "context-transfer-b.json"), `[{"role":"user","content":[{"type":"text","text":"another workspace"}]}]`)
	write(filepath.Join(root, "s2", "artifacts", "context-transfer-b.md"), "summary")
	since := time.Now().Add(-time.Minute)
	if _, found, _ := ReadCodeWhaleCompaction(work, since); found {
		t.Fatal("a history with no summary was taken for a compaction")
	}
	write(filepath.Join(arts, "context-transfer-a.md"), "summary of the work")
	got, found, err := ReadCodeWhaleCompaction(work, since)
	if err != nil || !found {
		t.Fatalf("found=%v err=%v", found, err)
	}
	if got.Session.ID != "s1" || len(got.Session.Messages) == 0 || got.Session.Messages[0].Text != "fix the tokenizer test" {
		t.Errorf("captured %s %+v", got.Session.ID, got.Session.Messages)
	}
	if _, found, _ := ReadCodeWhaleCompaction(work, time.Now().Add(time.Minute)); found {
		t.Error("a compaction from before the hook session began was taken")
	}
}

func quoteJSON(s string) string {
	return `"` + strings.ReplaceAll(strings.ReplaceAll(s, `\`, `\\`), `"`, `\"`) + `"`
}
