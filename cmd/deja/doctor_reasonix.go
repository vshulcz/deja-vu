package main

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// reasonixPackageEnabled reports whether Reasonix holds an enabled record of
// deja's package. Doctor reads it: a directory with no record loads nothing.
func reasonixPackageEnabled() bool {
	st, _, err := readReasonixState(reasonixStatePath())
	if err != nil {
		return false
	}
	i, _ := st.ours()
	if i < 0 {
		return false
	}
	var e reasonixEntry
	return json.Unmarshal(st.Plugins[i], &e) == nil && e.Enabled
}

// reasonixRuntimeMissing is the runtime command the installed package names
// when that file is gone, and "" otherwise. The command is a field of its own
// in the manifest, so the text scan doctor runs over hook files cannot see it.
func reasonixRuntimeMissing() string {
	b, err := os.ReadFile(reasonixInstalledManifest())
	if err != nil {
		return ""
	}
	var m struct {
		Runtime struct {
			Command string `json:"command"`
		} `json:"runtime"`
	}
	if json.Unmarshal(b, &m) != nil || !filepath.IsAbs(m.Runtime.Command) {
		return ""
	}
	if _, err := os.Stat(m.Runtime.Command); err != nil {
		return m.Runtime.Command
	}
	return ""
}

func reasonixRuntimeMissingFor(harness string) string {
	if harness != "reasonix" {
		return ""
	}
	return reasonixRuntimeMissing()
}
