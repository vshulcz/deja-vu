package main

import (
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/vshulcz/deja-vu/internal/sources"
)

// Cherry Studio keeps its MCP servers in its own SQLite store — a drizzle
// schema seeded from a built-in preset list (src/main/data/services/McpServerService.ts,
// src/main/data/db/seeding/seeders/builtinMcpServerSeeder.ts) — so there is no
// config file to write, and writing into a running app's database is not
// something an installer should do.
//
// What the app does have is three import paths in Settings → MCP: JSON, DXT and
// MCPB (src/renderer/pages/settings/McpSettings/McpServersList.tsx). So the
// installer writes the file the JSON import takes, the way the aider target
// writes a context file the tool itself will not fetch, and says where to point
// it. The MCPB bundle this project already publishes is the other path and
// needs nothing new.
func cherryStudioImportPath() string {
	base := os.Getenv("XDG_CONFIG_HOME")
	if base == "" {
		h, _ := os.UserHomeDir()
		base = filepath.Join(h, ".config")
	}
	return filepath.Join(base, "deja", "cherrystudio-mcp.json")
}

const cherryStudioImportNote = "import it in Cherry Studio: Settings → MCP → Import from JSON " +
	"(or Import from MCPB with the bundle from the release)"

func installCherryStudio(exe string, uninstall bool) (installResult, error) {
	path := cherryStudioImportPath()
	old, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return installResult{}, err
	}
	if uninstall {
		if len(old) == 0 {
			return installResult{Path: path, Action: "unchanged"}, nil
		}
		if err := os.Remove(path); err != nil {
			return installResult{}, err
		}
		return installResult{Path: path, Action: "removed"}, nil
	}
	body, err := json.MarshalIndent(map[string]any{
		"mcpServers": map[string]any{"deja": mcpServerEntry(exe)},
	}, "", "  ")
	if err != nil {
		return installResult{}, err
	}
	body = append(body, '\n')
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return installResult{}, err
	}
	action, err := writeIfChanged(path, old, body)
	if err != nil {
		return installResult{}, err
	}
	res := installResult{Path: path, Action: action}
	// The note rides on every result, not only the first: the file being
	// unchanged does not mean anyone has imported it yet.
	res.Note = joinNotes(res.Note, cherryStudioImportNote)
	return res, nil
}

// cherryStudioFirstRoot is what says the app is on this machine: a transcript
// root it has written, rather than the app directory, which an uninstalled
// Electron app can leave behind.
func cherryStudioFirstRoot() string {
	for _, root := range sources.CherryStudioRoots() {
		if _, err := os.Stat(root); err == nil {
			return root
		}
	}
	return filepath.Join(os.DevNull, "cherrystudio")
}
