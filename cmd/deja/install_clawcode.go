package main

import (
	"path/filepath"

	"github.com/vshulcz/deja-vu/internal/sources"
)

func clawCodeSettings() string {
	return filepath.Join(sources.ClawCodeConfigDir(), "settings.json")
}

func clawCodeSkillPath() string {
	return filepath.Join(sources.ClawCodeConfigDir(), "skills", "deja-history", "SKILL.md")
}

var clawCodeHookEvents = []struct {
	event string
	cmd   string
}{
	{"PreToolUse", "hook-tool"},
	{"PostToolUse", "hook-tool-after"},
	{"PostToolUseFailure", "hook-tool-after"},
}

func installClawCode(exe string, uninstall bool) (installResult, error) {
	mcpRes, err := installMCPJSON(clawCodeSettings(), exe, uninstall)
	if err != nil {
		return installResult{}, err
	}
	skillRes, err := installSkillFile(clawCodeSkillPath(), uninstall)
	if err != nil {
		return installResult{}, err
	}
	cmdRes, err := installCommandFile("clawcode", exe, uninstall)
	if err != nil {
		return installResult{}, err
	}
	return wroteAll(mcpRes, skillRes, cmdRes), nil
}

func installClawCodeAuto(exe string, uninstall bool) (installResult, error) {
	baseRes, err := installClawCode(exe, uninstall)
	if err != nil {
		return installResult{}, err
	}
	hookExe := hookExeFor(exe, uninstall)
	settingsPath := clawCodeSettings()
	results := []installResult{baseRes}
	for _, h := range clawCodeHookEvents {
		res, err := installSettingsHookCmd(settingsPath, h.event, "", 30, hookRun(hookExe, h.cmd), uninstall)
		if err != nil {
			return installResult{}, err
		}
		results = append(results, res)
	}
	return wroteAll(results...), nil
}
