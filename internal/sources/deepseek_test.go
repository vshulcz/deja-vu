package sources

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Every line here is a shape dsh 0.1.1-rc.2 wrote while a local model answered
// through it: the question, the two kinds of text a plugin splices into the
// same event, a tool call and its result, and the complete assistant message
// that supersedes the deltas streamed before it.
const deepSeekLog = `{"type":"session","version":0,"id":"session-eaf5c9ac-0e47-4d2f-b982-8bae306062d1","createdAt":1787320263519,"cwd":"/work/pgbouncer-lab","delegationDepth":0}
{"type":"permission/preset","seq":0,"time":1787320263520,"data":{"preset":"workspace-write"}}
{"type":"session/title","seq":10,"time":1787320263581,"data":{"title":"pgbouncer pool size","source":{"kind":"fallback"}}}
{"type":"user/message","seq":7,"time":1787320263580,"data":{"content":[{"type":"text","text":"сколько коннектов держим на шард?"}],"source":{"kind":"user"},"role":"user"}}
{"type":"user/message","seq":8,"time":1787320263580,"data":{"content":[{"type":"text","text":"Current runtime context. This snapshot supersedes earlier ones."}],"source":{"kind":"plugin","plugin":"@deepseek-ai/dsh-system-prompt"},"role":"user"}}
{"type":"user/message","seq":9,"time":1787320263580,"data":{"content":[{"type":"text","text":"<system-reminder>A skill is a reusable set of instructions.</system-reminder>"}],"source":{"kind":"skill-catalog"},"role":"user"}}
{"type":"assistant/chunk","seq":12,"time":1787320263585,"data":{"turn":1,"step":1,"chunk":{"type":"text-delta","text":"сей"}}}
{"type":"assistant/chunk","seq":13,"time":1787320263586,"data":{"turn":1,"step":1,"chunk":{"type":"text-delta","text":"час посмотрю"}}}
{"type":"assistant/message","seq":20,"time":1787320263700,"data":{"turn":1,"step":1,"message":{"role":"assistant","content":[{"type":"reasoning","text":"надо открыть конфиг"},{"type":"text","text":"сейчас посмотрю конфиг"}],"source":{"kind":"model","provider":"mini"}}}}
{"type":"tool/call","seq":21,"time":1787320263710,"data":{"turn":1,"step":1,"callId":"c1","name":"read","arguments":"{\"file_path\":\"/work/pgbouncer-lab/notes.txt\"}"}}
{"type":"tool/result","seq":22,"time":1787320263720,"data":{"turn":1,"step":1,"message":{"source":{"kind":"tool","callId":"c1"},"content":[{"type":"tool-result","toolCallId":"c1","content":[{"type":"text","text":"pgbouncer pool_size = 40"}]}]}}}
{"type":"assistant/message","seq":40,"time":1787320263900,"data":{"turn":1,"step":2,"message":{"role":"assistant","content":[{"type":"text","text":"держим 40 на шард"}],"source":{"kind":"model","provider":"mini"}}}}
{"type":"step/end","seq":41,"time":1787320263901,"data":{"turn":1,"step":2}}
{"type":"turn/end","seq":42,"time":1787320263902,"data":{"turn":1}}
`

// A run killed mid-answer never writes the complete message, and a run of three
// or more consecutive deltas is stored as one packed row rather than as the
// events themselves. Both together are what an interrupted session looks like.
const deepSeekInterrupted = `{"type":"session","version":0,"id":"session-81f6aa2b-d16e-47a8-9841-581fb5f0007b","createdAt":1787325263519,"cwd":"/work/pgbouncer-lab"}
{"type":"user/message","seq":7,"time":1787325263580,"data":{"content":[{"type":"text","text":"расскажи про пул соединений"}],"source":{"kind":"user"},"role":"user"}}
{"type":"reasoning-chunks","seq0":15,"time0":1787325263600,"data":{"turn":1,"step":1,"index":0,"dt":[76,76],"texts":["надо","подумать","как"]}}
{"type":"text-chunks","seq0":18,"time0":1787325263700,"data":{"turn":1,"step":1,"index":0,"dt":[79,75],"texts":["пул ","держит ","соединения"]}}
{"type":"assistant/chunk","seq":25,"time":1787325263800,"data":{"turn":1,"step":1,"chunk":{"type":"text-delta","text":" открытыми"}}}
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
	if err := exec.Command("zstd", "-q", "-o", out, path).Run(); err != nil {
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
	// resumed session keeps its directory and writes a new header.
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

	var roles []string
	for _, m := range s.Messages {
		roles = append(roles, m.Role)
	}
	want := []string{"user", "assistant", "tool-output", "assistant"}
	if strings.Join(roles, ",") != strings.Join(want, ",") {
		t.Fatalf("roles = %v, want %v:\n%+v", roles, want, s.Messages)
	}
	// The complete message replaces what was streamed of it; without that the
	// same answer lands twice, once in pieces.
	if s.Messages[1].Text != "сейчас посмотрю конфиг" {
		t.Errorf("answer = %q; the complete message supersedes the deltas", s.Messages[1].Text)
	}
	if strings.Contains(s.Messages[1].Text, "надо открыть конфиг") {
		t.Error("the model's reasoning was recalled as something it said")
	}
	if s.Messages[2].Text != "pgbouncer pool_size = 40" {
		t.Errorf("tool output = %q", s.Messages[2].Text)
	}
	if s.Messages[3].Text != "держим 40 на шард" {
		t.Errorf("final answer = %q", s.Messages[3].Text)
	}
	for i, m := range s.Messages {
		if m.Time.IsZero() {
			t.Errorf("message %d has no time: %+v", i, m)
		}
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

func TestParseDeepSeekFileKeepsAnInterruptedAnswer(t *testing.T) {
	root := t.TempDir()
	path := writeDeepSeekSession(t, root, "interrupted", deepSeekInterrupted, false)
	ss, err := ParseDeepSeekFile(path)
	if err != nil || len(ss) != 1 {
		t.Fatalf("parse: %v, %d sessions", err, len(ss))
	}
	s := ss[0]
	if len(s.Messages) != 2 {
		t.Fatalf("messages = %d, want the question and what was streamed:\n%+v", len(s.Messages), s.Messages)
	}
	// Three of the four pieces were stored as one packed row; a reader that
	// knows only the delta events keeps the last word and loses the answer.
	if s.Messages[1].Text != "пул держит соединения открытыми" {
		t.Errorf("streamed answer = %q", s.Messages[1].Text)
	}
	if strings.Contains(s.Messages[1].Text, "надо подумать") {
		t.Error("a packed run of reasoning deltas was read as speech")
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
	if len(ss) != 1 || len(ss[0].Messages) != 4 {
		t.Fatalf("compressed session read as %+v", ss)
	}
	if ss[0].Messages[3].Text != "держим 40 на шард" {
		t.Errorf("answer = %q", ss[0].Messages[3].Text)
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
