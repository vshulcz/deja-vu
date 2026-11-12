package main

import (
	"path/filepath"

	"github.com/vshulcz/deja-vu/internal/sources"
)

func mimoSkillPath() string {
	return filepath.Join(sources.MimoConfigDir(), "skills", "deja-history", "SKILL.md")
}

func mimoConfigPath() string {
	dir := sources.MimoConfigDir()
	jsonPath := filepath.Join(dir, "mimocode.json")
	if fileExists(jsonPath) {
		return jsonPath
	}
	return filepath.Join(dir, "mimocode.jsonc")
}

func installMimo(exe string, uninstall bool) (installResult, error) {
	cfg, err := installOpencodeShaped(sources.MimoConfigDir(), "mimocode", exe, uninstall)
	if err != nil {
		return cfg, err
	}
	skill, err := installSkillFile(mimoSkillPath(), uninstall)
	if err != nil {
		return skill, err
	}
	cmd, err := installCommandFile("mimo", exe, uninstall)
	if err != nil {
		return cmd, err
	}
	return wroteAll(cfg, skill, cmd), nil
}

func installMimoAuto(exe string, uninstall bool) (installResult, error) {
	base, err := installMimo(exe, uninstall)
	if err != nil {
		return base, err
	}
	plug, err := installOpencodeShapedPlugin(
		filepath.Join(sources.MimoConfigDir(), "plugins"), exe, uninstall, mimoPluginJS)
	if err != nil {
		return plug, err
	}
	tui, err := installMimoTUI(exe, uninstall)
	if err != nil {
		return tui, err
	}
	return wroteAll(base, plug, tui), nil
}

func mimoPluginJS(exe string) string {
	return legacyPluginJSFor("mimo-auto", exe)
}
