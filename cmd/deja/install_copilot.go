package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"

	"github.com/vshulcz/deja-vu/internal/sources"
)

// copilotManagedHeader is what Copilot CLI 1.0.79 writes at the top of the
// config.json it manages. Every config.json it has started on carries it, so
// refusing a file over its comments refused every real one.
const copilotManagedHeader = "// User settings belong in settings.json.\n// This file is managed automatically.\n"

// copilotHooksPath is the file Copilot will read deja's hook from.
//
// Copilot CLI 1.0.79 keeps user settings in settings.json and rewrites
// config.json as a file it manages. On every start it moves the user keys it
// finds in config.json into settings.json, and a `hooks` key moves whole: it
// replaces the one in settings.json rather than merging into it. So while the
// reader's hooks are still in config.json, an entry deja put in settings.json
// is gone after one launch; written beside theirs, it moves with them.
func copilotHooksPath() string {
	config := filepath.Join(sources.CopilotHome(), "config.json")
	if b, err := os.ReadFile(config); err == nil {
		var root map[string]any
		if json.Unmarshal([]byte(jsoncToJSON(string(bytes.TrimPrefix(b, utf8BOM)))), &root) == nil {
			if _, ok := root["hooks"]; ok {
				return config
			}
		}
	}
	return filepath.Join(sources.CopilotHome(), "settings.json")
}

// copilotHooks is every event deja wires in Copilot CLI. --copilot makes
// hook-context answer in the only shape Copilot reads. postToolUse carries
// the fix line after a failed command. The other two keep recall from
// answering with the session asking it (#4551):
// preMcpToolCall restamps it before each MCP request, which a sessionStart
// stamp alone stops covering twenty minutes in, and sessionEnd takes the
// stamp back. Both payloads name the session as `sessionId` (1.0.91).
var copilotHooks = []struct {
	event string
	args  []string
}{
	{"sessionStart", []string{"hook-context", "--copilot"}},
	{"postToolUse", []string{"hook-tool-after", "--copilot"}},
	{"preMcpToolCall", []string{"hook-mcp-call"}},
	{"sessionEnd", []string{"hook-session-end"}},
}

// installCopilotAuto wires Copilot CLI's sessionStart hook to the digest, with
// the MCP server beside it (#4231).
//
// The digest goes in on sessionStart. Measured on 1.0.79, what it prints goes in front of the
// first request as a message of its own, is kept for every later turn of the
// session, and is not written into the user's message; userPromptSubmitted
// output is appended to the user's own turn instead. The payload names the
// session as `sessionId`, and `source` is "resume" when an old one is reopened.
func installCopilotAuto(exe string, uninstall bool) (installResult, error) {
	target := copilotHooksPath()
	paths := []string{filepath.Join(sources.CopilotHome(), "settings.json"), filepath.Join(sources.CopilotHome(), "config.json")}
	// Both hook edits are worked out before anything is written, and the MCP
	// entry after them: a file deja has to refuse used to be found only once
	// mcp-config.json and settings.json had already been written.
	plans := make([]copilotHooksPlan, 0, len(paths))
	for _, path := range paths {
		// The other file is only ever cleared: an entry left there from an
		// earlier install would run twice, or be what the move overwrites.
		plan, err := planCopilotHooks(path, exe, uninstall || path != target)
		if err != nil {
			return installResult{}, err
		}
		plans = append(plans, plan)
	}
	mcp, err := installCopilotMCP(exe, uninstall)
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
	result     installResult
}

func (p copilotHooksPlan) apply() (installResult, error) {
	if p.next == nil {
		return p.result, nil
	}
	if p.addedBlock {
		noteBlockAdded(p.path, "hooks")
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
		setCopilotHook(hooks, h.event, exe, uninstall, h.args...)
	}
	// The object deja added can be in either file by now: Copilot moves
	// config.json's hooks into settings.json on start, and the record names
	// the file deja wrote.
	if len(hooks) == 0 && !added {
		for _, p := range []string{filepath.Join(filepath.Dir(path), "settings.json"), filepath.Join(filepath.Dir(path), "config.json")} {
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
// as a repository's .github/hooks/*.json.
func setCopilotHook(hooks map[string]any, event, exe string, uninstall bool, args ...string) {
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
		}
		kept = append(kept, entryAny)
	}
	if !uninstall && !found {
		entry := map[string]any{"type": "command", "bash": cmd, "timeoutSec": copilotHookTimeoutSec}
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
