package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/vshulcz/deja-vu/internal/sources"
)

// VS Code Copilot Chat has no commands directory; what it has is prompt files —
// `<User>/prompts/<name>.prompt.md`, listed in the chat box as `/<name>`, with
// optional frontmatter naming the mode, a description and an argument hint.
// That is the same `User` directory the reader already walks for
// workspaceStorage, so one file per host it finds.
//
// This is the one surface deja had left there: Copilot Chat fires no
// session-start or per-prompt hook, so recall cannot arrive on its own, and
// until now the only way in was the MCP tool with nothing telling the model to
// reach for it (#3651).
func copilotChatPromptPaths() []string {
	var out []string
	for _, user := range sources.CopilotChatRoots() {
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
follow up with mode recall_context using a term from it.

If the deja tools are not available in this window, run the CLI in the
terminal:

`+"```bash\n"+`%s search -- "$ARGUMENTS"
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
		res, err := installTextFile(p, copilotChatPrompt(exe), uninstall)
		if err != nil {
			return installResult{}, err
		}
		results = append(results, res)
	}
	return wroteAll(results...), nil
}

// installTextFile writes a file deja owns whole, or takes it back out: an
// unchanged file reports unchanged, and an uninstall leaves nothing behind.
func installTextFile(path, body string, uninstall bool) (installResult, error) {
	if uninstall {
		if _, err := os.Stat(path); err != nil {
			return installResult{Path: path, Action: "unchanged"}, nil
		}
		if err := os.Remove(path); err != nil {
			return installResult{}, err
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
	action, err := writeIfChanged(path, old, []byte(body))
	if err != nil {
		return installResult{}, err
	}
	return installResult{Path: path, Action: action}, nil
}
