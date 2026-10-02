package sources

import (
	"strings"
	"testing"
)

// Continue keeps each tool call on the assistant item: the call in
// message.toolCalls and its arguments, status and output in toolCallStates.
// Shape from the session file cn 1.5.47 wrote (#4373).
func TestContinueIndexesToolCallsFromToolCallStates(t *testing.T) {
	root := t.TempDir()
	t.Setenv("DEJA_CONTINUE_ROOT", root)
	state := func(name, status string, args map[string]any, out string) map[string]any {
		return map[string]any{
			"toolCallId": "call_" + name, "status": status, "parsedArgs": args,
			"toolCall": map[string]any{"id": "call_" + name, "type": "function",
				"function": map[string]any{"name": name, "arguments": "{}"}},
			"output": []any{map[string]any{"name": "Tool Result", "content": out}},
		}
	}
	item := func(st map[string]any) map[string]any {
		return map[string]any{
			"message":        map[string]any{"role": "assistant", "content": "", "toolCalls": []any{st["toolCall"]}},
			"toolCallStates": []any{st},
		}
	}
	path := writeContinueStore(t, root, map[string]any{
		"sessionId":          "8f1c2a3e-0000-4000-8000-000000000000",
		"workspaceDirectory": "/w/api",
		"history": []any{
			map[string]any{"message": map[string]any{"role": "user", "content": "fix the retry loop"}},
			item(state("Bash", "done", map[string]any{"command": "git status --short && cat retry.cfg"}, "?? retry.cfg\nretry = 3\n")),
			item(state("Read", "done", map[string]any{"filepath": "/w/api/retry.cfg"}, "retry = 3\n")),
			item(state("Edit", "done", map[string]any{"file_path": "/w/api/retry.cfg", "old_string": "retry = 3", "new_string": "retry = 5 # upstream handles five fine"}, "edited")),
			item(state("Write", "done", map[string]any{"filepath": "/w/api/backoff.txt", "content": "backoff = exponential with jitter for the retry loop\n"}, "Successfully created file")),
			// A call Continue marked errored changed nothing: its output is the
			// failure, and it leaves no edit behind.
			item(state("Edit", "errored", map[string]any{"file_path": "/w/api/limits.cfg", "old_string": "cap = 1", "new_string": "cap = 2"}, "You must use the Read tool first")),
		},
	}, nil)

	ss, err := ParseContinueFile(path)
	if err != nil || len(ss) != 1 {
		t.Fatalf("parsed %d sessions: %v", len(ss), err)
	}
	got := map[string][]string{}
	for _, m := range ss[0].Messages {
		got[m.Role] = append(got[m.Role], m.Text)
	}
	if c := got[RoleCommand]; len(c) != 1 || c[0] != "$ git status --short && cat retry.cfg" {
		t.Fatalf("commands = %q", c)
	}
	files := strings.Join(got[RoleFiles], "\n")
	for _, p := range []string{"/w/api/retry.cfg", "/w/api/backoff.txt", "/w/api/limits.cfg"} {
		if !strings.Contains(files, p) {
			t.Fatalf("files = %q, want %s", files, p)
		}
	}
	if e := got[RoleEdit]; len(e) != 1 || e[0] != "/w/api/retry.cfg\nretry = 3" {
		t.Fatalf("edit spans = %q, want only the done Edit's old side", e)
	}
	if w := got[RoleWrote]; len(w) != 2 || !strings.HasPrefix(w[0], "/w/api/retry.cfg\n") || !strings.HasPrefix(w[1], "/w/api/backoff.txt\n") {
		t.Fatalf("wrote = %q, want the Edit's new side and the Write", w)
	}
	out := strings.Join(got[RoleToolOutput], "\n")
	if !strings.Contains(out, "retry = 3") || !strings.Contains(out, "You must use the Read tool first") {
		t.Fatalf("tool output = %q", out)
	}
}
