package main

import (
	"os"
	"path/filepath"
)

// installSkillFile writes deja's shared manual at path, or takes it back out.
// It is installKilocodeSkill with the location as an argument: every harness
// that reads skills from its own directory needs the same three behaviours —
// the file is deja's own so an edited copy is kept rather than replaced, an
// unchanged file reports unchanged, and an uninstall leaves nothing behind.
func installSkillFile(path string, uninstall bool) (installResult, error) {
	old, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return installResult{}, err
	}
	if uninstall {
		if len(old) == 0 {
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
	next := []byte(skillFile(skillBody))
	if string(old) == string(next) {
		return installResult{Path: path, Action: "unchanged"}, nil
	}
	if err := writeSkillOfOurs(path, old, next); err != nil {
		return installResult{}, err
	}
	action := "wrote"
	if len(old) > 0 {
		action = "updated"
	}
	return installResult{Path: path, Action: action}, nil
}
