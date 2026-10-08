package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/vshulcz/deja-vu/internal/policy"
	"github.com/vshulcz/deja-vu/internal/sources"
	"github.com/vshulcz/deja-vu/internal/usage"
)

// CodeWhale's hooks run only in its TUI, and only two events can put text in
// front of the model (docs/HOOKS.md, checked on a 0.10.0 stand):
//
//   - message_submit gets {text, session_id, workspace} on stdin and may
//     answer {"text": …}, which replaces the message before it reaches history
//     or the model. session_start's stdout is discarded, so the digest rides
//     the first message of a session, and the prompt's recall every message.
//   - tool_call_before gets the tool in the environment (DEEPSEEK_TOOL_NAME,
//     DEEPSEEK_TOOL_ARGS) and may answer {"additionalContext": …}, which is
//     appended to the tool's result. No decision is sent: allow and none are
//     the same to CodeWhale, and deja has no business approving a tool.
//
// tool_call_after, turn_end and session_start stdout never reach the model, so
// what deja has to say after a tool waits for the next tool_call_before or
// message_submit (hook_deferred.go): the fix pair for a failed command, and the
// tool_call_before line of a call that failed to run, whose result goes out
// without it (turn_loop.rs, 0.10.1).
//
// The replaced message is what CodeWhale saves, so everything deja adds is
// framed in <deja-recall>, which the index strips before it reads the turn.
const (
	codewhaleTextMax    = 32000
	codewhaleContextMax = 2000
)

type codewhaleSubmit struct {
	Text      string `json:"text"`
	SessionID string `json:"session_id"`
	Workspace string `json:"workspace"`
}

func runHookCodeWhale(dir string, args []string, stdin io.Reader, stdout io.Writer) error {
	event := ""
	if len(args) > 0 {
		event = args[0]
	}
	switch event {
	case "message_submit":
		return codewhaleMessageSubmit(dir, stdin, stdout)
	case "tool_call_before":
		return codewhaleToolCallBefore(dir, stdout)
	case "tool_call_after":
		return codewhaleToolCallAfter(dir, stdin)
	case "session_end":
		sid := os.Getenv("DEEPSEEK_SESSION_ID")
		if sid == "" {
			var in codewhaleSubmit
			_ = json.Unmarshal(readHookPayload(stdin, hookStdinWait), &in)
			sid = in.SessionID
		}
		endSessionLive(dir, sid)
		return nil
	}
	return fmt.Errorf("hook-codewhale: unknown event %q — want message_submit, tool_call_before, tool_call_after or session_end", event)
}

func codewhaleMessageSubmit(dir string, stdin io.Reader, stdout io.Writer) error {
	var in codewhaleSubmit
	if json.Unmarshal(readHookPayload(stdin, hookStdinWait), &in) != nil || strings.TrimSpace(in.Text) == "" {
		return nil
	}
	if in.Workspace == "" {
		in.Workspace = os.Getenv("DEEPSEEK_WORKSPACE")
	}
	if recallIsOff() {
		return nil
	}
	markSessionLive(dir, in.SessionID)
	var parts []string
	if pending := takeDeferred(dir, deferredKey(in.SessionID, in.Workspace)); pending != "" {
		parts = append(parts, pending)
	}
	if packet := codewhaleCompactionPacket(dir, in.SessionID, in.Workspace); packet != "" {
		parts = append(parts, packet)
	}
	if !codewhaleDigestSent(dir, in.SessionID) {
		digest, sessions, raw, _, _, ids, projects := cachedHookDigestFor(dir, in.Workspace, "")
		if digest != "" {
			digest = frameRecall(startLead(sessionStartLead) + digest)
			usage.RecordDigestPolicySessionsFrom(dir, usage.KindHook, digest, "", sessions, raw,
				policy.Load().Describe(policy.ActivationAuto), ids, projects)
			parts = append(parts, digest)
		}
		// Asked once whatever the answer: an empty project has nothing to say
		// on the second message either.
		codewhaleRememberDigest(dir, in.SessionID)
	}
	if block := antigravityPromptBlock(dir, in.Text, in.SessionID, in.Workspace); block != "" {
		parts = append(parts, block)
	}
	if len(parts) == 0 {
		return nil
	}
	extra := strings.Join(parts, "\n\n")
	// A reply over the limit is invalid and CodeWhale keeps the message as it
	// was, so what deja adds is cut to fit rather than costing the person
	// their own text.
	if room := codewhaleTextMax - len(in.Text) - 2; len(extra) > room {
		if room <= 0 {
			return nil
		}
		extra = truncateToolLine(extra, room)
	}
	b, err := json.Marshal(map[string]string{"text": in.Text + "\n\n" + extra})
	if err != nil {
		return nil
	}
	fmt.Fprintln(stdout, string(b))
	return nil
}

func codewhaleDigestSent(dir, sessionID string) bool {
	if strings.TrimSpace(sessionID) == "" {
		return false
	}
	return alreadyInjected(dir, "cw:"+sessionID)[codewhaleDigestMarker]
}

func codewhaleRememberDigest(dir, sessionID string) {
	if strings.TrimSpace(sessionID) == "" {
		return
	}
	rememberInjectedIDs(dir, "cw:"+sessionID, codewhaleDigestMarker)
}

const codewhaleDigestMarker = "cw-digest"

// codewhaleCatchUp captures a compaction CodeWhale saved while this session
// ran: it has no compaction event, and keeps the history it compacted as an
// artifact of the saved session (sources.ReadCodeWhaleCompaction). The hooks'
// session id is not the saved one, so a compaction counts only from the first
// time this hook session was seen; one from before it is another run's.
func codewhaleCatchUp(dir, sessionID, workspace string) {
	if sessionID == "" || workspace == "" {
		return
	}
	since := codewhaleFirstSeen(dir, sessionID)
	pre := precompactHookInput{SessionID: sessionID, TranscriptPath: sources.CodeWhaleRoot(), CWD: workspace}
	catchUpCompactionWith(dir, pre, func() (sources.CompactionTranscript, bool, error) {
		return sources.ReadCodeWhaleCompaction(workspace, since)
	})
}

// codewhaleFirstSeen is when a hook first saw this session, written down then.
func codewhaleFirstSeen(dir, sessionID string) time.Time {
	key := "cw:" + sessionID
	for token := range alreadyInjected(dir, key) {
		if v, ok := strings.CutPrefix(token, "cw-first:"); ok {
			if n, err := strconv.ParseInt(v, 10, 64); err == nil {
				return time.Unix(n, 0)
			}
		}
	}
	// A second back: the summary's mtime and this stamp share a clock, and
	// the file system may round it.
	now := time.Now().Add(-time.Second)
	rememberInjectedIDs(dir, key, "cw-first:"+strconv.FormatInt(now.Unix(), 10))
	return now
}

// codewhaleCompactionPacket is the packet of a compaction not yet handed
// over, for the message that goes out next.
func codewhaleCompactionPacket(dir, sessionID, workspace string) string {
	codewhaleCatchUp(dir, sessionID, workspace)
	var out bytes.Buffer
	if delivered, _ := emitCompactionRecovery(dir, sessionID, workspace, "UserPromptSubmit", hookToolPlain, &out); !delivered {
		return ""
	}
	return strings.TrimSpace(out.String())
}

// codewhaleToolCallBefore turns CodeWhale's environment into the payload
// hook-tool reads. Its tool names are already ones hook-tool knows — bash,
// edit, write, read — with the file under `path` and the command under
// `command`, and apply_patch's patch is moved to where hook-tool looks for one.
func codewhaleToolCallBefore(dir string, stdout io.Writer) error {
	name := os.Getenv("DEEPSEEK_TOOL_NAME")
	if name == "" || recallIsOff() {
		return nil
	}
	sid, workspace := os.Getenv("DEEPSEEK_SESSION_ID"), os.Getenv("DEEPSEEK_WORKSPACE")
	input := map[string]any{}
	if raw := os.Getenv("DEEPSEEK_TOOL_ARGS"); raw != "" {
		_ = json.Unmarshal([]byte(raw), &input)
	}
	if patch, ok := input["patch"].(string); ok && name == "apply_patch" {
		input["command"] = patch
	}
	// hook-tool hands over the packet of a compaction caught up here.
	codewhaleCatchUp(dir, sid, workspace)
	payload, err := json.Marshal(map[string]any{
		"tool_name":  name,
		"tool_input": input,
		"session_id": sid,
		"cwd":        workspace,
	})
	if err != nil {
		return nil
	}
	var out bytes.Buffer
	if err := runHookToolMode(dir, bytes.NewReader(payload), &out, hookToolPlain); err != nil {
		return nil
	}
	ctx := strings.TrimSpace(out.String())
	// What waits from an earlier call goes first when both fit under the cap;
	// otherwise it waits for the next message, which takes far more.
	key := deferredKey(sid, workspace)
	if pending := takeDeferred(dir, key); pending != "" {
		switch {
		case ctx == "" && len(pending) <= codewhaleContextMax:
			ctx = pending
		case len(pending)+2+len(ctx) <= codewhaleContextMax:
			ctx = pending + "\n\n" + ctx
		default:
			deferText(dir, key, "hook-tool-after", pending)
		}
	}
	if ctx == "" {
		return nil
	}
	// Kept until tool_call_after says the call ran: a call that fails to run
	// goes back to the model without this context.
	if id := os.Getenv("DEEPSEEK_TOOL_CALL_ID"); id != "" {
		deferText(dir, codewhaleCallKey(id), "hook-tool", ctx)
	}
	b, err := json.Marshal(map[string]string{"additionalContext": truncateToolLine(ctx, codewhaleContextMax)})
	if err != nil {
		return nil
	}
	fmt.Fprintln(stdout, string(b))
	return nil
}

const codewhaleExecFailed = "Failed to execute tool: "

// codewhaleCallKey is where a tool_call_before answer waits for its call's
// tool_call_after.
func codewhaleCallKey(callID string) string { return deferredKey("cw-call:"+callID, "") }

// codewhaleCallErred reports whether a failed call's result is a ToolError
// rather than the tool's own failing output. Those are the results CodeWhale
// sends without the tool_call_before context (turn_loop.rs, 0.10.1), and each
// of its messages starts with one of these (crates/tools/src/lib.rs).
func codewhaleCallErred(result string) bool {
	return strings.HasPrefix(result, "Failed to ") || strings.HasPrefix(result, "Tool execution cancelled")
}

// codewhaleToolCallAfter runs once a tool call settles, and CodeWhale discards
// what it prints. So it parks text for the session's next tool_call_before or
// message_submit: the tool_call_before line of a call that failed to run, and
// the fix pair for a shell command that failed. A native shell call's receipt
// comes on stdin (docs/HOOKS.md, 0.10.1); other calls get no stdin document
// and their result in the environment.
func codewhaleToolCallAfter(dir string, stdin io.Reader) error {
	sid, workspace := os.Getenv("DEEPSEEK_SESSION_ID"), os.Getenv("DEEPSEEK_WORKSPACE")
	key := deferredKey(sid, workspace)
	line := ""
	if id := os.Getenv("DEEPSEEK_TOOL_CALL_ID"); id != "" {
		line = takeDeferred(dir, codewhaleCallKey(id))
	}
	if os.Getenv("DEEPSEEK_TOOL_SUCCESS") != "false" || recallIsOff() {
		return nil
	}
	result := os.Getenv("DEEPSEEK_TOOL_RESULT")
	if line != "" && codewhaleCallErred(result) {
		deferText(dir, key, "hook-tool", line)
	}
	switch os.Getenv("DEEPSEEK_TOOL_NAME") {
	case "exec_shell", "bash", "Bash", "task_shell_start":
	default:
		return nil
	}
	var doc struct {
		Receipt struct {
			Command string `json:"command"`
			Stdout  string `json:"stdout"`
			Stderr  string `json:"stderr"`
		} `json:"execution_receipt"`
	}
	_ = json.Unmarshal(readHookPayload(stdin, hookStdinWait), &doc)
	output := strings.TrimSpace(doc.Receipt.Stderr + "\n" + doc.Receipt.Stdout)
	if output == "" {
		// The TUI's bash tool sends no receipt, and a command that exits
		// non-zero comes back as a ToolError led by this prefix (0.10.1
		// stand). Left on, the error line no longer matches the one indexed.
		output = strings.TrimPrefix(result, codewhaleExecFailed)
	}
	payload, err := json.Marshal(map[string]any{
		"hook_event_name": "PostToolUse",
		"tool_name":       "Bash",
		"tool_input":      map[string]string{"command": doc.Receipt.Command},
		"tool_response":   output,
		"session_id":      sid,
		"cwd":             workspace,
	})
	if err != nil {
		return nil
	}
	var out bytes.Buffer
	if err := runHookToolAfterMode(dir, bytes.NewReader(payload), &out, true); err != nil {
		return nil
	}
	deferText(dir, key, "hook-tool-after", out.String())
	return nil
}
