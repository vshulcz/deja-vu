package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The issue template asks a reporter to run `deja doctor` and redact the local
// paths before pasting. The report is the one command that fills itself with
// them, and deja already contracts a home path to ~ in its progress lines
// (#2360).
func TestDoctorContractsTheHomePath(t *testing.T) {
	tmp := hermeticEnv(t)
	home := filepath.Join(tmp, "home")
	if err := os.MkdirAll(filepath.Join(home, ".claude", "projects", "-work-app"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(home, ".claude", "projects"))

	out, err := captureRun(t, "doctor")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "~/.claude/projects") && !strings.Contains(out, `~\.claude\projects`) {
		t.Errorf("doctor does not contract the home path:\n%s", storeLines(out))
	}
	if strings.Contains(out, home) {
		t.Errorf("doctor still prints the home directory:\n%s", storeLines(out))
	}

	// --json keeps the real path: a tool reading it may need one, and nobody
	// pastes JSON into an issue by hand.
	raw, err := captureRun(t, "doctor", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var report map[string]any
	if err := json.Unmarshal([]byte(raw), &report); err != nil {
		t.Fatalf("doctor --json is not JSON: %v", err)
	}
	// As JSON writes it: a Windows path is escaped in the encoding, so looking
	// for the raw spelling is looking for something no encoder produces.
	encoded, err := json.Marshal(home)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(raw, strings.Trim(string(encoded), `"`)) {
		t.Errorf("doctor --json lost the real path")
	}
}

// storeLines is the part of the report that names paths, for a readable
// failure.
func storeLines(out string) string {
	var keep []string
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, "claude") || strings.Contains(line, "index") {
			keep = append(keep, line)
		}
	}
	return strings.Join(keep, "\n")
}
