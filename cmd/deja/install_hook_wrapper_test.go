package main

import (
	"strings"
	"testing"
)

func sessionStartCommands(t *testing.T, root map[string]any) []string {
	t.Helper()
	hooks, _ := root["hooks"].(map[string]any)
	entries, _ := hooks["SessionStart"].([]any)
	var out []string
	for _, e := range entries {
		entry, _ := e.(map[string]any)
		hs, _ := entry["hooks"].([]any)
		for _, h := range hs {
			cmd, _ := h.(map[string]any)["command"].(string)
			out = append(out, cmd)
		}
	}
	return out
}

func hookRootWith(cmd string) map[string]any {
	return map[string]any{"hooks": map[string]any{"SessionStart": []any{
		map[string]any{"hooks": []any{map[string]any{"type": "command", "command": cmd}}},
	}}}
}

// The reader's own line that runs deja's hook is deja's hook: adding a second
// entry runs it twice at every session start, and rewriting the line throws
// away whatever else it did. Somebody else's tool that merely lives under a
// path containing the word is not deja's (#2477).
func TestAHookWrapperIsNeitherDuplicatedNorRewritten(t *testing.T) {
	const cmd = "/new/path/deja hook-context"
	for _, tc := range []struct {
		name    string
		was     string
		want    []string
		wantSub string
	}{
		{
			name: "a wrapper around deja's own hook",
			was:  `sh -c 'caffeinate -i /usr/local/bin/deja hook-context'`,
			want: []string{`sh -c 'caffeinate -i /usr/local/bin/deja hook-context'`},
		},
		{
			name: "a wrapper that runs something first",
			was:  "/usr/local/bin/mylog && /usr/local/bin/deja hook-context",
			want: []string{"/usr/local/bin/mylog && /usr/local/bin/deja hook-context"},
		},
		{
			name: "somebody else's tool under a path with the word in it",
			was:  "/home/deja/bin/mytool hook-context",
			want: []string{"/home/deja/bin/mytool hook-context", cmd},
		},
		{
			name: "deja's own hook at an older path",
			was:  "/old/path/deja hook-context",
			want: []string{cmd},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := sessionStartCommands(t, updateClaudeHook(hookRootWith(tc.was), "SessionStart", cmd, "", false))
			if strings.Join(got, "\n") != strings.Join(tc.want, "\n") {
				t.Errorf("install left\n  %s\nwant\n  %s", strings.Join(got, "\n  "), strings.Join(tc.want, "\n  "))
			}
		})
	}
}

// Uninstall takes deja's own line out and leaves the reader's alone: a wrapper
// is their command, and somebody else's tool was never deja's to remove.
func TestUninstallLeavesLinesThatAreNotDejasOwn(t *testing.T) {
	const cmd = "/new/path/deja hook-context"
	for _, tc := range []struct {
		was  string
		want []string
	}{
		{was: "/old/path/deja hook-context", want: nil},
		{was: "/home/deja/bin/mytool hook-context", want: []string{"/home/deja/bin/mytool hook-context"}},
		{was: "/usr/local/bin/mylog && /usr/local/bin/deja hook-context", want: []string{"/usr/local/bin/mylog && /usr/local/bin/deja hook-context"}},
	} {
		got := sessionStartCommands(t, updateClaudeHook(hookRootWith(tc.was), "SessionStart", cmd, "", true))
		if strings.Join(got, "\n") != strings.Join(tc.want, "\n") {
			t.Errorf("uninstall of %q left %q, want %q", tc.was, got, tc.want)
		}
	}
}
