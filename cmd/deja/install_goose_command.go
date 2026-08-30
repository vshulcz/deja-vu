package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Goose is the one harness where a command is not a file in a commands
// directory. It reads a `slash_commands:` list out of config.yaml, and each
// entry points at a recipe file elsewhere on disk — so /deja needs both.
//
// config.yaml is edited textually for the same reason the MCP entry is: it
// holds provider settings someone wrote by hand, and a YAML round-trip would
// drop their comments and ordering.

func gooseRecipePath() string {
	return filepath.Join(gooseConfigDir(), "deja-recipe.yaml")
}

// gooseRecipe is the workflow /deja runs. version, title, description and
// instructions are the required fields.
func gooseRecipe(exe string) string {
	return `version: 1.0.0
title: deja
description: Search this machine's past AI coding sessions (deja-vu)
instructions: |
` + indentLines(commandBody(exe, "the user's request"), "  ")
}

func indentLines(s, pad string) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	for i, l := range lines {
		if l == "" {
			continue
		}
		lines[i] = pad + l
	}
	return strings.Join(lines, "\n") + "\n"
}

// gooseSlashCommandsBlock returns the text after the slash_commands key, for
// reading the indent its entries are written at.
func gooseSlashCommandsBlock(s string) string {
	i := strings.Index("\n"+s, "\nslash_commands:\n")
	if i < 0 {
		return ""
	}
	return s[i+len("\nslash_commands:\n")-1:]
}

// gooseListIndent is the indent the entries of a list are written at, and
// whether there is a list to read one from. Distinct from yamlBlockIndent
// because "" is an answer here: a sequence in the first column is what every
// YAML serializer writes, and an entry spliced in at two spaces beside it is a
// file no parser will read (#2724).
func gooseListIndent(block string) (string, bool) {
	for _, line := range strings.Split(block, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		if !strings.HasPrefix(trimmed, "- ") {
			return "", false
		}
		return line[:len(line)-len(strings.TrimLeft(line, " \t"))], true
	}
	return "", false
}

// normaliseGooseNewlines is what installGoose does to the same file for the
// same reason: a CRLF config read line by line leaves every line ending in \r,
// so nothing matches and the writer added a second key on every run.
func normaliseGooseNewlines(s string) string {
	return strings.ReplaceAll(s, "\r\n", "\n")
}

// removeGooseSlashCommand takes our entry out of the slash_commands list and
// leaves every other entry, and the key itself, where they were.
func removeGooseSlashCommand(s string) string {
	lines := strings.Split(s, "\n")
	out := make([]string, 0, len(lines))
	for i := 0; i < len(lines); i++ {
		// Quoted is what we write, but a hand-edited config may say
		// `- command: deja`, and missing that would add a second entry beside
		// the one already there rather than replacing it.
		if t := strings.TrimSpace(lines[i]); t != `- command: "deja"` && t != "- command: deja" && t != "- command: 'deja'" {
			out = append(out, lines[i])
			continue
		}
		dash := yamlIndentWidth(lines[i])
		i++
		// The entry's remaining keys are indented further than its dash — its
		// dash, not four spaces: a list written at four had its neighbours'
		// lines eaten as if they were ours, and uninstall came back with the
		// key and every entry gone (#2724).
		for i < len(lines) && strings.TrimSpace(lines[i]) != "" && yamlIndentWidth(lines[i]) > dash {
			i++
		}
		i--
	}
	return dropEmptyYAMLKey(strings.Join(out, "\n"), "slash_commands:")
}

// dropEmptyYAMLKey removes a top-level key that has no entries left under it.
// Goose reads such a key as null and refuses the whole config, so leaving one
// behind would break the harness rather than merely litter it.
func dropEmptyYAMLKey(s, key string) string {
	lines := strings.Split(s, "\n")
	out := make([]string, 0, len(lines))
	for i := 0; i < len(lines); i++ {
		if strings.TrimRight(lines[i], " ") != key {
			out = append(out, lines[i])
			continue
		}
		j := i + 1
		for j < len(lines) && strings.TrimSpace(lines[j]) == "" {
			j++
		}
		// Nested is "indented further than the key", not "starts with two
		// spaces": one space is valid YAML and is not two, so a config written
		// that way had its key dropped and its entries left behind as a stray
		// list — which goose then cannot read at all (#2724). A tab is not
		// legal YAML indentation, and is treated as indentation here anyway,
		// because leaving a file alone is the safe answer for one this cannot
		// have written.
		if j < len(lines) && yamlLineBelongsTo(lines[j], lines[i]) {
			out = append(out, lines[i])
			continue
		}
		// Nothing under it: drop the key and the blank lines that followed.
		i = j - 1
	}
	return strings.Join(out, "\n")
}

// keepTrailingNewline gives back what the reader's file ended with. Dropping an
// empty key takes the blank lines after it, and the last of those is the file's
// own final newline — so a config someone owned came back without it, a diff in
// their dotfiles for nothing (#2606).
func keepTrailingNewline(old, next string) string {
	if strings.HasSuffix(old, "\n") && next != "" && !strings.HasSuffix(next, "\n") {
		return next + "\n"
	}
	return next
}

func installGooseCommand(exe string, uninstall bool) (installResult, error) {
	path := filepath.Join(gooseConfigDir(), "config.yaml")
	old, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return installResult{}, err
	}
	recipe := gooseRecipePath()
	if !uninstall {
		if err := os.MkdirAll(filepath.Dir(recipe), 0o755); err != nil {
			return installResult{}, err
		}
		oldRecipe, _ := os.ReadFile(recipe)
		if _, err := writeIfChanged(recipe, oldRecipe, []byte(gooseRecipe(exe))); err != nil {
			return installResult{}, err
		}
	}

	next := removeGooseSlashCommand(normaliseGooseNewlines(string(old)))
	if !uninstall {
		// At the indent the list already uses, the way the extensions writer
		// does: a two-space entry spliced into a list written at one, three or
		// four is a file no YAML parser will read (#2724).
		pad, ok := gooseListIndent(gooseSlashCommandsBlock(next))
		if !ok {
			pad = "  "
		}
		entry := fmt.Sprintf("%s- command: \"deja\"\n%s  recipe_path: %s\n", pad, pad, yamlQuote(recipe))
		if i := strings.Index("\n"+next, "\nslash_commands:\n"); i >= 0 {
			at := i + len("\nslash_commands:\n") - 1
			next = next[:at] + entry + next[at:]
		} else {
			if next != "" && !strings.HasSuffix(next, "\n") {
				next += "\n"
			}
			next += "slash_commands:\n" + entry
		}
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return installResult{}, err
	}
	a, err := writeIfChanged(path, old, []byte(keepTrailingNewline(string(old), next)))
	if err != nil {
		return installResult{}, err
	}
	// The recipe goes last on the way out. Removing it first and then failing
	// to rewrite the config would leave a command pointing at a file that is
	// no longer there, which fails when someone types /deja rather than now.
	if uninstall {
		if !restoredGuidance[recipe] {
			if err := os.Remove(recipe); err != nil && !os.IsNotExist(err) {
				return installResult{}, err
			}
			// And put back the recipe install replaced, or drop the copy when
			// it holds deja's own — the rules the skills and command files
			// have since #2581 and #2600. Without it the reader's recipe was
			// destroyed and their copy left as a .bak beside nothing (#2602).
			if _, err := restoreReplacedFile(recipe, mentionsDeja); err != nil {
				return installResult{}, err
			}
		}
	}
	return installResult{Path: path, Action: a}, nil
}

// yamlLineBelongsTo reports whether a line is part of the block under a key.
//
// Indented further is the ordinary shape. A sequence at the key's own indent is
// the other one, and it is what every YAML serializer writes —
// `slash_commands:` followed by `- command: mine` in the first column is a key
// with an entry, not an empty key beside a stray list, and reading it as the
// second dropped the key and left the entries orphaned (#2724).
func yamlLineBelongsTo(line, key string) bool {
	switch {
	case yamlIndentWidth(line) > yamlIndentWidth(key):
		return true
	case yamlIndentWidth(line) == yamlIndentWidth(key):
		return strings.HasPrefix(strings.TrimLeft(line, " \t"), "- ")
	}
	return false
}

// yamlIndentWidth is how far a line is indented, counting spaces and tabs
// alike. YAML forbids tabs, so this is not reading a shape deja writes — it is
// refusing to mistake one for the top level.
func yamlIndentWidth(line string) int {
	return len(line) - len(strings.TrimLeft(line, " \t"))
}
