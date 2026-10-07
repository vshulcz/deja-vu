package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync/atomic"
	"time"

	"github.com/vshulcz/deja-vu/internal/sources"
	"github.com/vshulcz/deja-vu/internal/usage"
)

// Deferred delivery is for a host whose event fires but drops what the hook
// prints, while a later event on the same session does reach the model:
//
//   - Grok Build drops SessionStart and UserPromptSubmit output, and delivers
//     PreToolUse and PostToolUse context with the tool's result (hook docs,
//     1.0.41).
//   - Kimi Code runs PostToolUse fire-and-forget, so the fix pair for a failed
//     command never arrives; UserPromptSubmit output does.
//   - Kiro shows nothing from preToolUse or postToolUse (2.28.0, measured), and
//     shows agentSpawn and userPromptSubmit output.
//
// The hook that cannot speak runs as usual with its output kept in a file
// under the session; the next hook of that session that can speak puts it in
// front of its own answer. The file is taken by renaming it away first, so two
// hooks racing for it deliver it once.
//
// Install marks the silent event with --defer. Grok needs no flag: it says
// which event a hook runs for (grokDropsContext).

const (
	// Under the 10,000 characters Grok clips a hook's context to.
	deferredMax = 9000
	// A digest or a recall that waited longer than this answers a moment that
	// has passed.
	deferredTTL = 2 * time.Hour
)

var deferredSeq atomic.Uint64

func deferredRoot(dir string) string { return filepath.Join(dir, "deferred") }

// deferredKey is the session the text waits for. Kiro's agent hooks may name
// no session, and then the working directory stands in for it.
func deferredKey(sid, cwd string) string {
	if sid != "" {
		return shortHash("sid:" + sid)
	}
	if cwd != "" {
		return shortHash("cwd:" + cwd)
	}
	return ""
}

// deferredKind orders what a session is handed and says which kinds replace
// an earlier one: the digest and the answer to the newest prompt replace what
// came before; failure pairs and edit lines add up.
func deferredKind(hook string) (order string, replace bool) {
	switch hook {
	case "hook-context":
		return "0start", true
	case "hook-prompt":
		return "1prompt", true
	default:
		return "2tool", false
	}
}

func deferText(dir, key, hook, text string) {
	text = strings.TrimSpace(text)
	if key == "" || text == "" {
		return
	}
	d := filepath.Join(deferredRoot(dir), key)
	if err := os.MkdirAll(d, 0o700); err != nil {
		return
	}
	order, replace := deferredKind(hook)
	if replace {
		if old, err := filepath.Glob(filepath.Join(d, order+"-*")); err == nil {
			for _, p := range old {
				_ = os.Remove(p)
			}
		}
	}
	// Windows' clock can hand two calls in a row the same nanosecond, and the
	// rename would put the second pair over the first: the sequence and the pid
	// keep the names apart and the order intact.
	name := fmt.Sprintf("%s-%020d-%010d-%d", order, time.Now().UnixNano(), deferredSeq.Add(1), os.Getpid())
	tmp := filepath.Join(d, "."+name)
	if err := os.WriteFile(tmp, []byte(text), 0o600); err != nil {
		return
	}
	_ = os.Rename(tmp, filepath.Join(d, name))
}

// hasDeferred is the cheap check every hook pays: one stat of a directory that
// exists only on a machine where a host defers.
func hasDeferred(dir string) bool {
	_, err := os.Stat(deferredRoot(dir))
	return err == nil
}

func hasDeferredFor(dir, key string) bool {
	if key == "" {
		return false
	}
	_, err := os.Stat(filepath.Join(deferredRoot(dir), key))
	return err == nil
}

// takeDeferred hands over what waits for this session, once.
func takeDeferred(dir, key string) string {
	if key == "" {
		return ""
	}
	src := filepath.Join(deferredRoot(dir), key)
	taken := fmt.Sprintf("%s.taken-%d-%d", src, os.Getpid(), time.Now().UnixNano())
	if err := os.Rename(src, taken); err != nil {
		return ""
	}
	defer os.RemoveAll(taken)
	entries, err := os.ReadDir(taken)
	if err != nil {
		return ""
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if !strings.HasPrefix(e.Name(), ".") {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	var parts []string
	seen := map[string]bool{}
	size := 0
	for _, n := range names {
		p := filepath.Join(taken, n)
		if fi, err := os.Stat(p); err != nil || time.Since(fi.ModTime()) > deferredTTL {
			continue
		}
		b, err := os.ReadFile(p)
		t := strings.TrimSpace(string(b))
		if err != nil || t == "" || seen[t] || size+len(t) > deferredMax {
			continue
		}
		seen[t] = true
		size += len(t) + 2
		parts = append(parts, t)
	}
	return strings.Join(parts, "\n\n")
}

// deferredPayload is what a deferring or delivering hook needs from the
// payload: whose session it is and which event the host named.
type deferredPayload struct {
	SessionID      string `json:"session_id"`
	SessionIDCamel string `json:"sessionId"`
	ConversationID string `json:"conversation_id"`
	CWD            string `json:"cwd"`
	Event          string `json:"hook_event_name"`
	EventCamel     string `json:"hookEventName"`
}

func readDeferredPayload(raw []byte) (key, event string) {
	var p deferredPayload
	_ = json.Unmarshal(raw, &p)
	sid := adoptGrok(adoptGrok(p.SessionID, p.SessionIDCamel), p.ConversationID)
	return deferredKey(sid, p.CWD), adoptGrok(p.Event, p.EventCamel)
}

// hookOutputContext is the part of a hook's answer meant for the model.
func hookOutputContext(out []byte) string {
	t := bytes.TrimSpace(out)
	if len(t) == 0 {
		return ""
	}
	if t[0] == '{' {
		var v struct {
			HookSpecificOutput struct {
				AdditionalContext string `json:"additionalContext"`
			} `json:"hookSpecificOutput"`
			AdditionalContext string `json:"additionalContext"`
		}
		if json.Unmarshal(t, &v) == nil {
			return adoptGrok(v.HookSpecificOutput.AdditionalContext, v.AdditionalContext)
		}
	}
	return string(t)
}

// hookOutputNotes keeps the line a deferring host does show the person.
func hookOutputNotes(out []byte) []byte {
	var v struct {
		SystemMessage string `json:"systemMessage"`
	}
	if json.Unmarshal(bytes.TrimSpace(out), &v) != nil || v.SystemMessage == "" {
		return nil
	}
	b, _ := json.Marshal(v)
	return append(b, '\n')
}

// defaultHookEvent is the event a hook command answers when the payload does
// not name it.
func defaultHookEvent(hook string) string {
	switch hook {
	case "hook-context":
		return "SessionStart"
	case "hook-prompt":
		return "UserPromptSubmit"
	case "hook-tool":
		return "PreToolUse"
	case "hook-tool-after":
		return "PostToolUse"
	}
	return ""
}

// claudeEventName is the reply's event name in the spelling the reply takes:
// Kiro's agent hooks and Grok's environment use their own.
func claudeEventName(event, hook string) string {
	switch strings.ToLower(strings.ReplaceAll(event, "_", "")) {
	case "sessionstart", "agentspawn":
		return "SessionStart"
	case "userpromptsubmit":
		return "UserPromptSubmit"
	case "pretooluse":
		return "PreToolUse"
	case "posttooluse":
		return "PostToolUse"
	case "posttoolusefailure":
		return "PostToolUseFailure"
	}
	return defaultHookEvent(hook)
}

// withDeferred puts pending text in front of the hook's own answer, in the
// shape the answer already has. A hook that answered nothing answers in plain
// text when the host reads plain text, and in Claude's envelope otherwise.
func withDeferred(out []byte, pending string, plain bool, event string) []byte {
	if pending == "" {
		return out
	}
	t := bytes.TrimSpace(out)
	join := func(own string) string {
		if strings.TrimSpace(own) == "" {
			return pending
		}
		return pending + "\n\n" + own
	}
	if len(t) > 0 && t[0] == '{' {
		var m map[string]any
		if json.Unmarshal(t, &m) == nil {
			if hso, ok := m["hookSpecificOutput"].(map[string]any); ok {
				own, _ := hso["additionalContext"].(string)
				hso["additionalContext"] = join(own)
				if _, named := hso["hookEventName"]; !named && event != "" {
					hso["hookEventName"] = event
				}
			} else if own, ok := m["additionalContext"].(string); ok {
				m["additionalContext"] = join(own)
			} else if copilotHookOutput {
				m["additionalContext"] = pending
			} else {
				m["hookSpecificOutput"] = map[string]any{"hookEventName": event, "additionalContext": pending}
			}
			if b, err := json.Marshal(m); err == nil {
				return append(b, '\n')
			}
		}
	}
	if plain || len(t) > 0 {
		return []byte(join(string(t)) + "\n")
	}
	if copilotHookOutput {
		b, _ := json.Marshal(map[string]string{"additionalContext": pending})
		return append(b, '\n')
	}
	b, _ := json.Marshal(map[string]any{"hookSpecificOutput": map[string]any{"hookEventName": event, "additionalContext": pending}})
	return append(b, '\n')
}

// deferrableHooks are the commands that can either defer or deliver.
var deferrableHooks = map[string]bool{
	"hook-context": true, "hook-prompt": true, "hook-tool": true, "hook-tool-after": true,
}

// runHookDeferred runs a hook command with deferred delivery around it. The
// payload is read once and handed back to the command, and its answer is
// caught in a file rather than a pipe, so a child the hook detaches (the
// background index build) cannot hold the read open.
func runHookDeferred(dir, name string, rest []string, cmd command) error {
	if name == "hook-tool" && hasFlag(rest, "--crush") {
		return runCrushTool(dir, rest, cmd)
	}
	if name == "hook-tool" && len(rest) == 0 && commandCodeStoreHere() {
		return runCommandCodeTool(dir, rest, cmd)
	}
	rest, deferIt := withoutFlag(rest, "--defer")
	if !deferIt && (name == "hook-context" || name == "hook-prompt") && grokDropsContext() {
		deferIt = true
	}
	if !deferIt && !hasDeferred(dir) {
		return cmd(dir, rest)
	}
	if stdinIsTerminal() {
		return cmd(dir, rest)
	}
	raw := readHookStdin()
	key, event := readDeferredPayload(raw)
	if !deferIt && !hasDeferredFor(dir, key) {
		return withStdin(raw, func() error { return cmd(dir, rest) })
	}
	var out []byte
	err := withStdin(raw, func() error {
		var cerr error
		out, cerr = captureDeferredStdout(func() error { return cmd(dir, rest) })
		return cerr
	})
	plain := hasFlag(rest, "--plain")
	if deferIt {
		deferText(dir, key, name, hookOutputContext(out))
		// What the host shows the person still goes out; the context does not,
		// so it is not counted twice when it is handed over.
		if notes := hookOutputNotes(out); notes != nil {
			_, _ = os.Stdout.Write(notes)
		}
		return err
	}
	_, _ = os.Stdout.Write(withDeferred(out, takeDeferred(dir, key), plain, claudeEventName(event, name)))
	return err
}

func withoutFlag(args []string, flag string) ([]string, bool) {
	out := make([]string, 0, len(args))
	found := false
	for _, a := range args {
		if a == flag || a == strings.TrimPrefix(flag, "-") {
			found = true
			continue
		}
		out = append(out, a)
	}
	return out, found
}

func hasFlag(args []string, flag string) bool {
	for _, a := range args {
		if a == flag || a == strings.TrimPrefix(flag, "-") {
			return true
		}
	}
	return false
}

// withStdin runs fn with os.Stdin reading raw.
func withStdin(raw []byte, fn func() error) error {
	f, err := os.CreateTemp("", "deja-hook-in-*")
	if err != nil {
		return fn()
	}
	defer func() { _ = os.Remove(f.Name()) }()
	defer f.Close()
	if _, err := f.Write(raw); err != nil {
		return fn()
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return fn()
	}
	old := os.Stdin
	os.Stdin = f
	defer func() { os.Stdin = old }()
	return fn()
}

// captureDeferredStdout runs fn with os.Stdout going to a file and returns what it
// wrote.
func captureDeferredStdout(fn func() error) ([]byte, error) {
	f, err := os.CreateTemp("", "deja-hook-out-*")
	if err != nil {
		return nil, fn()
	}
	defer func() { _ = os.Remove(f.Name()) }()
	defer f.Close()
	old := os.Stdout
	os.Stdout = f
	ferr := fn()
	os.Stdout = old
	b, _ := os.ReadFile(f.Name())
	return b, ferr
}

// runHookStop hands what waits for this session to the model through a Stop
// hook that blocks once. goose drops everything its hooks print, and a Stop
// block's reason is the one answer it puts in front of the model, as a user
// message in the same turn (goose 1.46 and 1.53, agent.rs:149-160). It blocks
// only when something waits, and takeDeferred hands each thing over once, so
// the turn ends on the next Stop.
func runHookStop(dir string, stdin io.Reader, stdout io.Writer) error {
	if recallIsOff() {
		return nil
	}
	raw := readHookPayload(stdin, hookStdinWait)
	key, _ := readDeferredPayload(raw)
	pending := takeDeferred(dir, key)
	if pending == "" {
		turn := gooseStopTurn(raw)
		var parts []string
		for _, p := range []string{gooseStopCompaction(dir, raw), gooseStopFixPair(dir, raw, turn), gooseStopEditLines(dir, raw, turn)} {
			if p != "" {
				parts = append(parts, p)
			}
		}
		pending = strings.Join(parts, "\n\n")
	}
	if pending == "" {
		return nil
	}
	b, err := json.Marshal(map[string]string{"decision": "block", "reason": pending})
	if err != nil {
		return nil
	}
	fmt.Fprintln(stdout, string(b))
	return nil
}

// gooseStopCompaction catches a compaction up at the end of the turn it
// happened in: goose has no compaction event, and its store keeps the turns the
// summary replaced (sources.ReadGooseCompaction).
func gooseStopCompaction(dir string, raw []byte) string {
	var p struct {
		SessionID  string `json:"session_id"`
		WorkingDir string `json:"working_dir"`
		CWD        string `json:"cwd"`
	}
	if json.Unmarshal(raw, &p) != nil || p.SessionID == "" {
		return ""
	}
	cwd := hookCWD(adoptGrok(p.WorkingDir, p.CWD))
	pre := precompactHookInput{SessionID: p.SessionID, TranscriptPath: "goose:" + p.SessionID, CWD: cwd}
	catchUpCompactionWith(dir, pre, func() (sources.CompactionTranscript, bool, error) {
		return sources.ReadGooseCompaction(p.SessionID)
	})
	var out bytes.Buffer
	if delivered, _ := emitCompactionRecovery(dir, p.SessionID, cwd, "Stop", hookToolPlain, &out); !delivered {
		return ""
	}
	return strings.TrimSpace(out.String())
}

type gooseStopPayload struct {
	SessionID  string `json:"session_id"`
	WorkingDir string `json:"working_dir"`
	CWD        string `json:"cwd"`
}

// gooseStopTurn reads the turn the Stop ends out of sessions.db, once for
// both answers below.
func gooseStopTurn(raw []byte) sources.GooseTurnState {
	var p gooseStopPayload
	if json.Unmarshal(raw, &p) != nil || p.SessionID == "" {
		return sources.GooseTurnState{}
	}
	return sources.GooseTurn(p.SessionID)
}

// gooseStopEditLines is the pre-edit line for each file the turn edited.
// goose drops what PreToolUse prints, so the line arrives when the turn ends,
// with the edit already made, the way it does on Antigravity.
func gooseStopEditLines(dir string, raw []byte, turn sources.GooseTurnState) string {
	var p gooseStopPayload
	if len(turn.Edits) == 0 || json.Unmarshal(raw, &p) != nil || p.SessionID == "" {
		return ""
	}
	return editFileLines(dir, turn.Edits, p.SessionID, hookCWD(adoptGrok(p.WorkingDir, p.CWD)))
}

// gooseStopFixPair is the fix pair for the command that failed in this turn,
// read from goose's store, once per pair and session.
func gooseStopFixPair(dir string, raw []byte, turn sources.GooseTurnState) string {
	var p gooseStopPayload
	if json.Unmarshal(raw, &p) != nil || p.SessionID == "" || !planIndexReady(dir) {
		return ""
	}
	out := turn.Failure
	if out == "" {
		return ""
	}
	line := fixPairLine(dir, hookCWD(adoptGrok(p.WorkingDir, p.CWD)), sources.UnwrapShellReport(out))
	if line == "" {
		return ""
	}
	token := "fix:" + shortHash(line)
	if alreadyInjected(dir, p.SessionID)[token] {
		return ""
	}
	rememberInjectedIDs(dir, p.SessionID, token)
	payload := frameRecall(truncateToolLine(line, toolAfterMaxBytes))
	usage.RecordDigestInto(dir, usage.KindTool, payload, p.SessionID, 1, 0, nil)
	return payload
}
