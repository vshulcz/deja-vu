package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/vshulcz/deja-vu/internal/sources"
)

func omnigentMCPPath() string {
	return filepath.Join(sources.OmnigentRoot(), "mcp.json")
}

func omnigentSettingsPath() string {
	return filepath.Join(sources.OmnigentRoot(), "settings.json")
}

func omnigentSkillPath() string {
	return filepath.Join(sources.OmnigentRoot(), "skills", "deja-history", "SKILL.md")
}

var omnigentHookWiring = []struct {
	Event, Matcher string
	Args           []string
}{
	{"SessionStart", "", []string{"hook-context"}},
	{"UserPromptSubmit", "", []string{"hook-prompt"}},
	{"PreToolUse", "Bash|Read|Edit|Write", []string{"hook-tool"}},
	{"PostToolUse", "Bash", []string{"hook-tool-after"}},
	{"PreCompact", "", []string{"hook-precompact", "--harness", "omnigent"}},
	{"SessionEnd", "", []string{"hook-session-end"}},
}

func installOmnigent(exe string, uninstall bool) (installResult, error) {
	mcp, err := installMCPJSON(omnigentMCPPath(), exe, uninstall)
	if err != nil {
		return installResult{}, err
	}
	skill, err := installSkillFile(omnigentSkillPath(), uninstall)
	if err != nil {
		return installResult{}, err
	}
	cmd, err := installCommandFile("omnigent", exe, uninstall)
	if err != nil {
		return installResult{}, err
	}
	return wroteAll(mcp, skill, cmd), nil
}

func installOmnigentAuto(exe string, uninstall bool) (installResult, error) {
	base, err := installOmnigent(exe, uninstall)
	if err != nil {
		return installResult{}, err
	}
	hookExe := hookExeFor(exe, uninstall)
	results := []installResult{base}
	for _, h := range omnigentHookWiring {
		r, ierr := installSettingsHookCmd(omnigentSettingsPath(), h.Event, h.Matcher, 30, hookRun(hookExe, h.Args...), uninstall)
		if ierr != nil {
			return installResult{}, ierr
		}
		results = append(results, r)
	}
	status, err := installSettingsStatusLine(omnigentSettingsPath(), hookExe, uninstall)
	if err != nil {
		return installResult{}, err
	}
	return wroteAll(append(results, status)...), nil
}

func installSettingsStatusLine(path, exe string, uninstall bool) (installResult, error) {
	old, err := readConfig(path)
	if err != nil {
		return installResult{}, err
	}
	var root map[string]any
	if len(bytes.TrimSpace(old)) == 0 {
		root = map[string]any{}
	} else if uerr := json.Unmarshal(old, &root); uerr != nil {
		return installResult{}, configParseError(path, uerr)
	}
	cmd := hookRun(exe, "statusline")
	existing, _ := root["statusLine"].(map[string]any)
	kind := hookNotDejas
	if existing != nil {
		kind = hookCommandKindOf(existing["command"], cmd)
	}
	note := ""
	if uninstall {
		if kind != hookDejas {
			return installResult{Path: path, Action: "unchanged"}, nil
		}
		delete(root, "statusLine")
	} else {
		if kind == hookWrapsDejas {
			return installResult{Path: path, Action: "unchanged"}, nil
		}
		if existing != nil && kind == hookNotDejas {
			prev, _ := existing["command"].(string)
			return installResult{}, fmt.Errorf("a statusline is already configured — to keep both, set statusLine.command to:\n\n  %s", combinedStatusline(prev, exe))
		}
		if prev, _ := existing["command"].(string); prev != "" && prev != cmd {
			note = "replaced the deja statusline that was already here, which ran " + strings.Fields(prev)[0]
		}
		root["statusLine"] = map[string]any{"type": "command", "command": cmd, "refreshInterval": 1000}
	}
	next, err := marshalConfigLike(old, root)
	if err != nil {
		return installResult{}, err
	}
	next = append(next, '\n')
	a, err := writeIfChanged(path, old, next)
	return installResult{Path: path, Action: a, Note: note}, err
}
