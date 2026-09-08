package main

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// `--auto` wires what it finds, and it found nineteen harnesses of twenty-five:
// Amp, prime-agent, Crush, Continue, Zed and VS Code had install targets and
// were in the published matrix with nothing looking for them, so a machine that
// had them got the others wired and no word about these (#3192).
//
// Derived from the target list rather than written beside it: a harness that
// gains a target and no way to be detected fails here, which is the check that
// was missing.
func TestEveryInstallTargetCanBeDetected(t *testing.T) {
	hermeticEnv(t)
	checks := existingTargetChecks()
	// Detection reports Claude under the name its target does not use, and
	// Copilot Chat is wired by the vscode target.
	alias := map[string]string{"claude": "claude-code", "copilot-chat": "vscode"}
	// Not harnesses: one is this machine's own status line, the other a timer.
	notAHarness := map[string]bool{"statusline": true, "sync-timer": true}

	var missing []string
	for _, target := range installTargetNames() {
		base := strings.TrimSuffix(target, "-auto")
		if notAHarness[base] {
			continue
		}
		if a, ok := alias[base]; ok {
			base = a
		}
		if _, ok := checks[base]; !ok {
			missing = append(missing, base)
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		t.Errorf("install targets nothing looks for, so --auto never wires them: %s",
			strings.Join(unique(missing), ", "))
	}
}

func unique(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range in {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}

// The other half: a directory deja writes itself must not count as the harness
// being here, or one install makes every machine look like every machine.
func TestDetectionDoesNotFireOnWhatDejaWrites(t *testing.T) {
	home := hermeticEnv(t)
	home = filepath.Join(home, "home")

	// The configs install writes, for the six harnesses this test is about.
	written := []string{
		filepath.Join(home, ".config", "crush", "crush.json"),
		filepath.Join(home, ".continue", "config.yaml"),
		filepath.Join(home, ".agents", "skills", "deja-history", "SKILL.md"),
	}
	for _, p := range written {
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("{}"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range existingTargets() {
		if name == "crush" || name == "continue" {
			t.Errorf("a config deja writes made the machine look like a %s machine", name)
		}
	}

	// And with what the harness itself writes, the same machine is detected.
	for _, d := range []string{
		filepath.Join(home, ".local", "share", "crush"),
		filepath.Join(home, ".continue", "sessions"),
		filepath.Join(home, ".local", "share", "amp", "threads"),
		filepath.Join(home, ".prime", "agent", "sessions"),
	} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(home, ".local", "share", "crush", "projects.json"),
		[]byte(`{"projects":[]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, name := range existingTargets() {
		got[name] = true
	}
	for _, name := range []string{"crush", "continue", "amp", "prime"} {
		if !got[name] {
			t.Errorf("%s was not detected from the directory it writes for itself", name)
		}
	}
}
