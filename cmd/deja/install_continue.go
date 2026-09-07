package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/vshulcz/deja-vu/internal/sources"
)

// Continue keeps one assistant config, and both surfaces deja can reach live in
// it: `mcpServers:` is where the tool comes from, and `prompts:` is where a
// slash command does. Both are sequences of mappings rather than the keyed
// objects most harnesses use, so the writer appends an item and finds ours
// again by its name.
//
// Measured on @continuedev/cli 1.5.47 against a recording endpoint: with the
// server in this file, `deja` is in the tool list of every request, and a
// scripted call came back with the seeded decision in the bytes Continue sent
// next. Continue's hooks system — Claude-shaped, Claude's own event set — is in the
// bundle and loads a config from ~/.continue/settings.json, but nothing fires
// it in that release: hooks written for every event produced no process and no
// additionalContext, while the same run's tool call went through. It becomes
// work the day a release fires them (#3062).
func continueConfigPath() string {
	return filepath.Join(sources.ContinueRoot(), "config.yaml")
}

// continueSkillPath is where Continue reads skills from: the global folder's
// skills directory. What lands there is named in the `Skills` tool's own
// description, so it is in front of the model on every turn — which is what
// makes recall arrive without being asked for.
func continueSkillPath() string {
	return filepath.Join(sources.ContinueRoot(), "skills", "deja-history", "SKILL.md")
}

const continuePromptBody = "Search this machine's past AI coding sessions with the deja tool " +
	"(mode: recall) and answer from what it returns. The query is everything after the command; " +
	"an exact error, a function name or a path is the strongest one."

func installContinue(exe string, uninstall bool) (installResult, error) {
	path := continueConfigPath()
	old, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return installResult{}, err
	}
	body, crlf := normaliseNewlines(string(old))
	next := removeContinueItem(body, "mcpServers", "deja")
	next = removeContinueItem(next, "prompts", "deja")
	next = dropEmptyYAMLKey(next, "mcpServers:")
	next = dropEmptyYAMLKey(next, "prompts:")

	if !uninstall {
		cmd, args := mcpCommandArgs(exe)
		var server strings.Builder
		fmt.Fprintf(&server, "  - name: deja\n    command: %s\n    args:\n", yamlQuote(cmd))
		for _, a := range args {
			fmt.Fprintf(&server, "      - %s\n", yamlQuote(a))
		}
		var prompt strings.Builder
		fmt.Fprintf(&prompt, "  - name: deja\n    description: Search this machine's past coding sessions\n")
		fmt.Fprintf(&prompt, "    prompt: %s\n", yamlQuote(continuePromptBody))

		for _, add := range []struct{ key, item string }{
			{"mcpServers", server.String()},
			{"prompts", prompt.String()},
		} {
			// An inline value — `prompts: []` — is not a block, and appending
			// under it would leave the key twice, of which a parser takes one.
			// The goose writer refuses the same shape for the same reason.
			if v := inlineYAMLValue(next, add.key+":"); v != "" {
				return installResult{}, fmt.Errorf("%s: %s: %s is on one line, and deja edits the block form — move it to a block and run this again", shortHome(path), add.key, v)
			}
			next = appendYAMLListItem(next, add.key, add.item)
		}
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return installResult{}, err
	}
	next = keepTrailingNewline(string(old), next)
	if crlf {
		next = strings.ReplaceAll(next, "\n", "\r\n")
	}
	a, err := writeIfChanged(path, old, []byte(next))
	return installResult{Path: path, Action: a}, err
}

// appendYAMLListItem puts item at the end of key's block, or writes the key
// with it when the file has none.
func appendYAMLListItem(text, key, item string) string {
	head := "\n" + key + ":\n"
	at := strings.Index("\n"+text, head)
	if at < 0 {
		if text != "" && !strings.HasSuffix(text, "\n") {
			text += "\n"
		}
		return text + key + ":\n" + item
	}
	start := at + len(head) - 1
	end := yamlBlockEnd(text, start)
	return text[:end] + item + text[end:]
}

// yamlBlockEnd is where the indented block that starts at from ends: the first
// line after it that is neither indented nor blank.
func yamlBlockEnd(text string, from int) int {
	i := from
	for i < len(text) {
		nl := strings.IndexByte(text[i:], '\n')
		line := text[i:]
		if nl >= 0 {
			line = text[i : i+nl]
		}
		if strings.TrimSpace(line) != "" && !strings.HasPrefix(line, " ") && !strings.HasPrefix(line, "\t") {
			return i
		}
		if nl < 0 {
			return len(text)
		}
		i += nl + 1
	}
	return len(text)
}

// removeContinueItem drops the list item under key whose mapping opens with the
// given name. Ours is found by the name rather than by a marker comment,
// because that is the field Continue itself keys these lists by.
func removeContinueItem(text, key, name string) string {
	head := "\n" + key + ":\n"
	at := strings.Index("\n"+text, head)
	if at < 0 {
		return text
	}
	start := at + len(head) - 1
	end := yamlBlockEnd(text, start)
	block := text[start:end]
	lines := strings.Split(block, "\n")
	var kept []string
	dropping := false
	dash := ""
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		isItem := strings.HasPrefix(trimmed, "- ")
		if dropping {
			// The item runs until the next item at the same indent, until
			// something less indented, or until the block ends. A blank line
			// is the end of it too — deja writes none inside its own item, and
			// treating one as part of it swallowed the newline the next key
			// needed.
			switch {
			case trimmed == "":
				dropping = false
			case isItem && yamlIndentOf(line) <= len(dash):
				dropping = false
			case yamlIndentOf(line) <= len(dash):
				dropping = false
			default:
				continue
			}
		}
		if isItem && strings.HasPrefix(trimmed, "- name: ") &&
			strings.TrimSpace(strings.TrimPrefix(trimmed, "- name:")) == name {
			dropping = true
			dash = strings.Repeat(" ", yamlIndentOf(line))
			continue
		}
		kept = append(kept, line)
	}
	return text[:start] + strings.Join(kept, "\n") + text[end:]
}

func yamlIndentOf(line string) int {
	return len(line) - len(strings.TrimLeft(line, " \t"))
}
