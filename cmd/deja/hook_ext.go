package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"github.com/vshulcz/deja-vu/internal/hookengine"
)

func cmdHookExt(dir string, rest []string) error {
	if len(rest) == 0 && sayIfTypedByHand("hook-ext") {
		return nil
	}
	return runHookExt(dir, rest, os.Stdin, os.Stdout)
}

func runHookExt(dir string, args []string, stdin io.Reader, stdout io.Writer) error {
	if stdout == nil {
		stdout = os.Stdout
	}
	host, op, err := parseHookExtFlags(args)
	if err != nil {
		return err
	}
	if op == "" {
		return errors.New("hook-ext: --op is required")
	}

	req, raw, err := readHookExtInput(stdin)
	if err != nil {
		return emitExtResponse(stdout, hookengine.ExtResponse{
			Ok:    false,
			Kind:  "error",
			Error: err.Error(),
		})
	}

	switch op {
	case "settle":
		return emitExtResponse(stdout, runHookExtSettle(host, req))
	case "slash", "tool", "remember":
		isWrite := op == "remember" || (op == "tool" && (req.Tool == "deja_remember" || req.Tool == "remember"))
		if !isWrite {
			if aerr := checkCLIAccess(dir, "read", ""); aerr != nil {
				return emitExtResponse(stdout, hookengine.ExtResponse{Ok: false, Kind: "error", Error: aerr.Error()})
			}
		}
	default:
		if !hookAccessAllowed(dir) {
			return emitExtResponse(stdout, hookengine.ExtResponse{Ok: true})
		}
	}

	var resp hookengine.ExtResponse
	switch op {
	case "context":
		resp = runHookExtContext(dir, host, req, raw)
	case "prompt":
		resp = runHookExtPrompt(dir, host, req, raw)
	case "tool-before":
		resp = runHookExtToolBefore(dir, host, req, raw)
	case "tool-after":
		resp = runHookExtToolAfter(dir, host, req, raw)
	case "precompact":
		resp = runHookExtPrecompact(dir, host, req, raw)
	case "session-end":
		payload := buildHookExtPayload(raw, req, nil)
		runHookSessionEnd(dir, bytes.NewReader(payload))
		resp = hookengine.ExtResponse{Ok: true}
	case "tool":
		resp = runHookExtTool(dir, host, req)
	case "remember":
		resp = runHookExtRemember(dir, req)
	case "slash":
		resp = runHookExtSlash(dir, host, req)
	case "status", "statusline":
		var buf bytes.Buffer
		payload := buildHookExtPayload(raw, req, nil)
		_ = runStatusline(dir, bytes.NewReader(payload), &buf)
		resp = hookengine.ExtResponse{Ok: true, Kind: "status", Output: strings.TrimSpace(buf.String())}
	case "warmup-status":
		var buf bytes.Buffer
		runWarmupStatus(dir, &buf)
		resp = hookengine.ExtResponse{Ok: true, Kind: "status", Output: strings.TrimRight(buf.String(), "\r\n")}
	default:
		return fmt.Errorf("hook-ext: unknown --op %q", op)
	}
	return emitExtResponse(stdout, resp)
}

func parseHookExtFlags(args []string) (host, op string, err error) {
	host = "generic"
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if k, v, ok := strings.Cut(arg, "="); ok && strings.HasPrefix(k, "--") {
			switch k {
			case "--host":
				host = v
			case "--op":
				op = v
			default:
				return "", "", fmt.Errorf("hook-ext: unknown flag %q", k)
			}
			continue
		}
		switch arg {
		case "--host":
			if i+1 >= len(args) {
				return "", "", errors.New("hook-ext: --host needs a value")
			}
			host = args[i+1]
			i++
		case "--op":
			if i+1 >= len(args) {
				return "", "", errors.New("hook-ext: --op needs a value")
			}
			op = args[i+1]
			i++
		default:
			return "", "", fmt.Errorf("hook-ext: unexpected argument %q", arg)
		}
	}
	return strings.ToLower(strings.TrimSpace(host)), strings.ToLower(strings.TrimSpace(op)), nil
}

func readHookExtInput(stdin io.Reader) (hookengine.ExtRequest, []byte, error) {
	if stdin == nil {
		return hookengine.ExtRequest{}, nil, nil
	}
	raw, err := io.ReadAll(io.LimitReader(stdin, hookengine.MaxExtRequestBytes+1))
	if err != nil {
		return hookengine.ExtRequest{}, nil, fmt.Errorf("read hook-ext request: %w", err)
	}
	if len(raw) > hookengine.MaxExtRequestBytes {
		return hookengine.ExtRequest{}, nil, fmt.Errorf("hook-ext request exceeds %d bytes", hookengine.MaxExtRequestBytes)
	}
	req, err := hookengine.ReadExtRequest(bytes.NewReader(raw))
	if err != nil {
		return hookengine.ExtRequest{}, nil, err
	}
	return req, raw, nil
}

func emitExtResponse(w io.Writer, resp hookengine.ExtResponse) error {
	b, err := json.Marshal(resp)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(w, string(b))
	return err
}

func buildHookExtPayload(raw []byte, req hookengine.ExtRequest, extra map[string]any) []byte {
	m := map[string]any{}
	if len(bytes.TrimSpace(raw)) > 0 {
		_ = json.Unmarshal(raw, &m)
	}
	if m == nil {
		m = map[string]any{}
	}
	if req.SessionID != "" {
		if _, ok := m["session_id"]; !ok {
			m["session_id"] = req.SessionID
		}
	}
	if req.Cwd != "" {
		if _, ok := m["cwd"]; !ok {
			m["cwd"] = req.Cwd
		}
	}
	if req.Once {
		m["deja_once"] = true
	}
	for k, v := range extra {
		if v != nil {
			m[k] = v
		}
	}
	out, err := json.Marshal(m)
	if err != nil {
		return raw
	}
	return out
}

func runHookExtContext(dir, host string, req hookengine.ExtRequest, raw []byte) hookengine.ExtResponse {
	if req.NotesOnly {
		notes := strings.TrimSpace(safeNote(humanNotesFor(dir, false)))
		return hookengine.ExtResponse{
			Ok:     true,
			Kind:   "notes",
			Note:   notes,
			Output: notes,
		}
	}
	restore := saveHookOutputFlags()
	defer restore()
	if req.Silent {
		strictHookOutput = true
	}

	var buf bytes.Buffer
	payload := buildHookExtPayload(raw, req, nil)
	_ = runHookContextWith(dir, bytes.NewReader(payload), &buf, req.Plain, req.Once)
	return finalizeExtHookReply(host, req, buf.String())
}

func runHookExtPrompt(dir, host string, req hookengine.ExtRequest, raw []byte) hookengine.ExtResponse {
	restore := saveHookOutputFlags()
	defer restore()
	if req.Silent {
		strictHookOutput = true
	}

	var buf bytes.Buffer
	payload := buildHookExtPayload(raw, req, nil)
	_ = runHookPromptMode(dir, bytes.NewReader(payload), &buf, req.Plain)
	return finalizeExtHookReply(host, req, buf.String())
}

func runHookExtToolBefore(dir, host string, req hookengine.ExtRequest, raw []byte) hookengine.ExtResponse {
	restore := saveHookOutputFlags()
	defer restore()
	if req.Silent {
		strictHookOutput = true
	}

	shape := hookToolClaude
	if req.Plain {
		shape = hookToolPlain
	}
	extra := map[string]any{}
	if req.Tool != "" {
		extra["tool_name"] = req.Tool
	}
	var buf bytes.Buffer
	payload := buildHookExtPayload(raw, req, extra)
	_ = runHookToolMode(dir, bytes.NewReader(payload), &buf, shape)
	return finalizeExtHookReply(host, req, buf.String())
}

func runHookExtToolAfter(dir, host string, req hookengine.ExtRequest, raw []byte) hookengine.ExtResponse {
	restore := saveHookOutputFlags()
	defer restore()
	if req.Silent {
		strictHookOutput = true
	}

	extra := map[string]any{}
	if req.Tool != "" {
		extra["tool_name"] = req.Tool
	}
	var (
		failed   bool
		exitCode *int
		signal   string
	)
	if host == "dsh" {
		if respStr, ok := req.ToolResponse.(string); ok {
			var body string
			body, failed, exitCode, signal = hookengine.ParseDSHExitStatus(respStr)
			if !failed && req.IsError {
				failed = true
			}
			if !failed {
				return hookengine.ExtResponse{
					Ok:         true,
					Failed:     false,
					ExitCode:   exitCode,
					ExitSignal: signal,
				}
			}
			extra["tool_response"] = hookengine.ClampToolWindow(body, 32*1024, true)
		} else {
			failed = req.IsError
			if !failed {
				return hookengine.ExtResponse{
					Ok:     true,
					Failed: false,
				}
			}
		}
	} else if respStr, ok := req.ToolResponse.(string); ok {
		extra["tool_response"] = hookengine.ClampToolWindow(respStr, 32*1024, true)
	}

	var buf bytes.Buffer
	payload := buildHookExtPayload(raw, req, extra)
	_ = runHookToolAfterMode(dir, bytes.NewReader(payload), &buf, req.Plain)
	resp := finalizeExtHookReply(host, req, buf.String())
	if host == "dsh" {
		resp.Failed = failed
		resp.ExitCode = exitCode
		resp.ExitSignal = signal
	}
	return resp
}

func runHookExtPrecompact(dir, host string, req hookengine.ExtRequest, raw []byte) hookengine.ExtResponse {
	bounded, dropped := hookengine.BoundPrecompactMessages(req.Messages, 0, 0, 0)
	harness := strings.TrimSpace(req.Source)
	if harness == "" && host != "" && host != "generic" {
		harness = host
	}
	msgs := bounded
	if msgs == nil {
		msgs = []any{}
	}
	payload := buildHookExtPayload(raw, req, map[string]any{
		"harness":  harness,
		"messages": msgs,
	})
	runHookPrecompactWith(dir, bytes.NewReader(payload), harness)
	return hookengine.ExtResponse{
		Ok:       true,
		Messages: bounded,
		Dropped:  dropped,
	}
}

func runHookExtTool(dir, host string, req hookengine.ExtRequest) hookengine.ExtResponse {
	tool := strings.TrimSpace(req.Tool)
	arg := extractExtToolArg(req)
	switch tool {
	case "deja_remember", "remember":
		if arg == "" {
			return hookengine.ExtResponse{Ok: false, Kind: "error", Error: "remember: text required"}
		}
		reqCopy := req
		if reqCopy.Content == "" {
			reqCopy.Content = arg
		}
		return runHookExtRemember(dir, reqCopy)
	case "deja_recall", "recall":
		q := strings.TrimSpace(arg)
		if q == "" {
			return hookengine.ExtResponse{Ok: false, Kind: "error", Error: "recall: what to recall? deja recall <words>"}
		}
		limit := req.Limit
		if limit <= 0 {
			limit = 5
		} else if limit > 20 {
			limit = 20
		}
		offset := max(0, min(1_000_000_000, req.Offset))
		flags := []string{"--limit", strconv.Itoa(limit)}
		if offset > 0 {
			flags = append(flags, "--offset", strconv.Itoa(offset))
		}
		if req.Project != "" {
			flags = append(flags, "--project", req.Project)
		}
		args := hookengine.BuildQueryArgs("", flags, q)
		var buf bytes.Buffer
		if err := runRecall(dir, args, &buf); err != nil {
			return hookengine.ExtResponse{Ok: false, Kind: "error", Error: err.Error()}
		}
		output := strings.TrimSpace(buf.String())
		if host == "dsh" || req.Neutralize {
			output = hookengine.NeutralizeHostTemplates(output)
		}
		return hookengine.ExtResponse{Ok: true, Kind: "success", Output: output}
	}

	var flags []string
	var fn func(string, []string) error
	switch tool {
	case "deja_fix", "fix":
		fn = nil
	case "deja_blame", "blame":
		if strings.EqualFold(req.Mode, "json") {
			flags = append(flags, "--json")
		}
		fn = runBlame
	case "deja_how", "how":
		fn = nil
	case "deja_session", "ctx", "recall_context":
		fn = cmdCtx
	default:
		return hookengine.ExtResponse{Ok: false, Kind: "error", Error: fmt.Sprintf("unknown tool %s", tool)}
	}
	q := strings.TrimSpace(arg)
	if q == "" {
		return hookengine.ExtResponse{Ok: false, Kind: "error", Error: fmt.Sprintf("%s: missing query", strings.TrimPrefix(tool, "deja_"))}
	}
	args := hookengine.BuildQueryArgs("", flags, q)
	var out string
	var err error
	if fn != nil {
		out, err = captureProcessStdout(func() error { return fn(dir, args) })
	} else {
		var buf bytes.Buffer
		if tool == "deja_fix" || tool == "fix" {
			err = runFix(dir, args, &buf)
		} else {
			err = runHow(dir, args, &buf)
		}
		out = buf.String()
	}
	if err != nil {
		return hookengine.ExtResponse{Ok: false, Kind: "error", Error: err.Error()}
	}
	output := strings.TrimSpace(out)
	if output != "" {
		output = frameRecall(output)
	}
	if host == "dsh" || req.Neutralize {
		output = hookengine.NeutralizeHostTemplates(output)
	}
	return hookengine.ExtResponse{Ok: true, Kind: "success", Output: output}
}

func runHookExtRemember(dir string, req hookengine.ExtRequest) hookengine.ExtResponse {
	text := strings.TrimSpace(firstNonEmptyString(req.Content, req.Text, req.Query, extractExtToolArg(req)))
	if text == "" {
		return hookengine.ExtResponse{Ok: false, Kind: "error", Error: "remember: text required — deja remember \"the note\""}
	}
	args := make([]string, 0, len(req.Tags)*2+4)
	for _, tag := range req.Tags {
		if t := strings.TrimSpace(tag); t != "" {
			args = append(args, "--tag", t)
		}
	}
	if req.Project != "" {
		args = append(args, "--project", req.Project)
	}
	args = append(args, "--", text)
	out, err := captureProcessStdout(func() error {
		return runRemember(dir, args)
	})
	if err != nil {
		return hookengine.ExtResponse{Ok: false, Kind: "error", Error: err.Error()}
	}
	return hookengine.ExtResponse{Ok: true, Kind: "success", Output: strings.TrimSpace(out)}
}

func runHookExtSlash(dir, host string, req hookengine.ExtRequest) hookengine.ExtResponse {
	raw := firstNonEmptyString(req.Args, req.Query, req.Text)
	sess, q, _, errMsg := hookengine.ParseDSHSlashInput(raw)
	if errMsg != "" {
		return hookengine.ExtResponse{Ok: false, Kind: "error", Error: errMsg}
	}
	var flags []string
	if sess != "" {
		flags = append(flags, "--session", sess)
	}
	args := hookengine.BuildQueryArgs("", flags, q)
	out, err := captureProcessStdout(func() error {
		return runSearch(dir, args, "")
	})
	if err != nil {
		return hookengine.ExtResponse{Ok: false, Kind: "error", Error: "deja: " + err.Error()}
	}
	return hookengine.ExtResponse{Ok: true, Kind: "success", Output: strings.TrimSpace(out)}
}

func runHookExtSettle(host string, req hookengine.ExtRequest) hookengine.ExtResponse {
	if host == "hermes" {
		hasRecallText := strings.TrimSpace(firstNonEmptyString(req.Text, req.Content)) != ""
		active, nextHeld, label := hookengine.SettleHermesNotes(hasRecallText, req.Notes, req.HeldNotes)
		return hookengine.ExtResponse{
			Ok:             true,
			Note:           strings.Join(active, "\n"),
			HeldNotes:      nextHeld,
			IndicatorLabel: label,
		}
	}
	settled := hookengine.SettleRecall(req.Text, req.Prompt)
	if req.Neutralize || host == "dsh" {
		settled = hookengine.NeutralizeHostTemplates(settled)
	}
	return hookengine.ExtResponse{
		Ok:      true,
		Text:    settled,
		Context: settled,
	}
}

func finalizeExtHookReply(host string, req hookengine.ExtRequest, rawOut string) hookengine.ExtResponse {
	var ctxText, notes string
	if req.Plain {
		ctxText = strings.TrimSpace(rawOut)
	} else if host == "openclaw" {
		ctxText, notes = hookengine.SplitHookReplyKeepSurrounding(rawOut)
	} else {
		ctxText, notes = hookengine.SplitHookReply(rawOut)
	}
	if req.Neutralize || host == "dsh" {
		ctxText = hookengine.NeutralizeHostTemplates(ctxText)
	}
	resp := hookengine.ExtResponse{
		Ok:      true,
		Context: ctxText,
		Note:    notes,
	}
	if !req.Plain {
		resp.UpdatedInput, resp.Decision = extractExtHookDecisionAndInput(rawOut)
	}
	if resp.Context != "" || resp.Note != "" || len(resp.UpdatedInput) > 0 {
		resp.Kind = "hook"
	}
	if host == "openclaw" && notes != "" {
		resp.Receipt = hookengine.FormatOpenClawReceipt(notes)
	}
	return resp
}

func extractExtHookDecisionAndInput(rawOut string) (map[string]any, string) {
	trimmed := strings.TrimSpace(rawOut)
	if trimmed == "" || !strings.Contains(trimmed, "\"updatedInput\"") {
		return nil, ""
	}
	lines := strings.Split(trimmed, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])
		if !strings.HasPrefix(line, "{") {
			continue
		}
		var env struct {
			HookSpecificOutput *struct {
				PermissionDecision string         `json:"permissionDecision"`
				UpdatedInput       map[string]any `json:"updatedInput"`
			} `json:"hookSpecificOutput"`
		}
		if json.Unmarshal([]byte(line), &env) == nil && env.HookSpecificOutput != nil && len(env.HookSpecificOutput.UpdatedInput) > 0 {
			return env.HookSpecificOutput.UpdatedInput, env.HookSpecificOutput.PermissionDecision
		}
	}
	return nil, ""
}

func extractExtToolArg(req hookengine.ExtRequest) string {
	if v := firstNonEmptyString(req.Query, req.Text, req.Content); v != "" {
		return v
	}
	m, _ := req.ToolInput.(map[string]any)
	if m == nil {
		return ""
	}
	for _, key := range []string{"query", "error", "path", "file_path", "what", "text", "command", "input"} {
		if s, ok := m[key].(string); ok && strings.TrimSpace(s) != "" {
			return s
		}
	}
	return ""
}

func firstNonEmptyString(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

func saveHookOutputFlags() func() {
	prevStrict := strictHookOutput
	prevCopilot := copilotHookOutput
	prevMcode := mcodeHookOutput
	return func() {
		strictHookOutput = prevStrict
		copilotHookOutput = prevCopilot
		mcodeHookOutput = prevMcode
	}
}

func captureProcessStdout(fn func() error) (string, error) {
	r, w, err := os.Pipe()
	if err != nil {
		return "", err
	}
	readCh := make(chan []byte, 1)
	go func() {
		b, _ := io.ReadAll(r)
		readCh <- b
		close(readCh)
	}()
	origStdout := os.Stdout
	os.Stdout = w
	var runErr error
	defer func() {
		os.Stdout = origStdout
		_ = w.Close()
		<-readCh
		_ = r.Close()
	}()
	runErr = fn()
	os.Stdout = origStdout
	_ = w.Close()
	out := <-readCh
	_ = r.Close()
	return string(out), runErr
}
