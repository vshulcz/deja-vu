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
	old, err := readConfig(path)
	if err != nil {
		return installResult{}, err
	}
	if uninstall {
		res := installResult{Path: path, Action: "unchanged"}
		if len(old) > 0 {
			if rerr := os.Remove(path); rerr != nil {
				return installResult{}, rerr
			}
			res.Action = "removed"
		}
		// The skill install wrote goes too, unless another harness that reads
		// the shared file is still installed (#4345).
		if sharedSkillStillWanted("cherrystudio") {
			return res, nil
		}
		skill, ierr := installSkillFile(sharedSkillPath(), true)
		if ierr != nil {
			return installResult{}, ierr
		}
		return cherryStudioResult(res, skill), nil
	}
	body, err := json.MarshalIndent(map[string]any{
		"mcpServers": map[string]any{"deja": mcpServerEntry(exe)},
	}, "", "  ")
	if err != nil {
		return installResult{}, err
	}
	body = append(body, '\n')
	noteCreatedDirs(filepath.Dir(path))
	if merr := os.MkdirAll(filepath.Dir(path), 0o755); merr != nil {
		return installResult{}, merr
	}
	action, err := writeIfChanged(path, old, body)
	if err != nil {
		return installResult{}, err
	}
	res := installResult{Path: path, Action: action}
	// The note rides on every result, not only the first: the file being
	// unchanged does not mean anyone has imported it yet.
	res.Note = joinNotes(res.Note, cherryStudioImportNote)

	// And the skill, which does not need importing: Cherry Studio discovers
	// the skill directories of the agent CLIs a machine has — `~/.agents/skills`
	// among them, which is deja's own shared channel — and lists what it finds
	// for the user to enable per agent (src/main/ai/skills/systemSkillSources.ts).
	// So the file is written where the app already looks rather than into a
	// directory of its own that nothing reads.
	skill, err := installSkillFile(sharedSkillPath(), uninstall)
	if err != nil {
		return installResult{}, err
	}
	out := cherryStudioResult(res, skill)
	out.Note = joinNotes(out.Note, "the skill is listed under Settings -> Skills once discovered; enable it for the agent")
	return out, nil
}

// installCherryStudioAuto adds Claude Code's hooks in the directory Cherry
// Studio gives its Claude agents as CLAUDE_CONFIG_DIR. Read out of 2.0.14's
// main bundle and run on a stand with the app's own SDK, binary, env and
// options: SessionStart, UserPromptSubmit, PreToolUse and PostToolUse output
// reached the model, and Stop and SessionEnd fired. Two kinds of agent get
// none of it, by the app's design: the sealed built-in Support agent loads no
// settings at all, and an external-CLI provider drops CLAUDE_CONFIG_DIR and
// reads only the project's settings.
func installCherryStudioAuto(exe string, uninstall bool) (installResult, error) {
	base, err := installCherryStudio(exe, uninstall)
	if err != nil {
		return installResult{}, err
	}
	var events []string
	for _, h := range claudeHookWiring {
		events = append(events, h.Event)
	}
	path := filepath.Join(sources.CherryStudioClaudeConfigDir(), "settings.json")
	if !uninstall {
		noteCreatedDirs(filepath.Dir(path))
		if merr := os.MkdirAll(filepath.Dir(path), 0o755); merr != nil {
			return installResult{}, merr
		}
	}
	hooks, err := installClaudeHooksAt(path, exe, events, uninstall)
	if err != nil {
		return installResult{}, err
	}
	base.also = append(base.also, hooks.Path)
	if hooks.Action != "unchanged" {
		base.Note = joinNotes(base.Note, "also "+hooks.Action+" "+shortHome(hooks.Path))
	}
	if !uninstall {
		base.Note = joinNotes(base.Note, "the hooks run in Cherry Studio's own agents, not in the sealed Support agent or an external-CLI provider")
	}
	return base, nil
}

// cherryStudioResult leads with the import file whatever the skill did.
// wroteAll falls back to the last result when nothing changed, so a second
// install named SKILL.md as the thing to import (#4343).
func cherryStudioResult(res, skill installResult) installResult {
	res.also = append(res.also, skill.Path)
	if skill.Action != "unchanged" {
		res.Note = joinNotes(res.Note, "also "+skill.Action+" "+shortHome(skill.Path))
	}
	return res
}

// cherryStudioAppWiring reads the app's own MCP servers for doctor. known is
// false when they cannot be read — no database, no sqlite3 — and then the
// import file is all doctor has to go on. path is the database when the app
// has deja, and missing the binary its entry names when that is gone (#4344).
// off is a deja server the app has but does not start: its switch is off.
func cherryStudioAppWiring() (known, wired, off bool, path, missing string) {
	db, servers, ok := sources.CherryStudioMCPServers()
	if !ok {
		return false, false, false, "", ""
	}
	for _, s := range servers {
		args := make([]any, len(s.Args))
		for i, a := range s.Args {
			args[i] = a
		}
		entry := map[string]any{"command": s.Command, "args": args}
		if !entryRunsDeja(entry) {
			continue
		}
		if !s.Active {
			off = true
			continue
		}
		cmd := mcpEntryDejaCommand(entry)
		if filepath.IsAbs(cmd) {
			if _, err := os.Stat(cmd); err != nil {
				return true, true, false, db, cmd
			}
		}
		return true, true, false, db, ""
	}
	return true, false, off, db, ""
}

// cherryStudioFirstRoot is what says the app is on this machine: a transcript
// root it has written, rather than the app directory, which an uninstalled
// Electron app can leave behind.
func cherryStudioFirstRoot() string {
	for _, root := range sources.CherryStudioAllRoots() {
		if _, err := os.Stat(root); err == nil {
			return root
		}
	}
	return filepath.Join(os.DevNull, "cherrystudio")
}
