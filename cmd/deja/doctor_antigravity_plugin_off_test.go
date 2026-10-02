package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// `agy plugin disable deja` keeps the hook file and records the switch in
// config.json, so doctor read the row as wired while Antigravity loaded
// nothing from the plugin (#4359).
func TestDoctorMarksTheAntigravityPluginSwitchedOff(t *testing.T) {
	hermeticEnv(t)
	bin := filepath.Join(os.Getenv("HOME"), "bin", "deja")
	if err := os.MkdirAll(filepath.Dir(bin), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(bin, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := installAntigravityAuto(bin, false); err != nil {
		t.Fatal(err)
	}
	config := filepath.Join(antigravityConfigHome(), "config.json")
	plugin := filepath.Join(antigravityConfigHome(), "plugins", "deja", "plugin.json")
	write := func(path, body string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	row := func() doctorAutoStatus {
		t.Helper()
		out, err := captureRun(t, "doctor", "--json")
		if err != nil {
			t.Fatal(err)
		}
		var report struct {
			AutoRecall []doctorAutoStatus `json:"auto_recall"`
		}
		if err := json.Unmarshal([]byte(out), &report); err != nil {
			t.Fatalf("doctor --json: %v: %s", err, out)
		}
		for _, r := range report.AutoRecall {
			if r.Name == "antigravity" {
				return r
			}
		}
		t.Fatalf("no antigravity row:\n%s", out)
		return doctorAutoStatus{}
	}

	if r := row(); r.State != "wired" || r.SwitchedOff {
		t.Fatalf("freshly installed: %+v, want wired and on", r)
	}

	// What agy 1.2.5 writes for `agy plugin disable deja`.
	write(config, "{\n  \"plugins\": {\n    \"deja\": {\n      \"enabled\": false\n    }\n  }\n}\n")
	if r := row(); r.State != "wired" || !r.SwitchedOff {
		t.Errorf("after plugin disable: %+v, want wired and switched_off", r)
	}
	text, err := captureRun(t, "doctor")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(text, "agy plugin enable deja") {
		t.Errorf("the text report does not say the plugin is off:\n%s", text)
	}

	// config.json wins over plugin.json either way: switched back on, a
	// plugin.json that ships disabled does not turn it off.
	write(config, `{"plugins":{"deja":{"enabled":true}}}`)
	write(plugin, `{"name":"deja","disabled":true}`)
	if r := row(); r.SwitchedOff {
		t.Errorf("enabled in config.json: %+v, want on", r)
	}
	// With no entry, plugin.json's own declaration decides.
	if err := os.Remove(config); err != nil {
		t.Fatal(err)
	}
	if r := row(); !r.SwitchedOff {
		t.Errorf("plugin.json disabled, no config entry: %+v, want switched_off", r)
	}
}
