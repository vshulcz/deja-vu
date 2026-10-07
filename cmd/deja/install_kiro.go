package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/vshulcz/deja-vu/internal/sources"
)

// Kiro takes MCP servers in a global settings file, the same `mcpServers`
// shape every other client here uses:
//
//	~/.kiro/settings/mcp.json
//
// (kiro.dev's own docs and `kiro-cli mcp add --scope global`, which writes that
// file). The CLI and the IDE read the same one, which is why this is a single
// target rather than one per client the way the reader has two (#3651).
//
// One thing a user has to know and the installer cannot do for them: a custom
// agent — `~/.kiro/agents/<name>.json` — reads this file only when it sets
// `"includeMcpJson": true`. The note says so rather than leaving a reader
// wondering why recall is missing in their agent; the agent kiro-auto writes
// sets it.
func kiroMCPSettingsPath() string {
	return filepath.Join(sources.KiroConfigDir(), "settings", "mcp.json")
}

const kiroAgentNote = "a custom agent in ~/.kiro/agents/*.json reads the global MCP servers only with " +
	"`\"includeMcpJson\": true` in it"

// kiroSteeringPath is Kiro's user-level guidance channel: `~/.kiro/steering`
// is the global half of steering, loaded for every project, and kiro-cli scans
// it alongside the workspace one.
//
// It is not a skill, and the difference matters for what goes in it. Steering
// documents declare an inclusion mode, and the only mode measured as actually
// loaded by kiro-cli is `always` — `manual` is not loaded and cannot be invoked
// from a session, `fileMatch` was withheld (KiroCrew's steering reference,
// measured against kiro-cli 2.19.1). So this text rides in front of every turn
// whether it is wanted or not, which is why it is four lines naming the tool
// rather than the full skill deja writes where a skill is loaded on demand.
func kiroSteeringPath() string {
	return filepath.Join(sources.KiroConfigDir(), "steering", "deja.md")
}

func kiroSteering(exe string) string {
	return fmt.Sprintf(`---
inclusion: always
---

# Past sessions are searchable

This machine indexes every coding session it has, across agents, with deja-vu.
Before debugging an error or re-implementing something, call the deja tool with
mode recall and the user's own words — the specific tokens win.
Before your first edit in a task, and again before you call a change done or
ready to merge, call deja with mode recall and the task's key nouns (file,
package, feature, setting). A rule, a rejected option or a check it returns
outranks your defaults: follow it and say so in one line.
Outside a session: %s search -- "<query>".
`, exe)
}

func kiroSkillPath() string {
	return filepath.Join(sources.KiroConfigDir(), "skills", "deja-history", "SKILL.md")
}

// kiroGlobalHooksPath is the user-level hook file the V3 engine (`--v3`, and
// the IDE) reads for every chat, whatever agent it runs: acp-server.js puts
// ~/.kiro/hooks in globalHookDirs. On a 2.28 stand SessionStart and
// UserPromptSubmit stdout reached the model and SessionEnd fired on leaving
// the TUI. PreToolUse and PostToolUse stdout is dropped by the host
// (sendStdout:false), so there is no pre-edit line here. The V2 engine, still
// the CLI default, ignores this file and runs the deja agent's hooks instead.
func kiroGlobalHooksPath() string {
	return filepath.Join(sources.KiroConfigDir(), "hooks", "deja.json")
}

func kiroGlobalHooksJSON(exe string) (string, error) {
	hook := func(name, trigger string, args ...string) map[string]any {
		return map[string]any{
			"name":    name,
			"trigger": trigger,
			"action":  map[string]any{"type": "command", "command": hookRun(exe, args...)},
		}
	}
	b, err := json.MarshalIndent(map[string]any{
		"version": "v1",
		"hooks": []map[string]any{
			hook("deja-digest", "SessionStart", "hook-context", "--plain"),
			hook("deja-recall", "UserPromptSubmit", "hook-prompt", "--plain"),
			hook("deja-end", "SessionEnd", "hook-session-end"),
		},
	}, "", "  ")
	return string(b) + "\n", err
}

func installKiro(exe string, uninstall bool) (installResult, error) {
	res, err := installMCPJSON(kiroMCPSettingsPath(), exe, uninstall)
	if err != nil {
		return res, err
	}
	steering, err := installTextFile(kiroSteeringPath(), kiroSteering(exe), uninstall)
	if err != nil {
		return installResult{}, err
	}
	// ~/.kiro/skills is loaded by kiro-cli 2.28 in both engines: a probe skill
	// there was listed in `disclose_context` on a stand.
	skill, err := installSkillFile(kiroSkillPath(), uninstall)
	if err != nil {
		return installResult{}, err
	}
	if uninstall {
		return wroteAll(res, steering, skill), nil
	}
	out := wroteAll(res, steering, skill)
	out.Note = joinNotes(out.Note, kiroAgentNote)
	return out, nil
}

// kiro-cli runs the hooks of the agent a chat starts in: an agent file in
// `~/.kiro/agents` takes agentSpawn, userPromptSubmit, preToolUse, postToolUse
// and stop. Measured on 2.22.0, what an agentSpawn or userPromptSubmit hook
// prints goes in front of the model, the first for the whole conversation;
// what preToolUse and postToolUse print does not, so there is no pre-edit
// line here (#4304), and a failed command's fix pair goes out with the next
// prompt instead.
//
// The hooks go in an agent of deja's own rather than into the reader's: the
// built-in kiro_default takes no hooks from a file (a kiro_default.json beside
// it is ignored), and deja does not switch `chat.defaultAgent` for them, since
// that would trade the default agent's prompt for this one. So the agent runs
// when a chat is started in it, and doctor says which of the two it is.
func kiroAgentPath() string {
	return filepath.Join(sources.KiroConfigDir(), "agents", "deja.json")
}

const kiroAgentDescription = "Kiro's tools with deja-vu recall — written by deja install kiro-auto"

const kiroAgentHookTimeoutMs = 10000

func kiroAgentJSON(exe string) (string, error) {
	hook := func(args ...string) []map[string]any {
		return []map[string]any{{"command": hookRun(exe, args...), "timeout_ms": kiroAgentHookTimeoutMs}}
	}
	b, err := json.MarshalIndent(map[string]any{
		"name":           "deja",
		"description":    kiroAgentDescription,
		"tools":          []string{"*"},
		"includeMcpJson": true,
		"hooks": map[string]any{
			"agentSpawn":       hook("hook-context", "--plain"),
			"userPromptSubmit": hook("hook-prompt", "--plain"),
			// What postToolUse prints never reaches the model, so a failed
			// command's fix pair waits for the next userPromptSubmit
			// (hook_deferred.go).
			"postToolUse": hook("hook-tool-after", "--defer"),
		},
	}, "", "  ")
	return string(b) + "\n", err
}

const kiroAutoNote = "the IDE and `kiro-cli --v3` run ~/.kiro/hooks/deja.json in every chat; the default V2 engine " +
	"runs the hooks only in the deja agent: `kiro-cli chat --agent deja`, or `kiro-cli agent set-default deja`"

func installKiroAuto(exe string, uninstall bool) (installResult, error) {
	// The server and steering first: an agent of the reader's own named deja
	// costs only the agent file, not what `deja install kiro` gives them.
	base, err := installKiro(exe, uninstall)
	if err != nil {
		return base, err
	}
	global, err := installKiroGlobalHooks(exe, uninstall)
	if err != nil {
		return installResult{}, err
	}
	base = wroteAll(base, global)
	path := kiroAgentPath()
	if b, err := os.ReadFile(path); err == nil && !strings.Contains(string(b), kiroAgentDescription) {
		if !uninstall {
			base.Note = joinNotes(base.Note, reportPath(path)+" is an agent deja did not write, so it was left as it is — rename it and run this again for the recall hooks")
		}
		return base, nil
	}
	body, err := kiroAgentJSON(hookExeFor(exe, uninstall))
	if err != nil {
		return installResult{}, err
	}
	agent, err := installTextFile(path, body, uninstall)
	if err != nil {
		return installResult{}, err
	}
	if uninstall && agent.Action == "removed" {
		if err := kiroForgetDefaultAgent(); err != nil {
			return installResult{}, err
		}
	}
	out := wroteAll(base, agent)
	if !uninstall {
		out.Note = joinNotes(out.Note, kiroAutoNote)
	}
	return out, nil
}

func installKiroGlobalHooks(exe string, uninstall bool) (installResult, error) {
	path := kiroGlobalHooksPath()
	if b, err := os.ReadFile(path); err == nil && !strings.Contains(string(b), `"deja-digest"`) {
		r := installResult{Path: path, Action: "unchanged"}
		if !uninstall {
			r.Note = reportPath(path) + " is a hook file deja did not write, so it was left as it is"
		}
		return r, nil
	}
	body, err := kiroGlobalHooksJSON(hookExeFor(exe, uninstall))
	if err != nil {
		return installResult{}, err
	}
	return installTextFile(path, body, uninstall)
}

// kiroSettingsPath is kiro-cli's own settings file, where
// `kiro-cli agent set-default` writes `chat.defaultAgent`.
func kiroSettingsPath() string {
	return filepath.Join(sources.KiroConfigDir(), "settings", "cli.json")
}

// kiroDefaultAgent is the agent a plain `kiro-cli chat` starts in, "" for the
// built-in one.
func kiroDefaultAgent() string {
	name, _ := readJSONConfig(kiroSettingsPath())["chat.defaultAgent"].(string)
	return name
}

// kiroForgetDefaultAgent takes `chat.defaultAgent` out when it names deja's
// agent, which uninstall has just removed: left in, kiro-cli is pointed at an
// agent that is gone. Any other value is the reader's.
func kiroForgetDefaultAgent() error {
	if kiroDefaultAgent() != "deja" {
		return nil
	}
	path := kiroSettingsPath()
	old, err := readConfig(path)
	if err != nil {
		return err
	}
	root := map[string]any{}
	if err := json.Unmarshal([]byte(jsoncToJSON(string(old))), &root); err != nil {
		return nil
	}
	delete(root, "chat.defaultAgent")
	next, err := marshalConfigLike(old, root)
	if err != nil {
		return err
	}
	_, err = writeIfChanged(path, old, append(next, '\n'))
	return err
}
