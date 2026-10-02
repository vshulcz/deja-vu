package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// The text report has said an MCP entry points at a deja binary that is gone
// since #2216; the JSON row for the same entry read plain "wired", so a script
// could tell a dead hook (binary_missing, #3502) from a live one but not a dead
// server (#4177).
func TestDoctorJSONMarksAnMCPEntryWhoseBinaryIsGone(t *testing.T) {
	hermeticEnv(t)
	home := os.Getenv("HOME")
	cfg := filepath.Join(home, ".codex", "config.toml")
	if err := os.MkdirAll(filepath.Dir(cfg), 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(bin string) {
		t.Helper()
		body := "[mcp_servers.deja]\ncommand = \"" + filepath.ToSlash(bin) + "\"\nargs = [\"mcp\"]\n"
		if err := os.WriteFile(cfg, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	row := func() doctorMCPStatus {
		t.Helper()
		out, err := captureRun(t, "doctor", "--json")
		if err != nil {
			t.Fatal(err)
		}
		var report struct {
			MCP []doctorMCPStatus `json:"mcp"`
		}
		if err := json.Unmarshal([]byte(out), &report); err != nil {
			t.Fatalf("doctor --json: %v: %s", err, out)
		}
		for _, r := range report.MCP {
			if r.Name == "codex" {
				return r
			}
		}
		t.Fatalf("no codex row:\n%s", out)
		return doctorMCPStatus{}
	}

	write(filepath.Join(home, "nowhere", "deja"))
	if got := row(); got.State != "wired" || !got.BinaryMissing {
		t.Errorf("an entry naming a binary that is gone reads %+v, want wired and binary_missing", got)
	}

	bin := filepath.Join(home, "bin", "deja")
	if err := os.MkdirAll(filepath.Dir(bin), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(bin, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	write(bin)
	if got := row(); got.State != "wired" || got.BinaryMissing {
		t.Errorf("an entry naming a binary that is there reads %+v, want wired and not binary_missing", got)
	}
}
