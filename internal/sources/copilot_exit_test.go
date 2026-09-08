package sources

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Copilot records every completion with `"success": true`, failed runs
// included, and puts the outcome in the telemetry and in a trailer under the
// output. deja took the field at its word, so no command carried an outcome
// and `deja fix` answered nothing for the harness (#3369).
func TestCopilotMarksACommandThatExitedNonZero(t *testing.T) {
	dir := t.TempDir()
	sess := filepath.Join(dir, "7dad4ddc")
	if err := os.MkdirAll(sess, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(sess, "events.jsonl")
	lines := strings.Join([]string{
		`{"type":"session.start","timestamp":"2026-09-05T10:00:00Z","data":{"sessionId":"7dad4ddc","startTime":"2026-09-05T10:00:00Z","context":{"cwd":"/w/api"}}}`,
		`{"type":"tool.execution_start","timestamp":"2026-09-05T10:00:01Z","data":{"toolName":"bash","toolCallId":"call-1","arguments":{"command":"go vet ./..."}}}`,
		`{"type":"tool.execution_complete","timestamp":"2026-09-05T10:00:02Z","data":{"toolCallId":"call-1","success":true,"result":{"content":"pattern ./...: directory prefix . does not contain main module\n<shellId: 0 completed with exit code 1>"},"toolTelemetry":{"metrics":{"commandTimeout":30000,"exit_code":1}}}}`,
		`{"type":"tool.execution_start","timestamp":"2026-09-05T10:00:03Z","data":{"toolName":"bash","toolCallId":"call-2","arguments":{"command":"go mod init api && go vet ./..."}}}`,
		`{"type":"tool.execution_complete","timestamp":"2026-09-05T10:00:04Z","data":{"toolCallId":"call-2","success":true,"result":{"content":"ok\n<shellId: 0 completed with exit code 0>"},"toolTelemetry":{"metrics":{"commandTimeout":30000}}}}`,
	}, "\n") + "\n"
	if err := os.WriteFile(path, []byte(lines), 0o644); err != nil {
		t.Fatal(err)
	}

	ss, err := ParseCopilotFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(ss) != 1 {
		t.Fatalf("sessions = %d, want 1", len(ss))
	}
	var cmds []string
	for _, m := range ss[0].Messages {
		if m.Role == RoleCommand {
			cmds = append(cmds, m.Text)
		}
	}
	if len(cmds) != 2 {
		t.Fatalf("commands = %d, want 2: %q", len(cmds), cmds)
	}
	if !strings.HasSuffix(cmds[0], "  → exit 1") {
		t.Errorf("the failed run reads %q, want it to end in the exit marker", cmds[0])
	}
	if strings.Contains(cmds[1], "→ exit") {
		t.Errorf("the run that succeeded carries an outcome it did not have: %q", cmds[1])
	}
}

// And the outcome is what the fix pair reads: with it, the command that
// followed a failed run is the remedy; without it the failed run itself could
// be handed back as one (#3369).
func TestCopilotFailedRunIsNotOfferedAsTheRemedy(t *testing.T) {
	dir := t.TempDir()
	sess := filepath.Join(dir, "s1")
	if err := os.MkdirAll(sess, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(sess, "events.jsonl")
	lines := strings.Join([]string{
		`{"type":"session.start","timestamp":"2026-09-05T10:00:00Z","data":{"sessionId":"s1","startTime":"2026-09-05T10:00:00Z","context":{"cwd":"/w/api"}}}`,
		`{"type":"tool.execution_start","timestamp":"2026-09-05T10:00:01Z","data":{"toolName":"bash","toolCallId":"c1","arguments":{"command":"go build ./..."}}}`,
		`{"type":"tool.execution_complete","timestamp":"2026-09-05T10:00:02Z","data":{"toolCallId":"c1","success":true,"result":{"content":"queue/retry.go:12:2: undefined: backoffCap\n<shellId: 0 completed with exit code 1>"},"toolTelemetry":{"metrics":{"exit_code":1}}}}`,
	}, "\n") + "\n"
	if err := os.WriteFile(path, []byte(lines), 0o644); err != nil {
		t.Fatal(err)
	}
	ss, err := ParseCopilotFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range ss[0].Messages {
		if m.Role == RoleCommand && !strings.Contains(m.Text, "→ exit 1") {
			t.Errorf("the only command in the session failed and reads %q", m.Text)
		}
	}
}

// The scanner decodes numbers with UseNumber, so the telemetry's exit code
// arrives as json.Number. Asserting float64 made that path dead and left every
// marker to the trailer, which a run without one never has (review of #3369).
func TestCopilotReadsTheExitCodeFromTelemetryWithoutATrailer(t *testing.T) {
	dir := t.TempDir()
	sess := filepath.Join(dir, "s2")
	if err := os.MkdirAll(sess, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(sess, "events.jsonl")
	lines := strings.Join([]string{
		`{"type":"session.start","timestamp":"2026-09-05T10:00:00Z","data":{"sessionId":"s2","startTime":"2026-09-05T10:00:00Z","context":{"cwd":"/w/api"}}}`,
		`{"type":"tool.execution_start","timestamp":"2026-09-05T10:00:01Z","data":{"toolName":"bash","toolCallId":"c1","arguments":{"command":"go test ./..."}}}`,
		`{"type":"tool.execution_complete","timestamp":"2026-09-05T10:00:02Z","data":{"toolCallId":"c1","success":true,"result":{"content":"--- FAIL: TestRetry (0.01s)\nFAIL\tqueue\t0.4s"},"toolTelemetry":{"metrics":{"exit_code":1}}}}`,
	}, "\n") + "\n"
	if err := os.WriteFile(path, []byte(lines), 0o644); err != nil {
		t.Fatal(err)
	}
	ss, err := ParseCopilotFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range ss[0].Messages {
		if m.Role == RoleCommand && !strings.HasSuffix(m.Text, "  → exit 1") {
			t.Errorf("command = %q, want the outcome the telemetry recorded", m.Text)
		}
	}
}

// One outcome belongs to one call: an id reused later must not put its exit
// code on a run that already finished (second review of #3369).
func TestCopilotDoesNotMarkAnEarlierRunWithALaterOutcome(t *testing.T) {
	dir := t.TempDir()
	sess := filepath.Join(dir, "s3")
	if err := os.MkdirAll(sess, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(sess, "events.jsonl")
	lines := strings.Join([]string{
		`{"type":"session.start","timestamp":"2026-09-05T10:00:00Z","data":{"sessionId":"s3","startTime":"2026-09-05T10:00:00Z","context":{"cwd":"/w/api"}}}`,
		`{"type":"tool.execution_start","timestamp":"2026-09-05T10:00:01Z","data":{"toolName":"bash","toolCallId":"dup","arguments":{"command":"go build ./..."}}}`,
		`{"type":"tool.execution_complete","timestamp":"2026-09-05T10:00:02Z","data":{"toolCallId":"dup","success":true,"result":{"content":"ok"},"toolTelemetry":{"metrics":{"exit_code":0}}}}`,
		`{"type":"tool.execution_start","timestamp":"2026-09-05T10:00:03Z","data":{"toolName":"bash","toolCallId":"dup","arguments":{"command":"go test ./..."}}}`,
		`{"type":"tool.execution_complete","timestamp":"2026-09-05T10:00:04Z","data":{"toolCallId":"dup","success":true,"result":{"content":"--- FAIL: TestRetry"},"toolTelemetry":{"metrics":{"exit_code":1}}}}`,
	}, "\n") + "\n"
	if err := os.WriteFile(path, []byte(lines), 0o644); err != nil {
		t.Fatal(err)
	}
	ss, err := ParseCopilotFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var cmds []string
	for _, m := range ss[0].Messages {
		if m.Role == RoleCommand {
			cmds = append(cmds, m.Text)
		}
	}
	if len(cmds) != 2 {
		t.Fatalf("commands = %d, want 2: %q", len(cmds), cmds)
	}
	if strings.Contains(cmds[0], "→ exit") {
		t.Errorf("the run that succeeded wears a later failure: %q", cmds[0])
	}
	if !strings.HasSuffix(cmds[1], "  → exit 1") {
		t.Errorf("the run that failed lost its outcome: %q", cmds[1])
	}
}
