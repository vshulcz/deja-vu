package main

import (
	"path/filepath"

	"github.com/vshulcz/deja-vu/internal/sources"
)

// Kiro takes MCP servers in a global settings file, the same `mcpServers`
// shape every other client here uses:
//
//	~/.kiro/settings/mcp.json
//
// (kiro.dev's own docs and `kiro-cli mcp add --scope global`, which writes that
// file). The CLI and the IDE read the same one, which is why this is a single
// target rather than one per client the way the reader has two (#3651).
//
// One thing a user has to know and the installer cannot do for them: a custom
// agent — `~/.kiro/agents/<name>.json` — does not inherit global servers, so
// the entry has to be repeated inside that agent's own file. The note says so
// rather than leaving a reader wondering why recall is missing in their agent.
func kiroMCPSettingsPath() string {
	return filepath.Join(sources.KiroConfigDir(), "settings", "mcp.json")
}

const kiroAgentNote = "a custom agent in ~/.kiro/agents/*.json does not inherit global MCP servers — " +
	"copy the deja entry into that agent's own mcpServers block"

func installKiro(exe string, uninstall bool) (installResult, error) {
	res, err := installMCPJSON(kiroMCPSettingsPath(), exe, uninstall)
	if err != nil || uninstall {
		return res, err
	}
	res.Note = joinNotes(res.Note, kiroAgentNote)
	return res, nil
}
