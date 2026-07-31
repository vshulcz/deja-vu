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
	t.Setenv("USERPROFILE", home)
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

// Antigravity puts prose and tool transcripts in the same MODEL stream, and
// reading only the source made shell dumps into assistant speech — 333 of 369
// rows on a real store, 90%, ranked as things the agent said. The step's kind
// is what separates them, and the same header that identifies a tool step
// carries the work: a command on "Task Description:", a file on "File Path:".
func TestAntigravityStepSeparatesProseFromTools(t *testing.T) {
	dir := t.TempDir()
	logs := filepath.Join(dir, "brain", "conv-1", ".system_generated", "logs")
	if err := os.MkdirAll(logs, 0o755); err != nil {
		t.Fatal(err)
	}
	// The workspace the session belongs to, which the parser used to ignore.
	cache := filepath.Join(dir, "cache")
	if err := os.MkdirAll(cache, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cache, "conversation_metadata.json"),
		[]byte(`{"conversations":{"conv-1":{"summary":{"WorkspaceURIs":["file:///Users/me/coding/api-gateway"]}}}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_ANTIGRAVITY_ROOT", dir)
	rows := []string{
		`{"source":"USER_EXPLICIT","type":"USER_REQUEST","created_at":"2026-07-20T10:00:00Z","content":"<USER_REQUEST>\nfix the retry loop\n</USER_REQUEST>"}`,
		`{"source":"MODEL","type":"PLANNER_RESPONSE","created_at":"2026-07-20T10:01:00Z","content":"I will start by reading the file."}`,
		`{"source":"MODEL","type":"RUN_COMMAND","created_at":"2026-07-20T10:02:00Z","content":"Created At: 2026-07-20T10:02:00Z\nTask Description: go test ./...\nOutput:\nFAIL\tgithub.com/x/y"}`,
		`{"source":"MODEL","type":"VIEW_FILE","created_at":"2026-07-20T10:03:00Z","content":"Created At: 2026-07-20T10:03:00Z\nCompleted At: 2026-07-20T10:03:01Z\nFile Path: ` + "`" + `file:///w/app/retry.go` + "`" + `\nTotal Lines: 40"}`,
		`{"source":"MODEL","type":"GREP_SEARCH","created_at":"2026-07-20T10:04:00Z","content":"Created At: 2026-07-20T10:04:00Z\nCompleted At: 2026-07-20T10:04:00Z"}`,
	}
	p := filepath.Join(logs, "transcript.jsonl")
	if err := os.WriteFile(p, []byte(strings.Join(rows, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ss, err := ParseAntigravityFile(p)
	if err != nil || len(ss) != 1 {
		t.Fatalf("%v %#v", err, ss)
	}
	// Reachable by project: every session used to report "-".
	if ss[0].Project != "api-gateway" {
		t.Errorf("project = %q, want the workspace from the metadata", ss[0].Project)
	}
	byRole := map[string][]string{}
	for _, m := range ss[0].Messages {
		byRole[m.Role] = append(byRole[m.Role], m.Text)
	}
	// Prose is the only thing that counts as speech.
	if len(byRole["assistant"]) != 1 || !strings.Contains(byRole["assistant"][0], "reading the file") {
		t.Errorf("assistant = %q, want only the planner prose", byRole["assistant"])
	}
	for _, blob := range byRole["assistant"] {
		if strings.Contains(blob, "Task Description") || strings.Contains(blob, "File Path") {
			t.Errorf("a tool transcript is still speech: %q", blob)
		}
	}
	if len(byRole[RoleCommand]) != 1 || byRole[RoleCommand][0] != "$ go test ./..." {
		t.Errorf("commands = %q", byRole[RoleCommand])
	}
	if len(byRole[RoleFiles]) != 1 || byRole[RoleFiles][0] != "/w/app/retry.go" {
		t.Errorf("files = %q, want the plain path without the file:// scheme or backticks", byRole[RoleFiles])
	}
	// A step whose whole body is its own timestamps has nothing to index.
	for _, out := range byRole[RoleToolOutput] {
		if strings.HasPrefix(out, "Created At:") || out == "" {
			t.Errorf("indexed a step with no content but its header: %q", out)
		}
	}
}

func TestAntigravityBodyDropsTheHeader(t *testing.T) {
	got := antigravityBody("Created At: 2026-07-20T10:02:00Z\nCompleted At: 2026-07-20T10:02:01Z\nthe real output")
	if got != "the real output" {
		t.Fatalf("got %q", got)
	}
	if got := antigravityBody("Created At: x\nCompleted At: y"); got != "" {
		t.Fatalf("a header-only step should strip to empty, got %q", got)
	}
}

func TestAntigravityPathForms(t *testing.T) {
	for in, want := range map[string]string{
		"File Path: `file:///w/a.go`":              "/w/a.go",
		"File Path: file:///w/b.go":                "/w/b.go",
		"Created file file:///w/c.md with content": "/w/c.md",
		"File Path: not-a-uri":                     "",
	} {
		if got := antigravityPath(in); got != want {
			t.Errorf("antigravityPath(%q) = %q, want %q", in, got, want)
		}
	}
}

// Every antigravity session reported project "-", so the harness was
// unreachable from any --project query — while the workspace sat in plain
// JSON in a directory deja already walks.
func TestAntigravityProjectComesFromTheMetadata(t *testing.T) {
	root := t.TempDir()
	cache := filepath.Join(root, "cache")
	if err := os.MkdirAll(cache, 0o755); err != nil {
		t.Fatal(err)
	}
	meta := `{"conversations":{"conv-1-abc":{"summary":{"WorkspaceURIs":["file:///Users/me/coding/api-gateway"]}}}}`
	if err := os.WriteFile(filepath.Join(cache, "conversation_metadata.json"), []byte(meta), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_ANTIGRAVITY_ROOT", root)
	if got := antigravityProject("conv-1-abc"); got != "api-gateway" {
		t.Fatalf("project = %q, want the workspace from the metadata", got)
	}
	// A conversation the metadata does not know still has to parse.
	if got := antigravityProject("never-seen"); got != "-" {
		t.Fatalf("unknown conversation = %q, want the placeholder", got)
	}
}
