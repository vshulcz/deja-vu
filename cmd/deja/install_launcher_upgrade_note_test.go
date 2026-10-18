package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// OpenClaw's MCP entry moved to the launcher (#5323), and every upgrade then
// said "replaced the deja entry that was already here, which ran <deja>" of
// the very binary the launcher wraps — the noise #2479 took out. Said only
// when the old entry ran some other deja.
func TestOpenClawUpgradeToLauncherSaysNothingOfTheSameDeja(t *testing.T) {
	if dejaLauncherPath() == "" {
		t.Skip("no launcher on this platform; the entry keeps the binary's path")
	}
	for name, tc := range map[string]struct {
		was   func(exe string) string
		jsonc bool
		quiet bool
	}{
		"this binary":         {was: func(exe string) string { return exe }, quiet: true},
		"this binary, JSONC":  {was: func(exe string) string { return exe }, jsonc: true, quiet: true},
		"an older launcher":   {was: func(string) string { return "/old/.config/deja/bin/deja-hook" }, quiet: true},
		"another deja":        {was: func(string) string { return "/opt/other/deja" }},
		"another deja, JSONC": {was: func(string) string { return "/opt/other/deja" }, jsonc: true},
	} {
		t.Run(name, func(t *testing.T) {
			tmp := hermeticEnv(t)
			t.Setenv("OPENCLAW_STATE_DIR", filepath.Join(tmp, "openclaw"))
			exe := filepath.Join(tmp, "deja")
			if err := os.WriteFile(exe, []byte("#!/bin/sh\n"), 0o755); err != nil {
				t.Fatal(err)
			}
			was, _ := json.Marshal(tc.was(exe))
			cfg := `{"mcp": {"servers": {"deja": {"command": ` + string(was) + `, "args": ["mcp"]}}}}`
			if tc.jsonc {
				cfg = "// mine\n" + cfg
			}
			path := openclawConfigPath()
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte(cfg), 0o644); err != nil {
				t.Fatal(err)
			}
			res, err := installOpenClawMCP(exe, false)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(readFile(t, path), dejaLauncherPath()) {
				t.Fatalf("the entry did not move to the launcher:\n%s", readFile(t, path))
			}
			said := strings.Contains(res.Note, "replaced the deja entry")
			if tc.quiet && said {
				t.Errorf("an upgrade from the same deja says: %s", res.Note)
			}
			if !tc.quiet && (!said || !strings.Contains(res.Note, "/opt/other/deja")) {
				t.Errorf("taking over another deja's entry went unsaid: %q", res.Note)
			}
		})
	}
}
