package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Every target that writes an MCP server has a row in the report. Nine did
// not: deepseek's entry on the author's machine pointed at a throwaway build
// for weeks with every other row repaired, because no row asked (#3661).
func TestEveryMCPTargetHasADoctorRow(t *testing.T) {
	hermeticEnv(t)
	rows := map[string]bool{}
	for _, c := range doctorMCPConfigs() {
		rows[c.name] = true
		if c.path == "" {
			t.Errorf("%s: row has no path — a report that names no file cannot be checked", c.name)
		}
	}
	// claude-code is the row's name for the claude target, and vscode and
	// copilot are rows for harnesses whose target is spelled differently.
	rows["claude"] = rows["claude-code"]
	for _, target := range installTargetNames() {
		if strings.HasSuffix(target, "-auto") {
			continue
		}
		switch target {
		case "statusline", "sync-timer", "aider":
			// No MCP client: aider's target writes a context file, the other
			// two are not harnesses.
			continue
		}
		if !writesMCPServer(t, target) {
			continue
		}
		if !rows[target] {
			t.Errorf("`deja install %s` writes an MCP server and doctor has no row for it", target)
		}
	}
}

// writesMCPServer runs the target against a temp HOME and reports whether it
// wrote a file naming deja's server. Read off the install rather than a list
// beside it: a list would only ever agree with itself.
func writesMCPServer(t *testing.T, target string) bool {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	if _, err := installTarget(target, filepath.Join(home, "bin", "deja"), false); err != nil {
		return false
	}
	found := false
	_ = filepath.Walk(home, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || found {
			return nil
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		text := string(b)
		if strings.Contains(text, `"deja"`) && strings.Contains(text, "mcp") ||
			strings.Contains(text, "serverName: deja") {
			found = true
		}
		return nil
	})
	return found
}

// dsh has no server key at all — the name is a field in a patch-list row — so
// the reader that names the wired binary stopped one line short of it and a
// stale build there was reported as healthy.
func TestTheCommandIsReadableInADSHPatchLayer(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cordis.patch.yml")
	layer := "- insert:\n" +
		"    - id: mcp-deja\n" +
		"      name: '@deepseek-ai/dsh-mcp-client'\n" +
		"      config:\n" +
		"        serverName: deja\n" +
		"        transport: stdio\n" +
		"        command: \"/scratch/deja-probe\"\n" +
		"        args: ['mcp']\n"
	if err := os.WriteFile(path, []byte(layer), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := dejaCommandIn(path); got != "/scratch/deja-probe" {
		t.Errorf("dejaCommandIn = %q, want the command beside serverName", got)
	}
	if !doctorDSHWired(path) {
		t.Error("a layer holding deja's entry did not read as wired")
	}
	// Someone else's entry keeps its own command out of it.
	other := filepath.Join(dir, "other.yml")
	if err := os.WriteFile(other, []byte("- insert:\n    - id: mcp-x\n      config:\n        serverName: x\n        command: \"/scratch/x\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := dejaCommandIn(other); got != "" {
		t.Errorf("another server's command was read as deja's: %q", got)
	}
	if doctorDSHWired(other) {
		t.Error("a layer without deja's entry read as wired")
	}
}

// goose keeps a `slash_commands` list at the bottom of the same config it keeps
// its MCP extensions in, and one of those commands is called `deja`. The
// whole-file scan answered with that name — a bare word, so neither binary
// check had anything to look at — and the extension three lines from the top,
// pointing at a build in a scratch directory, was never read (#3662).
func TestASlashCommandDoesNotMaskTheWiredBinary(t *testing.T) {
	dir := t.TempDir()
	stray := filepath.Join(dir, "tmp", "deja-shapes")
	if err := os.MkdirAll(filepath.Dir(stray), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(stray, []byte("x"), 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "config.yaml")
	body := "extensions:\n  deja:\n    enabled: true\n    type: stdio\n    name: deja\n" +
		"    cmd: \"" + stray + "\"\n    args:\n      - \"mcp\"\n    timeout: 60\n" +
		"  developer:\n    enabled: true\n    type: builtin\n" +
		"slash_commands:\n  - command: \"deja\"\n    prompt: \"search past sessions\"\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := dejaCommandIn(path); got != stray {
		t.Errorf("dejaCommandIn = %q, want the extension's binary %q", got, stray)
	}
	was := wiringPathIsTemporary
	wiringPathIsTemporary = func(p string) bool { return p == stray }
	t.Cleanup(func() { wiringPathIsTemporary = was })
	if note := otherBinaryNote(path, "goose"); !strings.Contains(note, stray) {
		t.Errorf("note = %q, want it to name the build in the scratch directory", note)
	}
}

// The guidance column said "unsupported" about files deja had written: four
// harnesses' manuals come from their own install target rather than from the
// generic guidance step.
func TestGuidanceStatusSeesAHarnessOwnSkillFile(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	for _, h := range []string{"kilocode", "gjc", "commandcode", "kiro"} {
		if got := guidanceStatus(h); got != "missing" {
			t.Errorf("%s: nothing installed, status = %q, want missing", h, got)
		}
	}
	if _, err := installTarget("kilocode", filepath.Join(home, "bin", "deja"), false); err != nil {
		t.Fatal(err)
	}
	if _, err := installSkillFile(kilocodeSkillPath(), false); err != nil {
		t.Fatal(err)
	}
	if got := guidanceStatus("kilocode"); got != "written" {
		t.Errorf("with the skill on disk, status = %q, want written", got)
	}
}
