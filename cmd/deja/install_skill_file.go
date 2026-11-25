package main

import (
	"bytes"
	"os"
	"path/filepath"
)

// installSkillFile writes deja's shared manual at path, or takes it back out.
// It is installKilocodeSkill with the location as an argument: every harness
// that reads skills from its own directory needs the same three behaviours —
// the file is deja's own so an edited copy is kept rather than replaced, an
// unchanged file reports unchanged, and an uninstall leaves nothing behind.
func installSkillFile(path string, uninstall bool) (installResult, error) {
	return runSkillFileSpec(path, uninstall)
}

// dropRetiredSkillFile removes the manual deja wrote under `deja-search`, the
// CLI skill's name, beside the `deja-history` directory it writes now.
//
// A skill's name and its directory have to agree — Claude Code requires it and
// Gemini renames the loser of a collision — and that file declared
// `name: deja-history` inside a directory called `deja-search`, so on kilocode,
// gjc and Command Code the manual was a file the loader could drop without a
// word (#3700).
//
// The mismatch is what identifies it: a `deja-search` directory holding a skill
// that calls itself `deja-search` is the CLI skill, which deja also installs
// and which is nobody's to delete.
func dropRetiredSkillFile(path string) error {
	dir := filepath.Dir(path)
	if filepath.Base(dir) != "deja-history" {
		return nil
	}
	retired := filepath.Join(filepath.Dir(dir), cliSkillName, "SKILL.md")
	b, err := os.ReadFile(retired)
	if err != nil {
		return nil //nolint:nilerr // no retired skill to remove
	}
	if !bytes.Contains(b, []byte("\nname: deja-history\n")) {
		return nil
	}
	if err := os.Remove(retired); err != nil {
		return err
	}
	pruneCreatedDir(filepath.Dir(retired))
	return nil
}
