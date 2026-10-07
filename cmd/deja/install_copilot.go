package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"strings"

	"github.com/vshulcz/deja-vu/internal/sources"
)

// copilotManagedHeader is what Copilot CLI 1.0.79 writes at the top of the
// config.json it manages. Every config.json it has started on carries it, so
// refusing a file over its comments refused every real one.
const copilotManagedHeader = "// User settings belong in settings.json.\n// This file is managed automatically.\n"

// copilotHooksPath is deja's own hook file, which two hosts read.
//
// Copilot CLI loads every *.json under ~/.copilot/hooks as user-level hooks
// (since 0.0.422), and VS Code Copilot Chat lists the same directory among
// its hook locations (`copilot-personal` in 1.140's workbench). One file
// therefore wires both, and Copilot CLI's settings.json is left alone: it
// rewrote config.json on start and moved a `hooks` key into settings.json
// whole, which is what deja's entries used to be written around.
//
// VS Code reads the literal ~/.copilot/hooks, so under a COPILOT_HOME
// somewhere else only Copilot CLI finds the file.
func copilotHooksPath() string {
	return filepath.Join(sources.CopilotHome(), "hooks", "deja.json")
}

// copilotLegacyHookFiles are where installs before the hook file wrote deja's
// entries. An install clears them, so an upgrade does not run deja twice.
func copilotLegacyHookFiles() []string {
	return []string{filepath.Join(sources.CopilotHome(), "settings.json"), filepath.Join(sources.CopilotHome(), "config.json")}
}

// copilotHooks is every event deja wires in the hook file. Measured on Copilot
// CLI 1.0.92 against a stub model:
//
//   - sessionStart and userPromptSubmitted: a flat additionalContext reaches
//     the model, the first as a message of its own, the second inside the
//     user's turn. A nested hookSpecificOutput is dropped by both.
//   - preToolUse: additionalContext arrives as a message after the tool's
//     result. The matcher is honored, so deja runs only for a shell command
//     or a file write.
//   - postToolUse is appended to the result; a command that exits non-zero
//     is still a success there. postToolUseFailure fires for a tool that
//     errors, with the message under `error`.
//   - PreCompact is the one key spelled VS Code's way: VS Code maps only the
//     camelCase events it shares with Copilot CLI, and Copilot CLI runs a
//     PascalCase key too, with snake_case fields, so one entry serves both.
//     The payload names events.jsonl as transcript_path.
//
// preMcpToolCall and sessionEnd keep recall from answering with the session
// asking it (#4551). VS Code runs sessionStart, userPromptSubmitted,
// preToolUse and postToolUse from the same entries and ignores the rest; it
// drops `matcher`, so there deja answers for every tool and stays silent on
// the ones it has nothing for. --copilot answers flat for Copilot CLI and in
// VS Code's nested shape when the payload carries hook_event_name.
var copilotHooks = []struct {
	event   string
	args    []string
	matcher string
}{
	{"sessionStart", []string{"hook-context", "--copilot"}, ""},
	{"userPromptSubmitted", []string{"hook-prompt", "--copilot"}, ""},
	{"preToolUse", []string{"hook-tool", "--copilot"}, copilotPreToolMatcher},
	{"postToolUse", []string{"hook-tool-after", "--copilot"}, copilotShellMatcher},
	{"postToolUseFailure", []string{"hook-tool-after", "--copilot"}, copilotShellMatcher},
	{"PreCompact", []string{"hook-precompact"}, ""},
	{"preMcpToolCall", []string{"hook-mcp-call"}, ""},
	{"sessionEnd", []string{"hook-session-end"}, ""},
}

// Copilot CLI's own tool names, full-match regexes (1.0.36). The shell is
// powershell on Windows.
const (
	copilotShellMatcher   = "bash|powershell"
	copilotPreToolMatcher = "bash|powershell|edit|create"
)

// installCopilotAuto wires Copilot CLI's hooks, and VS Code Copilot Chat's
// through the same file, with the MCP server beside them (#4231).
func installCopilotAuto(exe string, uninstall bool) (installResult, error) {
	r, err := installCopilotHookFile(exe, uninstall, installCopilotMCP, "vscode")
	if err != nil {
		return installResult{}, err
	}
	// VS Code Copilot Chat shares the hook file, not the CLI's status line.
	status, err := installCopilotStatusline(exe, uninstall)
	if err != nil {
		return installResult{}, err
	}
	return wroteAll(r, status), nil
}

// installVSCodeAuto is the same hook file, reached from the VS Code side: a
// reader with VS Code and no Copilot CLI gets the hooks Copilot Chat reads.
func installVSCodeAuto(exe string, uninstall bool) (installResult, error) {
	return installCopilotHookFile(exe, uninstall, installVSCode, "copilot")
}

// installVSCode writes Copilot Chat's MCP entry and the prompt file that is
// its /deja command.
func installVSCode(exe string, uninstall bool) (installResult, error) {
	mcp, err := installVSCodeMCP(exe, uninstall)
	if err != nil {
		return installResult{}, err
	}
	prompt, err := installCopilotChatPrompt(exe, uninstall)
	if err != nil {
		return installResult{}, err
	}
	if prompt.Path == "" {
		return mcp, nil
	}
	return wroteAll(mcp, prompt), nil
}

// copilotHookFileWantedBy reports whether the other target sharing the hook
// file is installed and staying.
func copilotHookFileWantedBy(other string) bool {
	if removingTargets[other] {
		return false
	}
	return slices.Contains(readWiringState().Targets, other+"-auto")
}

// installCopilotHookFile writes deja.json and clears the entries older
// installs left in settings.json and config.json. Every edit is worked out
// before anything is written, and the MCP entry after them: a file deja has
// to refuse used to be found only once mcp-config.json had been written.
//
// The file is shared by copilot-auto and vscode-auto, so taking one of them
// out leaves it while the other is still installed.
func installCopilotHookFile(exe string, uninstall bool, mcpFor func(string, bool) (installResult, error), other string) (installResult, error) {
	target := copilotHooksPath()
	keep := uninstall && copilotHookFileWantedBy(other)
	plans := make([]copilotHooksPlan, 0, 3)
	for _, path := range copilotLegacyHookFiles() {
		plan, err := planCopilotHooks(path, exe, true)
		if err != nil {
			return installResult{}, err
		}
		plan.clearing = true
		plans = append(plans, plan)
	}
	if !keep {
		plan, err := planCopilotHooks(target, exe, uninstall)
		if err != nil {
			return installResult{}, err
		}
		plans = append(plans, plan)
	}
	mcp, err := mcpFor(exe, uninstall)
	if err != nil {
		return installResult{}, err
	}
	results := []installResult{mcp}
	for _, plan := range plans {
		r, err := plan.apply()
		if err != nil {
			return installResult{}, err
		}
		// Unchanged files ride along too: the kept-snapshot line reads them,
		// and the config.json deja wrote into before Copilot moved its hooks
		// still has deja's snapshot beside it.
		results = append(results, r)
	}
	return wroteAll(results...), nil
}

// copilotHookTimeoutSec bounds how long Copilot waits for the digest before
// the first request. It is served from a cache and answers in milliseconds;
// one that cannot is not worth holding someone's prompt for.
const copilotHookTimeoutSec = 10

// copilotHooksPlan is one file's edit, worked out and not yet written.
type copilotHooksPlan struct {
	path       string
	old, next  []byte
	addedBlock bool
	// clearing is an older install's file being emptied of deja, which is a
	// removal even on the install path: a settings.json deja created goes.
	clearing bool
	result   installResult
}

func (p copilotHooksPlan) apply() (installResult, error) {
	if p.next == nil {
		return p.result, nil
	}
	if p.addedBlock {
		noteBlockAdded(p.path, "hooks")
	}
	if p.clearing && !removingWiring {
		removingWiring = true
		defer func() { removingWiring = false }()
	}
	a, err := writeIfChanged(p.path, p.old, p.next)
	return installResult{Path: p.path, Action: a}, err
}

func planCopilotHooks(path, exe string, uninstall bool) (copilotHooksPlan, error) {
	unchanged := copilotHooksPlan{path: path, result: installResult{Path: path, Action: "unchanged"}}
	exe = hookExeFor(exe, uninstall)
	old, err := readConfig(path)
	if err != nil {
		return copilotHooksPlan{}, err
	}
	if uninstall && len(bytes.TrimSpace(old)) == 0 {
		return unchanged, nil
	}
	// Copilot's own header comes off before the comment test and goes back on
	// the way out; anything else that is a comment is the reader's.
	header := ""
	body := old
	if bytes.HasPrefix(old, []byte(copilotManagedHeader)) {
		header = copilotManagedHeader
		body = old[len(header):]
	}
	jsonc := configIsJSONC(body)
	source := body
	if jsonc {
		source = []byte(jsoncToJSON(string(body)))
	}
	root := map[string]any{}
	if len(bytes.TrimSpace(source)) > 0 {
		if err := json.Unmarshal(source, &root); err != nil {
			return copilotHooksPlan{}, configParseError(path, err)
		}
		if root == nil {
			root = map[string]any{}
		}
	}
	before, _ := json.Marshal(root)
	hooks, isMap := root["hooks"].(map[string]any)
	if _, has := root["hooks"]; has && !isMap {
		if uninstall {
			return unchanged, nil
		}
		return copilotHooksPlan{}, fmt.Errorf("%s: `hooks` is not an object — deja leaves it as it is", path)
	}
	added := false
	if hooks == nil {
		if uninstall {
			return unchanged, nil
		}
		hooks = map[string]any{}
		root["hooks"] = hooks
		added = true
	}
	for _, h := range copilotHooks {
		setCopilotHook(hooks, h.event, h.matcher, exe, uninstall, h.args...)
	}
	// The object deja added can be in either file by now: Copilot moves
	// config.json's hooks into settings.json on start, and the record names
	// the file deja wrote.
	if len(hooks) == 0 && !added {
		for _, p := range append([]string{path}, copilotLegacyHookFiles()...) {
			if blockWasAdded(p, "hooks") {
				delete(root, "hooks")
				forgetBlockAdded(p, "hooks")
			}
		}
	}
	after, _ := json.Marshal(root)
	if string(after) == string(before) {
		return unchanged, nil
	}
	if jsonc {
		return copilotHooksPlan{}, fmt.Errorf("%s: deja cannot edit hooks in a file that carries comments — add or remove the hook by hand, or take the comments out", path)
	}
	next, err := marshalConfigLike(body, root)
	if err != nil {
		return copilotHooksPlan{}, err
	}
	next = append([]byte(header), append(next, '\n')...)
	if uninstall {
		next = snapshotIfSame(path, root, next)
	}
	return copilotHooksPlan{path: path, old: old, next: next, addedBlock: added}, nil
}

// snapshotIfSame gives back the snapshot deja took before its first write when
// what is left after taking deja out says the same thing. Marshalling keeps the
// top-level order and nothing below it, so Copilot's own
// {"type","bash","timeoutSec"} came back as {"bash","timeoutSec","type"}: the
// same settings, and a diff in the reader's dotfiles all the same.
func snapshotIfSame(path string, root map[string]any, next []byte) []byte {
	if !snapshotTaken(path) {
		return next
	}
	b, err := os.ReadFile(path + ".bak")
	if err != nil {
		return next
	}
	b = bytes.TrimPrefix(b, utf8BOM)
	var was map[string]any
	if json.Unmarshal([]byte(jsoncToJSON(string(b))), &was) != nil || !reflect.DeepEqual(was, root) {
		return next
	}
	return b
}

// setCopilotHook keeps one deja entry under an event and leaves every other
// one alone. Entries are flat — {"type","bash","timeoutSec"} — the same schema
// as a repository's .github/hooks/*.json. matcher, when set, is the tool-name
// regex Copilot CLI runs the entry for.
func setCopilotHook(hooks map[string]any, event, matcher, exe string, uninstall bool, args ...string) {
	cmd := hookRun(exe, args...)
	base := strings.TrimSuffix(cmd, " --copilot")
	entries, _ := hooks[event].([]any)
	var kept []any
	found := false
	for _, entryAny := range entries {
		entry, _ := entryAny.(map[string]any)
		kind := hookNotDejas
		if entry != nil {
			s, _ := entry["bash"].(string)
			// The flag is deja's own; without it the command is the plain
			// hook-context line, which answers in a shape Copilot ignores and
			// is taken over the same way.
			kind = hookCommandKindOf(strings.TrimSuffix(strings.TrimSpace(s), " --copilot"), base)
		}
		if kind == hookWrapsDejas {
			found = true
			kept = append(kept, entryAny)
			continue
		}
		if kind == hookDejas {
			if uninstall || found {
				continue
			}
			found = true
			entry["type"] = "command"
			entry["bash"] = cmd
			entry["timeoutSec"] = copilotHookTimeoutSec
			if runtime.GOOS == "windows" {
				entry["powershell"] = copilotPowerShellCommand(exe, args...)
			}
			setHookMatcher(entry, matcher)
		}
		kept = append(kept, entryAny)
	}
	if !uninstall && !found {
		entry := map[string]any{"type": "command", "bash": cmd, "timeoutSec": copilotHookTimeoutSec}
		setHookMatcher(entry, matcher)
		// On Windows Copilot picks the powershell line when there is one;
		// `&` is what lets PowerShell run a quoted path.
		if runtime.GOOS == "windows" {
			entry["powershell"] = copilotPowerShellCommand(exe, args...)
		}
		kept = append(kept, entry)
	}
	if len(kept) == 0 {
		delete(hooks, event)
		return
	}
	hooks[event] = kept
}

func setHookMatcher(entry map[string]any, matcher string) {
	if matcher == "" {
		delete(entry, "matcher")
		return
	}
	entry["matcher"] = matcher
}

// copilotHooksOffIn names the file whose disableAllHooks turns every Copilot
// hook off, and "" when neither does. deja's entry stays in place and runs
// nothing.
func copilotHooksOffIn() string {
	for _, name := range []string{"settings.json", "config.json"} {
		p := filepath.Join(sources.CopilotHome(), name)
		if readJSONConfig(p)["disableAllHooks"] == true {
			return p
		}
	}
	return ""
}

// copilotPowerShellCommand is the line Copilot runs on Windows. PowerShell
// runs a quoted path only behind `&`, and a double-quoted one expands `$` and
// backticks, so the path goes in single quotes, where only the quote itself
// means anything and is written twice.
func copilotPowerShellCommand(exe string, args ...string) string {
	p := strings.ReplaceAll(exe, `\`, "/")
	return strings.Join(append([]string{"& '" + strings.ReplaceAll(p, "'", "''") + "'"}, args...), " ")
}
