package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// continueInstalledConfig runs the installer against an empty home and returns
// the config it wrote — the artifact, not a description of it.
func continueInstalledConfig(t *testing.T, exe string) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("DEJA_CONTINUE_ROOT", filepath.Join(home, ".continue"))
	t.Setenv("CONTINUE_GLOBAL_DIR", "")
	if _, err := installContinue(exe, false); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(home, ".continue", "config.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// continueCommandLine is the `command:` line install writes on this platform.
// Windows spawns a stdio MCP client through cmd, so the executable is an
// argument there rather than the command — three of these tests asserted the
// POSIX shape and were red on the Windows leg and nowhere else.
func continueCommandLine(exe string) string {
	cmd, _ := mcpCommandArgs(exe)
	return `command: "` + cmd + `"`
}

// continueNamesBinary counts the entries that carry this executable, wherever
// the platform puts it: in `command:` or in the args list under it.
func continueNamesBinary(config, exe string) int {
	return strings.Count(config, `"`+exe+`"`)
}

// The Windows leg is the only place the wrapped shape appears, so pin the
// counter against both spellings here rather than only where it runs.
func TestContinueNamesBinaryCountsEitherShape(t *testing.T) {
	posix := "mcpServers:\n  - name: deja\n    command: \"/bin/deja\"\n    args:\n      - \"mcp\"\n"
	windows := "mcpServers:\n  - name: deja\n    command: \"cmd\"\n    args:\n      - \"/c\"\n      - \"/bin/deja\"\n      - \"mcp\"\n"
	for name, config := range map[string]string{"posix": posix, "windows": windows} {
		if got := continueNamesBinary(config, "/bin/deja"); got != 1 {
			t.Errorf("%s: the binary is named %d times, want once:\n%s", name, got, config)
		}
		if got := continueNamesBinary(config+config, "/bin/deja"); got != 2 {
			t.Errorf("%s: a doubled entry counted %d, so doubling would go unnoticed", name, got)
		}
	}
}

// Continue keeps the MCP server and the slash command in the same assistant
// config, both as sequences of mappings rather than the keyed objects every
// other harness uses. Measured on @continuedev/cli 1.5.47: with this entry the
// `deja` tool is in the tool list of every request (#3062).
func TestInstallContinueWritesTheServerAndTheCommand(t *testing.T) {
	got := continueInstalledConfig(t, "/usr/local/bin/deja")
	for _, want := range []string{
		"mcpServers:",
		"  - name: deja",
		continueCommandLine("/usr/local/bin/deja"),
		"      - \"mcp\"",
		"prompts:",
		"description: Search this machine's past coding sessions",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("the config does not carry %q:\n%s", want, got)
		}
	}
	// Two items, one under each key — not one key with both.
	if n := strings.Count(got, "- name: deja"); n != 2 {
		t.Fatalf("deja is named %d times, want once under each key:\n%s", n, got)
	}
}

// The config is the reader's own file: whatever else is in it survives, and a
// second install neither doubles the entry nor moves anything.
func TestInstallContinueKeepsTheRestOfTheConfig(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	root := filepath.Join(home, ".continue")
	t.Setenv("DEJA_CONTINUE_ROOT", root)
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "config.yaml")
	before := `name: mine
version: 0.0.1
models:
  - name: sonnet
    provider: anthropic
    model: claude-sonnet-4
mcpServers:
  - name: sqlite
    command: uvx
    args:
      - mcp-server-sqlite
rules:
  - be brief
`
	if err := os.WriteFile(path, []byte(before), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := installContinue("/bin/deja", false); err != nil {
		t.Fatal(err)
	}
	got := readFileString(t, path)
	for _, want := range []string{"name: mine", "- name: sonnet", "- name: sqlite", "- mcp-server-sqlite", "rules:", "  - be brief"} {
		if !strings.Contains(got, want) {
			t.Fatalf("the reader's own config lost %q:\n%s", want, got)
		}
	}
	// deja's server joins the list rather than replacing it.
	if strings.Index(got, "- name: sqlite") > strings.Index(got, "- name: deja") {
		t.Fatalf("deja was inserted ahead of the reader's own server:\n%s", got)
	}

	if _, err := installContinue("/bin/deja", false); err != nil {
		t.Fatal(err)
	}
	twice := readFileString(t, path)
	if continueNamesBinary(twice, "/bin/deja") != 1 {
		t.Fatalf("a second install doubled the entry:\n%s", twice)
	}

	// And the way out leaves the file as it was found.
	if _, err := installContinue("/bin/deja", true); err != nil {
		t.Fatal(err)
	}
	after := readFileString(t, path)
	if strings.Contains(after, "deja") {
		t.Fatalf("uninstall left deja behind:\n%s", after)
	}
	if !strings.Contains(after, "- name: sqlite") || !strings.Contains(after, "  - be brief") {
		t.Fatalf("uninstall took something that was not deja's:\n%s", after)
	}
}

// A binary path moves; the entry has to move with it rather than doubling.
func TestInstallContinueAdoptsAMovedBinary(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("DEJA_CONTINUE_ROOT", filepath.Join(home, ".continue"))
	if _, err := installContinue("/old/deja", false); err != nil {
		t.Fatal(err)
	}
	if _, err := installContinue("/new/deja", false); err != nil {
		t.Fatal(err)
	}
	got := readFileString(t, filepath.Join(home, ".continue", "config.yaml"))
	if strings.Contains(got, "/old/deja") {
		t.Fatalf("the old path is still there:\n%s", got)
	}
	if continueNamesBinary(got, "/new/deja") != 1 {
		t.Fatalf("the moved binary is not named once:\n%s", got)
	}
}

// An inline list is not a block, and appending under it would leave the key
// twice — of which a parser takes one, silently dropping the reader's servers.
// The goose writer refuses the same shape for the same reason.
func TestInstallContinueRefusesAnInlineList(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	root := filepath.Join(home, ".continue")
	t.Setenv("DEJA_CONTINUE_ROOT", root)
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "config.yaml")
	if err := os.WriteFile(path, []byte("name: mine\nmcpServers: []\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := installContinue("/bin/deja", false); err == nil {
		t.Fatal("an inline list was edited anyway")
	}
	if got := readFileString(t, path); !strings.Contains(got, "mcpServers: []") {
		t.Fatalf("the refused file was changed:\n%s", got)
	}
}

// Continue reads skills from its own global folder and names each one in the
// Skills tool's description — that is the surface that makes recall arrive
// without being asked for, so the file has to land there and nowhere else.
func TestContinueSkillLandsWhereContinueReadsIt(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	root := filepath.Join(home, ".continue")
	t.Setenv("DEJA_CONTINUE_ROOT", root)
	want := filepath.Join(root, "skills", "deja-history", "SKILL.md")
	if got := guidancePath("continue"); got != want {
		t.Fatalf("guidance path = %q, want %q", got, want)
	}
	if _, err := installGuidance("continue", false); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(want)
	if err != nil {
		t.Fatalf("the skill was not written where Continue reads: %v", err)
	}
	for _, line := range []string{"name: deja-history", "description:"} {
		if !strings.Contains(string(b), line) {
			t.Fatalf("the skill has no %q, so Continue lists nothing:\n%s", line, b)
		}
	}
}
