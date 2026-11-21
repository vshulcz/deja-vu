package main

import (
	"path/filepath"

	"github.com/vshulcz/deja-vu/internal/sources"
)

func anteSettingsPath() string {
	return filepath.Join(sources.AnteConfigDir(), "settings.json")
}

func installAnte(exe string, uninstall bool) (installResult, error) {
	command, args := mcpCommandArgs(exe)
	entry := map[string]any{"command": command, "args": args}
	return installMCPJSONEntry(anteSettingsPath(), "mcp_servers", entry, uninstall)
}
