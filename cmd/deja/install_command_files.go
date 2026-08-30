package main

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/vshulcz/deja-vu/internal/sources"
)

// A command is the one surface a person reaches for deliberately. Auto-recall
// and the skill both wait to be needed; typing "/" and seeing deja listed is
// how someone who never read the docs finds it at all.
//
// Unlike skills, commands are not in the Agent Plugins standard, so every
// harness has its own file format and its own directory. Each writer below
// follows that harness's documented shape rather than a shared one.

// shellQuoteIfNeeded quotes a path only when a shell would otherwise split it.
// This snippet is copied into a terminal by whoever reads it — a model or a
// person — and a home like "/Users/John Smith" makes the bare form run
// "/Users/John" with the rest as arguments. Ordinary paths are left plain:
// quoting every one of them makes the instruction look like escaping matters
// when it does not.
func shellQuoteIfNeeded(s string) string {
	if !strings.ContainsAny(s, " \t'\"\\$`") {
		return s
	}
	return shellQuote(s)
}

// commandBody is the instruction the command sends, in the plain second person
// each harness expects. $ARGUMENTS is substituted by markdown-based harnesses;
// the TOML ones use {{args}} and get their own copy.
func commandBody(exe, argsToken string) string {
	return `Search the user's own past sessions across every AI coding tool on this
machine, then answer from what you find.

Use the deja recall tool with the user's words as the query — the most specific
tokens win: an exact error string, a function name, a file path, a flag. If a
result looks right but is too short to act on, follow up with recall_context.

If the deja MCP tools are unavailable, run the CLI instead:

` + "```bash\n" + shellQuoteIfNeeded(exe) + ` "` + argsToken + `"
` + "```" + `

Answer with what actually happened in those sessions — when it was, which
project and tool, what was decided or fixed. Say plainly if nothing matched
rather than filling the gap from general knowledge.
`
}

func markdownCommand(exe string) string {
	return `---
description: Search this machine's past AI coding sessions (deja-vu)
---

` + commandBody(exe, "$ARGUMENTS")
}

// tomlCommand is Gemini's shape: a description line and a prompt string. Args
// arrive through {{args}}; without it the CLI appends them to the prompt, which
// would read as an unrelated paragraph.
//
// The prompt is a literal string, not a basic one. A Windows exe path is full
// of backslashes, and TOML reads those as escapes inside "…" — C:\Users would
// have become an invalid escape and taken the whole file with it.
func tomlCommand(exe string) string {
	return "description = \"Search this machine's past AI coding sessions (deja-vu)\"\nprompt = '''\n" +
		commandBody(exe, "{{args}}") + "'''\n"
}

// commandFilePaths is where each harness reads a user-level command from.
func commandFilePath(harness string) string {
	switch harness {
	case "opencode":
		return filepath.Join(opencodeConfigHome(), "opencode", "commands", "deja.md")
	case "cursor":
		return filepath.Join(sources.CursorCLIHome(), "commands", "deja.md")
	case "roo":
		return filepath.Join(homeDir(), ".roo", "commands", "deja.md")
	case "gemini":
		return filepath.Join(sources.GeminiHome(), "commands", "deja.toml")
	case "omp":
		// The active profile's agent directory; the default profile's is
		// ~/.omp/agent. A named profile reads its own, which is why install
		// writes the profiles it finds rather than this one alone.
		return filepath.Join(sources.OmpConfigDir(), "commands", "deja.md")
	}
	return ""
}

func commandFileText(harness, exe string) string {
	if harness == "gemini" {
		return tomlCommand(exe)
	}
	return markdownCommand(exe)
}

// installCommandFile writes the /deja command for harnesses that read one from
// a file. Silent when the harness has none: not every one does, and a missing
// command is not a failure to install.
func installCommandFile(harness, exe string, uninstall bool) (installResult, error) {
	// Goose keeps its commands in config.yaml rather than a commands directory.
	if harness == "goose" {
		return installGooseCommand(exe, uninstall)
	}
	path := commandFilePath(harness)
	if path == "" {
		return installResult{}, nil
	}
	if uninstall {
		if _, err := os.Stat(path); err != nil {
			return installResult{Path: path, Action: "unchanged"}, nil
		}
		// `uninstall cursor` runs cursor-auto too, and the second pass met the
		// file the first pass had just put back (#2600, the shape #2581 hit).
		if restoredGuidance[path] {
			return installResult{Path: path, Action: "kept"}, nil
		}
		if err := os.Remove(path); err != nil {
			return installResult{}, err
		}
		// The same rules the skills have since #2581 and #2585: put back what
		// install replaced, and drop a backup that holds deja's own text. A
		// full round used to destroy eight files of the reader's this way, the
		// command files among them (#2600).
		restored, rerr := restoreReplacedFile(path, isOurCommandFile)
		if rerr != nil {
			return installResult{}, rerr
		}
		if restored {
			return installResult{Path: path, Action: "restored"}, nil
		}
		return installResult{Path: path, Action: "removed"}, nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return installResult{}, err
	}
	old, err := readConfig(path)
	if err != nil {
		return installResult{}, err
	}
	a, err := writeIfChanged(path, old, []byte(commandFileText(harness, exe)))
	return installResult{Path: path, Action: a}, err
}

// isOurCommandFile reports that a command file is one deja generated rather than
// one the reader wrote at the same path. Every generated one names the binary's
// own subcommands, which is what mentionsDeja reads.
func isOurCommandFile(b []byte) bool { return mentionsDeja(b) }
