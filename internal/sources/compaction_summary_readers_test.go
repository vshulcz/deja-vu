package sources

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/model"
)

// Three readers dropped the summary their harness writes when it compacts, or
// read it as the assistant's speech (#4795). Each shape here is the one the
// harness wrote on a stand: Zed 1.22's thread enum, the Cline CLI 3.0.69
// compaction file, Continue cn 1.5.47's rewritten session.
func roles(ms []model.Message) map[string]string {
	out := map[string]string{}
	for _, m := range ms {
		out[m.Text] = m.Role
	}
	return out
}

func TestZedCompactionSummaryIsKeptUnderItsRole(t *testing.T) {
	zedHome(t)
	body := `{"version":"0.3.0","title":"t","updated_at":"2026-10-07T09:00:02Z","messages":[` +
		`{"User":{"id":"u1","content":[{"Text":"why does the exporter drop retries"}]}},` +
		`{"Agent":{"content":[{"Text":"the backoff counts from zero"}],"tool_results":{}}},` +
		`{"Compaction":{"Summary":"ZED-SUMMARY the exporter retry budget was fixed"}},` +
		`{"Compaction":{"ProviderNative":{"provider":"anthropic","items":[{"opaque":true}]}}},` +
		`{"User":{"id":"u2","content":[{"Text":"continue"}]}}]}`
	sql := zedSchema + `
insert into threads (id,summary,updated_at,data_type,data,folder_paths,created_at) values
 ('c1','t','2026-10-07T09:00:02+00:00','json','` + body + `','/w/p','2026-10-07T09:00:00+00:00');`
	ss, err := ParseZedDB(zedTestDB(t, sql))
	if err != nil || len(ss) != 1 {
		t.Fatalf("parse: %v %d", err, len(ss))
	}
	got := roles(ss[0].Messages)
	if got["ZED-SUMMARY the exporter retry budget was fixed"] != RoleSummary {
		t.Errorf("summary = %q, want the summary role: %v", got["ZED-SUMMARY the exporter retry budget was fixed"], got)
	}
	if got["why does the exporter drop retries"] != "user" || got["continue"] != "user" {
		t.Errorf("the turns around it moved: %v", got)
	}
	if len(ss[0].Messages) != 4 {
		t.Errorf("messages = %d, want 4 (the provider-native compaction has no text): %v", len(ss[0].Messages), got)
	}
}

func TestClineCompactionSummaryIsReadFromItsFile(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "sessions", "1791367873575_myzey")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	files := map[string]string{
		"1791367873575_myzey.json": `{"session_id":"1791367873575_myzey","cwd":"/w/proj","started_at":"2026-10-07T10:11:00Z"}`,
		"1791367873575_myzey.messages.json": `{"agent":"lead","messages":[` +
			`{"role":"user","content":[{"type":"text","text":"probe the stub"}],"ts":1791367860000},` +
			`{"role":"assistant","content":[{"type":"text","text":"probed"}],"ts":1791367861000},` +
			`{"role":"user","content":[{"type":"text","text":"and again"}],"ts":1791367880000}]}`,
		"1791367873575_myzey.compaction.json": `{"version":1,"updated_at":"2026-10-07T10:11:16.387Z","conversation_id":"1791367873575_myzey","source_message_count":14,"messages":[` +
			`{"role":"user","content":[{"type":"text","text":"Context summary:\n\nSTUB-SUMMARY-5: the conversation so far was about probes."}],` +
			`"metadata":{"kind":"compaction_summary","displayRole":"system","summary":"STUB-SUMMARY-5: the conversation so far was about probes.","generatedAt":1791367876386}},` +
			`{"role":"user","content":[{"type":"text","text":"and again"}]}]}`,
	}
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	ss, err := ParseClineFile(filepath.Join(dir, "1791367873575_myzey.messages.json"))
	if err != nil || len(ss) != 1 {
		t.Fatalf("parse: %v %d", err, len(ss))
	}
	order := make([]string, 0, len(ss[0].Messages))
	for _, m := range ss[0].Messages {
		order = append(order, m.Role+":"+m.Text)
	}
	want := "user:probe the stub|assistant:probed|" + RoleSummary + ":STUB-SUMMARY-5: the conversation so far was about probes.|user:and again"
	if strings.Join(order, "|") != want {
		t.Errorf("messages = %q\nwant       %q", strings.Join(order, "|"), want)
	}
	// The file changes without the transcript, so it has to move the
	// fingerprint that re-reads the session.
	before, _ := clineSDKSidecar(filepath.Join(dir, "1791367873575_myzey.messages.json"))
	if err := os.WriteFile(filepath.Join(dir, "1791367873575_myzey.compaction.json"), []byte(files["1791367873575_myzey.compaction.json"]+" "), 0o644); err != nil {
		t.Fatal(err)
	}
	if after, _ := clineSDKSidecar(filepath.Join(dir, "1791367873575_myzey.messages.json")); after == before {
		t.Error("a new compaction file does not re-read the session")
	}
}

func TestContinueConversationSummaryIsNotTheAssistant(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "sessions")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "632143e2.json")
	body := `{"sessionId":"632143e2","title":"Untitled Session","workspaceDirectory":"/w/proj","history":[` +
		`{"message":{"role":"assistant","content":"CN-SUMMARY the file was read"},"contextItems":[],"conversationSummary":"CN-SUMMARY the file was read"},` +
		`{"message":{"role":"user","content":"next question"},"contextItems":[]}]}`
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	ss, err := ParseContinueFile(path)
	if err != nil || len(ss) != 1 {
		t.Fatalf("parse: %v %d", err, len(ss))
	}
	got := roles(ss[0].Messages)
	if got["CN-SUMMARY the file was read"] != RoleSummary || got["next question"] != "user" || len(ss[0].Messages) != 2 {
		t.Errorf("messages = %v, want the summary under its own role once", got)
	}
}
