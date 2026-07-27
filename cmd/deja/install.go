package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"github.com/vshulcz/deja-vu/internal/digest"
	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/sources"
)

type installResult struct{ Path, Action string }

func runInstall(dir string, args []string, uninstall bool) error {
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
			case "gemini":
				targets = append(targets, "gemini-auto")
			case "qwen":
				targets = append(targets, "qwen-auto")
			case "kimi":
				targets = append(targets, "kimi-auto")
			case "cursor":
				targets = append(targets, "cursor-auto")
			case "pi":
				targets = append(targets, "pi-auto")
			case "hermes":
				targets = append(targets, "hermes-auto")
			case "openclaw":
				targets = append(targets, "openclaw-auto")
			case "antigravity":
				targets = append(targets, "antigravity-auto")
			case "cline":
				targets = append(targets, "cline", "cline-auto")
			default:
				// grok and the IDE extensions: the MCP server is the deepest
				// integration those harnesses support.
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
	guidanceCount := 0
	mcpCount := 0
	hookCount := 0
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
			if gr.Path != "" && !uninstall {
				guidanceCount++
			}
		}
		if banner {
			done = append(done, lineItem{t, r.Action, shortHome(r.Path)})
		} else {
			fmt.Printf("%s: %s %s\n", t, r.Action, r.Path)
		}
		if !uninstall && t != "statusline" {
			mcpCount++
		}
		if !uninstall && strings.HasSuffix(t, "-auto") {
			hookCount++
		}
	}
	if !uninstall && (targetArgs[0] == "--auto" || targetArgs[0] == "--all") {
		installIndexWarmup(dir, mcpCount, hookCount, guidanceCount)
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
		if hint := installIndexHint(dir); hint != "" {
			info = append(info, "", hint)
		}
		printLogo(os.Stdout, info)
	}
	return nil
}

func installIndexWarmup(dir string, mcp, hooks, guidance int) {
	built := false
	detected := 0
	if !index.HasManifest(dir) {
		for _, check := range doctorStoreChecks() {
			store, _ := inspectDoctorStore(check)
			if store.Files > 0 {
				detected++
				if err := index.Ensure(dir, "", false, os.Stderr); err == nil {
					built = true
				}
				break
			}
		}
	}
	fmt.Fprintf(os.Stderr, "installed: %d MCP, %d hooks, %d guidance files\n", mcp, hooks, guidance)
	if built {
		b := index.LastBuild
		fmt.Fprintf(os.Stderr, "index: built (%d sessions, %d messages)\n", b.Sessions, b.Messages)
	} else if !index.HasManifest(dir) && detected > 0 {
		fmt.Fprintln(os.Stderr, "next: run `deja index` to finish building memory")
	} else if !index.HasManifest(dir) {
		fmt.Fprintln(os.Stderr, "index: no agent history detected")
	} else {
		fmt.Fprintln(os.Stderr, "index: already built")
	}
	fmt.Fprintln(os.Stderr, "try: deja \"something you fixed weeks ago\"")
	printInstallProof(dir)
}

// printInstallProof shows the "starts full" moment right in the install: a
// few real sessions deja already indexed from this machine's history, so the
// value is visible before the first agent session ever runs.
func printInstallProof(dir string) {
	if !index.HasManifest(dir) {
		return
	}
	recent, err := index.Recent(dir, 12)
	if err != nil || len(recent) == 0 {
		return
	}
	seenProject := map[string]bool{}
	shown := 0
	var lines []string
	for _, s := range recent {
		if seenProject[s.Project] || s.Project == "" || s.Project == "-" {
			continue
		}
		title := firstUserTitle(s)
		if title == "" {
			title = s.Title
		}
		if title == "" {
			continue
		}
		if len(title) > 76 {
			title = digest.UTF8SafeCut(title, 76) + "…"
		}
		seenProject[s.Project] = true
		date := ""
		if !s.Updated.IsZero() {
			date = s.Updated.Format("Jan 2")
		}
		lines = append(lines, fmt.Sprintf("  [%s · %s · %s] %s", s.Harness, s.Project, date, title))
		shown++
		if shown == 3 {
			break
		}
	}
	if len(lines) == 0 {
		return
	}
	fmt.Fprintln(os.Stderr, "\ndeja already knows this machine:")
	for _, l := range lines {
		fmt.Fprintln(os.Stderr, l)
	}
	fmt.Fprintln(os.Stderr, "ask your agent about any of these — it will remember.")
}

// shortHome contracts the home directory to ~ for display.
func shortHome(p string) string {
	if h := homeDir(); h != "" && strings.HasPrefix(p, h) {
		return "~" + strings.TrimPrefix(p, h)
	}
	return p
}

func installIndexHint(dir string) string {
	if index.HasManifest(dir) {
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
		"kimi":        sources.KimiConfigDir(),
		"cline":       sources.ClineConfigDir(),
		"hermes":      sources.HermesHome(),
		"pi":          sources.PiConfigDir(),
		"openclaw":    sources.OpenClawStateDir(),
		// aider keeps no config directory: its history file is what says it
		// has been used here. The binary alone would match every machine that
		// merely has it on PATH.
		"aider": filepath.Join(homeDir(), ".aider.chat.history.md"),
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
	case "cursor-auto":
		return installCursorAuto(exe, uninstall)
	case "gemini":
		return installMCPJSON(filepath.Join(sources.GeminiHome(), "settings.json"), exe, uninstall)
	case "gemini-auto":
		return installGeminiAuto(exe, uninstall)
	case "antigravity":
		return installMCPJSON(filepath.Join(antigravityConfigHome(), "mcp_config.json"), exe, uninstall)
	case "antigravity-auto":
		return installAntigravityAuto(exe, uninstall)
	case "grok":
		return installGrok(exe, uninstall)
	case "qwen":
		return installMCPJSON(filepath.Join(sources.QwenConfigDir(), "settings.json"), exe, uninstall)
	case "qwen-auto":
		return installQwenAuto(exe, uninstall)
	case "kimi":
		return installMCPJSON(filepath.Join(sources.KimiConfigDir(), "mcp.json"), exe, uninstall)
	case "kimi-auto":
		return installKimiAuto(exe, uninstall)
	case "cline":
		return installMCPJSON(sources.ClineMCPSettingsPath(), exe, uninstall)
	case "cline-auto":
		return installClineAuto(exe, uninstall)
	case "copilot":
		return installCopilotMCP(exe, uninstall)
	case "hermes":
		return installHermesMCP(exe, uninstall)
	case "hermes-auto":
		return installHermesAuto(exe, uninstall)
	case "pi":
		return installMCPJSON(filepath.Join(sources.PiConfigDir(), "mcp.json"), exe, uninstall)
	case "pi-auto":
		return installPiAuto(exe, uninstall)
	case "openclaw":
		return installOpenClawMCP(exe, uninstall)
	case "openclaw-auto":
		return installOpenClawAuto(exe, uninstall)
	case "opencode":
		return installOpencode(exe, uninstall)
	case "opencode-auto":
		return installOpencodeAuto(exe, uninstall)
	case "aider":
		return installAider(exe, uninstall)
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
	if _, err := installClaudeCommands(exe, uninstall); err != nil {
		return installResult{}, err
	}
	return installClaudeHook(exe, uninstall)
}

func installAntigravityAuto(exe string, uninstall bool) (installResult, error) {
	if _, err := installMCPJSON(filepath.Join(antigravityConfigHome(), "mcp_config.json"), exe, uninstall); err != nil {
		return installResult{}, err
	}
	return installAntigravityPlugin(exe, uninstall)
}

func installOpenClawAuto(exe string, uninstall bool) (installResult, error) {
	if _, err := installOpenClawMCP(exe, uninstall); err != nil {
		return installResult{}, err
	}
	return installOpenClawHooks(exe, uninstall)
}

func installHermesAuto(exe string, uninstall bool) (installResult, error) {
	if _, err := installHermesMCP(exe, uninstall); err != nil {
		return installResult{}, err
	}
	return installHermesPlugin(exe, uninstall)
}

func installPiAuto(exe string, uninstall bool) (installResult, error) {
	if _, err := installMCPJSON(filepath.Join(sources.PiConfigDir(), "mcp.json"), exe, uninstall); err != nil {
		return installResult{}, err
	}
	return installPiExtension(exe, uninstall)
}

func installCursorAuto(exe string, uninstall bool) (installResult, error) {
	if _, err := installCursor(exe, uninstall); err != nil {
		return installResult{}, err
	}
	return installCursorHooks(exe, uninstall)
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
	// Configs can carry MCP credentials; the snapshot is owner-only even
	// when the live file is looser.
	return os.WriteFile(bak, b, 0o600)
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
	// Preserve the live file's mode; brand-new configs start owner-only
	// because they may carry MCP credentials.
	mode := os.FileMode(0o600)
	if fi, err := os.Stat(path); err == nil {
		mode = fi.Mode().Perm()
	}
	if err := tmp.Chmod(mode); err != nil {
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
		m["deja"] = mcpServerEntry(exe)
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
	nextRoot = updateClaudeHook(nextRoot, "PreCompact", exe+" hook-precompact", "manual|auto", uninstall)
	nextRoot = updateClaudeHook(nextRoot, "UserPromptSubmit", exe+" hook-prompt", "", uninstall)
	next, err := json.MarshalIndent(nextRoot, "", "  ")
	if err != nil {
		return installResult{}, err
	}
	next = append(next, '\n')
	a, err := writeIfChanged(path, old, next)
	return installResult{Path: path, Action: a}, err
}

func updateClaudeSessionStartHook(root map[string]any, exe string, uninstall bool) map[string]any {
	root = updateClaudeHook(root, "SessionStart", exe+" hook-context", "", uninstall)
	return root
}

// hookStatusMessage labels the hook while it runs. Codex carries it through
// to its hook run summary; Claude Code 2.1.220 parses the field but shows
// nothing for it — there the receipt in systemMessage is what the user sees,
// which was checked by rendering the actual TUI rather than by reading docs.
func hookStatusMessage(event string) string {
	switch event {
	case "SessionStart":
		return "Recalling past sessions…"
	case "UserPromptSubmit":
		return "Searching past sessions…"
	case "PreCompact":
		return "Saving this session to memory…"
	}
	return ""
}

// isDejaHookCommand reports whether an existing hook entry is one of ours for
// the same subcommand. Matching on the trailing subcommand rather than the
// whole string means moving the binary replaces the old entry instead of
// leaving a duplicate that fires alongside the new one.
func isDejaHookCommand(existing any, cmd string) bool {
	s, ok := existing.(string)
	if !ok {
		return false
	}
	if s == cmd {
		return true
	}
	sub := cmd[strings.LastIndex(cmd, " ")+1:]
	return strings.HasSuffix(s, " "+sub) && strings.Contains(s, "deja")
}

func updateClaudeHook(root map[string]any, event, cmd, matcher string, uninstall bool) map[string]any {
	hooks, _ := root["hooks"].(map[string]any)
	if hooks == nil {
		hooks = map[string]any{}
		root["hooks"] = hooks
	}
	entries, _ := hooks[event].([]any)
	var out []any
	found := false
	for _, entryAny := range entries {
		entry, _ := entryAny.(map[string]any)
		if entry == nil {
			out = append(out, entryAny)
			continue
		}
		hs, hasHooks := entry["hooks"].([]any)
		// Self-heal: an entry whose hooks are null/absent is invalid — Claude
		// Code rejects the whole file over it. Earlier deja versions could
		// leave one behind on uninstall when the entry carried a matcher.
		if !hasHooks || len(hs) == 0 {
			continue
		}
		var kept []any
		removed := false
		for _, hAny := range hs {
			h, _ := hAny.(map[string]any)
			if h != nil && h["type"] == "command" && isDejaHookCommand(h["command"], cmd) {
				if uninstall {
					removed = true
					continue
				}
				// An entry of ours for this event already exists. Take it over
				// rather than adding a second one: installing from a new path
				// used to leave the old entry behind, and both would fire.
				found = true
				h["command"] = cmd
				if msg := hookStatusMessage(event); msg != "" {
					h["statusMessage"] = msg
				}
			}
			kept = append(kept, hAny)
		}
		if len(kept) != len(hs) || found {
			entry["hooks"] = kept
		}
		if removed {
			if len(kept) == 0 {
				continue
			}
			entry["hooks"] = kept
		}
		out = append(out, entry)
	}
	if !uninstall && !found {
		h := map[string]any{"type": "command", "command": cmd}
		if msg := hookStatusMessage(event); msg != "" {
			h["statusMessage"] = msg
		}
		entry := map[string]any{"hooks": []any{h}}
		if matcher != "" {
			entry["matcher"] = matcher
		}
		out = append(out, entry)
	}
	if len(out) == 0 {
		delete(hooks, event)
	} else {
		hooks[event] = out
	}
	if len(hooks) == 0 {
		delete(root, "hooks")
	}
	return root
}

// combinedStatusline builds a command that runs the existing statusline and
// deja's, separated by a middle dot.
func combinedStatusline(existing, exe string) string {
	return fmt.Sprintf(`sh -c 'json=$(cat); printf "%%s" "$json" | %s; printf " · "; printf "%%s" "$json" | %s statusline'`, existing, exe)
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
			// Most people already run something here. Replacing it silently
			// would be rude, and "append our output" is not actionable — so
			// hand over the line that runs both. Claude pipes session JSON to
			// the command, so it is captured once and fed to each in turn.
			prev, _ := existing["command"].(string)
			return installResult{}, fmt.Errorf("a statusline is already configured — to keep both, set statusLine.command to:\n\n  %s", combinedStatusline(prev, exe))
		}
		// refreshInterval makes Claude Code re-run the command on a timer
		// instead of only after a turn, so the first index build shows a
		// moving bar rather than a number frozen at whatever it was when the
		// user last typed.
		root["statusLine"] = map[string]any{"type": "command", "command": cmd, "refreshInterval": 1000}
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
	cmd, args := mcpCommandArgs(exe)
	block := fmt.Sprintf("[mcp_servers.deja]\ntype = \"stdio\"\ncommand = %q\nargs = %s\n", cmd, tomlStringArray(args))
	return installTOML(path, block, uninstall)
}

// tomlStringArray renders a Go string slice as a TOML inline array, e.g.
// ["/c", "deja", "mcp"], so the Windows cmd /c shim survives the config write.
func tomlStringArray(xs []string) string {
	quoted := make([]string, len(xs))
	for i, x := range xs {
		quoted[i] = fmt.Sprintf("%q", x)
	}
	return "[" + strings.Join(quoted, ", ") + "]"
}

// installGrok wires the MCP server into Grok Build's config.toml.
// Same [mcp_servers.NAME] TOML shape as Codex; Grok's hook stdout is ignored
// on passive events, so MCP is the deepest integration available.
func installGrok(exe string, uninstall bool) (installResult, error) {
	path := filepath.Join(sources.GrokHome(), "config.toml")
	cmd, args := mcpCommandArgs(exe)
	block := fmt.Sprintf("[mcp_servers.deja]\ncommand = %q\nargs = %s\n", cmd, tomlStringArray(args))
	res, err := installTOML(path, block, uninstall)
	if err != nil {
		return res, err
	}
	// The other CLI sharing this directory reads a different file entirely.
	user, uerr := installGrokUserSettings(exe, uninstall)
	if uerr != nil {
		return res, uerr
	}
	if res.Action == "unchanged" {
		return user, nil
	}
	return res, nil
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
// mcpServerEntry is the JSON mcpServers value for deja. Windows stdio MCP
// clients (Claude Code among them) spawn through cmd, so the entry uses the
// cmd /c wrapper there; elsewhere it is the executable directly.
func mcpServerEntry(exe string) map[string]any {
	command, args := mcpCommandArgs(exe)
	return map[string]any{"type": "stdio", "command": command, "args": args}
}

func mcpCommandArgs(exe string) (string, []string) {
	if runtime.GOOS == "windows" {
		return "cmd", []string{"/c", exe, "mcp"}
	}
	return exe, []string{"mcp"}
}

// mcpServers shape in their own files.
func installCursor(exe string, uninstall bool) (installResult, error) {
	return installMCPJSON(filepath.Join(sources.CursorCLIHome(), "mcp.json"), exe, uninstall)
}

// installCopilotMCP wires deja into GitHub Copilot CLI's MCP registry
// (~/.copilot/mcp-config.json). Copilot's schema differs from the common
// mcpServers shape: entries carry a type and an enabled-tools list.
func installCopilotMCP(exe string, uninstall bool) (installResult, error) {
	path := filepath.Join(sources.Home(), ".copilot", "mcp-config.json")
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
		command, args := mcpCommandArgs(exe)
		m["deja"] = map[string]any{"type": "local", "command": command, "args": args, "tools": []string{"*"}}
	}
	next, err := json.MarshalIndent(root, "", "  ")
	if err != nil {
		return installResult{}, err
	}
	next = append(next, '\n')
	a, err := writeIfChanged(path, old, next)
	return installResult{Path: path, Action: a}, err
}

// installOpenClawMCP wires deja into openclaw.json — OpenClaw keeps MCP
// servers under mcp.servers, not the common mcpServers root.
func installOpenClawMCP(exe string, uninstall bool) (installResult, error) {
	path := filepath.Join(sources.OpenClawStateDir(), "openclaw.json")
	old, _ := os.ReadFile(path)
	var root map[string]any
	if len(bytes.TrimSpace(old)) == 0 {
		root = map[string]any{}
	} else if err := json.Unmarshal(old, &root); err != nil {
		return installResult{}, err
	}
	mcp, _ := root["mcp"].(map[string]any)
	if mcp == nil {
		mcp = map[string]any{}
		root["mcp"] = mcp
	}
	servers, _ := mcp["servers"].(map[string]any)
	if servers == nil {
		servers = map[string]any{}
		mcp["servers"] = servers
	}
	if uninstall {
		delete(servers, "deja")
	} else {
		command, args := mcpCommandArgs(exe)
		servers["deja"] = map[string]any{"command": command, "args": args}
	}
	next, err := json.MarshalIndent(root, "", "  ")
	if err != nil {
		return installResult{}, err
	}
	next = append(next, '\n')
	a, err := writeIfChanged(path, old, next)
	return installResult{Path: path, Action: a}, err
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
		command, args := mcpCommandArgs(exe)
		m["deja"] = map[string]any{"command": command, "args": args}
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
