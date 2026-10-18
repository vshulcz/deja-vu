package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/sources"
)

// OpenClaw loads a non-bundled plugin only when a non-empty plugins.allow
// names it, and none at all under plugins.enabled: false. Install and doctor
// called deja's plugin wired while `openclaw plugins list` had it disabled.
func TestOpenClawPluginOffByAllowListOrMasterSwitch(t *testing.T) {
	for _, tc := range []struct {
		name, plugins, want string
	}{
		{"allow list without deja", `{"allow": ["my-plugin"]}`, "plugins.allow"},
		{"allow list with padded names", `{"allow": [" my-plugin ", ""]}`, "plugins.allow"},
		{"plugins switched off", `{"enabled": false}`, "plugins.enabled"},
		{"allow list naming deja", `{"allow": ["my-plugin", " deja "]}`, ""},
		{"empty allow list", `{"allow": []}`, ""},
		{"allow list of empty names", `{"allow": ["", "  "]}`, ""},
		{"memory slot names deja", `{"allow": ["x"], "slots": {"memory": "deja"}}`, ""},
		{"context engine slot names deja", `{"allow": ["x"], "slots": {"contextEngine": "deja"}}`, ""},
		{"plugins on", `{"enabled": true}`, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			hermeticEnv(t)
			path := filepath.Join(sources.OpenClawStateDir(), "openclaw.json")
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte("{\n  \"plugins\": "+tc.plugins+"\n}\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			out, err := captureRun(t, "install", "openclaw-auto", "--no-index")
			if err != nil {
				t.Fatalf("install: %v\n%s", err, out)
			}
			var row *doctorAutoStatus
			for _, st := range collectDoctorAutoRecall() {
				if st.Name == "openclaw" {
					st := st
					row = &st
				}
			}
			if row == nil {
				t.Fatal("no openclaw row in doctor's auto recall")
			}
			doctor, _ := captureRun(t, "doctor")
			if tc.want == "" {
				if row.SwitchedOff || strings.Contains(out, "switched off") || strings.Contains(out, "blocked") {
					t.Fatalf("plugin is on, but reported off:\n%s\nswitched_off=%v", out, row.SwitchedOff)
				}
				return
			}
			if !strings.Contains(out, tc.want) {
				t.Errorf("install did not say %s keeps the plugin off:\n%s", tc.want, out)
			}
			if !row.SwitchedOff {
				t.Errorf("doctor auto recall: openclaw row not switched off: %+v", *row)
			}
			if !strings.Contains(doctor, tc.want) {
				t.Errorf("doctor text did not name %s:\n%s", tc.want, doctor)
			}
		})
	}
}
