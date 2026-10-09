package main

import (
	"os/exec"
	"sort"
	"strings"
)

// agentNames is the registry's display_name for every harness, which the
// interactive screen prints in place of ids. TestAgentNamesMatchRegistry keeps
// the two in step.
var agentNames = map[string]string{
	"aider": "aider", "amp": "Amp", "antigravity": "Antigravity", "claude": "Claude Code",
	"cline": "Cline", "codex": "Codex CLI", "copilot": "Copilot CLI", "copilot-chat": "VS Code Copilot Chat",
	"cursor": "Cursor", "deepseek": "DeepSeek Harness", "gemini": "Gemini CLI", "goose": "Goose",
	"grok": "Grok Build", "hermes": "Hermes", "kimi": "Kimi Code", "omp": "omp (Oh My Pi)",
	"openclaw": "OpenClaw", "opencode": "opencode", "continue": "Continue", "crush": "Crush",
	"pi": "pi", "prime": "prime-agent (PrimeIntellect)", "qwen": "Qwen Code", "cherrystudio": "Cherry Studio",
	"senpi": "Senpi", "gjc": "gajae-code", "kimchi": "Kimchi Coding", "commandcode": "Command Code",
	"zcode": "ZCode", "kiro": "Kiro", "kilocode": "Kilo Code", "roo": "Roo Code", "zed": "Zed",
	"devin": "Devin CLI", "codewhale": "CodeWhale", "junie": "Junie", "jetbrains": "JetBrains AI Assistant",
	"codebuddy": "CodeBuddy Code", "reasonix": "Reasonix", "trae": "TRAE CLI", "muse": "Muse Code",
}

// agentName is the short form for a chip or a card: the parenthesised gloss
// some registry names carry is dropped.
func agentName(h string) string {
	n, ok := agentNames[h]
	if !ok {
		return h
	}
	if i := strings.Index(n, " ("); i > 0 {
		return n[:i]
	}
	return n
}

// continueTarget is one agent the continue-in list offers.
type continueTarget struct {
	id        string
	installed bool
	paste     bool
}

// continueTargets lists every agent a session can be handed to: the ones whose
// command is on PATH first, the ones the reader uses most leading, then the
// rest by name, then the ones that only take a paste.
func continueTargets(lookPath func(string) (string, error), used map[string]int) []continueTarget {
	if lookPath == nil {
		lookPath = exec.LookPath
	}
	var out []continueTarget
	for _, id := range handoffTargets() {
		t := continueTarget{id: id}
		if argv, ok := handoffCommand(id, ""); ok && len(argv) > 0 {
			_, err := lookPath(argv[0])
			t.installed = err == nil
		}
		out = append(out, t)
	}
	for id := range handoffPasteOnly {
		out = append(out, continueTarget{id: id, paste: true})
	}
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.paste != b.paste {
			return !a.paste
		}
		if a.installed != b.installed {
			return a.installed
		}
		if used[a.id] != used[b.id] {
			return used[a.id] > used[b.id]
		}
		return strings.ToLower(agentName(a.id)) < strings.ToLower(agentName(b.id))
	})
	return out
}
