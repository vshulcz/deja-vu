package main

import (
	"encoding/json"
	"os"
	"strings"

	"github.com/vshulcz/deja-vu/internal/sources"
)

// runJunieHook adapts Junie's hook payload and fills in the two events it does
// not fire. Its cwd is Junie's home directory, not the project; project_path
// is the project, and becomes cwd for the command. Before the command runs, a
// compaction the session's events.jsonl records is captured, so the command's
// own recovery delivers the packet; and when the newest command in the log
// failed, the fix pair hook-tool-after would have given is put in front of the
// answer. Both arrive one hook late: at the next prompt or tool call.
func runJunieHook(dir, name string, rest []string, cmd command) error {
	rest, _ = withoutFlag(rest, "--junie")
	if stdinIsTerminal() {
		return runHookDeferred(dir, name, rest, cmd)
	}
	raw := readHookStdin()
	var m map[string]any
	if json.Unmarshal(raw, &m) != nil || m == nil {
		return withStdin(raw, func() error { return runHookDeferred(dir, name, rest, cmd) })
	}
	sid, _ := m["session_id"].(string)
	project, _ := m["project_path"].(string)
	if project == "" {
		project = sources.JunieProjectDir(sid)
	}
	if project != "" {
		m["cwd"] = project
	}
	if b, err := json.Marshal(m); err == nil {
		raw = b
	}
	extra := ""
	if name != "hook-context" && sid != "" && !recallIsOff() {
		junieCatchUpCompaction(dir, sid, project)
		extra = junieFailurePair(dir, sid, project)
	}
	var out []byte
	err := withStdin(raw, func() error {
		var cerr error
		out, cerr = captureDeferredStdout(func() error { return runHookDeferred(dir, name, rest, cmd) })
		return cerr
	})
	_, _ = os.Stdout.Write(withPlainExtra(out, extra))
	return err
}

func junieCatchUpCompaction(dir, sid, project string) {
	path := sources.JunieSessionEvents(sid)
	if path == "" {
		return
	}
	pre := precompactHookInput{SessionID: sid, TranscriptPath: path, CWD: project}
	catchUpCompactionWith(dir, pre, func() (sources.CompactionTranscript, bool, error) {
		return sources.ReadJunieCompaction(sid)
	})
}

// junieFailurePair is hook-tool-after's answer for the newest command in the
// session's log when it failed, once per command.
func junieFailurePair(dir, sid, project string) string {
	st := sources.JunieTurn(sid)
	if st.FailureID == "" || st.Failure == "" {
		return ""
	}
	key := "junie-failure:" + st.FailureID
	if alreadyInjected(dir, sid)[key] {
		return ""
	}
	rememberInjectedIDs(dir, sid, key)
	b, _ := json.Marshal(map[string]any{
		"hook_event_name": "PostToolUse", "session_id": sid, "cwd": project,
		"tool_name": "Bash", "tool_input": map[string]any{"command": st.FailureCommand},
		"tool_response": st.Failure,
	})
	var got []byte
	_ = withStdin(b, func() error {
		got, _ = captureDeferredStdout(func() error { return commands["hook-tool-after"](dir, []string{"--plain"}) })
		return nil
	})
	return hookOutputContext(got)
}

// withPlainExtra puts extra in front of a plain hook answer.
func withPlainExtra(out []byte, extra string) []byte {
	if extra == "" {
		return out
	}
	own := strings.TrimSpace(string(out))
	if own == "" {
		return []byte(extra + "\n")
	}
	return []byte(extra + "\n\n" + own + "\n")
}
