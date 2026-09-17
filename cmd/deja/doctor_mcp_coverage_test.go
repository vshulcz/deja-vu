package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
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

// OpenClaw and ZCode keep their servers one level deeper, under `mcp.servers`.
// That map was read as if it were a single entry, so nothing here could name
// the binary either of them runs and both rows reported `wired` about builds in
// a scratch directory (#3663).
func TestTheCommandIsReadableUnderNestedMCPServers(t *testing.T) {
	dir := t.TempDir()
	stray := filepath.Join(dir, "tmp", "deja-prog2")
	if err := os.MkdirAll(filepath.Dir(stray), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(stray, []byte("x"), 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "openclaw.json")
	body, err := json.Marshal(map[string]any{
		"mcp": map[string]any{"servers": map[string]any{
			"deja": map[string]any{"command": stray, "args": []string{"mcp"}},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatal(err)
	}
	if got := dejaCommandIn(path); got != stray {
		t.Errorf("dejaCommandIn = %q, want the nested entry's command %q", got, stray)
	}
	was := wiringPathIsTemporary
	wiringPathIsTemporary = func(p string) bool { return p == stray }
	t.Cleanup(func() { wiringPathIsTemporary = was })
	if note := otherBinaryNote(path, "openclaw"); !strings.Contains(note, stray) {
		t.Errorf("note = %q, want it to name the build in the scratch directory", note)
	}
	// And a binary that is gone is the other half of the same read.
	if err := os.Remove(stray); err != nil {
		t.Fatal(err)
	}
	if got := dejaCommandMissing(path); got != stray {
		t.Errorf("dejaCommandMissing = %q, want %q", got, stray)
	}
}

// A TOML table keeps every key at the header's own indent, so the scan that
// looked for the command *under* the anchor stopped at the first sibling that
// was not one — `type = "stdio"`, the line codex writes right after the header.
// A codex config whose command is not the first key read as naming no binary at
// all, and the row said `wired` about a build in a scratch directory (#3668).
func TestTheCommandIsReadableAnywhereInATOMLTable(t *testing.T) {
	dir := t.TempDir()
	stray := filepath.Join(dir, "tmp", "deja-prog")
	if err := os.MkdirAll(filepath.Dir(stray), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(stray, []byte("x"), 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "config.toml")
	body := "model = \"gpt-5\"\n\n[mcp_servers.deja]\ntype = \"stdio\"\ncommand = " +
		strconv.Quote(stray) + "\nargs = [\"mcp\"]\nstartup_timeout_sec = 30\n\n" +
		"[mcp_servers.other]\ncommand = \"/usr/bin/other\"\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := dejaCommandIn(path); got != stray {
		t.Errorf("dejaCommandIn = %q, want deja's own command %q", got, stray)
	}
	was := wiringPathIsTemporary
	wiringPathIsTemporary = func(p string) bool { return p == stray }
	t.Cleanup(func() { wiringPathIsTemporary = was })
	if note := otherBinaryNote(path, "codex"); !strings.Contains(note, stray) {
		t.Errorf("note = %q, want it to name the build in the scratch directory", note)
	}
	// And the table that ends before a command of its own is not answered for
	// by the table after it.
	empty := filepath.Join(dir, "empty.toml")
	emptyBody := "[mcp_servers.deja]\ntype = \"stdio\"\n\n[mcp_servers.other]\ncommand = \"/usr/bin/other\"\n"
	if err := os.WriteFile(empty, []byte(emptyBody), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := dejaCommandIn(empty); got != "" {
		t.Errorf("another table's command was read as deja's: %q", got)
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

// Zed's server key is the id its extension owns — `deja-context-server`, not
// `deja` — and its settings carry comments, so the file is read as text. With a
// build under another name in that entry, neither the name test nor the `deja`
// key matched and the row said `wired` about a server pointing into a scratch
// directory (#3683).
func TestTheCommandIsReadableUnderDejasOtherKeys(t *testing.T) {
	dir := t.TempDir()
	stray := filepath.Join(dir, "tmp", "deja-arm")
	if err := os.MkdirAll(filepath.Dir(stray), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(stray, []byte("x"), 0o755); err != nil {
		t.Fatal(err)
	}

	// JSONC, the shape Zed writes: a comment at the top is what keeps this out
	// of the JSON branch.
	jsonc := filepath.Join(dir, "settings.json")
	body := "// Zed settings\n{\n  \"context_servers\": {\n    \"deja-context-server\": {\n" +
		"      \"args\": [\n        \"mcp\"\n      ],\n      \"command\": " +
		strconv.Quote(stray) + "\n    }\n  }\n}\n"
	if err := os.WriteFile(jsonc, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := dejaCommandIn(jsonc); got != stray {
		t.Errorf("jsonc: dejaCommandIn = %q, want %q", got, stray)
	}

	// And the same key in a file that does parse as JSON.
	plain := filepath.Join(dir, "plain.json")
	b, err := json.Marshal(map[string]any{"context_servers": map[string]any{
		"deja-context-server": map[string]any{"command": stray, "args": []string{"mcp"}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(plain, b, 0o600); err != nil {
		t.Fatal(err)
	}
	if got := dejaCommandIn(plain); got != stray {
		t.Errorf("json: dejaCommandIn = %q, want %q", got, stray)
	}

	// Somebody else's server keeps its own command out of it, whichever shape.
	other := filepath.Join(dir, "other.json")
	ob, err := json.Marshal(map[string]any{"context_servers": map[string]any{
		"memory": map[string]any{"command": "/usr/local/bin/memory"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(other, ob, 0o600); err != nil {
		t.Fatal(err)
	}
	if got := dejaCommandIn(other); got != "" {
		t.Errorf("another server's command was read as deja's: %q", got)
	}
}
