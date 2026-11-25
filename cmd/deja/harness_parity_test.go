package main

import (
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"
)

func runUninstallStep(t *testing.T, fn func() (installResult, error)) installResult {
	t.Helper()
	prev := removingWiring
	removingWiring = true
	defer func() { removingWiring = prev }()
	res, err := fn()
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func TestHarnessParityJSONAndDoctor(t *testing.T) {
	home := hermeticEnv(t)
	cfgPath := filepath.Join(home, "client", "mcp.json")
	exe := filepath.Join(home, "bin", "deja")
	entry := map[string]any{"command": exe, "args": []string{"mcp"}}

	res, err := installMCPJSONEntry(cfgPath, "mcpServers", entry, false)
	if err != nil {
		t.Fatalf("install: %v", err)
	}
	if res.Action != "created" {
		t.Fatalf("first action = %q, want created", res.Action)
	}
	if !doctorJSONWired("mcpServers")(cfgPath) {
		t.Fatal("doctorJSONWired reported false after install")
	}
	if keys := doctorJSONDejaKeys("mcpServers")(cfgPath); !reflect.DeepEqual(keys, []string{"deja"}) {
		t.Fatalf("doctorJSONDejaKeys = %v, want [deja]", keys)
	}

	res2, err := installMCPJSONEntry(cfgPath, "mcpServers", entry, false)
	if err != nil || res2.Action != "unchanged" {
		t.Fatalf("second install = %+v, %v", res2, err)
	}

	ures := runUninstallStep(t, func() (installResult, error) {
		return installMCPJSONEntry(cfgPath, "mcpServers", entry, true)
	})
	if ures.Action != "removed" {
		t.Fatalf("uninstall action = %q, want removed", ures.Action)
	}
	if doctorJSONWired("mcpServers")(cfgPath) {
		t.Fatal("doctorJSONWired reported true after uninstall")
	}
	if _, err := os.Stat(cfgPath); !os.IsNotExist(err) {
		t.Fatalf("expected created config %s to be deleted on uninstall, err=%v", cfgPath, err)
	}
}

func TestHarnessParityCopilotAndOpenClaw(t *testing.T) {
	home := hermeticEnv(t)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("COPILOT_HOME", filepath.Join(home, ".copilot"))
	t.Setenv("OPENCLAW_STATE_DIR", filepath.Join(home, ".openclaw"))

	exe := filepath.Join(home, "bin", "deja")
	copRes, err := installCopilotMCP(exe, false)
	if err != nil || copRes.Action != "created" {
		t.Fatalf("installCopilotMCP = %+v, %v", copRes, err)
	}
	copPath := copilotMCPConfigPath()
	if !doctorJSONWired("mcpServers")(copPath) {
		t.Fatal("copilot config not detected by doctorJSONWired")
	}
	if copRes2, cerr := installCopilotMCP(exe, false); cerr != nil || copRes2.Action != "unchanged" {
		t.Fatalf("second installCopilotMCP = %+v, %v", copRes2, cerr)
	}

	clawPath := filepath.Join(home, ".openclaw", "openclaw.json")
	if mkErr := os.MkdirAll(filepath.Dir(clawPath), 0o755); mkErr != nil {
		t.Fatal(mkErr)
	}
	seedClaw := "// openclaw comment\n{\n  \"theme\": \"dark\",\n}\n"
	if wErr := os.WriteFile(clawPath, []byte(seedClaw), 0o600); wErr != nil {
		t.Fatal(wErr)
	}
	clawRes, err := installOpenClawMCPEntry(exe, false)
	if err != nil {
		t.Fatalf("installOpenClawMCPEntry: %v", err)
	}
	if clawRes.Action != "updated" {
		t.Fatalf("openclaw action = %q, want updated", clawRes.Action)
	}
	gotClaw := readFileT(t, clawPath)
	if !strings.Contains(gotClaw, "// openclaw comment") {
		t.Fatalf("lost JSONC comment in openclaw.json:\n%s", gotClaw)
	}
	if !doctorOpenClawWired(clawPath) {
		t.Fatal("doctorOpenClawWired false after install")
	}

	_ = runUninstallStep(t, func() (installResult, error) {
		return installOpenClawMCPEntry(exe, true)
	})
	_ = runUninstallStep(t, func() (installResult, error) {
		return installCopilotMCP(exe, true)
	})
	if doctorOpenClawWired(clawPath) {
		t.Fatal("openclaw still wired after uninstall")
	}
}

func TestHarnessParityJSONDisabledAndAdoption(t *testing.T) {
	home := hermeticEnv(t)
	cfgPath := filepath.Join(home, "disabled.json")
	exe := filepath.Join(home, "bin", "deja")
	entry := map[string]any{"command": exe, "args": []string{"mcp"}}

	initial := `{
  "mcpServers": {
    "deja-vu": {
      "command": "/old/bin/deja",
      "args": ["mcp"],
      "disabled": true
    }
  }
}
`
	if err := os.WriteFile(cfgPath, []byte(initial), 0o600); err != nil {
		t.Fatal(err)
	}
	res, err := installMCPJSONEntry(cfgPath, "mcpServers", entry, false)
	if err != nil {
		t.Fatalf("install: %v", err)
	}
	if !strings.Contains(res.Note, switchedOffNote) {
		t.Fatalf("expected switchedOffNote, got %q", res.Note)
	}
	if keys := doctorJSONDejaKeys("mcpServers")(cfgPath); !reflect.DeepEqual(keys, []string{"deja-vu"}) {
		t.Fatalf("adopted keys = %v, want [deja-vu]", keys)
	}
	body := readFileT(t, cfgPath)
	if !strings.Contains(body, `"disabled": true`) {
		t.Fatalf("disabled flag lost:\n%s", body)
	}

	// Uninstall removes adopted entry deja had rewritten.
	ures := runUninstallStep(t, func() (installResult, error) {
		return installMCPJSONEntry(cfgPath, "mcpServers", entry, true)
	})
	if ures.Action != "updated" {
		t.Fatalf("uninstall action = %q, want updated", ures.Action)
	}
	if doctorJSONWired("mcpServers")(cfgPath) {
		t.Fatal("adopted entry remained wired after uninstall")
	}

	// Repurposed foreign key stays untouched on uninstall with a note.
	other := `{
  "mcpServers": {
    "deja": {"command": ` + strconv.Quote(exe) + `, "args": ["mcp"]},
    "other-deja": {"command": "/custom/deja", "args": ["mcp"]}
  }
}
`
	otherPath := filepath.Join(home, "other.json")
	if err := os.WriteFile(otherPath, []byte(other), 0o600); err != nil {
		t.Fatal(err)
	}
	u2 := runUninstallStep(t, func() (installResult, error) {
		return installMCPJSONEntry(otherPath, "mcpServers", entry, true)
	})
	if !strings.Contains(u2.Note, "other-deja") {
		t.Fatalf("expected left note naming other-deja, got %q", u2.Note)
	}
	if !strings.Contains(readFileT(t, otherPath), "other-deja") {
		t.Fatal("uninstall removed unowned other-deja entry")
	}
}

func TestHarnessParityTOMLAndDoctor(t *testing.T) {
	home := hermeticEnv(t)
	cfgPath := filepath.Join(home, "config.toml")
	exe := filepath.Join(home, "bin", "deja")
	block := "[mcp_servers.deja]\ncommand = " + strconv.Quote(exe) + "\nargs = [\"mcp\"]\n"

	seed := "model = \"o3\"\n\n[mcp_servers.\"deja.old\"]\ncommand = \"/old/deja\"\nargs = [\"mcp\"]\nenabled = false\n"
	if err := os.WriteFile(cfgPath, []byte(seed), 0o600); err != nil {
		t.Fatal(err)
	}

	res, err := installTOML(cfgPath, block, false)
	if err != nil {
		t.Fatalf("installTOML: %v", err)
	}
	if !strings.Contains(res.Note, "deja.old") || !strings.Contains(res.Note, "already ran deja") {
		t.Fatalf("unexpected adoption note = %q", res.Note)
	}
	got := readFileT(t, cfgPath)
	if !strings.Contains(got, "enabled = false") {
		t.Fatalf("expected enabled = false preserved:\n%s", got)
	}
	if !doctorTOMLWired(cfgPath) {
		t.Fatal("doctorTOMLWired false after TOML install")
	}
	if keys := doctorTOMLDejaKeys(cfgPath); !slices.Contains(keys, `"deja.old"`) {
		t.Fatalf("doctorTOMLDejaKeys = %v, want \"deja.old\"", keys)
	}

	if res2, cerr := installTOML(cfgPath, block, false); cerr != nil || res2.Action != "unchanged" {
		t.Fatalf("second installTOML = %+v, %v", res2, cerr)
	}

	ures := runUninstallStep(t, func() (installResult, error) {
		return installTOML(cfgPath, block, true)
	})
	if ures.Action != "updated" {
		t.Fatalf("uninstall action = %q, want updated", ures.Action)
	}
	if doctorTOMLWired(cfgPath) {
		t.Fatal("doctorTOMLWired still true after uninstall")
	}
}

func TestHarnessParitySkillFileAndRetiredCleanup(t *testing.T) {
	home := hermeticEnv(t)
	skillPath := filepath.Join(home, "agent", "skills", "deja-history", "SKILL.md")
	retiredPath := filepath.Join(home, "agent", "skills", "deja-search", "SKILL.md")
	if err := os.MkdirAll(filepath.Dir(retiredPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(retiredPath, []byte("---\nname: deja-history\n---\n# retired manual\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	res, err := installSkillFile(skillPath, false)
	if err != nil {
		t.Fatalf("installSkillFile: %v", err)
	}
	if res.Action != "wrote" {
		t.Fatalf("first action = %q, want wrote", res.Action)
	}
	if _, statErr := os.Stat(retiredPath); !os.IsNotExist(statErr) {
		t.Fatalf("expected retired skill %s to be deleted", retiredPath)
	}
	if res2, cerr := installSkillFile(skillPath, false); cerr != nil || res2.Action != "unchanged" {
		t.Fatalf("second installSkillFile = %+v, %v", res2, cerr)
	}

	ures := runUninstallStep(t, func() (installResult, error) {
		return installSkillFile(skillPath, true)
	})
	if ures.Action != "removed" {
		t.Fatalf("uninstall action = %q, want removed", ures.Action)
	}
	if _, statErr := os.Stat(skillPath); !os.IsNotExist(statErr) {
		t.Fatalf("expected skill file removed after uninstall, err=%v", statErr)
	}
}
