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

Example: for "what did we decide about token refresh?", call recall with query "token refresh decision", then call recall_context if the result needs more detail.`

func guidancePath(harness string) string {
	switch harness {
	case "claude-code", "claude":
		return filepath.Join(sources.ClaudeConfigDir(), "skills", "deja-history", "SKILL.md")
	case "codex":
		return filepath.Join(sources.CodexHome(), "AGENTS.md")
	case "gemini":
		return filepath.Join(sources.GeminiHome(), "GEMINI.md")
	case "opencode":
		return filepath.Join(opencodeConfigHome(), "opencode", "AGENTS.md")
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
	if harness == "claude-code" || harness == "claude" {
		return "---\ntitle: deja-history\ndescription: Consult deja MCP history tools when the user refers to past work or previous decisions.\n---\n\n" + guidanceBody + "\n"
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
	if harness == "claude-code" || harness == "claude" {
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
		}
	} else {
		next = []byte(updateGuidanceBlock(string(old), uninstall))
	}
	a, err := writeIfChanged(path, old, next)
	return installResult{Path: path, Action: a}, err
}

func updateGuidanceBlock(old string, uninstall bool) string {
	newline := "\n"
	if strings.Contains(old, "\r\n") {
		newline = "\r\n"
	}
	start := strings.Index(old, guidanceStart)
	if start >= 0 {
		end := strings.Index(old[start+len(guidanceStart):], guidanceEnd)
		if end >= 0 {
			end += start + len(guidanceStart) + len(guidanceEnd)
			old = old[:start] + old[end:]
		}
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
	if canonical == "cursor" || canonical == "grok" || canonical == "antigravity" {
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
