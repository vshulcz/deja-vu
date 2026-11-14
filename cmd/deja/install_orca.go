package main

import (
	"path/filepath"

	"github.com/vshulcz/deja-vu/internal/sources"
)

func orcaMCPPath() string {
	return filepath.Join(sources.OrcaRoot(), "mcp.json")
}

func orcaSettingsPath() string {
	return filepath.Join(sources.OrcaRoot(), "settings.json")
}

func orcaSkillPath() string {
	return filepath.Join(sources.OrcaRoot(), "skills", "deja-history", "SKILL.md")
}

var orcaHookWiring = []struct {
	Event, Matcher string
	Args           []string
}{
	{"SessionStart", "", []string{"hook-context"}},
	{"UserPromptSubmit", "", []string{"hook-prompt"}},
	{"PreToolUse", "Bash|Read|Edit|Write", []string{"hook-tool"}},
	{"PostToolUse", "Bash", []string{"hook-tool-after"}},
	{"PreCompact", "", []string{"hook-precompact", "--harness", "orca"}},
	{"SessionEnd", "", []string{"hook-session-end"}},
}

func installOrca(exe string, uninstall bool) (installResult, error) {
	mcp, err := installMCPJSON(orcaMCPPath(), exe, uninstall)
	if err != nil {
		return installResult{}, err
	}
	skill, err := installSkillFile(orcaSkillPath(), uninstall)
	if err != nil {
		return installResult{}, err
	}
	cmd, err := installCommandFile("orca", exe, uninstall)
	if err != nil {
		return installResult{}, err
	}
	return wroteAll(mcp, skill, cmd), nil
}

func installOrcaAuto(exe string, uninstall bool) (installResult, error) {
	base, err := installOrca(exe, uninstall)
	if err != nil {
		return installResult{}, err
	}
	hookExe := hookExeFor(exe, uninstall)
	results := []installResult{base}
	for _, h := range orcaHookWiring {
		r, ierr := installSettingsHookCmd(orcaSettingsPath(), h.Event, h.Matcher, 30, hookRun(hookExe, h.Args...), uninstall)
		if ierr != nil {
			return installResult{}, ierr
		}
		results = append(results, r)
	}
	status, err := installSettingsStatusLine(orcaSettingsPath(), hookExe, uninstall)
	if err != nil {
		return installResult{}, err
	}
	return wroteAll(append(results, status)...), nil
}
