package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/sources"
)

// Codex 0.163 warns that `mcp_servers.deja.type` is a setting it does not
// know, and picks stdio from `command` anyway (#5590).

const oldCodexBlockWithType = "model = \"gpt-5\"\n\n[mcp_servers.deja]\ntype = \"stdio\"\ncommand = \"/old/bin/deja\"\nargs = [\"mcp\"]\nstartup_timeout_sec = 30\n"

func codexConfigForTest(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(sources.CodexHome(), "config.toml")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if body != "" {
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return path
}

func codexDejaBlock(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, blk := range tomlMCPBlocks(string(b)) {
		if blk.key == "deja" {
			lines := strings.Split(string(b), "\n")
			return strings.Join(lines[blk.start:blk.end], "\n")
		}
	}
	t.Fatalf("no [mcp_servers.deja] in:\n%s", b)
	return ""
}

func blockHasKey(block, key string) bool {
	for line := range strings.SplitSeq(block, "\n") {
		if k, _, ok := tomlLineKeyValue(line); ok && k == key {
			return true
		}
	}
	return false
}

func fakeDejaExe(t *testing.T) string {
	t.Helper()
	exe := filepath.Join(t.TempDir(), "deja")
	if err := os.WriteFile(exe, []byte("x"), 0o755); err != nil {
		t.Fatal(err)
	}
	return exe
}

func TestInstallCodexWritesNoType(t *testing.T) {
	for name, install := range map[string]func(string, bool) (installResult, error){
		"codex":      installCodex,
		"codex-auto": installCodexAuto,
	} {
		t.Run(name, func(t *testing.T) {
			hermeticEnv(t)
			path := codexConfigForTest(t, "")
			if _, err := install(fakeDejaExe(t), false); err != nil {
				t.Fatal(err)
			}
			block := codexDejaBlock(t, path)
			if blockHasKey(block, "type") {
				t.Errorf("a fresh install wrote `type` for codex:\n%s", block)
			}
			if !blockHasKey(block, "command") || !blockHasKey(block, "args") {
				t.Errorf("the block lost command or args:\n%s", block)
			}
		})
	}
}

func TestReinstallCodexDropsOldType(t *testing.T) {
	hermeticEnv(t)
	path := codexConfigForTest(t, oldCodexBlockWithType)
	exe := fakeDejaExe(t)
	if _, err := installCodex(exe, false); err != nil {
		t.Fatal(err)
	}
	block := codexDejaBlock(t, path)
	if blockHasKey(block, "type") {
		t.Errorf("a reinstall kept the old `type` line:\n%s", block)
	}
	if !strings.Contains(block, "startup_timeout_sec = 30") {
		t.Errorf("a reinstall dropped the reader's own key:\n%s", block)
	}
	if want, _ := mcpLaunchCommandArgs(exe, false); dejaCommandIn(path) != want {
		t.Errorf("the block runs %q, want %q", dejaCommandIn(path), want)
	}
	// And a second run has nothing left to change.
	res, err := installCodex(exe, false)
	if err != nil {
		t.Fatal(err)
	}
	if res.Action != "unchanged" {
		t.Errorf("a repeat install reported %q, want unchanged", res.Action)
	}
}

func TestUninstallCodexRemovesOldBlockWithType(t *testing.T) {
	hermeticEnv(t)
	path := codexConfigForTest(t, oldCodexBlockWithType)
	if _, err := installCodex(fakeDejaExe(t), true); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "mcp_servers.deja") || strings.Contains(string(b), "type =") {
		t.Errorf("uninstall left the old block behind:\n%s", b)
	}
	if !strings.Contains(string(b), `model = "gpt-5"`) {
		t.Errorf("uninstall took the reader's own settings:\n%s", b)
	}
}

// The block without `type` is what doctor reads from now on, so it has to be
// wired and name its command, the same as the old one did.
func TestDoctorReadsCodexBlockWithoutType(t *testing.T) {
	hermeticEnv(t)
	path := codexConfigForTest(t, "")
	exe := fakeDejaExe(t)
	if _, err := installCodex(exe, false); err != nil {
		t.Fatal(err)
	}
	if blockHasKey(codexDejaBlock(t, path), "type") {
		t.Fatalf("the install still writes `type`, so this measures nothing")
	}
	if !doctorTOMLWired(path) {
		t.Error("doctor does not count the block as wired")
	}
	if want, _ := mcpLaunchCommandArgs(exe, false); dejaCommandIn(path) != want {
		t.Errorf("dejaCommandIn = %q, want %q", dejaCommandIn(path), want)
	}
	if got := dejaCommandMissing(path); got != "" {
		t.Errorf("dejaCommandMissing = %q for a binary that exists", got)
	}
	var buf bytes.Buffer
	doctorMCP(&buf)
	var row string
	for line := range strings.SplitSeq(buf.String(), "\n") {
		if f := strings.Fields(line); len(f) > 1 && f[0] == "codex" {
			row = line
		}
	}
	if f := strings.Fields(row); len(f) < 2 || f[1] != "wired" {
		t.Errorf("codex row = %q, want wired\n%s", row, buf.String())
	}
}
