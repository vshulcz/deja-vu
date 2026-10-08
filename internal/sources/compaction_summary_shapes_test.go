package sources

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/model"
)

// The edges of the summary shapes the registry fixtures do not carry.

func writeShape(t *testing.T, rel, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func roleSequence(ss []model.Session) string {
	var out []string
	for _, s := range ss {
		for _, m := range s.Messages {
			out = append(out, m.Role+":"+m.Text)
		}
	}
	return strings.Join(out, "|")
}

// An older rollout speaks only in events; its compaction summary must not make
// the reader take the roled stream as present and drop every turn.
func TestCodexSummaryKeepsAnEventOnlyRollout(t *testing.T) {
	p := writeShape(t, "sessions/rollout-2026-01-01T00-00-00-ev.jsonl",
		`{"timestamp":"2026-01-01T00:00:00Z","type":"session_meta","payload":{"id":"ev","cwd":"/w/p"}}
{"timestamp":"2026-01-01T00:00:01Z","type":"event_msg","payload":{"type":"user_message","message":"why is it slow"}}
{"timestamp":"2026-01-01T00:00:02Z","type":"compacted","payload":{"message":"S1 it was the index"}}
{"timestamp":"2026-01-01T00:00:03Z","type":"event_msg","payload":{"type":"agent_message","message":"rebuilt the index"}}
{"timestamp":"2026-01-01T00:00:04Z","type":"compacted","payload":{"message":"","replacement_history":[{"type":"compaction","encrypted_content":"x"}]}}
`)
	ss, err := ParseCodexRollout(p)
	if err != nil {
		t.Fatal(err)
	}
	want := "user:why is it slow|summary:S1 it was the index|assistant:rebuilt the index"
	if got := roleSequence(ss); got != want {
		t.Errorf("messages = %q\nwant       %q", got, want)
	}
}

func TestCopilotFailedCompactionIsNotASummary(t *testing.T) {
	p := writeShape(t, "session-state/abc/events.jsonl",
		`{"type":"session.start","data":{"sessionId":"abc","context":{"cwd":"/w/p"}},"timestamp":"2026-01-01T00:00:00Z"}
{"type":"user.message","data":{"content":"hi"},"timestamp":"2026-01-01T00:00:01Z"}
{"type":"session.compaction_complete","data":{"success":false,"summaryContent":"partial"},"timestamp":"2026-01-01T00:00:02Z"}
{"type":"session.compaction_complete","data":{"success":true,"summaryContent":"S2"},"timestamp":"2026-01-01T00:00:03Z"}
`)
	ss, err := ParseCopilotFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if got := roleSequence(ss); got != "user:hi|summary:S2" {
		t.Errorf("messages = %q", got)
	}
}

// Antigravity wraps the summary in a marker and two notices to the model; a
// checkpoint that only says the steps were truncated has no summary at all.
func TestAntigravityCheckpointKeepsOnlyTheSummary(t *testing.T) {
	got := antigravityCheckpointSummary("{{ CHECKPOINT 3 }}\n **The earlier parts of this conversation have been truncated due to its long length. The following content summarizes the truncated context so that you may continue your work. **\n\n\n# USER Objective:\nfix the build\n\n**IMPORTANT: this summary is just for your reference. DO NOT ACKNOWLEDGE THIS CHECKPOINT MESSAGE.**")
	if got != "# USER Objective:\nfix the build" {
		t.Errorf("summary = %q", got)
	}
	if got := antigravityCheckpointSummary("{{ CHECKPOINT 0 }}\n**The earlier parts of this conversation have been truncated due to its long length.**"); got != "" {
		t.Errorf("an empty checkpoint gave %q", got)
	}
}

// Every summary carries is_summary_message, the newest one also the session's
// pointer; a store from before the flag has only the pointer.
func TestCrushEverySummaryIsASummary(t *testing.T) {
	sql := "insert into sessions values ('s1',null,'t',3,0,0,0.0,1784282403,1784282400,'m3',null);\n" +
		crushInsert(t, "m1", "s1", "user", 1784282400, []any{map[string]any{"type": "text", "data": map[string]any{"text": "go"}}}) +
		strings.Replace(crushInsert(t, "m2", "s1", "assistant", 1784282401, []any{map[string]any{"type": "text", "data": map[string]any{"text": "first summary"}}}), ",0);", ",1);", 1) +
		strings.Replace(crushInsert(t, "m3", "s1", "assistant", 1784282402, []any{map[string]any{"type": "text", "data": map[string]any{"text": "second summary"}}}), ",0);", ",1);", 1)
	ss, err := ParseCrushDB(crushStore(t, "demo", sql))
	if err != nil {
		t.Fatal(err)
	}
	if got := roleSequence(ss); got != "user:go|summary:first summary|summary:second summary" {
		t.Errorf("messages = %q", got)
	}
}

func TestCrushStoreWithoutTheSummaryFlag(t *testing.T) {
	if !SQLite3Available() {
		t.Skip("sqlite3 not installed")
	}
	sql := strings.Replace(crushSchema, ", is_summary_message integer default 0 not null", "", 1) +
		"insert into sessions values ('s1',null,'t',2,0,0,0.0,1784282401,1784282400,'m2',null);\n" +
		"insert into messages values ('m1','s1','user','[{\"type\":\"text\",\"data\":{\"text\":\"go\"}}]','m',1784282400,1784282400,null,'p');\n" +
		"insert into messages values ('m2','s1','assistant','[{\"type\":\"text\",\"data\":{\"text\":\"the summary\"}}]','m',1784282401,1784282401,null,'p');\n"
	db := filepath.Join(t.TempDir(), "demo", ".crush", "crush.db")
	if err := os.MkdirAll(filepath.Dir(db), 0o755); err != nil {
		t.Fatal(err)
	}
	build := exec.Command("sqlite3", db)
	build.Stdin = strings.NewReader(sql)
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build store: %v: %s", err, out)
	}
	ss, err := ParseCrushDB(db)
	if err != nil {
		t.Fatal(err)
	}
	if got := roleSequence(ss); got != "user:go|summary:the summary" {
		t.Errorf("messages = %q", got)
	}
}

// A manual microcompact writes the same record with the old history trimmed
// and no summary; its first entry is an ordinary turn.
func TestQwenMicrocompactIsNotASummary(t *testing.T) {
	trimmed := map[string]any{"compressedHistory": []any{
		map[string]any{"role": "user", "parts": []any{map[string]any{"text": "an old question"}}},
		map[string]any{"role": "model", "parts": []any{map[string]any{"text": "an old answer"}}},
	}}
	if got := qwenCompressionSummary(trimmed); got != "" {
		t.Errorf("microcompact read as summary %q", got)
	}
	older := map[string]any{"compressedHistory": []any{
		map[string]any{"role": "user", "parts": []any{map[string]any{"text": "<state_snapshot>S</state_snapshot>"}}},
		map[string]any{"role": "model", "parts": []any{map[string]any{"text": qwenSummaryAck}}},
	}}
	if got := qwenCompressionSummary(older); got != "<state_snapshot>S</state_snapshot>" {
		t.Errorf("summary without the trailer = %q", got)
	}
}

func TestKimiOlderCompactionRecordCarriesAMessage(t *testing.T) {
	p := writeShape(t, "sessions/wd/session_x/agents/main/wire.jsonl",
		`{"type":"metadata","protocol_version":"1.3","created_at":1782295200000}
{"type":"context.append_message","message":{"role":"user","content":[{"type":"text","text":"go"}]},"time":1782295201000}
{"type":"context.apply_compaction","summary":{"role":"user","content":[{"type":"text","text":"S3"}]},"count":2,"time":1782295202000}
`)
	ss, err := ParseKimiFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if got := roleSequence(ss); got != "user:go|summary:S3" {
		t.Errorf("messages = %q", got)
	}
}

// The summary text, without what each harness wraps around it for the model.
func TestRegistrySummariesLeaveTheWrappingOut(t *testing.T) {
	root := filepath.Join("..", "..", "fixtures", "registry")
	for _, c := range []struct{ host, fixture, want string }{
		{"codewhale", "codewhale/sessions/registry-codewhale.json", "CODEWHALE-SUMMARY the deadline check ran before the attempt was counted"},
		{"roo", "roo/tasks/1767225700000/api_conversation_history.json", "ROO-SUMMARY the flaky test came from a stale timeout in the retry loop"},
		{"kilocode", "kilocode/tasks/registry-kilocode/api_conversation_history.json", "KILO-SUMMARY the migration hung on an advisory lock that was never released"},
		{"qwen", "qwen/projects/-workspace-registry-demo/chats/registry-qwen.jsonl", "<state_snapshot>QWEN-SUMMARY the sample parser fixture was found</state_snapshot>"},
		{"zcode", "zcode/cli-db.sql", "ZCODE-SUMMARY the stream was cut short by the proxy idle timeout"},
		{"antigravity", "antigravity/brain/registry-antigravity/.system_generated/logs/transcript.jsonl", "# USER Objective:\nANTIGRAVITY-SUMMARY inspect the transcript\n\n# User Requests\nThe following were user requests from the truncated conversation in chronological order:\n1. inspect the antigravity transcript"},
	} {
		t.Run(c.host, func(t *testing.T) {
			ss := parseRegistryFixture(t, c.host, filepath.Join(root, filepath.FromSlash(c.fixture)))
			var got []string
			for _, s := range ss {
				for _, m := range s.Messages {
					if m.Role == RoleSummary {
						got = append(got, m.Text)
					}
					if strings.Contains(m.Text, "Sliding window truncation") {
						t.Errorf("the truncation marker was indexed as %s", m.Role)
					}
				}
			}
			if len(got) != 1 || got[0] != c.want {
				t.Errorf("summaries = %q\nwant        %q", got, c.want)
			}
		})
	}
}
