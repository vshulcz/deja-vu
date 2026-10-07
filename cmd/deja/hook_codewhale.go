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
// tool_call_after, turn_end and session_start stdout never reach the model.
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
	return fmt.Errorf("hook-codewhale: unknown event %q — want message_submit, tool_call_before or session_end", event)
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
	if name == "" {
		return nil
	}
	input := map[string]any{}
	if raw := os.Getenv("DEEPSEEK_TOOL_ARGS"); raw != "" {
		_ = json.Unmarshal([]byte(raw), &input)
	}
	if patch, ok := input["patch"].(string); ok && name == "apply_patch" {
		input["command"] = patch
	}
	// hook-tool hands over the packet of a compaction caught up here.
	codewhaleCatchUp(dir, os.Getenv("DEEPSEEK_SESSION_ID"), os.Getenv("DEEPSEEK_WORKSPACE"))
	payload, err := json.Marshal(map[string]any{
		"tool_name":  name,
		"tool_input": input,
		"session_id": os.Getenv("DEEPSEEK_SESSION_ID"),
		"cwd":        os.Getenv("DEEPSEEK_WORKSPACE"),
	})
	if err != nil {
		return nil
	}
	var out bytes.Buffer
	if err := runHookToolMode(dir, bytes.NewReader(payload), &out, hookToolPlain); err != nil {
		return nil
	}
	ctx := strings.TrimSpace(out.String())
	if ctx == "" {
		return nil
	}
	b, err := json.Marshal(map[string]string{"additionalContext": truncateToolLine(ctx, codewhaleContextMax)})
	if err != nil {
		return nil
	}
	fmt.Fprintln(stdout, string(b))
	return nil
}
