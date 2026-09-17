package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/vshulcz/deja-vu/internal/sources"
)

// The third surface an install writes and no report read. MCP wiring and hooks
// each have a section; `/deja` — the thing that makes deja discoverable when
// someone types a slash — had none, which is why Gemini renaming it to
// `/user.deja` was invisible to every check deja ships and had to be found on
// Gemini's own screen (#3655, #3664).
//
// The name matters as much as the state here: it is what the reader types, and
// two of these are not `deja`.
func doctorCommands(w io.Writer) {
	fmt.Fprintln(w, "Commands:")
	for _, c := range doctorCommandFiles() {
		fmt.Fprintf(w, "  %-12s %-14s %s\n", c.name, commandFileState(c.path), reportPath(c.path))
	}
}

type doctorCommandFile struct {
	name string
	path string
}

// doctorCommandFiles is every user-level command file an install writes, read
// from the same helpers the installers use rather than from a list beside them.
func doctorCommandFiles() []doctorCommandFile {
	out := []doctorCommandFile{
		{"claude-code", filepath.Join(sources.ClaudeConfigDir(), "commands", "deja.md")},
	}
	// The shared table, in the order `deja install --all` walks it.
	for _, name := range []string{
		"opencode", "cursor", "gemini", "roo", "kilocode", "crush",
		"omp", "gjc", "commandcode",
	} {
		if p := commandFilePath(name); p != "" {
			out = append(out, doctorCommandFile{name, p})
		}
	}
	// VS Code Copilot Chat's command is a prompt file, one per host, and the
	// row speaks for the first host that has one. The reader lists only hosts
	// that exist, so the fallback is where plain VS Code would keep it — a row
	// with no path says less than no row at all.
	out = append(out, doctorCommandFile{"copilot-chat", doctorFirstExisting(
		copilotChatPromptPaths(),
		filepath.Join(vsCodeDefaultUserDir(), "prompts", "deja.prompt.md"))})
	return out
}

// commandFileState separates "deja never wrote one here" from "someone else's
// file has that name", because only the second is a reason not to install.
func commandFileState(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return "missing"
	}
	if !isOurCommandFile(b) {
		return "someone else's"
	}
	return "written"
}
