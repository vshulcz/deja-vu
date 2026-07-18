package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/sources"
)

type installResult struct{ Path, Action string }

func runInstall(args []string, uninstall bool) error {
	guidance := true
	var targetArgs []string
	for _, arg := range args {
		if arg == "--no-guidance" {
			guidance = false
			continue
		}
		targetArgs = append(targetArgs, arg)
	}
	if len(targetArgs) != 1 {
		if uninstall {
			return fmt.Errorf("uninstall needs target")
		}
		return fmt.Errorf("install needs target")
	}
	targets := []string{targetArgs[0]}
	if targetArgs[0] == "--auto" {
		targets = nil
		for _, t := range existingTargets() {
			switch t {
			case "claude-code":
				targets = append(targets, "claude-auto")
			case "codex":
				targets = append(targets, "codex-auto")
			case "opencode":
				targets = append(targets, "opencode-auto")
			default:
				// cursor, gemini, antigravity, grok: the MCP server is the
				// deepest integration those harnesses support.
				targets = append(targets, t)
			}
		}
		if len(targets) == 0 {
			fmt.Println("no known agent config directories found")
			return nil
		}
	}
	if targetArgs[0] == "--all" {
		targets = existingTargets()
		if len(targets) == 0 {
			fmt.Println("no known agent config directories found")
			return nil
		}
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	exe, _ = filepath.Abs(exe)
	banner := !uninstall && (targetArgs[0] == "--auto" || targetArgs[0] == "--all") && logoWanted(os.Stdout)
	type lineItem struct{ target, action, path string }
	var done []lineItem
	for _, t := range targets {
		r, err := installTarget(t, exe, uninstall)
		if err != nil {
			return err
		}
		if guidance {
			gr, err := guidanceResult(t, uninstall)
			if err != nil {
				return err
			}
			if gr.Path != "" && !banner {
				fmt.Println(guidanceOutput(t, gr))
			}
		}
		if banner {
			done = append(done, lineItem{t, r.Action, shortHome(r.Path)})
		} else if t != "copilot" {
			fmt.Printf("%s: %s %s\n", t, r.Action, r.Path)
		}
	}
	if banner {
		info := append(brandInfo(), "")
		nameW := 0
		for _, d := range done {
			if len(d.target) > nameW {
				nameW = len(d.target)
			}
		}
		for _, d := range done {
			info = append(info, fmt.Sprintf("%-*s  %s%-9s%s %s", nameW, d.target, logoBold, d.action, logoReset, logoDim+d.path+logoReset))
		}
		if hint := installIndexHint(); hint != "" {
			info = append(info, "", hint)
		}
		printLogo(os.Stdout, info)
	}
	return nil
}

// shortHome contracts the home directory to ~ for display.
func shortHome(p string) string {
	if h := homeDir(); h != "" && strings.HasPrefix(p, h) {
		return "~" + strings.TrimPrefix(p, h)
	}
	return p
}

func installIndexHint() string {
	if index.HasManifest(index.DefaultDir()) {
		return ""
	}
	checks := doctorStoreChecks()
	detected := 0
	onlyMissingOrEmpty := true
	var paths []string
	seen := map[string]bool{}
	for _, check := range checks {
		store, _ := inspectDoctorStore(check)
		if store.Files > 0 {
			detected++
		}
		if store.State != "missing" && store.State != "empty" {
			onlyMissingOrEmpty = false
		}
		for _, path := range store.Paths {
			if path != "" && !seen[path] {
				seen[path] = true
				paths = append(paths, shortHome(path))
			}
		}
	}
	if onlyMissingOrEmpty {
		return "no agent history found on this machine; checked " + strings.Join(paths, ", ")
	}
	return fmt.Sprintf("next: run `deja index` to index %d agent stores", detected)
}

func existingTargets() []string {
	checks := map[string]string{
		"claude-code": sources.ClaudeConfigDir(),
		"codex":       sources.CodexHome(),
		"opencode":    filepath.Join(opencodeConfigHome(), "opencode"),
		"cursor":      sources.CursorCLIHome(),
		"gemini":      filepath.Join(sources.GeminiHome(), "settings.json"),
		"antigravity": antigravityConfigHome(),
		"copilot":     filepath.Join(homeDir(), ".copilot"),
		"grok":        sources.GrokRoot(),
		"qwen":        sources.QwenConfigDir(),
	}
	var out []string
	for name, p := range checks {
		if _, err := os.Stat(p); err == nil {
			out = append(out, name)
		} else if name == "claude-code" {
			if _, err := os.Stat(sources.ClaudeJSONPath()); err == nil {
				out = append(out, name)
			}
		}
	}
	sort.Strings(out)
	return out
}

func installTarget(target, exe string, uninstall bool) (installResult, error) {
	switch target {
	case "claude-auto":
		return installClaudeAuto(exe, uninstall)
	case "claude-code", "claude":
		return installClaude(exe, uninstall)
	case "codex":
		return installCodex(exe, uninstall)
	case "codex-auto":
		return installCodexAuto(exe, uninstall)
	case "cursor":
		return installCursor(exe, uninstall)
	case "gemini":
		return installMCPJSON(filepath.Join(sources.GeminiHome(), "settings.json"), exe, uninstall)
	case "antigravity":
		return installMCPJSON(filepath.Join(antigravityConfigHome(), "mcp_config.json"), exe, uninstall)
	case "grok":
		return installGrok(exe, uninstall)
	case "qwen":
		return installMCPJSON(filepath.Join(sources.QwenConfigDir(), "settings.json"), exe, uninstall)
	case "copilot":
		return installResult{Path: guidancePath("copilot"), Action: "guidance-only"}, nil
	case "opencode":
		return installOpencode(exe, uninstall)
	case "opencode-auto":
		return installOpencodeAuto(exe, uninstall)
	case "statusline":
		return installStatusline(exe, uninstall)
	default:
		return installResult{}, fmt.Errorf("unknown target %q", target)
	}
}

func installClaudeAuto(exe string, uninstall bool) (installResult, error) {
	if _, err := installClaude(exe, uninstall); err != nil {
		return installResult{}, err
	}
	return installClaudeHook(exe, uninstall)
}

func installCodexAuto(exe string, uninstall bool) (installResult, error) {
	if _, err := installCodex(exe, uninstall); err != nil {
		return installResult{}, err
	}
	return installCodexHooks(exe, uninstall)
}

func installOpencodeAuto(exe string, uninstall bool) (installResult, error) {
	if _, err := installOpencode(exe, uninstall); err != nil {
		return installResult{}, err
	}
	return installOpencodePlugin(exe, uninstall)
}

func backupOnce(path string) error {
	if _, err := os.Stat(path); err != nil {
		return nil
	}
	bak := path + ".bak"
	if _, err := os.Stat(bak); err == nil {
		return nil
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return os.WriteFile(bak, b, 0o644)
}

func writeIfChanged(path string, old, next []byte) (string, error) {
	if bytes.Equal(old, next) {
		return "unchanged", nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", err
	}
	if err := backupOnce(path); err != nil {
		return "", err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".deja-tmp-")
	if err != nil {
		return "", err
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }()
	if _, err := tmp.Write(next); err != nil {
		_ = tmp.Close()
		return "", err
	}
	if err := tmp.Chmod(0o644); err != nil {
		_ = tmp.Close()
		return "", err
	}
	if err := tmp.Close(); err != nil {
		return "", err
	}
	if err := os.Rename(tmpName, path); err != nil {
		return "", err
	}
	if len(old) == 0 {
		return "created", nil
	}
	return "updated", nil
}

func installClaude(exe string, uninstall bool) (installResult, error) {
	path := sources.ClaudeJSONPath()
	old, _ := os.ReadFile(path)
	var root map[string]any
	if len(bytes.TrimSpace(old)) == 0 {
		root = map[string]any{}
	} else if err := json.Unmarshal(old, &root); err != nil {
		return installResult{}, err
	}
	m, _ := root["mcpServers"].(map[string]any)
	if m == nil {
		m = map[string]any{}
		root["mcpServers"] = m
	}
	if uninstall {
		delete(m, "deja")
	} else {
		m["deja"] = map[string]any{"type": "stdio", "command": exe, "args": []string{"mcp"}}
	}
	next, err := json.MarshalIndent(root, "", "  ")
	if err != nil {
		return installResult{}, err
	}
	next = append(next, '\n')
	a, err := writeIfChanged(path, old, next)
	return installResult{Path: path, Action: a}, err
}

func installClaudeHook(exe string, uninstall bool) (installResult, error) {
	path := filepath.Join(sources.ClaudeConfigDir(), "settings.json")
	old, _ := os.ReadFile(path)
	var root map[string]any
	if len(bytes.TrimSpace(old)) == 0 {
		root = map[string]any{}
	} else if err := json.Unmarshal(old, &root); err != nil {
		return installResult{}, err
	}
	nextRoot := updateClaudeSessionStartHook(root, exe, uninstall)
	next, err := json.MarshalIndent(nextRoot, "", "  ")
	if err != nil {
		return installResult{}, err
	}
	next = append(next, '\n')
	a, err := writeIfChanged(path, old, next)
	return installResult{Path: path, Action: a}, err
}

func updateClaudeSessionStartHook(root map[string]any, exe string, uninstall bool) map[string]any {
	hooks, _ := root["hooks"].(map[string]any)
	if hooks == nil {
		hooks = map[string]any{}
		root["hooks"] = hooks
	}
	cmd := exe + " hook-context"
	entries, _ := hooks["SessionStart"].([]any)
	var out []any
	found := false
	for _, entryAny := range entries {
		entry, _ := entryAny.(map[string]any)
		if entry == nil {
			out = append(out, entryAny)
			continue
		}
		hs, _ := entry["hooks"].([]any)
		var kept []any
		removed := false
		for _, hAny := range hs {
			h, _ := hAny.(map[string]any)
			if h != nil && h["type"] == "command" && h["command"] == cmd {
				found = true
				if uninstall {
					removed = true
					continue
				}
			}
			kept = append(kept, hAny)
		}
		if removed {
			if len(kept) == 0 && len(entry) == 1 {
				continue
			}
			entry["hooks"] = kept
		}
		out = append(out, entry)
	}
	if !uninstall && !found {
		out = append(out, map[string]any{"hooks": []any{map[string]any{"type": "command", "command": cmd}}})
	}
	if len(out) == 0 {
		delete(hooks, "SessionStart")
	} else {
		hooks["SessionStart"] = out
	}
	if len(hooks) == 0 {
		delete(root, "hooks")
	}
	return root
}

// installStatusline wires `deja statusline` as the Claude Code status bar.
// It refuses to replace a statusline the user already configured (many run
// ccstatusline or their own script) — printing how to combine instead.
func installStatusline(exe string, uninstall bool) (installResult, error) {
	path := filepath.Join(sources.ClaudeConfigDir(), "settings.json")
	old, _ := os.ReadFile(path)
	var root map[string]any
	if len(bytes.TrimSpace(old)) == 0 {
		root = map[string]any{}
	} else if err := json.Unmarshal(old, &root); err != nil {
		return installResult{}, err
	}
	cmd := exe + " statusline"
	existing, _ := root["statusLine"].(map[string]any)
	if uninstall {
		if existing == nil || existing["command"] != cmd {
			return installResult{Path: path, Action: "unchanged"}, nil
		}
		delete(root, "statusLine")
	} else {
		if existing != nil && existing["command"] != cmd {
			return installResult{}, fmt.Errorf("a statusline is already configured (%v) — append `deja statusline` output to it instead of replacing it", existing["command"])
		}
		root["statusLine"] = map[string]any{"type": "command", "command": cmd}
	}
	next, err := json.MarshalIndent(root, "", "  ")
	if err != nil {
		return installResult{}, err
	}
	next = append(next, '\n')
	a, err := writeIfChanged(path, old, next)
	return installResult{Path: path, Action: a}, err
}

func installCodex(exe string, uninstall bool) (installResult, error) {
	path := filepath.Join(sources.CodexHome(), "config.toml")
	block := fmt.Sprintf("[mcp_servers.deja]\ntype = \"stdio\"\ncommand = %q\nargs = [\"mcp\"]\n", exe)
	return installTOML(path, block, uninstall)
}

// installGrok wires the MCP server into Grok Build's config.toml.
// Same [mcp_servers.NAME] TOML shape as Codex; Grok's hook stdout is ignored
// on passive events, so MCP is the deepest integration available.
func installGrok(exe string, uninstall bool) (installResult, error) {
	path := filepath.Join(sources.GrokHome(), "config.toml")
	block := fmt.Sprintf("[mcp_servers.deja]\ncommand = %q\nargs = [\"mcp\"]\n", exe)
	return installTOML(path, block, uninstall)
}

func installTOML(path, block string, uninstall bool) (installResult, error) {
	old, _ := os.ReadFile(path)
	s := removeCodexDejaBlock(string(old))
	s = strings.TrimRight(s, "\n")
	if !uninstall {
		if s != "" {
			s += "\n\n"
		}
		s += block
	} else if s != "" {
		s += "\n"
	}
	a, err := writeIfChanged(path, old, []byte(s))
	return installResult{Path: path, Action: a}, err
}

func removeCodexDejaBlock(s string) string {
	lines := strings.Split(s, "\n")
	var out []string
	for i := 0; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) != "[mcp_servers.deja]" {
			out = append(out, lines[i])
			continue
		}
		i++
		for i < len(lines) && !strings.HasPrefix(strings.TrimSpace(lines[i]), "[") {
			i++
		}
		i--
	}
	return strings.Join(out, "\n")
}

func homeDir() string {
	h, _ := os.UserHomeDir()
	return h
}

func antigravityConfigHome() string {
	return filepath.Join(homeDir(), ".gemini", "config")
}

// installCursor wires the MCP server into Cursor's global config
// (~/.cursor/mcp.json). Gemini CLI and Antigravity use the identical
// mcpServers shape in their own files.
func installCursor(exe string, uninstall bool) (installResult, error) {
	return installMCPJSON(filepath.Join(sources.CursorCLIHome(), "mcp.json"), exe, uninstall)
}

func installMCPJSON(path, exe string, uninstall bool) (installResult, error) {
	old, _ := os.ReadFile(path)
	var root map[string]any
	if len(bytes.TrimSpace(old)) == 0 {
		root = map[string]any{}
	} else if err := json.Unmarshal(old, &root); err != nil {
		return installResult{}, err
	}
	m, _ := root["mcpServers"].(map[string]any)
	if m == nil {
		m = map[string]any{}
		root["mcpServers"] = m
	}
	if uninstall {
		delete(m, "deja")
	} else {
		m["deja"] = map[string]any{"command": exe, "args": []string{"mcp"}}
	}
	next, err := json.MarshalIndent(root, "", "  ")
	if err != nil {
		return installResult{}, err
	}
	next = append(next, '\n')
	a, err := writeIfChanged(path, old, next)
	return installResult{Path: path, Action: a}, err
}

func installOpencode(exe string, uninstall bool) (installResult, error) {
	dir := filepath.Join(opencodeConfigHome(), "opencode")
	path := filepath.Join(dir, "opencode.json")
	if _, err := os.Stat(path); err != nil {
		if _, e := os.Stat(filepath.Join(dir, "opencode.jsonc")); e == nil {
			path = filepath.Join(dir, "opencode.jsonc")
		}
	}
	old, _ := os.ReadFile(path)
	var next []byte
	var err error
	if strings.HasSuffix(path, ".jsonc") {
		next = updateOpencodeJSONC(old, exe, uninstall)
	} else {
		next, err = updateOpencodeJSON(old, exe, uninstall)
		if err != nil {
			return installResult{}, err
		}
	}
	a, err := writeIfChanged(path, old, next)
	return installResult{Path: path, Action: a}, err
}

func updateOpencodeJSON(old []byte, exe string, uninstall bool) ([]byte, error) {
	var root map[string]any
	if len(bytes.TrimSpace(old)) == 0 {
		root = map[string]any{}
	} else if err := json.Unmarshal(old, &root); err != nil {
		return nil, err
	}
	m, _ := root["mcp"].(map[string]any)
	if m == nil {
		m = map[string]any{}
		root["mcp"] = m
	}
	if uninstall {
		delete(m, "deja")
	} else {
		m["deja"] = map[string]any{"type": "local", "command": []string{exe, "mcp"}}
	}
	next, err := json.MarshalIndent(root, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(next, '\n'), nil
}

func updateOpencodeJSONC(old []byte, exe string, uninstall bool) []byte {
	line := fmt.Sprintf(`    "deja": {"type":"local","command":[%q,"mcp"]}`, exe)
	s := string(old)
	if strings.TrimSpace(s) == "" {
		if uninstall {
			return []byte("{}\n")
		}
		return []byte("{\n  \"mcp\": {\n" + line + "\n  }\n}\n")
	}
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	start, end := -1, -1
	for i, l := range lines {
		if strings.Contains(l, `"mcp"`) && strings.Contains(l, "{") {
			start = i
			depth := strings.Count(l, "{") - strings.Count(l, "}")
			for j := i + 1; j < len(lines); j++ {
				depth += strings.Count(lines[j], "{") - strings.Count(lines[j], "}")
				if depth <= 0 {
					end = j
					break
				}
			}
			break
		}
	}
	if start >= 0 && end > start {
		var body []string
		for _, l := range lines[start+1 : end] {
			if !strings.Contains(l, `"deja"`) {
				body = append(body, l)
			}
		}
		if !uninstall {
			for i := len(body) - 1; i >= 0; i-- {
				trim := strings.TrimSpace(body[i])
				if trim != "" && !strings.HasPrefix(trim, "//") && !strings.HasSuffix(trim, ",") {
					body[i] += ","
					break
				}
			}
			body = append(body, line)
		}
		out := append([]string{}, lines[:start+1]...)
		out = append(out, body...)
		out = append(out, lines[end:]...)
		return []byte(strings.Join(out, "\n") + "\n")
	}
	if uninstall {
		return []byte(strings.Join(lines, "\n") + "\n")
	}
	insert := len(lines) - 1
	comma := ""
	for i := insert - 1; i >= 0; i-- {
		trim := strings.TrimSpace(lines[i])
		if trim != "" && !strings.HasPrefix(trim, "//") && !strings.HasSuffix(trim, ",") && trim != "{" {
			lines[i] += ","
			break
		}
	}
	mcp := []string{comma + `  "mcp": {`, line, "  }"}
	out := append([]string{}, lines[:insert]...)
	out = append(out, mcp...)
	out = append(out, lines[insert:]...)
	return []byte(strings.Join(out, "\n") + "\n")
}
