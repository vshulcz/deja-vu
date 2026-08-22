package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A session that hit an error and then ran the command that made it go away,
// which is what deja mines a fix pair out of.
func seedFixPair(t *testing.T, errLine, fixCmd string) string {
	t.Helper()
	tmp := hermeticEnv(t)
	proj := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-work-app")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	at := time.Now().Add(-72 * time.Hour)
	line := func(role, text string, i int) string {
		return fmt.Sprintf(`{"type":%q,"sessionId":"fixpair","timestamp":%q,"cwd":"/work/app","message":{"role":%q,"content":%q}}`,
			role, at.Add(time.Duration(i)*time.Minute).UTC().Format(time.RFC3339), role, text) + "\n"
	}
	// The command has to be a real tool_use record: a pair is mined from an
	// error record followed by a command record, and "$ cmd" typed into an
	// assistant message is prose, not a command deja ran.
	toolUse := func(cmd string, i int) string {
		return fmt.Sprintf(`{"type":"assistant","sessionId":"fixpair","timestamp":%q,"cwd":"/work/app","message":{"role":"assistant","content":[{"type":"tool_use","name":"Bash","input":{"command":%q}}]}}`,
			at.Add(time.Duration(i)*time.Minute).UTC().Format(time.RFC3339), cmd) + "\n"
	}
	// Twice, in two sessions. One session running something after an error is
	// half the evidence — deja keeps it as a candidate and does not serve it
	// until a second session confirms the same remedy.
	for n := 0; n < 2; n++ {
		body := line("user", "the build is broken", 0) +
			line("assistant", "ran it and got:\n"+errLine, 1) +
			toolUse(fixCmd, 2) +
			line("assistant", "that did it, the build is green now", 3)
		body = strings.ReplaceAll(body, `"sessionId":"fixpair"`, fmt.Sprintf(`"sessionId":"fixpair-%d"`, n))
		name := fmt.Sprintf("fixpair-%d.jsonl", n)
		if err := os.WriteFile(filepath.Join(proj, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := captureRunStderr(t, "index"); err != nil {
		t.Fatal(err)
	}
	return tmp
}

func postToolPayload(tool, command, stdout, stderr, session string) string {
	b, _ := json.Marshal(map[string]any{
		"hook_event_name": "PostToolUse",
		"tool_name":       tool,
		"tool_input":      map[string]any{"command": command},
		"tool_response":   map[string]any{"stdout": stdout, "stderr": stderr},
		"session_id":      session,
		"cwd":             "/work/app",
	})
	return string(b)
}

// The measured finding behind this hook: an agent chose the `fix` tool zero
// times in eighteen opportunities, because it does not occur to a model to ask
// whether an error has been solved here before. So the pair arrives on its own,
// in the turn the command failed (#1298).
func TestFixPairArrivesWhenTheCommandFails(t *testing.T) {
	seedFixPair(t, "panic: sql: database is closed", "make clean && make CGO_ENABLED=0")

	var out bytes.Buffer
	payload := postToolPayload("Bash", "make", "", "goroutine 1 [running]:\npanic: sql: database is closed", "agent-1")
	if err := runHookToolAfter(os.Getenv("DEJA_INDEX_DIR"), strings.NewReader(payload), &out); err != nil {
		t.Fatal(err)
	}
	// Decode rather than grep the wire: the payload is JSON, and `&&` in a
	// command comes back as \u0026\u0026.
	var resp sessionStartHookResponse
	if err := json.Unmarshal(out.Bytes(), &resp); err != nil {
		t.Fatalf("undecodable hook response %q: %v", out.String(), err)
	}
	got := resp.HookSpecificOutput.AdditionalContext
	if !strings.Contains(got, "make clean && make CGO_ENABLED=0") {
		t.Fatalf("the remembered command was not delivered:\n%s", got)
	}
	if strings.Contains(got, "$ make") {
		t.Errorf("the line hands the agent a shell prompt to strip:\n%s", got)
	}
	if resp.HookSpecificOutput.HookEventName != "PostToolUse" {
		t.Errorf("the response answers %q", resp.HookSpecificOutput.HookEventName)
	}

	// Once per agent session: a hook that fires on every action must not repeat
	// the same fact turn after turn.
	var again bytes.Buffer
	if err := runHookToolAfter(os.Getenv("DEJA_INDEX_DIR"), strings.NewReader(payload), &again); err != nil {
		t.Fatal(err)
	}
	if again.Len() != 0 {
		t.Errorf("the same pair was injected twice in one session:\n%s", again.String())
	}
}

// Silence is the common case. Output that carries no error signature, and an
// error nobody on this machine ever solved, both get nothing.
func TestFixPairStaysQuietWithoutOne(t *testing.T) {
	seedFixPair(t, "panic: sql: database is closed", "make clean && make CGO_ENABLED=0")

	for _, c := range []struct {
		name    string
		payload string
	}{
		{"ordinary output", postToolPayload("Bash", "ls", "README.md\ngo.mod\n", "", "quiet-1")},
		{"an error with no pair", postToolPayload("Bash", "go test ./...", "", "panic: something nobody here has ever seen", "quiet-2")},
		{"a file edit, not a command", postToolPayload("Edit", "", "", "goroutine 1 [running]:\npanic: sql: database is closed", "quiet-3")},
		{"no output at all", postToolPayload("Bash", "true", "", "", "quiet-4")},
	} {
		var out bytes.Buffer
		if err := runHookToolAfter(os.Getenv("DEJA_INDEX_DIR"), strings.NewReader(c.payload), &out); err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if out.Len() != 0 {
			t.Errorf("%s: expected silence, got:\n%s", c.name, out.String())
		}
	}
}

// Harnesses disagree about the shape of a tool response, and a reader that only
// knows one of them is blind on the rest.
func TestFixPairReadsWhateverTheHarnessCallsOutput(t *testing.T) {
	seedFixPair(t, "panic: sql: database is closed", "make clean && make CGO_ENABLED=0")

	shapes := map[string]any{
		"a bare string":      "goroutine 1 [running]:\npanic: sql: database is closed",
		"an output field":    map[string]any{"output": "goroutine 1 [running]:\npanic: sql: database is closed"},
		"an error field":     map[string]any{"error": "goroutine 1 [running]:\npanic: sql: database is closed"},
		"stdout, not stderr": map[string]any{"stdout": "goroutine 1 [running]:\npanic: sql: database is closed", "stderr": ""},
	}
	i := 0
	for name, resp := range shapes {
		i++
		b, _ := json.Marshal(map[string]any{
			"hook_event_name": "PostToolUse",
			"tool_name":       "Bash",
			"tool_input":      map[string]any{"command": "make"},
			"tool_response":   resp,
			"session_id":      fmt.Sprintf("shape-%d", i),
			"cwd":             "/work/app",
		})
		var out bytes.Buffer
		if err := runHookToolAfter(os.Getenv("DEJA_INDEX_DIR"), strings.NewReader(string(b)), &out); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if !strings.Contains(out.String(), "make clean") {
			t.Errorf("%s: nothing delivered:\n%s", name, out.String())
		}
	}
}

// A remembered command that itself exited non-zero is not a remedy, and telling
// an agent "this is what followed it" hands it something that already failed
// here. Found on a real store: the top pair for "command not found: python" was
// `python3 -m pytest  → exit 1`.
func TestFixPairRefusesACommandThatFailed(t *testing.T) {
	for _, c := range []struct {
		in   string
		want string
		ok   bool
	}{
		{"go mod tidy", "go mod tidy", true},
		{"go mod tidy  → exit 0", "go mod tidy", true},
		{"python3 -m pytest  → exit 1", "", false},
		{"make  → exit 2", "", false},
	} {
		got, ok := withoutFailedExit(c.in)
		if ok != c.ok || (ok && got != c.want) {
			t.Errorf("withoutFailedExit(%q) = (%q, %v), want (%q, %v)", c.in, got, ok, c.want, c.ok)
		}
	}
}
