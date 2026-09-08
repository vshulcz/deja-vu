package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Grok's install writes two files — config.toml for the CLI and
// user-settings.json for grok-dev — and the result only ever carried one of
// them, so the snapshot beside the other was never counted: the uninstall said
// "kept 1 snapshot" with two of them on disk (#3388).
func TestGrokInstallReportsBothFilesItWrote(t *testing.T) {
	tmp := hermeticEnv(t)
	grok := filepath.Join(tmp, "grok")
	if err := os.MkdirAll(grok, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_GROK_ROOT", grok)
	t.Setenv("GROK_HOME", grok)
	write := func(name, body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(grok, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("config.toml", "model = \"grok-4\"\n[mcp_servers.other]\ncommand = \"x\"\n")
	write("user-settings.json", "{\"theme\":\"dark\"}\n")

	res, err := installGrok("/usr/local/bin/deja", false)
	if err != nil {
		t.Fatal(err)
	}
	var named []string
	for _, p := range res.touched() {
		named = append(named, filepath.Base(p))
	}
	joined := strings.Join(named, " ")
	if !strings.Contains(joined, "config.toml") || !strings.Contains(joined, "user-settings.json") {
		t.Errorf("touched = %v, want both files the install wrote", named)
	}
}
