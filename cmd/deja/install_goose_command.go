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
		i++
		// The entry's remaining keys are indented further than its dash.
		for i < len(lines) && strings.HasPrefix(strings.TrimRight(lines[i], " "), "    ") {
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
		if j < len(lines) && strings.HasPrefix(lines[j], "  ") {
			out = append(out, lines[i])
			continue
		}
		// Nothing under it: drop the key and the blank lines that followed.
		i = j - 1
	}
	return strings.Join(out, "\n")
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

	next := removeGooseSlashCommand(string(old))
	if !uninstall {
		entry := fmt.Sprintf("  - command: \"deja\"\n    recipe_path: %s\n", yamlQuote(recipe))
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
	a, err := writeIfChanged(path, old, []byte(next))
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
