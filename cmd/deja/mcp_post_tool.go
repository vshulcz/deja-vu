package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/vshulcz/deja-vu/internal/atomicfile"
	"github.com/vshulcz/deja-vu/internal/sources"
)

const (
	mcpStagedFile = ".mcp-staged"
	mcpStagedTTL  = 30 * time.Second
)

type mcpStagedEntry struct {
	At   int64  `json:"at"`
	Tool string `json:"tool"`
	Key  string `json:"key"`
	Text string `json:"text"`
}

func mcpStagedPath(dir string) string { return dir + mcpStagedFile }

// isDejaMCPToolCall reports whether a PostToolUse or AfterTool hook payload
// names one of deja's own MCP tools (mcp__deja__deja in Qwen Code,
// mcp_deja_deja or deja__deja in Gemini CLI).
func isDejaMCPToolCall(name string) bool {
	return strings.HasPrefix(name, "mcp__deja__") ||
		strings.HasPrefix(name, "mcp_deja_") ||
		strings.HasPrefix(name, "deja__")
}

// stageMCPToolResult holds a <deja-...> MCP answer for the PostToolUse /
// AfterTool hook that Qwen Code and Gemini CLI fire right after tools/call
// returns. Both harnesses print callToolResult.content uncollapsed in the
// terminal TUI while appending the post-tool hook's additionalContext only to
// the functionResponse sent to the model. When the post-tool hook is wired and
// active, staging the block here lets tools/call return empty display content
// so the TUI shows only the one-line tool header.
func stageMCPToolResult(dir, client, tool string, args json.RawMessage, text string) bool {
	if dir == "" || !strings.HasPrefix(text, "<deja-") || !mcpPostToolHookReady(client) {
		return false
	}
	unlock, _ := holdHookseenLock(dir)
	defer unlock()

	now := time.Now()
	entries := readMCPStagedEntries(dir, now)
	entries = append(entries, mcpStagedEntry{
		At:   now.UnixNano(),
		Tool: tool,
		Key:  mcpStageKey(args),
		Text: text,
	})
	if len(entries) > 16 {
		entries = entries[len(entries)-16:]
	}
	b, err := json.Marshal(entries)
	if err != nil {
		return false
	}
	return atomicfile.Write(mcpStagedPath(dir), b, 0o600) == nil
}

// popStagedMCPToolResult takes the staged MCP answer matching a PostToolUse /
// AfterTool hook payload. Called while holdHookseenLock(dir) is held.
func popStagedMCPToolResult(dir string, raw []byte) string {
	if dir == "" {
		return ""
	}
	now := time.Now()
	entries := readMCPStagedEntries(dir, now)
	if len(entries) == 0 {
		return ""
	}
	var env struct {
		ToolInput json.RawMessage `json:"tool_input"`
	}
	_ = json.Unmarshal(raw, &env)
	want := mcpStageKey(env.ToolInput)

	idx := -1
	for i, e := range entries {
		if e.Key == want {
			idx = i
			break
		}
	}
	if idx < 0 {
		idx = 0
	}
	text := entries[idx].Text
	entries = append(entries[:idx], entries[idx+1:]...)
	if len(entries) == 0 {
		_ = os.Remove(mcpStagedPath(dir))
	} else if b, err := json.Marshal(entries); err == nil {
		_ = atomicfile.Write(mcpStagedPath(dir), b, 0o600)
	}
	return text
}

func readMCPStagedEntries(dir string, now time.Time) []mcpStagedEntry {
	b, err := os.ReadFile(mcpStagedPath(dir))
	if err != nil {
		return nil
	}
	var all []mcpStagedEntry
	if json.Unmarshal(b, &all) != nil {
		return nil
	}
	cutoff := now.Add(-mcpStagedTTL).UnixNano()
	var kept []mcpStagedEntry
	for _, e := range all {
		if e.At >= cutoff && e.Text != "" {
			kept = append(kept, e)
		}
	}
	return kept
}

func mcpStageKey(raw json.RawMessage) string {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || string(trimmed) == "null" {
		return "{}"
	}
	var v any
	if json.Unmarshal(trimmed, &v) != nil {
		return string(trimmed)
	}
	b, err := json.Marshal(v)
	if err != nil {
		return string(trimmed)
	}
	return string(b)
}

// mcpPostToolHookReady reports whether the connected MCP client is Qwen Code
// or Gemini CLI and has its post-tool hook wired to run deja hook-tool on
// deja's MCP tool calls. When unwired or disabled, tools/call returns the text
// inline so a server-only install still answers the model.
func mcpPostToolHookReady(client string) bool {
	c := strings.ToLower(strings.TrimSpace(client))
	switch {
	case strings.HasPrefix(c, "qwen"):
		return qwenMCPPostHookReady()
	case strings.HasPrefix(c, "gemini"):
		return geminiMCPAfterHookReady()
	default:
		return false
	}
}

func qwenMCPPostHookReady() bool {
	b, err := os.ReadFile(filepath.Join(sources.QwenConfigDir(), "settings.json"))
	if err != nil {
		return false
	}
	var root map[string]any
	if json.Unmarshal([]byte(jsoncToJSON(string(b))), &root) != nil {
		return false
	}
	if off, _ := root["disableAllHooks"].(bool); off {
		return false
	}
	if hooksCfg, _ := root["hooksConfig"].(map[string]any); hooksCfg != nil {
		if en, ok := hooksCfg["enabled"].(bool); ok && !en {
			return false
		}
	}
	hooks, _ := root["hooks"].(map[string]any)
	return hookEntriesCoverMCP(hooks["PostToolUse"], "mcp__deja__")
}

func geminiMCPAfterHookReady() bool {
	if sb, err := os.ReadFile(filepath.Join(sources.GeminiHome(), "settings.json")); err == nil {
		var sroot map[string]any
		if json.Unmarshal([]byte(jsoncToJSON(string(sb))), &sroot) == nil {
			if cfg, _ := sroot["hooksConfig"].(map[string]any); cfg != nil {
				if en, ok := cfg["enabled"].(bool); ok && !en {
					return false
				}
			}
		}
	}
	b, err := os.ReadFile(geminiHooksPath())
	if err != nil {
		return false
	}
	var root map[string]any
	if json.Unmarshal([]byte(jsoncToJSON(string(b))), &root) != nil {
		return false
	}
	hooks, _ := root["hooks"].(map[string]any)
	return hookEntriesCoverMCP(hooks["AfterTool"], "mcp_deja_") ||
		hookEntriesCoverMCP(hooks["AfterTool"], "deja__")
}

func hookEntriesCoverMCP(entriesAny any, wantPrefix string) bool {
	entries, _ := entriesAny.([]any)
	for _, entryAny := range entries {
		entry, _ := entryAny.(map[string]any)
		if entry == nil || !entryHasCommand(entry, "deja hook-tool") {
			continue
		}
		m, _ := entry["matcher"].(string)
		m = strings.TrimSpace(m)
		if m == "" || m == "*" || strings.Contains(m, wantPrefix) {
			return true
		}
	}
	return false
}
