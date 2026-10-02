package sources

import (
	"os"
	"path/filepath"
	"testing"
)

func parseAntigravityLines(t *testing.T, lines string) map[string][]string {
	t.Helper()
	root := t.TempDir()
	t.Setenv("DEJA_ANTIGRAVITY_ROOT", root)
	dir := filepath.Join(root, "brain", "7d1e0c3a-5b6f-4a2e-9c1d-0e2f3a4b5c6d", ".system_generated", "logs")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, "transcript.jsonl")
	if err := os.WriteFile(p, []byte(lines), 0o644); err != nil {
		t.Fatal(err)
	}
	ss, err := ParseAntigravityFile(p)
	if err != nil || len(ss) != 1 {
		t.Fatalf("parse: %v, %d sessions", err, len(ss))
	}
	got := map[string][]string{}
	for _, m := range ss[0].Messages {
		got[m.Role] = append(got[m.Role], m.Text)
	}
	got["project"] = []string{ss[0].Project}
	return got
}

// The planner row carries the structured call; the RUN_COMMAND step after it
// need not name the command at all, and then the command was lost (#4358).
func TestAntigravityReadsTheCommandAndFilesFromToolCalls(t *testing.T) {
	got := parseAntigravityLines(t, `{"step_index":0,"source":"USER_EXPLICIT","type":"USER_INPUT","status":"DONE","created_at":"2026-09-30T10:00:00Z","content":"<USER_REQUEST>\nfix the retry loop in client.go\n</USER_REQUEST>"}
{"step_index":1,"source":"MODEL","type":"PLANNER_RESPONSE","status":"DONE","created_at":"2026-09-30T10:00:05Z","content":"Running the tests first.","tool_calls":[{"name":"run_command","args":{"CommandLine":"go test ./...","Cwd":"/tmp/proj","WaitMsBeforeAsync":500}}]}
{"step_index":2,"source":"MODEL","type":"RUN_COMMAND","status":"DONE","created_at":"2026-09-30T10:00:09Z","content":"--- FAIL: TestRetry (0.00s)\nFAIL"}
{"step_index":3,"source":"MODEL","type":"PLANNER_RESPONSE","status":"DONE","created_at":"2026-09-30T10:00:12Z","content":"","tool_calls":[{"name":"view_file","args":{"AbsolutePath":"/tmp/proj/client.go"}},{"name":"replace_file_content","args":{"TargetFile":"/tmp/proj/client.go"}},{"name":"write_to_file","args":{"TargetFile":"/tmp/proj/retry_test.go"}}]}
{"step_index":4,"source":"MODEL","type":"PLANNER_RESPONSE","status":"DONE","created_at":"2026-09-30T10:00:20Z","content":"Fixed the off-by-one in the retry loop."}
`)
	if c := got[RoleCommand]; len(c) != 1 || c[0] != "$ go test ./..." {
		t.Errorf("commands = %q, want the run_command call", c)
	}
	f := got[RoleFiles]
	if len(f) != 3 || f[0] != "/tmp/proj/client.go" || f[2] != "/tmp/proj/retry_test.go" {
		t.Errorf("files = %q, want the viewed, edited and written paths", f)
	}
	// No files outside the root directory: the command's Cwd names the project.
	if p := got["project"]; p[0] != "tmp/proj" {
		t.Errorf("project = %q, want the command's Cwd", p)
	}
}

// When the step does carry the header too, the command is the same one run
// once, not two.
func TestAntigravityToolCallAndStepHeaderAreOneCommand(t *testing.T) {
	got := parseAntigravityLines(t, `{"step_index":1,"source":"MODEL","type":"PLANNER_RESPONSE","status":"DONE","created_at":"2026-09-30T10:00:05Z","content":"Running the tests first.","tool_calls":[{"name":"run_command","args":{"CommandLine":"go test ./...","Cwd":"/tmp/proj"}}]}
{"step_index":2,"source":"MODEL","type":"RUN_COMMAND","status":"DONE","created_at":"2026-09-30T10:00:09Z","content":"Task Description: go test ./...\n--- FAIL: TestRetry (0.00s)"}
{"step_index":3,"source":"MODEL","type":"PLANNER_RESPONSE","status":"DONE","created_at":"2026-09-30T10:00:12Z","content":"Again.","tool_calls":[{"name":"run_command","args":{"CommandLine":"go test ./...","Cwd":"/tmp/proj"}}]}
{"step_index":4,"source":"MODEL","type":"RUN_COMMAND","status":"DONE","created_at":"2026-09-30T10:00:15Z","content":"Task Description: go test ./...\nok"}
{"step_index":5,"source":"MODEL","type":"PLANNER_RESPONSE","status":"DONE","created_at":"2026-09-30T10:00:20Z","content":"Edited.","tool_calls":[{"name":"replace_file_content","args":{"TargetFile":"/tmp/proj/client.go"}}]}
{"step_index":6,"source":"MODEL","type":"CODE_ACTION","status":"DONE","created_at":"2026-09-30T10:00:21Z","content":"The following changes were made by the replace_file_content tool to: /tmp/proj/client.go. If relevant, run it.\n"}
`)
	if c := got[RoleCommand]; len(c) != 2 {
		t.Errorf("commands = %q, want one per run", c)
	}
	if f := got[RoleFiles]; len(f) != 1 {
		t.Errorf("files = %q, want the edit once", f)
	}
}

// On disk every arg is itself JSON: CommandLine is `"go test ./..."`, quotes
// and all, and so are Cwd, AbsolutePath and TargetFile. Read as they stand,
// the command was indexed with its quotes and a second time from the step
// header without them, and no path was absolute (#4358).
func TestAntigravityToolCallArgsAreJSONEncoded(t *testing.T) {
	got := parseAntigravityLines(t, `{"step_index":1,"source":"MODEL","type":"PLANNER_RESPONSE","status":"DONE","created_at":"2026-09-30T10:00:05Z","content":"Running the tests first.","tool_calls":[{"name":"run_command","args":{"CommandLine":"\"go test ./...\"","Cwd":"\"/tmp/proj\"","WaitMsBeforeAsync":500}}]}
{"step_index":2,"source":"MODEL","type":"RUN_COMMAND","status":"DONE","created_at":"2026-09-30T10:00:09Z","content":"Task Description: go test ./...\n--- FAIL: TestRetry (0.00s)"}
{"step_index":3,"source":"MODEL","type":"PLANNER_RESPONSE","status":"DONE","created_at":"2026-09-30T10:00:12Z","content":"","tool_calls":[{"name":"view_file","args":{"AbsolutePath":"\"/tmp/proj/client.go\""}},{"name":"write_to_file","args":{"TargetFile":"\"/tmp/proj/retry_test.go\""}}]}
`)
	if c := got[RoleCommand]; len(c) != 1 || c[0] != "$ go test ./..." {
		t.Errorf("commands = %q, want the one run, unquoted", c)
	}
	if f := got[RoleFiles]; len(f) != 2 || f[0] != "/tmp/proj/client.go" || f[1] != "/tmp/proj/retry_test.go" {
		t.Errorf("files = %q, want the paths unquoted", f)
	}
	if p := got["project"]; p[0] != "tmp/proj" {
		t.Errorf("project = %q, want tmp/proj", p)
	}
}
