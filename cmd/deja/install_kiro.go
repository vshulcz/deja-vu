package main

import (
	"fmt"
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

// kiroSteeringPath is Kiro's user-level guidance channel: `~/.kiro/steering`
// is the global half of steering, loaded for every project, and kiro-cli scans
// it alongside the workspace one.
//
// It is not a skill, and the difference matters for what goes in it. Steering
// documents declare an inclusion mode, and the only mode measured as actually
// loaded by kiro-cli is `always` — `manual` is not loaded and cannot be invoked
// from a session, `fileMatch` was withheld (KiroCrew's steering reference,
// measured against kiro-cli 2.19.1). So this text rides in front of every turn
// whether it is wanted or not, which is why it is four lines naming the tool
// rather than the full skill deja writes where a skill is loaded on demand.
func kiroSteeringPath() string {
	return filepath.Join(sources.KiroConfigDir(), "steering", "deja.md")
}

func kiroSteering(exe string) string {
	return fmt.Sprintf(`---
inclusion: always
---

# Past sessions are searchable

This machine indexes every coding session it has, across agents, with deja-vu.
Before debugging an error or re-implementing something, call the deja tool with
mode recall and the user's own words — the specific tokens win. Outside a
session: %s search -- "<query>".
`, exe)
}

func installKiro(exe string, uninstall bool) (installResult, error) {
	res, err := installMCPJSON(kiroMCPSettingsPath(), exe, uninstall)
	if err != nil {
		return res, err
	}
	steering, err := installTextFile(kiroSteeringPath(), kiroSteering(exe), uninstall)
	if err != nil {
		return installResult{}, err
	}
	if uninstall {
		return wroteAll(res, steering), nil
	}
	out := wroteAll(res, steering)
	out.Note = joinNotes(out.Note, kiroAgentNote)
	return out, nil
}
