package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/vshulcz/deja-vu/internal/digest"
	"github.com/vshulcz/deja-vu/internal/policy"
	"github.com/vshulcz/deja-vu/internal/sources"
)

// What the sidecar does at each point, and how long it may take there. Every
// answer is deja's existing hook run as a child process with the payload that
// hook already reads, so the text the model gets is the text every other
// harness gets. A child that fails, prints nothing or runs past its budget
// leaves the turn exactly as the host built it.

var (
	// rxPromptBudget bounds the per-prompt recall and, on a session's first
	// turn, the session digest that runs beside it.
	rxPromptBudget = 4 * time.Second
	// rxToolBudget bounds the pre-tool line and the fix pair at a failure.
	rxToolBudget = 2 * time.Second
	// rxCompactBudget bounds the compaction note.
	rxCompactBudget = 10 * time.Second
	// rxSessionRaceWindow is how close behind a turn a session event may
	// arrive and still be the event for that turn. Events travel a queue of
	// their own, so the one announcing a session can land after its first
	// intercept; read as a new session it would send the digest twice.
	rxSessionRaceWindow = 2 * time.Second
)

// rxRunHook runs one deja hook subcommand. A variable so the protocol tests
// can stand a fake in for the child.
var rxRunHook = func(ctx context.Context, exe string, args []string, stdin []byte) (string, error) {
	cmd := exec.CommandContext(ctx, exe, args...)
	cmd.Stdin = bytes.NewReader(stdin)
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = io.Discard
	cmd.WaitDelay = 200 * time.Millisecond
	if err := cmd.Run(); err != nil {
		return "", err
	}
	return strings.TrimSpace(out.String()), nil
}

// rxExt is the sidecar's state: one per process, one session at a time.
type rxExt struct {
	dir  string
	exe  string
	conn *rxConn

	mu          sync.Mutex
	hostSession string
	workspace   string
	generation  uint64
	ui          bool
	started     time.Time
	sess        *rxSession
	lastStatus  string
	// retired holds the ids of sessions this process has seen end; the
	// store lookup never hands one of them to a later session.
	retired map[string]bool
}

// rxSession is what deja keeps about the Reasonix session being served.
type rxSession struct {
	boundary time.Time
	key      string
	realKey  bool
	turns    int
	lastTurn time.Time
	// digested is whether the session digest went out since the session
	// began or was last compacted.
	digested bool
	// announced is whether this session has had its "recalled" line.
	announced bool
}

func newRxExt(dir, exe string, conn *rxConn) *rxExt {
	now := time.Now()
	return &rxExt{dir: dir, exe: exe, conn: conn, started: now, retired: map[string]bool{},
		sess: &rxSession{boundary: now}}
}

func (x *rxExt) begin(hostSession, workspace string, generation uint64, ui bool) {
	x.mu.Lock()
	defer x.mu.Unlock()
	x.hostSession, x.workspace, x.generation, x.ui = hostSession, workspace, generation, ui
}

// observe handles the session events. A session that starts, loads or
// rotates is a new reader: its digest, its announcement and its dedupe key
// start over.
//
// Reasonix names the session in sessionPath where it has one: the new
// session's id at start (internal/control/session_binding.go), the loaded
// session at load, and the session that is ending at rotate. Events travel a
// queue of their own, so the one for a session can land after its first
// turn; an event with no name that arrives right behind a turn is read as
// that turn's.
func (x *rxExt) observe(p rxEventParams) {
	switch p.Event {
	case "session.start", "session.load", "session.rotate":
	default:
		return
	}
	var payload struct {
		SessionPath string `json:"sessionPath"`
	}
	_ = json.Unmarshal(p.Payload, &payload)
	id := reasonixSessionIDFrom(payload.SessionPath)
	x.mu.Lock()
	defer x.mu.Unlock()
	s := x.sess
	quiet := s.turns == 0 || (!s.lastTurn.IsZero() && time.Since(s.lastTurn) < rxSessionRaceWindow)
	if p.Event == "session.rotate" {
		// The id is the session ending. Serving another one already means
		// the rotate overtook nothing; serving that one means it is over.
		switch {
		case id != "" && s.realKey && s.key != id:
			x.retired[id] = true
			return
		case id != "" && s.realKey && s.key == id:
		case quiet:
			if id != "" {
				x.retired[id] = true
			}
			return
		}
		x.retire(s, id)
		return
	}
	if id != "" {
		if s.realKey && s.key == id {
			return
		}
		if quiet && !x.retired[id] {
			// The event for the session being served: name it.
			s.key, s.realKey = id, true
			return
		}
		x.retire(s, "")
		x.sess.key, x.sess.realKey = id, true
		return
	}
	if quiet {
		return
	}
	x.retire(s, "")
}

// retire ends the session being served and starts the next one. x.mu is held.
func (x *rxExt) retire(s *rxSession, ended string) {
	if s.realKey {
		x.retired[s.key] = true
	}
	if ended != "" {
		x.retired[ended] = true
	}
	x.sess = &rxSession{boundary: time.Now()}
}

// intercept rules on one point and never fails the host's operation: any
// error, timeout or panic below is a "continue".
func (x *rxExt) intercept(p rxInterceptParams) (res rxInterceptResult) {
	res = rxContinue()
	defer func() {
		if r := recover(); r != nil {
			fmt.Fprintf(os.Stderr, "deja reasonix-ext: %s: %v\n", p.Event, r)
			res = rxContinue()
		}
	}()
	ceiling := rxToolBudget
	switch p.Event {
	case "input.receive":
		ceiling = rxPromptBudget
	case "compaction.prepare":
		ceiling = rxCompactBudget
	case "tool.after":
	default:
		return res
	}
	ctx, cancel := context.WithTimeout(context.Background(), interceptBudget(p.TimeoutMillis, ceiling))
	defer cancel()
	payload, err := x.conn.resolvePayload(ctx, p.Payload, p.Externalized)
	if err != nil || len(bytes.TrimSpace(payload)) == 0 {
		return res
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(payload, &fields) != nil {
		return res
	}
	var replaced map[string]json.RawMessage
	switch p.Event {
	case "input.receive":
		replaced = x.onInput(ctx, fields)
	case "tool.after":
		replaced = x.onToolAfter(ctx, fields)
	case "compaction.prepare":
		replaced = x.onCompaction(ctx, fields)
	}
	if replaced == nil {
		return res
	}
	b, err := json.Marshal(replaced)
	if err != nil {
		return res
	}
	return rxInterceptResult{Decision: "replace", Replacement: b}
}

func rxString(fields map[string]json.RawMessage, key string) string {
	var s string
	_ = json.Unmarshal(fields[key], &s)
	return s
}

// withString is fields with one string replaced, every other field as the
// host sent it: the host decodes a replacement strictly, and a field deja
// does not know about is still one it must hand back.
func withString(fields map[string]json.RawMessage, key, value string) map[string]json.RawMessage {
	out := make(map[string]json.RawMessage, len(fields))
	for k, v := range fields {
		out[k] = v
	}
	b, _ := json.Marshal(value)
	out[key] = b
	return out
}

func (x *rxExt) hook(ctx context.Context, payload any, args ...string) string {
	b, err := json.Marshal(payload)
	if err != nil {
		return ""
	}
	out, err := rxRunHook(ctx, x.exe, args, b)
	if err != nil || ctx.Err() != nil {
		return ""
	}
	return out
}

// rxInjectionCap mirrors the host's own cap on one hook's context
// (maxHookContextChars in internal/control/input.go). deja's blocks are
// budgeted well under it; this only stops a surprise from reaching the model.
const rxInjectionCap = 10000

// onInput is the turn tail: the session digest on a session's first turn, and
// the per-prompt recall on every turn it has something to say.
func (x *rxExt) onInput(ctx context.Context, fields map[string]json.RawMessage) map[string]json.RawMessage {
	text := rxString(fields, "text")
	typed := rxTypedText(text)
	if typed == "" {
		return nil
	}
	key := x.sessionKey()
	x.mu.Lock()
	s, cwd := x.sess, x.workspace
	first := !s.digested
	s.digested = true
	s.turns++
	s.lastTurn = time.Now()
	x.mu.Unlock()

	var digestText string
	var wg sync.WaitGroup
	if first {
		wg.Add(1)
		go func() {
			defer wg.Done()
			digestText = x.hook(ctx, map[string]any{
				"session_id": key, "cwd": cwd, "source": "startup", "deja_once": true,
			}, "hook-context", "--plain")
		}()
	}
	recall := x.hook(ctx, map[string]any{"prompt": typed, "session_id": key, "cwd": cwd}, "hook-prompt", "--plain")
	wg.Wait()

	var parts []string
	for _, block := range []string{digestText, recall} {
		if block != "" && len(block) <= rxInjectionCap {
			parts = append(parts, block)
		}
	}
	x.surface(s, parts)
	if len(parts) == 0 {
		return nil
	}
	return withString(fields, "text", text+"\n\n"+strings.Join(parts, "\n\n"))
}

// rxHookToolInput turns a Reasonix tool call into the Claude-shaped fields
// deja's tool hooks read: the shell tool's command — bash, or pwsh and
// powershell on Windows (tool.IsShellToolName) — and a file tool's path as
// file_path, resolved against the workspace the way Reasonix resolves it;
// move_file's is the file it moved from.
func rxHookToolInput(name, arguments, workspace string) (string, map[string]string) {
	var args struct {
		Command    string `json:"command"`
		Path       string `json:"path"`
		SourcePath string `json:"source_path"`
	}
	_ = json.Unmarshal([]byte(arguments), &args)
	switch {
	case rxIsShellTool(name):
		if strings.TrimSpace(args.Command) == "" {
			return "", nil
		}
		return "Bash", map[string]string{"command": args.Command}
	case name == "read_file", name == "write_file", name == "edit_file", name == "multi_edit",
		// The file tools beside them: a notebook cell, a range or symbol
		// cut out, and a move, under the file it moved from (#4541).
		name == "notebook_edit", name == "delete_range", name == "delete_symbol", name == "move_file":
		p := strings.TrimSpace(args.Path)
		if name == "move_file" {
			p = strings.TrimSpace(args.SourcePath)
		}
		if p == "" {
			return "", nil
		}
		if !filepath.IsAbs(p) && workspace != "" {
			p = filepath.Join(workspace, p)
		}
		// read_file goes out as the lowercase `read` pi and omp send: this
		// hook holds a finished result, so on an edit the line arrives after
		// the write, and Reasonix refuses an edit on a file the session has
		// not read. The read is the last step where the line can still change
		// what gets written (#4410).
		tool := map[string]string{"read_file": "read", "write_file": "Write", "edit_file": "Edit", "multi_edit": "MultiEdit",
			"notebook_edit": "NotebookEdit", "delete_range": "Edit", "delete_symbol": "Edit", "move_file": "Edit"}[name]
		return tool, map[string]string{"file_path": p}
	}
	return "", nil
}

// rxIsShellTool mirrors Reasonix's tool.IsShellToolName.
func rxIsShellTool(name string) bool {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "bash", "pwsh", "powershell", "shell":
		return true
	}
	return false
}

// onToolAfter adds to the result the model is about to read: what this
// machine knows about the command or file — the line other harnesses get
// before the call — and at a failed command the fix that followed the same
// failure before. Both are asked for here rather than at tool.before: a
// tool.before answer can only let the call run, change it or stop it, and a
// line asked for there and then never shown, because the call was refused,
// would count as seen for the rest of the session. tool.after fires only for
// a call that ran.
func (x *rxExt) onToolAfter(ctx context.Context, fields map[string]json.RawMessage) map[string]json.RawMessage {
	name, arguments := rxString(fields, "name"), rxString(fields, "arguments")
	result := rxString(fields, "result")
	var isError bool
	_ = json.Unmarshal(fields["isError"], &isError)
	x.mu.Lock()
	cwd := x.workspace
	x.mu.Unlock()
	tool, input := rxHookToolInput(name, arguments, cwd)
	if tool == "" {
		return nil
	}
	key := x.sessionKey()
	var note, fix string
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		note = x.hook(ctx, map[string]any{
			"hook_event_name": "PreToolUse", "tool_name": tool, "tool_input": input,
			"session_id": key, "cwd": cwd,
		}, "hook-tool", "--plain")
	}()
	if isError && tool == "Bash" {
		fix = x.hook(ctx, map[string]any{
			"hook_event_name": "PostToolUseFailure", "tool_name": "Bash", "tool_input": input,
			"tool_response": result, "session_id": key, "cwd": cwd,
		}, "hook-tool-after", "--plain")
	}
	wg.Wait()
	var parts []string
	for _, block := range []string{note, fix} {
		if block != "" && len(block) <= rxInjectionCap {
			parts = append(parts, block)
		}
	}
	if len(parts) == 0 {
		return nil
	}
	if rxLooksLikeCILog(result) {
		// Reasonix cuts a log like this to its first and last eight lines
		// (internal/agent/ci_output.go) after tool.after has run. Appended,
		// deja's lines would take the place of the real tail; as one line
		// at the head they cost one line of it and arrive whole.
		lines := make([]string, len(parts))
		for i, p := range parts {
			lines[i] = rxOneLine(p)
		}
		return withString(fields, "result", strings.Join(lines, "\n")+"\n"+result)
	}
	return withString(fields, "result", strings.TrimRight(result, "\n")+"\n\n"+strings.Join(parts, "\n\n"))
}

// onCompaction does what deja's PreCompact hook does for other harnesses and
// carries what it knows into the summary. The ledger of blocks this session
// was shown is cleared, since the fold takes those blocks away, and the fold
// itself — the same message shape Reasonix writes to its JSONL store — is
// read into deja's compaction packet: the objective, what was concluded, and
// each command with how it went. The packet goes to the summarizer as
// guidance, so the summary keeps it. The next turn gets the digest again.
func (x *rxExt) onCompaction(ctx context.Context, fields map[string]json.RawMessage) map[string]json.RawMessage {
	key := x.sessionKey()
	x.mu.Lock()
	cwd := x.workspace
	x.sess.digested = false
	x.mu.Unlock()
	forgetInjected(x.dir, key)
	forgetInjected(x.dir, compactionFailureKey(key))
	if recallIsOff() || cwd == "" || !policy.Load().Allows(policy.ActivationAuto, cwd) {
		return nil
	}
	var messages []json.RawMessage
	if json.Unmarshal(fields["messages"], &messages) != nil || len(messages) == 0 {
		return nil
	}
	lines := make([][]byte, len(messages))
	for i, m := range messages {
		lines[i] = withoutOwnRecall(m)
	}
	session := sources.ParseReasonixMessages(lines, time.Now())
	if len(session.Messages) == 0 {
		return nil
	}
	data := digest.ExtractCompactionContext(session, digest.ExtractOptions{})
	withCommandOutcomes(&data, session)
	data.Carry = digest.ExtractCarry(session.Messages, nil)
	data.Freshness = compactionFreshness(compactionWorkspace(cwd))
	if ctx.Err() != nil {
		return nil
	}
	packet := digest.RenderCompactionContext(data, compactionRecoveryBytes-recallFrameOverhead-1)
	if strings.TrimSpace(packet) == "" {
		return nil
	}
	guidance := strings.TrimSpace(rxString(fields, "guidance"))
	note := "deja's record of the turns being folded follows. Keep in the summary what still holds — the objective, the conclusions, and each command with its outcome.\n" + frameRecall(packet)
	if guidance != "" {
		note = guidance + "\n\n" + note
	}
	return withString(fields, "guidance", note)
}
