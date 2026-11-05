package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/vshulcz/deja-vu/internal/sources"
)

// VS Code Copilot Chat has no commands directory; what it has is prompt files —
// `<User>/prompts/<name>.prompt.md`, listed in the chat box as `/<name>`, with
// optional frontmatter naming the mode, a description and an argument hint.
// That is the same `User` directory the reader already walks for
// workspaceStorage, so one file per host it finds.
//
// Before this the only way in was the MCP tool with nothing telling the model
// to reach for it (#3651). Recall that arrives on its own is vscode-auto's
// hook file.
func copilotChatPromptPaths() []string {
	roots := sources.CopilotChatRoots()
	out := make([]string, 0, len(roots))
	for _, user := range roots {
		out = append(out, filepath.Join(user, "prompts", "deja.prompt.md"))
	}
	return out
}

// copilotChatPrompt is the file's text. The frontmatter keys are the ones a
// real prompt file carries; `$ARGUMENTS` is what the chat box substitutes, the
// same placeholder Claude Code's command file uses.
func copilotChatPrompt(exe string) string {
	return fmt.Sprintf(`---
agent: agent
description: Search this machine's past AI coding sessions (deja-vu)
argument-hint: what you half-remember — an error string, a function name, a flag
---

Search the user's own past sessions across every AI coding tool on this
machine, then answer from what you find.

Call the deja MCP tool with mode recall and the user's words as the query — the
most specific tokens win. If a hit looks right but is too short to act on,
follow up with mode context (or recall_context) using a term from it.

If the deja tools are not available in this window, run the CLI in the
terminal:

`+"```bash\n"+`%s --framed search -- "$ARGUMENTS"
`+"```"+`

Answer with what the sessions say, and name the session you took it from so the
user can open it. If nothing matches, say so rather than guessing — an empty
result is a fact about their history, not a failure.
`, exe)
}

func installCopilotChatPrompt(exe string, uninstall bool) (installResult, error) {
	paths := copilotChatPromptPaths()
	if len(paths) == 0 {
		// No VS Code host on this machine: nothing to write, and creating one
		// would leave a prompt file for an editor that was never installed.
		return installResult{}, nil
	}
	var results []installResult
	for _, p := range paths {
		res, err := installTextFile(p, copilotChatPrompt(exe), exe, uninstall)
		if err != nil {
			return installResult{}, err
		}
		results = append(results, res)
	}
	return wroteAll(results...), nil
}

// installTextFile writes a file deja owns whole, or takes it back out: an
// unchanged file reports unchanged, and an uninstall leaves nothing behind.
// exe is the binary path body was built from; a rewrite from another path
// snapshots deja's previous text as a .bak, which uninstall must tell from the
// reader's own copy (#5299).
func installTextFile(path, body, exe string, uninstall bool) (installResult, error) {
	if uninstall {
		if _, err := os.Stat(path); err != nil {
			return installResult{Path: path, Action: "unchanged"}, nil //nolint:nilerr // nothing to remove
		}
		if err := os.Remove(path); err != nil {
			return installResult{}, err
		}
		// Put back the copy install replaced, or drop it when it holds deja's own
		// text: the rule skills, command files and the goose recipe have had
		// since #2600.
		if _, err := restoreReplacedFile(path, func(b []byte) bool { return sameTextExceptExe(body, string(b), exe) }); err != nil {
			return installResult{}, err
		}
		// And the directory deja made for it (#3698).
		pruneCreatedDir(filepath.Dir(path))
		return installResult{Path: path, Action: "removed"}, nil
	}
	noteCreatedDirs(filepath.Dir(path))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return installResult{}, err
	}
	old, err := readConfig(path)
	if err != nil {
		return installResult{}, err
	}
	action, err := writeIfChanged(path, old, []byte(body))
	if err != nil {
		return installResult{}, err
	}
	return installResult{Path: path, Action: action}, nil
}

// sameTextExceptExe reports whether other is body as written for another binary
// path. Only the lines that carry exe may differ, and only in that part; every
// other line must match, so a reader's edited copy is never taken for deja's.
// An exe that body spells with JSON escapes will not be found on its line, and
// then the whole line has to match: the safe side, the snapshot is kept.
func sameTextExceptExe(body, other, exe string) bool {
	want := strings.Split(body, "\n")
	got := strings.Split(strings.ReplaceAll(other, "\r\n", "\n"), "\n")
	if len(want) != len(got) {
		return false
	}
	for i, line := range want {
		if line == got[i] {
			continue
		}
		if exe == "" || !strings.Contains(line, exe) {
			return false
		}
		parts := strings.Split(line, exe)
		for j := range parts {
			parts[j] = regexp.QuoteMeta(parts[j])
		}
		if !regexp.MustCompile("^" + strings.Join(parts, ".+") + "$").MatchString(got[i]) {
			return false
		}
	}
	return true
}
