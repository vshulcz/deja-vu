package main

import (
	"path/filepath"

	"github.com/vshulcz/deja-vu/internal/sources"
)

// installTargetFunc writes or removes one target's wiring.
type installTargetFunc func(exe string, uninstall bool) (installResult, error)

// installTargetCase is the writer for a canonical target name. Built per call
// rather than held in a package variable: installTarget is reachable from the
// writers themselves, which a package-level table would turn into an
// initialization cycle.
func installTargetCase(target string) (installTargetFunc, bool) {
	cases := map[string]installTargetFunc{
		"claude-auto": installTargetClaudeAuto,
		"claude-code": installClaude,
		"codex":       installCodex,
		"codex-auto":  installTargetCodexAuto,
		"cursor":      installCursor,
		"cursor-auto": installTargetCursorAuto,
		"gemini": func(exe string, uninstall bool) (installResult, error) {
			return installMCPJSON(filepath.Join(sources.GeminiHome(), "settings.json"), exe, uninstall)
		},
		"gemini-auto": installTargetGeminiAuto,
		"antigravity": func(exe string, uninstall bool) (installResult, error) {
			return installMCPJSON(filepath.Join(antigravityConfigHome(), "mcp_config.json"), exe, uninstall)
		},
		"antigravity-auto": installAntigravityAuto,
		"grok":             installGrok,
		"grok-auto":        installTargetGrokAuto,
		"qwen": func(exe string, uninstall bool) (installResult, error) {
			return installMCPJSON(filepath.Join(sources.QwenConfigDir(), "settings.json"), exe, uninstall)
		},
		"qwen-auto":      installTargetQwenAuto,
		"trae":           installTrae,
		"trae-auto":      installTargetTraeAuto,
		"trae-ide":       installTraeIDE,
		"trae-ide-auto":  installTraeIDEAuto,
		"codebuddy":      installCodeBuddyMCP,
		"codebuddy-auto": installCodeBuddyAuto,
		"workbuddy": func(exe string, uninstall bool) (installResult, error) {
			return installMCPJSON(workBuddyMCPPath(), exe, uninstall)
		},
		"workbuddy-auto": installWorkBuddyAuto,
		"muse":           installMuseMCP,
		"muse-auto":      installMuseAuto,
		"mcode":          installMcode,
		"mcode-auto":     installMcodeAuto,
		"kimi":           installKimiMCP,
		"kimi-auto":      installTargetKimiAuto,
		"zed": func(exe string, uninstall bool) (installResult, error) {
			return installZedMCP(sources.ZedSettingsPath(), exe, uninstall)
		},
		"devin":      installDevinMCP,
		"devin-auto": installDevinAuto,
		"cline": func(exe string, uninstall bool) (installResult, error) {
			return installMCPJSON(sources.ClineMCPSettingsPath(), exe, uninstall)
		},
		"roo":               installRoo,
		"kilocode":          installKilocode,
		"kilocode-auto":     installKilocodeAuto,
		"cherrystudio":      installCherryStudio,
		"cherrystudio-auto": installCherryStudioAuto,
		"kiro":              installKiro,
		"kiro-auto":         installKiroAuto,
		"senpi":             installSenpi,
		"senpi-auto":        installSenpiAuto,
		"kimchi":            installKimchi,
		"kimchi-auto":       installKimchiAuto,
		"codewhale":         installCodeWhale,
		"codewhale-auto":    installCodeWhaleAuto,
		"junie":             installJunie,
		"junie-auto":        installJunieAuto,
		"jetbrains":         installJetBrains,
		"windsurf":          installWindsurf,
		"gjc":               installGjc,
		"gjc-auto":          installGjcAuto,
		"zcode":             installZCode,
		"commandcode":       installCommandCode,
		"commandcode-auto":  installCommandCodeAuto,
		"zcode-auto":        installZCodeAuto,
		"cline-auto":        installTargetClineAuto,
		"continue":          installContinue,
		"crush":             installCrushMCP,
		"crush-auto":        installTargetCrushAuto,
		"copilot":           installCopilotMCP,
		"copilot-auto":      installCopilotAuto,
		"vscode":            installVSCode,
		"copilot-chat":      installVSCode,
		"vscode-auto":       installTargetVSCodeAuto,
		"copilot-chat-auto": installTargetVSCodeAuto,
		"hermes":            installHermesMCP,
		"hermes-auto":       installHermesAuto,
		"pi":                installPiMCP,
		"pi-auto":           installPiAuto,
		"omp": func(exe string, uninstall bool) (installResult, error) {
			return installMCPJSON(filepath.Join(sources.OmpConfigDir(), "mcp.json"), exe, uninstall)
		},
		"omp-auto":      installOmpAuto,
		"amp":           installAmpMCP,
		"amp-auto":      installAmpAuto,
		"prime":         installPrimeMCP,
		"prime-auto":    installPrimeAuto,
		"deepseek":      installDeepSeekMCP,
		"deepseek-auto": installDeepSeekAuto,
		"openclaw":      installOpenClawMCP,
		"openclaw-auto": installOpenClawAuto,
		"reasonix": func(exe string, uninstall bool) (installResult, error) {
			return installReasonix(exe, uninstall, false)
		},
		"reasonix-auto": func(exe string, uninstall bool) (installResult, error) {
			return installReasonix(exe, uninstall, true)
		},
		"opencode":      installOpencode,
		"opencode-auto": installOpencodeAuto,
		"aider":         installAider,
		"goose":         installGoose,
		"goose-auto":    installGooseAuto,
		"statusline":    installStatusline,
		"sync-timer":    installSyncTimer,
	}
	f, ok := cases[target]
	return f, ok
}

// installTargetClaudeAuto checks settings.json reads before wiring claude-auto.
func installTargetClaudeAuto(exe string, uninstall bool) (installResult, error) {
	if err := readableStrictJSON(filepath.Join(sources.ClaudeConfigDir(), "settings.json")); err != nil {
		return installResult{}, err
	}
	return installClaudeAuto(exe, uninstall)
}

// installTargetCodexAuto checks hooks.json reads before wiring codex-auto.
func installTargetCodexAuto(exe string, uninstall bool) (installResult, error) {
	if err := readableStrictJSON(filepath.Join(sources.CodexHome(), "hooks.json")); err != nil {
		return installResult{}, err
	}
	return installCodexAuto(exe, uninstall)
}

// installTargetCursorAuto runs cursor's preflight before wiring cursor-auto.
func installTargetCursorAuto(exe string, uninstall bool) (installResult, error) {
	if err := cursorAutoPreflight(exe, uninstall); err != nil {
		return installResult{}, err
	}
	return installCursorAuto(exe, uninstall)
}

// installTargetGeminiAuto wires the gemini hooks extension and then MCP.
func installTargetGeminiAuto(exe string, uninstall bool) (installResult, error) {
	// The hooks extension, then MCP. `--auto` maps gemini to this target
	// alone, so installing only the extension left the harness without
	// the tools: its own `gemini mcp list` said "No MCP servers
	// configured" on a machine that had just run `deja install --auto`.
	// grok-auto pairs them the same way. The extension goes first for the
	// reason qwen-auto has: whichever half may refuse goes first (#2745).
	ext, err := installGeminiAuto(exe, uninstall)
	if err != nil {
		return installResult{}, err
	}
	mcp, err := installMCPJSON(filepath.Join(sources.GeminiHome(), "settings.json"), exe, uninstall)
	if err != nil {
		return installResult{}, err
	}
	// Both writes in one result, like every other -auto target: the
	// extension's path and its note about hooksConfig reached nobody
	// while the first result was dropped (#3185).
	return wroteAll(ext, mcp), nil
}

// installTargetGrokAuto wires every grok-auto half: config, status line,
// user settings and hooks.
func installTargetGrokAuto(exe string, uninstall bool) (installResult, error) {
	probe := []string{filepath.Join(sources.GrokHome(), "user-settings.json")}
	if !uninstall {
		// Uninstall removes the hook file rather than reading it, so a
		// file it can still take is not one to refuse over.
		probe = append(probe, grokHooksPath())
	}
	if err := readableStrictJSON(probe...); err != nil {
		return installResult{}, err
	}
	if !uninstall {
		if err := grokPreflight(exe, true); err != nil {
			return installResult{}, err
		}
	}
	// Both config.toml halves first, so a config.toml deja cannot write
	// is refused before the other files are touched.
	config, err := installGrokConfig(exe, uninstall)
	if err != nil {
		return installResult{}, err
	}
	status, err := installGrokStatusline(exe, uninstall)
	if err != nil {
		return installResult{}, err
	}
	user, err := installGrokUserSettings(exe, uninstall)
	if err != nil {
		return installResult{}, err
	}
	hooks, err := installGrokAuto(exe, uninstall)
	if err != nil {
		return installResult{}, err
	}
	// Every half in one result: the config and settings writes reached
	// nobody while the first result was dropped (#3220, the #3185 shape).
	return wroteAll(config, user, hooks, status), nil
}

// installTargetQwenAuto wires qwen's hook, MCP entry and status line.
func installTargetQwenAuto(exe string, uninstall bool) (installResult, error) {
	// The hook first: it lives in the same file as the MCP entry, and a
	// refusal after the entry was written left the target reported as
	// refused with half its wiring in the file and a .bak beside it
	// (#2745, the shape #2744 was about).
	hooks, err := installQwenAuto(exe, uninstall)
	if err != nil {
		return installResult{}, err
	}
	mcp, err := installMCPJSON(filepath.Join(sources.QwenConfigDir(), "settings.json"), exe, uninstall)
	if err != nil {
		return installResult{}, err
	}
	status, err := installQwenStatusline(exe, uninstall)
	if err != nil {
		return installResult{}, err
	}
	// Every half, not just the last one. Returning the MCP result alone
	// meant a machine that already had the server was told "unchanged"
	// while the hooks under it were being rewired.
	return wroteAll(hooks, mcp, status), nil
}

// installTargetTraeAuto checks the hooks file reads before wiring trae-auto.
func installTargetTraeAuto(exe string, uninstall bool) (installResult, error) {
	if err := readableStrictJSON(traeHooksPath()); err != nil {
		return installResult{}, err
	}
	return installTraeAuto(exe, uninstall)
}

// installTargetKimiAuto wires kimi's MCP entry, hooks and status line.
func installTargetKimiAuto(exe string, uninstall bool) (installResult, error) {
	// Both halves in the result: the report is what says which files were
	// touched, and this one wrote mcp.json without ever naming it (#3254).
	// The refusals of the hooks and the status line are asked first, so a
	// refused target writes nothing.
	if err := kimiAutoPreflight(exe, uninstall); err != nil {
		return installResult{}, err
	}
	mcp, err := installKimiMCP(exe, uninstall)
	if err != nil {
		return installResult{}, err
	}
	hooks, err := installKimiAuto(exe, uninstall)
	if err != nil {
		return installResult{}, err
	}
	status, err := installKimiStatusline(exe, uninstall)
	if err != nil {
		return installResult{}, err
	}
	return wroteAll(mcp, hooks, status), nil
}

// installTargetClineAuto wires cline's MCP entry and its plugin.
func installTargetClineAuto(exe string, uninstall bool) (installResult, error) {
	if !uninstall {
		if err := clineAutoPreflight(exe); err != nil {
			return installResult{}, err
		}
	}
	mcp, err := installMCPJSON(sources.ClineMCPSettingsPath(), exe, uninstall)
	if err != nil {
		return installResult{}, err
	}
	plugin, err := installClineAuto(exe, uninstall)
	if err != nil {
		return installResult{}, err
	}
	return wroteAll(mcp, plugin), nil
}

// installTargetCrushAuto wires crush's hook and then its MCP entry.
func installTargetCrushAuto(exe string, uninstall bool) (installResult, error) {
	// The hook first: it shares crush.json with the MCP entry, and a
	// refusal after the entry was written left the target reported as
	// refused with half its wiring in the file (#2745).
	hooks, err := installCrushAuto(exe, uninstall)
	if err != nil {
		return installResult{}, err
	}
	mcp, err := installCrushMCP(exe, uninstall)
	if err != nil {
		return installResult{}, err
	}
	return wroteAll(hooks, mcp), nil
}

// installTargetVSCodeAuto wires the VS Code hooks and status item.
func installTargetVSCodeAuto(exe string, uninstall bool) (installResult, error) {
	hooks, err := installVSCodeAuto(exe, uninstall)
	if err != nil {
		return installResult{}, err
	}
	status, err := installVSCodeStatusItem(exe, uninstall)
	if err != nil {
		return installResult{}, err
	}
	return wroteAll(hooks, status), nil
}
