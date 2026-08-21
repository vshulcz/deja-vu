package sources

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The lines are what dsh 0.1.1-rc.2 actually wrote, trimmed to the events that
// carry the conversation.
const deepSeekLog = `{"type":"session","version":0,"id":"session-eaf5c9ac-0e47-4d2f-b982-8bae306062d1","createdAt":1787320263519,"cwd":"/work/pgbouncer-lab","delegationDepth":0}
{"type":"permission/preset","seq":0,"time":1787320263520,"data":{"preset":"workspace-write"}}
{"type":"session/title","seq":10,"time":1787320263581,"data":{"title":"pgbouncer pool size","source":{"kind":"fallback"}}}
{"type":"user/message","seq":7,"time":1787320263580,"data":{"content":[{"type":"text","text":"сколько коннектов держим на шард?"}],"source":{"kind":"user"},"role":"user"}}
{"type":"user/message","seq":8,"time":1787320263580,"data":{"content":[{"type":"text","text":"Current runtime context. This snapshot supersedes earlier ones."}],"source":{"kind":"plugin","plugin":"@deepseek-ai/dsh-system-prompt"},"role":"user"}}
{"type":"user/message","seq":9,"time":1787320263580,"data":{"content":[{"type":"text","text":"<system-reminder>A skill is a reusable set of instructions.</system-reminder>"}],"source":{"kind":"skill-catalog"},"role":"user"}}
{"type":"assistant/chunk","seq":12,"time":1787320263585,"data":{"turn":1,"step":1,"chunk":{"type":"text-delta","text":"держим "}}}
{"type":"assistant/chunk","seq":13,"time":1787320263586,"data":{"turn":1,"step":1,"chunk":{"type":"text-delta","text":"40 на шард"}}}
{"type":"assistant/chunk","seq":14,"time":1787320263589,"data":{"turn":1,"step":1,"chunk":{"type":"finish","reason":{"kind":"stop"}}}}
{"type":"turn/end","seq":15,"time":1787320263590,"data":{"turn":1}}
`

func writeDeepSeekSession(t *testing.T, root, id, body string, compress bool) string {
	t.Helper()
	dir := filepath.Join(root, "--work-pgbouncer-lab--", "session-"+id)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "session.jsonl")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if !compress {
		return path
	}
	out := path + ".zstd"
	cmd := exec.Command("zstd", "-q", "-o", out, path)
	if err := cmd.Run(); err != nil {
		t.Fatalf("zstd: %v", err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestParseDeepSeekFile(t *testing.T) {
	root := t.TempDir()
	// The directory is named after an older id than the header carries: a
	// resumed session keeps its directory and writes a new header, and the
	// header is what dsh resumes by.
	path := writeDeepSeekSession(t, root, "stale-directory-name", deepSeekLog, false)

	ss, err := ParseDeepSeekFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(ss) != 1 {
		t.Fatalf("sessions = %d, want 1", len(ss))
	}
	s := ss[0]
	if s.Harness != "deepseek" {
		t.Errorf("harness = %q", s.Harness)
	}
	if s.ID != "eaf5c9ac-0e47-4d2f-b982-8bae306062d1" {
		t.Errorf("id = %q; the header's id wins over the directory name", s.ID)
	}
	if s.Project != "pgbouncer-lab" {
		t.Errorf("project = %q; it comes from the header's cwd", s.Project)
	}
	if s.Title != "pgbouncer pool size" {
		t.Errorf("title = %q", s.Title)
	}
	if len(s.Messages) != 2 {
		t.Fatalf("messages = %d, want the question and the answer:\n%+v", len(s.Messages), s.Messages)
	}
	if s.Messages[0].Role != "user" || s.Messages[0].Text != "сколько коннектов держим на шард?" {
		t.Errorf("first message = %+v", s.Messages[0])
	}
	// The deltas are one answer, not two messages.
	if s.Messages[1].Role != "assistant" || s.Messages[1].Text != "держим 40 на шард" {
		t.Errorf("second message = %+v", s.Messages[1])
	}
	if s.Started.IsZero() {
		t.Error("started is zero; the header carries createdAt in milliseconds")
	}
}

// What a plugin splices into the session is the harness describing itself: the
// sandbox policy, the skill catalogue. It arrives as user/message like a
// person's turn and must not be recalled as one.
func TestParseDeepSeekFileSkipsWhatPluginsSpliced(t *testing.T) {
	root := t.TempDir()
	path := writeDeepSeekSession(t, root, "plugins", deepSeekLog, false)
	ss, err := ParseDeepSeekFile(path)
	if err != nil || len(ss) != 1 {
		t.Fatalf("parse: %v, %d sessions", err, len(ss))
	}
	for _, m := range ss[0].Messages {
		if strings.Contains(m.Text, "system-reminder") || strings.Contains(m.Text, "runtime context") {
			t.Errorf("a plugin's own text was recalled as a turn: %q", m.Text)
		}
	}
}

func TestParseDeepSeekFileReadsZstdFrames(t *testing.T) {
	if !ZstdAvailable() {
		t.Skip("zstd CLI not installed")
	}
	root := t.TempDir()
	path := writeDeepSeekSession(t, root, "compressed", deepSeekLog, true)
	ss, err := ParseDeepSeekFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(ss) != 1 || len(ss[0].Messages) != 2 {
		t.Fatalf("compressed session read as %+v", ss)
	}
	if ss[0].Messages[1].Text != "держим 40 на шард" {
		t.Errorf("answer = %q", ss[0].Messages[1].Text)
	}
}

func TestDeepSeekSessionFilesFindsBothEncodings(t *testing.T) {
	root := t.TempDir()
	t.Setenv("DEJA_DEEPSEEK_ROOT", root)
	writeDeepSeekSession(t, root, "raw", deepSeekLog, false)
	files := DeepSeekSessionFiles()
	if len(files) != 1 {
		t.Fatalf("files = %v", files)
	}
	if got := LoadDeepSeek(); len(got) != 1 {
		t.Fatalf("LoadDeepSeek = %d sessions", len(got))
	}
}
