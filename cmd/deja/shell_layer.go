package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/vshulcz/deja-vu/internal/usage"
)

// Shell-level channel (#1214): every other channel deja has — MCP, hooks,
// plugins, instruction files — needs the harness to honour its own contract.
// When a harness breaks its MCP client, drops hooks, or has neither, the one
// thing every coding agent still does is spawn shell commands.
//
// `deja shell` operates at the command layer in three ways:
//   - `eval "$(deja shell zsh|bash|fish)"` installs preexec/postexec shell
//     hooks (`deja shell preexec` / `deja shell postexec`) that print prior
//     history before a command and known fixes after a failed command.
//   - `deja shell -c "<cmd>"` makes `deja` work as `$SHELL` (`SHELL=deja` or a
//     wrapper script): it prints pre-command recall to stderr, runs the command
//     through the user's real shell, and on a non-zero exit prints any matching
//     fix pair from earlier sessions.
//   - `deja shell --verify [--json]` audits every wired harness config and
//     verifies that its configured `deja mcp` server actually answers
//     `initialize` and `tools/list` over stdio.

type shellVerifyRow struct {
	Target  string `json:"target"`
	Path    string `json:"path"`
	Status  string `json:"status"`
	Tools   int    `json:"tools,omitempty"`
	Message string `json:"message,omitempty"`
}

func runShell(dir string, args []string, stdout, stderr io.Writer) error {
	if len(args) > 0 {
		switch args[0] {
		case "preexec":
			return runShellPreexec(dir, args[1:], stderr)
		case "postexec":
			return runShellPostexec(dir, args[1:], stderr)
		}
	}

	var (
		verify   bool
		jsonOut  bool
		cmdFlag  bool
		cmdStr   string
		shellArg string
	)
	for rest := args; len(rest) > 0; rest = rest[1:] {
		a := rest[0]
		switch a {
		case "--verify", "-verify":
			verify = true
		case "--json", "-json":
			jsonOut = true
		case "-c":
			if len(rest) < 2 {
				return errors.New("shell -c requires a command string")
			}
			cmdFlag = true
			cmdStr = rest[1]
			rest = rest[1:]
		default:
			if strings.HasPrefix(a, "-") {
				return fmt.Errorf("unknown flag for shell: %s (try: deja shell bash|zsh|fish, deja shell -c <cmd>, or deja shell --verify)", a)
			}
			if shellArg != "" {
				return fmt.Errorf("shell takes one shell name — got %q after %q", a, shellArg)
			}
			shellArg = a
		}
	}

	if verify {
		if cmdFlag || shellArg != "" {
			return errors.New("shell --verify takes no shell name or -c command")
		}
		return runShellVerify(dir, jsonOut, stdout)
	}
	if jsonOut {
		return errors.New("shell --json is only supported with --verify")
	}
	if cmdFlag {
		return runShellCommand(dir, cmdStr, stdout, stderr)
	}
	switch shellArg {
	case "bash":
		_, err := fmt.Fprint(stdout, bashShellLayerScript)
		return err
	case "zsh":
		_, err := fmt.Fprint(stdout, zshShellLayerScript)
		return err
	case "fish":
		_, err := fmt.Fprint(stdout, fishShellLayerScript)
		return err
	case "":
		return errors.New("usage: deja shell <bash|zsh|fish> | deja shell -c <command> | deja shell --verify [--json]")
	default:
		return fmt.Errorf("unsupported shell %q (choose: bash, zsh, fish)", shellArg)
	}
}

// runShellPreexec handles `deja shell preexec <session> <command...>`.
func runShellPreexec(dir string, args []string, stderr io.Writer) error {
	if len(args) < 2 {
		return nil
	}
	sessionID := args[0]
	if sessionID == "" {
		sessionID = fmt.Sprintf("shell-%d", os.Getppid())
	}
	cmdStr := strings.TrimSpace(strings.Join(args[1:], " "))
	if cmdStr == "" {
		return nil
	}
	runHookShellBefore(dir, sessionID, cmdStr, stderr)
	return nil
}

// runShellPostexec handles `deja shell postexec <session> <exitCode> <command> [output]`.
func runShellPostexec(dir string, args []string, stderr io.Writer) error {
	if len(args) < 3 {
		return nil
	}
	sessionID := args[0]
	if sessionID == "" {
		sessionID = fmt.Sprintf("shell-%d", os.Getppid())
	}
	exitCode, _ := strconv.Atoi(args[1])
	cmdStr := strings.TrimSpace(args[2])
	var output string
	if len(args) > 3 {
		output = strings.Join(args[3:], " ")
	}
	if cmdStr == "" || exitCode == 0 {
		return nil
	}
	runHookShellAfter(dir, sessionID, cmdStr, exitCode, output, stderr)
	return nil
}

func runShellCommand(dir, cmdStr string, stdout, stderr io.Writer) error {
	sessionID := os.Getenv("DEJA_SHELL_SESSION")
	if sessionID == "" {
		sessionID = fmt.Sprintf("shell-%d", os.Getppid())
	}
	runHookShellBefore(dir, sessionID, cmdStr, stderr)

	sh, shArgs := realShellCommand(cmdStr)
	c := exec.CommandContext(context.Background(), sh, shArgs...)
	c.Stdin = os.Stdin
	var captured boundedBuffer
	c.Stdout = io.MultiWriter(stdout, &captured)
	c.Stderr = io.MultiWriter(stderr, &captured)

	err := c.Run()
	if err != nil {
		code := 1
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			code = exitErr.ExitCode()
		}
		runHookShellAfter(dir, sessionID, cmdStr, code, captured.String(), stderr)
		return exitStatus(code)
	}
	return nil
}

type boundedBuffer struct {
	buf bytes.Buffer
}

func (b *boundedBuffer) Write(p []byte) (int, error) {
	const maxCapture = 16384
	rem := maxCapture - b.buf.Len()
	if rem > 0 {
		if len(p) > rem {
			b.buf.Write(p[:rem])
		} else {
			b.buf.Write(p)
		}
	}
	return len(p), nil
}

func (b *boundedBuffer) String() string {
	return b.buf.String()
}

func realShellCommand(cmdStr string) (string, []string) {
	if s := strings.TrimSpace(os.Getenv("DEJA_REAL_SHELL")); s != "" {
		return s, []string{"-c", cmdStr}
	}
	if runtime.GOOS == "windows" {
		return "cmd.exe", []string{"/c", cmdStr}
	}
	return "/bin/sh", []string{"-c", cmdStr}
}

func isDejaInvocation(cmdStr string) bool {
	fields := strings.Fields(cmdStr)
	if len(fields) == 0 {
		return true
	}
	first := filepath.Base(fields[0])
	return first == "deja" || first == "deja.exe"
}

func runHookShellBefore(dir, sessionID, cmdStr string, stderr io.Writer) {
	if !filepath.IsAbs(dir) || isDejaInvocation(cmdStr) || !planIndexReady(dir) {
		return
	}
	injected := alreadyInjected(dir, sessionID)
	used := func(line string) bool { return injected["tool:"+shortHash(dedupeFact(line))] }
	cwd := hookCWD("")
	line := commandHookLineSkipping(dir, cwd, cmdStr, sessionID, used)
	if line == "" {
		return
	}
	token := "tool:" + shortHash(dedupeFact(line))
	if injected[token] {
		return
	}
	rememberInjectedIDs(dir, sessionID, token)
	line = truncateToolLine(line, toolHookMaxBytes)
	out := frameRecall(line)
	usage.RecordDigestInto(dir, usage.KindTool, out, sessionID, 1, 0, nil)
	fmt.Fprintln(stderr, out)
}

func runHookShellAfter(dir, sessionID, cmdStr string, exitCode int, output string, stderr io.Writer) {
	if !filepath.IsAbs(dir) || exitCode == 0 || isDejaInvocation(cmdStr) || !planIndexReady(dir) {
		return
	}
	cwd := hookCWD("")
	line := fixPairLineFor(dir, cwd, cmdStr, output)
	if line == "" {
		return
	}
	out := frameRecall(line)
	usage.RecordDigestInto(dir, usage.KindTool, out, sessionID, 1, 0, nil)
	fmt.Fprintln(stderr, out)
}

func runShellVerify(dir string, jsonOut bool, stdout io.Writer) error {
	configs := doctorMCPConfigs()
	var rows []shellVerifyRow
	probeCache := map[string]shellVerifyRow{}

	for _, c := range configs {
		if !doctorExists(c.path) || !c.wired(c.path) {
			continue
		}
		if missing := dejaCommandMissing(c.path); missing != "" {
			rows = append(rows, shellVerifyRow{
				Target:  c.name,
				Path:    c.path,
				Status:  "missing-binary",
				Message: fmt.Sprintf("configured binary %s does not exist", missing),
			})
			continue
		}
		if dejaEntrySwitchedOff(c.name, c.path) || doctorMCPSwitchedOff(c.name) != "" {
			rows = append(rows, shellVerifyRow{
				Target:  c.name,
				Path:    c.path,
				Status:  "disabled",
				Message: "MCP server entry is switched off in agent config",
			})
			continue
		}
		bin := wiredDejaBinary(c.path)
		cached, ok := probeCache[bin]
		if !ok {
			cached = probeMCPCallThrough(dir, bin)
			probeCache[bin] = cached
		}
		rows = append(rows, shellVerifyRow{
			Target:  c.name,
			Path:    c.path,
			Status:  cached.Status,
			Tools:   cached.Tools,
			Message: cached.Message,
		})
	}

	if jsonOut {
		if rows == nil {
			rows = []shellVerifyRow{}
		}
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(rows)
	}

	if len(rows) == 0 {
		fmt.Fprintln(stdout, "No wired MCP targets found. Use `eval \"$(deja shell zsh)\"` or `deja shell -c <cmd>` to run at the shell layer without harness wiring.")
		return nil
	}

	broken := 0
	for _, r := range rows {
		if r.Status == "ok" {
			fmt.Fprintf(stdout, "  %-14s ok (%d tools)  %s\n", r.Target, r.Tools, reportPath(r.Path))
		} else {
			broken++
			fmt.Fprintf(stdout, "  %-14s %-14s %s — %s\n", r.Target, r.Status, reportPath(r.Path), r.Message)
		}
	}
	if broken > 0 {
		fmt.Fprintf(stdout, "\n%d target(s) cannot complete MCP call-through; use `eval \"$(deja shell zsh)\"` or `SHELL=deja` for shell-layer recall independent of the harness.\n", broken)
		return exitStatus(1)
	}
	return nil
}

func probeMCPCallThrough(dir, bin string) shellVerifyRow {
	const req = `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"deja-shell-verify","version":"1.0"}}}` + "\n" +
		`{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}` + "\n"

	var out bytes.Buffer
	selfExe, _ := os.Executable()
	useInProcess := bin == "" || bin == selfExe || strings.HasSuffix(bin, ".test") || strings.HasSuffix(bin, ".test.exe")
	if !useInProcess {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, bin, "mcp")
		cmd.Stdin = strings.NewReader(req)
		cmd.Stdout = &out
		if err := cmd.Run(); err != nil {
			return shellVerifyRow{
				Status:  "broken-handshake",
				Message: fmt.Sprintf("mcp server exited with error: %v", err),
			}
		}
	} else {
		if err := serveMCP(dir, strings.NewReader(req), &out); err != nil {
			return shellVerifyRow{
				Status:  "broken-handshake",
				Message: fmt.Sprintf("mcp server failed: %v", err),
			}
		}
	}

	var initOK bool
	var toolCount int
	for line := range strings.SplitSeq(strings.TrimSpace(out.String()), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var resp struct {
			ID     int `json:"id"`
			Result struct {
				ProtocolVersion string            `json:"protocolVersion"`
				Tools           []json.RawMessage `json:"tools"`
			} `json:"result"`
		}
		if err := json.Unmarshal([]byte(line), &resp); err != nil {
			continue
		}
		if resp.ID == 1 && resp.Result.ProtocolVersion != "" {
			initOK = true
		}
		if resp.ID == 2 && len(resp.Result.Tools) > 0 {
			toolCount = len(resp.Result.Tools)
		}
	}
	if !initOK || toolCount == 0 {
		return shellVerifyRow{
			Status:  "broken-handshake",
			Message: "mcp server did not return tools on stdio",
		}
	}
	return shellVerifyRow{
		Status: "ok",
		Tools:  toolCount,
	}
}

const zshShellLayerScript = `# deja shell layer for zsh — recall before commands and after failures
_deja_shell_last_cmd=""

_deja_shell_preexec() {
  _deja_shell_last_cmd="$1"
  case "$1" in
    deja|deja\ *) return ;;
  esac
  command deja shell preexec "$$" "$1"
}

_deja_shell_precmd() {
  local st=$?
  local cmd="$_deja_shell_last_cmd"
  _deja_shell_last_cmd=""
  if (( st != 0 )) && [[ -n "$cmd" ]]; then
    case "$cmd" in
      deja|deja\ *) return ;;
    esac
    command deja shell postexec "$$" "$st" "$cmd"
  fi
}

autoload -Uz add-zsh-hook
add-zsh-hook preexec _deja_shell_preexec
add-zsh-hook precmd _deja_shell_precmd
`

const bashShellLayerScript = `# deja shell layer for bash — recall before commands and after failures
_deja_shell_last_cmd=""
_deja_shell_in_prompt=0

_deja_shell_preexec() {
  (( _deja_shell_in_prompt )) && return
  [[ "$BASH_COMMAND" == "_deja_shell_precmd"* ]] && return
  case "$BASH_COMMAND" in
    deja|deja\ *) return ;;
  esac
  _deja_shell_last_cmd="$BASH_COMMAND"
  command deja shell preexec "$$" "$BASH_COMMAND"
}

_deja_shell_precmd() {
  local st=$?
  _deja_shell_in_prompt=1
  local cmd="$_deja_shell_last_cmd"
  _deja_shell_last_cmd=""
  if (( st != 0 )) && [[ -n "$cmd" ]]; then
    command deja shell postexec "$$" "$st" "$cmd"
  fi
  _deja_shell_in_prompt=0
}

trap '_deja_shell_preexec' DEBUG
if [[ "${PROMPT_COMMAND:-}" != *"_deja_shell_precmd"* ]]; then
  PROMPT_COMMAND="_deja_shell_precmd${PROMPT_COMMAND:+;$PROMPT_COMMAND}"
fi
`

const fishShellLayerScript = `# deja shell layer for fish — recall before commands and after failures
function _deja_shell_preexec --on-event fish_preexec
    switch "$argv[1]"
        case 'deja' 'deja *'
            return
    end
    command deja shell preexec "$fish_pid" "$argv[1]"
end

function _deja_shell_postexec --on-event fish_postexec
    set -l st $status
    if test $st -ne 0
        switch "$argv[1]"
            case 'deja' 'deja *'
                return
        end
        command deja shell postexec "$fish_pid" "$st" "$argv[1]"
    end
end
`
