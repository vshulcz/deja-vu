package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/vshulcz/deja-vu/internal/sources"
)

// The Copilot CLI row read the guidance skill, so it said wired with no deja
// server in mcp-config.json and could never see a moved binary (#4232).
func TestDoctorReadsCopilotsMCPConfig(t *testing.T) {
	hermeticEnv(t)
	if _, err := captureRun(t, "install", "copilot", "--no-index"); err != nil {
		t.Fatal(err)
	}
	var row doctorMCPConfig
	for _, c := range doctorMCPConfigs() {
		if c.name == "copilot" {
			row = c
		}
	}
	want := filepath.Join(sources.Home(), ".copilot", "mcp-config.json")
	if row.path != want {
		t.Fatalf("copilot row reads %s, want %s", row.path, want)
	}
	if !row.wired(row.path) {
		t.Fatal("not wired right after install")
	}
	b, err := os.ReadFile(want)
	if err != nil {
		t.Fatal(err)
	}
	var cfg map[string]map[string]any
	if err := json.Unmarshal(b, &cfg); err != nil {
		t.Fatal(err)
	}
	delete(cfg["mcpServers"], "deja")
	b, _ = json.Marshal(cfg)
	if err := os.WriteFile(want, b, 0o644); err != nil {
		t.Fatal(err)
	}
	if row.wired(row.path) {
		t.Error("still wired with no deja server in mcp-config.json")
	}
}
