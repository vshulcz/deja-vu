package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/vshulcz/deja-vu/internal/sources"
)

// The third surface an install writes and no report read. MCP wiring and hooks
// each have a section; `/deja` — the thing that makes deja discoverable when
// someone types a slash — had none, which is why Gemini renaming it to
// `/user.deja` was invisible to every check deja ships and had to be found on
// Gemini's own screen (#3655, #3664).
//
// The name matters as much as the state here: it is what the reader types, and
// not all of these are `deja`.
func doctorCommands(w io.Writer) {
	fmt.Fprintln(w, "Commands:")
	skills := false
	for _, c := range doctorCommandFiles() {
		fmt.Fprintf(w, "  %-12s %-14s %s\n", c.name, c.state(), reportPath(c.path))
		skills = skills || c.skill
	}
	// Ten harnesses have no file, and an omitted row reads as "deja has no
	// command here" when the truth is that the command is the skill and it is
	// installed. The spelling is left to the harness on purpose: codex offers
	// /skills, Kimi invokes /skill:<name>, Copilot /<skill-name> (#3667).
	if skills {
		fmt.Fprintf(w, "  %-12s %s\n", "",
			"`skill` means there is no file of deja's to install: the skill is the command there, and each harness spells the invocation its own way")
	}
}

type doctorCommandFile struct {
	name string
	path string
	// skill marks a harness that makes a skill invocable by name. deja writes
	// no command file for those — a file beside the skill is one entry too
	// many, which Gemini said out loud by renaming it (#3665).
	skill bool
}

// state separates "deja never wrote one here" from "someone else's file has
// that name", because only the second is a reason not to install.
func (c doctorCommandFile) state() string {
	b, err := os.ReadFile(c.path)
	if err != nil {
		return "missing"
	}
	if c.skill {
		return "skill"
	}
	if !isOurCommandFile(b) {
		return "someone else's"
	}
	return "written"
}

// doctorCommandFiles is every user-level command an install writes, read from
// the same helpers the installers use rather than from a list beside them.
func doctorCommandFiles() []doctorCommandFile {
	out := []doctorCommandFile{
		{name: "claude-code", path: filepath.Join(sources.ClaudeConfigDir(), "commands", "deja.md")},
	}
	// The shared table, in the order `deja install --all` walks it.
	for _, name := range []string{
		"opencode", "cursor", "roo", "kilocode", "crush",
		"omp", "gjc", "commandcode",
	} {
		if p := commandFilePath(name); p != "" {
			out = append(out, doctorCommandFile{name: name, path: p})
		}
	}
	// VS Code Copilot Chat's command is a prompt file, one per host, and the
	// row speaks for the first host that has one. The reader lists only hosts
	// that exist, so the fallback is where plain VS Code would keep it — a row
	// with no path says less than no row at all.
	out = append(out, doctorCommandFile{name: "copilot-chat", path: doctorFirstExisting(
		copilotChatPromptPaths(),
		filepath.Join(vsCodeDefaultUserDir(), "prompts", "deja.prompt.md"))})
	for _, name := range skillIsTheCommandHarnesses() {
		out = append(out, doctorCommandFile{name: name, path: commandSkillPath(name), skill: true})
	}
	return out
}

// commandSkillPath is the skill file that is the command for a harness. Usually
// that is its guidance file, but not for grok: grok keeps its guidance in
// GROK.md, which is loaded for the whole session and is not invocable, and
// takes the shared skill besides — so the row has to name the skill rather than
// the instructions file.
func commandSkillPath(harness string) string {
	p := guidancePath(harness)
	if strings.Contains(p, string(filepath.Separator)+"skills"+string(filepath.Separator)) {
		return p
	}
	return sharedSkillPath()
}

// skillIsTheCommandHarnesses make a skill invocable by name, so the skill deja
// installs is the command and a second file would only add another entry.
// OpenClaw reports "Available as command: yes"; `agy plugin validate` converts
// a plugin command into a skill; Codex offers /skills and $name and calls
// custom prompts deprecated in favour of skills; Qwen lists them under /skills;
// Kimi invokes /skill:<name>; Copilot invokes /<skill-name>. Grok Build lists
// skills with user-invocable frontmatter and an argument hint for its
// slash-command autocomplete. Zed lists skills under `/` and invokes them by
// their frontmatter name. Gemini is measured by its own complaint: with a
// command file of deja's present it renamed one of the two (#3665).
//
// One list, read by the report and by the capability test, so neither can
// drift from the other.
func skillIsTheCommandHarnesses() []string {
	return []string{"antigravity", "codex", "copilot", "gemini", "grok",
		"kimi", "openclaw", "qwen", "zed"}
}

func skillIsTheCommand(harness string) bool {
	for _, name := range skillIsTheCommandHarnesses() {
		if name == harness {
			return true
		}
	}
	return false
}
