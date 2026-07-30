package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/vshulcz/deja-vu/internal/sources"
)

const (
	guidanceStart = "<!-- deja guidance:start -->"
	guidanceEnd   = "<!-- deja guidance:end -->"
)

const guidanceBody = `Before re-deriving past work, search deja when the user refers to past work, previous sessions, or what was decided before. Use the deja MCP tools:

- recall: search history with a specific error, function, or decision.
- recall_context: get a concise digest of the best matching session.

Example: for "what did we decide about token refresh?", call recall with query "token refresh decision", then call recall_context if the result needs more detail.

When recalled history genuinely helps — a reused fix, a skipped re-debug, even a partial hint that changed your approach — say so to the user in one digest.Short line: "deja-vu recalled: <what> — <how it was reused>". Never credit recalls that did not help.`

func guidancePath(harness string) string {
	switch harness {
	case "claude-code", "claude":
		return filepath.Join(sources.ClaudeConfigDir(), "skills", "deja-history", "SKILL.md")
	case "antigravity":
		// Inside the plugin, not beside it. Antigravity ingests skills/ from a
		// directory marked by plugin.json — which is what `agy plugin validate`
		// confirms ("skills: 1 processed") — and a SKILL.md written one level up
		// is read by nothing. doctor checked the same wrong path, so it reported
		// guidance missing on a machine where the skill was installed and
		// working.
		return filepath.Join(antigravityConfigHome(), "plugins", antigravityPluginName, "skills", "deja-history", "SKILL.md")
	case "copilot":
		return filepath.Join(homeDir(), ".copilot", "skills", "deja-history", "SKILL.md")
	case "pi":
		return filepath.Join(sources.PiConfigDir(), "skills", "deja-history", "SKILL.md")
	case "qwen":
		return filepath.Join(sources.QwenConfigDir(), "QWEN.md")
	case "codex":
		return filepath.Join(sources.CodexHome(), "AGENTS.md")
	case "kimi":
		return filepath.Join(sources.KimiConfigDir(), "AGENTS.md")
	case "gemini":
		return filepath.Join(sources.GeminiHome(), "GEMINI.md")
	case "grok":
		// grok reads <cwd>/.grok/GROK.md first and only falls back to the
		// home copy, so this never shadows a project's own instructions.
		return filepath.Join(sources.GrokHome(), "GROK.md")
	case "opencode":
		return filepath.Join(opencodeConfigHome(), "opencode", "AGENTS.md")
	case "roo":
		// Global rules are read verbatim into the system prompt for every
		// mode and every task, which is the only always-on channel Roo has.
		return rooRulesPath()
	default:
		return ""
	}
}

func opencodeConfigHome() string {
	if xdg := os.Getenv("XDG_CONFIG_HOME"); xdg != "" {
		return xdg
	}
	return filepath.Join(homeDir(), ".config")
}

func guidanceText(harness string) string {
	if harness == "claude-code" || harness == "claude" || harness == "antigravity" || harness == "copilot" || harness == "pi" {
		body := guidanceBody
		if harness == "copilot" {
			body = "deja does not index Copilot history. It is a consumer: use the deja MCP tools to search memory from the other harnesses.\n\n" + guidanceBody
		}
		if harness == "pi" {
			body = `If the deja MCP tools are available (via pi-mcp-adapter), use them:

- recall: search history with a specific error, function, or decision.
- recall_context: get a concise digest of the best matching session.

If MCP is not available, use the deja CLI via bash instead:

- Search: bash("deja 'connection pool exhausted'")
- Context: bash("deja context 'connection pool exhausted'")
- Blame: bash("deja blame src/db.go")
- Remember: bash("deja remember 'we use advisory locks because redis lost messages'")

Example: for "what did we decide about token refresh?", try recall first; if unavailable, run bash("deja 'token refresh decision'").`
		}

		return "---\nname: deja-history\ndescription: Search the user's past AI coding sessions. Use when they say things like 'didn't we fix this before', 'what did we decide about X', or before re-debugging an error that may already be solved.\n---\n\n" + body + "\n"
	}
	return guidanceStart + "\n" + guidanceBody + "\n" + guidanceEnd + "\n"
}

func installGuidance(harness string, uninstall bool) (installResult, error) {
	path := guidancePath(harness)
	if path == "" {
		return installResult{}, nil
	}
	old, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return installResult{}, err
	}
	var next []byte
	if harness == "claude-code" || harness == "claude" || harness == "antigravity" || harness == "copilot" || harness == "pi" {
		if uninstall {
			if len(old) == 0 {
				return installResult{Path: path, Action: "unchanged"}, nil
			}
			if err := os.Remove(path); err != nil {
				return installResult{}, err
			}
			return installResult{Path: path, Action: "removed"}, nil
		} else {
			next = []byte(guidanceText(harness))
			// A skill inside an antigravity plugin directory is ingested only
			// when plugin.json marks that directory as a plugin — without it the
			// whole directory is skipped silently, which `agy plugin validate`
			// reports as "missing plugin.json". `install antigravity-auto`
			// writes the marker; plain `install antigravity` did not, so the
			// guidance landed somewhere nothing would read.
			if harness == "antigravity" {
				if err := ensureAntigravityPluginMarker(); err != nil {
					return installResult{}, err
				}
			}
		}
	} else {
		next = []byte(updateGuidanceBlock(string(old), uninstall))
	}
	a, err := writeIfChanged(path, old, next)
	if err != nil {
		return installResult{}, err
	}
	// grok is three CLIs sharing one directory: the one that still reads
	// GROK.md is not the one being maintained, and grok-dev reads AGENTS.md
	// instead. Writing both is the only way to cover a user of either.
	if harness == "grok" {
		alt := filepath.Join(sources.GrokHome(), "AGENTS.md")
		oldAlt, rerr := os.ReadFile(alt)
		if rerr != nil && !os.IsNotExist(rerr) {
			return installResult{}, rerr
		}
		if _, werr := writeIfChanged(alt, oldAlt, []byte(updateGuidanceBlock(string(oldAlt), uninstall))); werr != nil {
			return installResult{}, werr
		}
	}
	return installResult{Path: path, Action: a}, nil
}

func updateGuidanceBlock(old string, uninstall bool) string {
	newline := "\n"
	if strings.Contains(old, "\r\n") {
		newline = "\r\n"
	}
	start, end := guidanceMarkerLines(old)
	if start >= 0 && end >= 0 {
		old = old[:start] + old[end:]
	}
	if uninstall {
		return old
	}
	old = strings.TrimRight(old, "\r\n")
	if old != "" {
		old += newline + newline
	}
	return old + strings.ReplaceAll(guidanceText("append"), "\n", newline)
}

func guidanceMarkerLines(s string) (start, end int) {
	start, end = -1, -1
	offset := 0
	for _, line := range strings.SplitAfter(s, "\n") {
		content := strings.TrimSuffix(strings.TrimSuffix(line, "\n"), "\r")
		if start < 0 && content == guidanceStart {
			start = offset
		} else if start >= 0 && content == guidanceEnd {
			end = offset + len(line)
			break
		}
		offset += len(line)
	}
	return start, end
}

func guidanceHarness(harness string) string {
	switch harness {
	case "claude-auto":
		return "claude-code"
	case "codex-auto":
		return "codex"
	case "opencode-auto":
		return "opencode"
	default:
		return harness
	}
}

func guidanceStatus(harness string) string {
	path := guidancePath(harness)
	if path == "" {
		return "unsupported"
	}
	if _, err := os.Stat(path); err == nil {
		return "written"
	}
	return "missing"
}

func guidanceResult(harness string, uninstall bool) (installResult, error) {
	canonical := guidanceHarness(harness)
	if canonical == "cursor" {
		return installResult{}, nil
	}
	return installGuidance(canonical, uninstall)
}

func guidanceOutput(harness string, result installResult) string {
	if result.Path == "" {
		return fmt.Sprintf("%s: guidance unsupported", harness)
	}
	return fmt.Sprintf("%s: guidance %s %s", harness, result.Action, result.Path)
}
