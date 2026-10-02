package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// `kiro-auto` is one edit from `kimi-auto`, and following that hint wires a
// different agent. A name that is a known target plus `-auto` says so (#4301).
func TestUnknownAutoTargetPointsAtThePlainOne(t *testing.T) {
	for _, h := range []string{"kiro", "roo"} {
		err := unknownTargetError(h + "-auto")
		if err == nil {
			t.Fatalf("%s-auto: no error", h)
		}
		msg := err.Error()
		if !strings.Contains(msg, h+" has no -auto target") || !strings.Contains(msg, "deja install "+h) {
			t.Errorf("%s-auto: %q, want it to point at `deja install %s`", h, msg, h)
		}
		if strings.Contains(msg, "did you mean") {
			t.Errorf("%s-auto: %q names another agent", h, msg)
		}
	}
	// A typo still gets the nearest name.
	if msg := unknownTargetError("claud").Error(); !strings.Contains(msg, "did you mean") {
		t.Errorf("claud: %q, want the nearest target", msg)
	}
}

// A deja entry the user switched off stays off, and the run says so even when
// nothing else changed: the note was dropped with the unchanged MCP write,
// and the line named the steering file (#4302).
func TestInstallKiroSaysTheEntryIsSwitchedOff(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	if _, err := installKiro("/usr/local/bin/deja", false); err != nil {
		t.Fatal(err)
	}
	cfg := filepath.Join(home, ".kiro", "settings", "mcp.json")
	switchOffDeja(t, cfg)

	res, err := installKiro("/usr/local/bin/deja", false)
	if err != nil {
		t.Fatal(err)
	}
	if res.Path != cfg {
		t.Errorf("path = %q, want the mcp.json the note is about", res.Path)
	}
	if !strings.Contains(res.Note, "switched off") {
		t.Errorf("note = %q, want it to say the entry is still off", res.Note)
	}
}

// Doctor read a switched-off entry as wired, though the client will never start
// it (#4303). Cursor goes through the same check.
func TestDoctorMarksASwitchedOffMCPEntry(t *testing.T) {
	hermeticEnv(t)
	home := os.Getenv("HOME")
	bin := filepath.Join(home, "bin", "deja")
	if err := os.MkdirAll(filepath.Dir(bin), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(bin, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	kiro := kiroMCPSettingsPath()
	cursor := filepath.Join(home, ".cursor", "mcp.json")
	for _, p := range []string{kiro, cursor} {
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		body := `{"mcpServers":{"deja":{"command":"` + filepath.ToSlash(bin) + `","args":["mcp"],"disabled":true}}}`
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
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
	seen := 0
	for _, r := range report.MCP {
		if r.Name != "kiro" && r.Name != "cursor" {
			continue
		}
		seen++
		if !r.SwitchedOff {
			t.Errorf("%s row = %+v, want switched_off", r.Name, r)
		}
	}
	if seen != 2 {
		t.Fatalf("rows seen = %d:\n%s", seen, out)
	}
	text, err := captureRun(t, "doctor")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(text, "switched off") < 2 {
		t.Errorf("the text report does not say the entries are off:\n%s", text)
	}

	// An entry that is on is not marked.
	body := `{"mcpServers":{"deja":{"command":"` + filepath.ToSlash(bin) + `","args":["mcp"]}}}`
	if err := os.WriteFile(kiro, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if dejaEntrySwitchedOff(kiro) {
		t.Error("an entry that is on reads as switched off")
	}
}

func switchOffDeja(t *testing.T, cfg string) {
	t.Helper()
	b, err := os.ReadFile(cfg)
	if err != nil {
		t.Fatal(err)
	}
	var root map[string]any
	if err := json.Unmarshal(b, &root); err != nil {
		t.Fatal(err)
	}
	root["mcpServers"].(map[string]any)["deja"].(map[string]any)["disabled"] = true
	out, err := json.MarshalIndent(root, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cfg, out, 0o644); err != nil {
		t.Fatal(err)
	}
}
