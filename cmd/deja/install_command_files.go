package main

import (
	"bytes"
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

` + "```bash\n" + shellQuoteIfNeeded(exe) + ` search -- "` + argsToken + `"
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

// commandFilePaths is where each harness reads a user-level command from.
func commandFilePath(harness string) string {
	switch harness {
	case "opencode":
		return filepath.Join(opencodeConfigHome(), "opencode", "commands", "deja.md")
	case "cursor":
		return filepath.Join(sources.CursorCLIHome(), "commands", "deja.md")
	case "roo":
		return filepath.Join(homeDir(), ".roo", "commands", "deja.md")
	case "crush":
		return crushCommandPath()
	case "commandcode":
		// `~/.commandcode/commands/<name>.md`, the name taken from the
		// basename — the surface two independent integrations describe from
		// the vendor's docs.
		return filepath.Join(homeDir(), ".commandcode", "commands", "deja.md")
	case "kilocode":
		// Kilo's own workflows doc: global commands live in
		// `~/.config/kilo/commands/`, project ones in `.kilo/commands/`, and a
		// file named `deja.md` is invoked as `/deja`. XDG_CONFIG_HOME moves the
		// config home the way it does for every other tool that keeps its
		// directory there.
		return filepath.Join(opencodeConfigHome(), "kilo", "commands", "deja.md")
	case "gjc":
		// Evidence for the directory is its own plugin marketplace's
		// verification script (devswha/oh-my-gjc INSTALLATION.md), which after
		// an install checks `$HOME/.gjc/agent/commands/<name>.md` and invokes
		// the entries as `/omg:*`. Same shape as the others here.
		return filepath.Join(sources.GjcConfigDir(), "commands", "deja.md")
	case "omp":
		// The active profile's agent directory; the default profile's is
		// ~/.omp/agent. A named profile reads its own, which is why install
		// writes the profiles it finds rather than this one alone.
		return filepath.Join(sources.OmpConfigDir(), "commands", "deja.md")
	}
	return ""
}

// Gemini deliberately has no entry in commandFilePath. Its command namespace is
// flat and everything deja installs lands in it: the MCP server's own prompt is
// `/deja`, and each skill deja writes is a command too — which Gemini's own
// screen says out loud. A file of ours collided with the prompt first ("User
// command '/deja' was renamed to '/user.deja'"), and once renamed to
// `deja-search` it collided with deja's CLI skill instead ("Skill command
// '/deja-search' was renamed to '/deja-search1'"). A third name would add a
// third entry doing what the other two already do, so Gemini joins the eight
// harnesses where the skill is the command (#3665).
func commandFileText(harness, exe string) string {
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
		// A harness with no command of ours may still be carrying one an older
		// deja wrote. Gemini is the case: both names it used claim something
		// its own skills already have, so they come out here rather than
		// outliving the fix for everyone who installed before (#3665).
		if !uninstall {
			if err := dropRetiredCommandFile(harness); err != nil {
				return installResult{}, err
			}
		}
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
	// A command file deja used to write under another name is dropped here
	// rather than left beside the new one: for gemini the old name is what
	// collided with the server's prompt, so leaving it keeps the bug for
	// everyone who installed before the rename (#3655).
	if err := dropRetiredCommandFile(harness); err != nil {
		return installResult{}, err
	}
	old, err := readConfig(path)
	if err != nil {
		return installResult{}, err
	}
	a, err := writeIfChanged(path, old, []byte(commandFileText(harness, exe)))
	return installResult{Path: path, Action: a}, err
}

// isOurCommandFile reports that a command file is one deja generated rather
// than one the reader wrote at the same path.
//
// Not mentionsDeja: that reads the markers deja leaves in *configs* — a hook
// subcommand, an `[mcp_servers.deja]` header, a server id — and a command file
// carries none of them. It is prose telling the model to call the recall tool,
// so what identifies it is the description deja writes and the sentence that
// names the tool. Asking mentionsDeja meant the answer was always no, which
// silently turned the uninstall restore into a no-op and would have done the
// same to the retired-name drop (#3655).
func isOurCommandFile(b []byte) bool {
	for _, marker := range []string{"(deja-vu)", "the deja recall tool", "deja MCP tools"} {
		if bytes.Contains(b, []byte(marker)) {
			return true
		}
	}
	return mentionsDeja(b)
}

// retiredCommandFiles are command files deja wrote under a name it no longer
// uses, by harness.
func retiredCommandFiles(harness string) []string {
	if harness == "gemini" {
		// Both names this file has had. Gemini has no command of deja's own
		// any more — see the comment above commandFileText — and either
		// leftover claims a name one of deja's own skills already has.
		return []string{
			filepath.Join(sources.GeminiHome(), "commands", "deja.toml"),
			filepath.Join(sources.GeminiHome(), "commands", "deja-search.toml"),
		}
	}
	return nil
}

// dropRetiredCommandFile removes those, and only when the file is still deja's
// own: a reader who wrote their own /deja for gemini keeps it.
func dropRetiredCommandFile(harness string) error {
	for _, path := range retiredCommandFiles(harness) {
		b, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		if !isOurCommandFile(b) {
			continue
		}
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	return nil
}
