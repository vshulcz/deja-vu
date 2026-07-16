package sources

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func antigravityTree(t *testing.T) (root, transcript string) {
	t.Helper()
	root = t.TempDir()
	t.Setenv("DEJA_ANTIGRAVITY_ROOT", root)
	transcript = filepath.Join(root, "brain", "traj-123", ".system_generated", "logs", "transcript.jsonl")
	if err := os.MkdirAll(filepath.Dir(transcript), 0o755); err != nil {
		t.Fatal(err)
	}
	return root, transcript
}

func TestParseAntigravityFile(t *testing.T) {
	_, p := antigravityTree(t)
	long := strings.Repeat("x", 70*1024)
	lines := `{"step_index":1,"source":"USER_EXPLICIT","type":"USER_INPUT","created_at":"2026-07-08T14:18:27Z","content":"<USER_REQUEST>\nBuild this\n<USER_SETTINGS_CHANGE>{\"theme\":\"dark\"}</USER_SETTINGS_CHANGE>\n<ADDITIONAL_METADATA>{\"cwd\":\"/tmp\"}</ADDITIONAL_METADATA>\n</USER_REQUEST>"}
{"step_index":2,"source":"SYSTEM","type":"SYSTEM","created_at":"2026-07-08T14:18:28Z","content":"system noise"}
{"step_index":3,"source":"MODEL","type":"PLANNER_RESPONSE","created_at":"2026-07-08T14:18:29Z","thinking":"secret reasoning","content":"I can help."}
{"step_index":4,"source":"MODEL","type":"CODE_ACTION","created_at":"2026-07-08T14:18:30Z","content":""}
not-json
{"step_index":5,"source":"MODEL","type":"PLANNER_RESPONSE","created_at":"2026-07-08T14:18:31Z","content":"` + long + `"}
`
	if err := os.WriteFile(p, []byte(lines), 0o644); err != nil {
		t.Fatal(err)
	}
	ss, err := ParseAntigravityFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if len(ss) != 1 {
		t.Fatalf("sessions = %d, want 1: %#v", len(ss), ss)
	}
	s := ss[0]
	if s.Harness != "antigravity" || s.ID != "traj-123" || s.Project != "-" {
		t.Fatalf("bad meta: %#v", s)
	}
	if len(s.Messages) != 3 {
		t.Fatalf("messages = %d, want 3: %#v", len(s.Messages), s.Messages)
	}
	if s.Messages[0].Role != "user" || s.Messages[0].Text != "Build this" {
		t.Fatalf("user unwrap wrong: %#v", s.Messages[0])
	}
	if s.Messages[1].Role != "assistant" || s.Messages[1].Text != "I can help." || strings.Contains(s.Messages[1].Text, "secret") {
		t.Fatalf("assistant wrong: %#v", s.Messages[1])
	}
	if s.Messages[0].Time.Format("2006-01-02T15:04:05Z") != "2026-07-08T14:18:27Z" {
		t.Fatalf("timestamp wrong: %v", s.Messages[0].Time)
	}
	if s.Started != s.Messages[0].Time || s.Updated != s.Messages[2].Time {
		t.Fatalf("session times wrong: started=%v updated=%v", s.Started, s.Updated)
	}
	if len(s.Messages[2].Text) != 64*1024 {
		t.Fatalf("message cap = %d, want %d", len(s.Messages[2].Text), 64*1024)
	}
}

func TestAntigravityTranscriptsEnvOverride(t *testing.T) {
	root, p := antigravityTree(t)
	if err := os.WriteFile(p, []byte(`{"source":"USER_EXPLICIT","created_at":"2026-07-08T14:18:27Z","content":"hi"}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	files := AntigravityTranscripts()
	if len(files) != 1 || files[0] != p {
		t.Fatalf("transcripts = %v, want %s", files, p)
	}
	roots := AntigravityRoots()
	if len(roots) != 1 || roots[0] != root {
		t.Fatalf("roots = %v, want %s", roots, root)
	}
}

func TestAntigravityRootsGlob(t *testing.T) {
	t.Setenv("DEJA_ANTIGRAVITY_ROOT", "")
	home := t.TempDir()
	t.Setenv("HOME", home)
	want := filepath.Join(home, ".gemini", "antigravity-cli")
	if err := os.MkdirAll(want, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".gemini", "antigravity-file"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	roots := AntigravityRoots()
	if len(roots) != 1 || roots[0] != want {
		t.Fatalf("roots = %v, want %s", roots, want)
	}
}
