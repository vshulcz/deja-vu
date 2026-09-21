package main

import (
	"os"
	"path/filepath"

	"github.com/vshulcz/deja-vu/internal/sources"
)

// Claude Code reads slash commands from ~/.claude/commands/*.md. Auto-recall
// covers the common case, but a command gives the user a way to ask deja
// something directly — and, more importantly, makes deja discoverable: it
// shows up when they type "/".
func installClaudeCommands(exe string, uninstall bool) (installResult, error) {
	dir := filepath.Join(sources.ClaudeConfigDir(), "commands")
	path := filepath.Join(dir, "deja.md")
	if uninstall {
		if _, err := os.Stat(path); err != nil {
			return installResult{Path: path, Action: "unchanged"}, nil
		}
		if err := os.Remove(path); err != nil {
			return installResult{}, err
		}
		// The two rules the other command files have had since #2581 and
		// #2600: put back the file install replaced, and take the directory
		// with the last command in it when deja made it. This writer is
		// Claude's own and had neither, so a reader's `/deja` command came
		// back as a `.bak` nobody restores and an empty ~/.claude/commands
		// outlived every uninstall (#3685).
		restored, rerr := restoreReplacedFile(path, isOurCommandFile)
		if rerr != nil {
			return installResult{}, rerr
		}
		if restored {
			return installResult{Path: path, Action: "restored"}, nil
		}
		pruneCreatedDir(dir)
		return installResult{Path: path, Action: "removed"}, nil
	}
	// No MkdirAll: writeIfChanged creates the directory and records that it
	// did, which is what lets the uninstall above take it back.
	old, err := readConfig(path)
	if err != nil {
		return installResult{}, err
	}
	a, err := writeIfChanged(path, old, []byte(claudeCommandMD(exe)))
	if err != nil {
		return installResult{}, err
	}
	return installResult{Path: path, Action: a}, nil
}

func claudeCommandMD(exe string) string {
	return `---
name: deja
description: Search this machine's past AI coding sessions (deja-vu)
---

Search the user's own past sessions across every AI coding tool on this
machine, then answer from what you find.

Call the deja tool in ` + "`recall`" + ` mode with the user's words as the query — the
most specific tokens win (an exact error string, a function name, a file path,
a flag). If a result looks right but is too short to act on, follow up with
` + "`context`" + ` mode using a term from it.

If the deja MCP tool is unavailable, fall back to the CLI:

` + "```bash\n" + exe + ` search -- "$ARGUMENTS"
` + "```" + `

Answer with what actually happened in those sessions — when it was, which
project and tool, what was decided or fixed. Say plainly if nothing matched
rather than filling the gap from general knowledge.
`
}
