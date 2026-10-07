package main

import (
	"bytes"
	"encoding/json"
	"os"
	"strings"

	"github.com/vshulcz/deja-vu/internal/sources"
)

// Crush fires one hook, PreToolUse (internal/hooks names one constant, v0.97),
// and puts its context in front of the model with the tool's result. So that
// one hook carries what other hosts get from three: the session's digest on
// its first tool call, the recall for the newest message the person sent, and
// the fix pair when the previous command failed. The message and the failure
// come out of crush.db by the session id the payload names. All of it arrives
// one tool call late, and a turn with no tool call gets none of it.
func runCrushTool(dir string, rest []string, cmd command) error {
	if stdinIsTerminal() {
		return cmd(dir, rest)
	}
	raw := readHookStdin()
	var p struct {
		SessionID string `json:"session_id"`
		CWD       string `json:"cwd"`
	}
	_ = json.Unmarshal(raw, &p)
	var out []byte
	err := withStdin(raw, func() error {
		var cerr error
		out, cerr = captureDeferredStdout(func() error { return cmd(dir, rest) })
		return cerr
	})
	extra := crushExtras(dir, raw, p.SessionID, hookCWD(p.CWD))
	_, _ = os.Stdout.Write(withCrushContext(out, extra))
	return err
}

func crushExtras(dir string, raw []byte, sid, cwd string) string {
	if sid == "" || recallIsOff() {
		return ""
	}
	run := func(name string, args []string, payload []byte) string {
		var got []byte
		_ = withStdin(payload, func() error {
			got, _ = captureDeferredStdout(func() error { return commands[name](dir, args) })
			return nil
		})
		return hookOutputContext(got)
	}
	var parts []string
	if p := crushCompactionPacket(dir, sid, cwd); p != "" {
		parts = append(parts, p)
	}
	if d := run("hook-context", []string{"--plain", "--once"}, raw); d != "" {
		parts = append(parts, d)
	}
	st := sources.CrushTurn(cwd, sid)
	asked := alreadyInjected(dir, sid)
	if st.FailureID != "" && !asked["crush-failure:"+st.FailureID] {
		rememberInjectedIDs(dir, sid, "crush-failure:"+st.FailureID)
		b, _ := json.Marshal(map[string]any{
			"hook_event_name": "PostToolUse", "session_id": sid, "cwd": cwd,
			"tool_name": "bash", "tool_response": st.Failure,
		})
		if pair := run("hook-tool-after", []string{"--plain"}, b); pair != "" {
			parts = append(parts, pair)
		}
	}
	if st.PromptID != "" && !asked["crush-prompt:"+st.PromptID] {
		rememberInjectedIDs(dir, sid, "crush-prompt:"+st.PromptID)
		b, _ := json.Marshal(map[string]any{
			"hook_event_name": "UserPromptSubmit", "session_id": sid, "cwd": cwd, "prompt": st.Prompt,
		})
		if r := run("hook-prompt", []string{"--plain"}, b); r != "" {
			parts = append(parts, r)
		}
	}
	return strings.Join(parts, "\n\n")
}

// crushCompactionPacket catches a summary up at the next tool call: Crush has
// no compaction event, and its store keeps the turns the summary replaced.
func crushCompactionPacket(dir, sid, cwd string) string {
	db := sources.CrushStoreFor(cwd)
	if db == "" {
		return ""
	}
	workspace := sources.CrushProjectDir(db)
	pre := precompactHookInput{SessionID: sid, TranscriptPath: db, CWD: workspace}
	catchUpCompactionWith(dir, pre, func() (sources.CompactionTranscript, bool, error) {
		return sources.ReadCrushCompaction(cwd, sid)
	})
	var out bytes.Buffer
	if delivered, _ := emitCompactionRecovery(dir, sid, workspace, "PreToolUse", hookToolPlain, &out); !delivered {
		return ""
	}
	return strings.TrimSpace(out.String())
}

// Command Code fires no per-prompt or compaction hook: SessionStart,
// PreToolUse, PostToolUse and Stop (1.77.0 buildHookPayload). A compaction is
// written into the transcript the PreToolUse payload names, so it is caught
// up there before hook-tool runs, and its packet is what this call answers
// with. The question is not read from there: a turn reaches the file only
// once it commits, after its tool calls (1.77.0 stand), so the mod answers it
// (commandCodeModTS).
func commandCodeStoreHere() bool {
	fi, err := os.Stat(sources.CommandCodeRoot())
	return err == nil && fi.IsDir()
}

func runCommandCodeTool(dir string, rest []string, cmd command) error {
	if stdinIsTerminal() {
		return cmd(dir, rest)
	}
	raw := readHookStdin()
	var p struct {
		SessionID      string `json:"session_id"`
		CWD            string `json:"cwd"`
		TranscriptPath string `json:"transcript_path"`
	}
	_ = json.Unmarshal(raw, &p)
	if p.SessionID != "" && sources.CommandCodeUnderRoot(p.TranscriptPath) {
		pre := precompactHookInput{SessionID: p.SessionID, TranscriptPath: p.TranscriptPath, CWD: p.CWD}
		catchUpCompactionWith(dir, pre, func() (sources.CompactionTranscript, bool, error) {
			return sources.ReadCommandCodeCompaction(p.TranscriptPath, p.SessionID)
		})
	}
	return withStdin(raw, func() error { return cmd(dir, rest) })
}

// withCrushContext puts extra in front of the context of Crush's own answer,
// {"version":1,"context":…}.
func withCrushContext(out []byte, extra string) []byte {
	if extra == "" {
		return out
	}
	t := bytes.TrimSpace(out)
	m := map[string]any{}
	if len(t) > 0 && json.Unmarshal(t, &m) != nil {
		return out
	}
	if _, ok := m["version"]; !ok {
		m["version"] = 1
	}
	if own, _ := m["context"].(string); strings.TrimSpace(own) != "" {
		m["context"] = extra + "\n\n" + own
	} else {
		m["context"] = extra
	}
	b, err := json.Marshal(m)
	if err != nil {
		return out
	}
	return append(b, '\n')
}
