package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/sources"
)

// pi before 0.99 has no MCP of its own: ~/.pi/agent/mcp.json is read by the
// pi-mcp-adapter package and nothing else. Without it in pi's packages the
// server deja declares there never starts, and the row said wired (#4583).
func TestDoctorPiMCPRowNeedsTheAdapter(t *testing.T) {
	hermeticEnv(t)
	fakePiOnPath(t, "@mariozechner/pi-coding-agent", "0.73.1")
	if _, err := installPiAuto("/bin/deja", false); err != nil {
		t.Fatalf("install: %v", err)
	}
	row := func() (string, doctorMCPStatus) {
		var out bytes.Buffer
		doctorMCP(&out)
		var line string
		for l := range strings.SplitSeq(out.String(), "\n") {
			if strings.HasPrefix(l, "  pi ") {
				line = l
			}
		}
		for _, r := range collectDoctorMCP() {
			if r.Name == "pi" {
				return out.String(), r
			}
		}
		t.Fatalf("no pi row:\n%s", out.String())
		return line, doctorMCPStatus{}
	}
	text, js := row()
	if js.State == "wired" || strings.Contains(text, "  pi           wired") {
		t.Errorf("pi has no adapter to read mcp.json and the row says wired:\n%s\njson: %+v", text, js)
	}
	if !strings.Contains(text, "pi-mcp-adapter") || !strings.Contains(js.Note, "pi-mcp-adapter") {
		t.Errorf("the row does not name the package pi needs:\n%s\njson: %+v", text, js)
	}

	// The control: with the adapter in pi's packages the same file is wired.
	settings := filepath.Join(sources.PiConfigDir(), "settings.json")
	if err := os.WriteFile(settings, []byte(`{"packages":["npm:pi-mcp-adapter"]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if text, js := row(); js.State != "wired" || !strings.Contains(text, "  pi           wired") {
		t.Errorf("with the adapter installed the row is not wired:\n%s\njson: %+v", text, js)
	}
}

// And install says so, rather than writing a file nothing will read and
// reporting it as done.
func TestInstallPiSaysTheAdapterIsNeeded(t *testing.T) {
	hermeticEnv(t)
	fakePiOnPath(t, "@mariozechner/pi-coding-agent", "0.73.1")
	r, err := installPiAuto("/bin/deja", false)
	if err != nil {
		t.Fatalf("install: %v", err)
	}
	if !strings.Contains(r.Note, "pi-mcp-adapter") {
		t.Errorf("install wrote mcp.json without saying pi needs pi-mcp-adapter to read it: %+v", r)
	}
}

// pi also loads an extension from a path in settings.json "extensions" and
// from whatever sits in its extensions/ directory, so an adapter cloned or
// pointed at that way is installed too.
func TestPiAdapterFoundOutsidePackages(t *testing.T) {
	for _, tc := range []struct {
		name  string
		setup func(dir string) error
	}{
		{"settings extensions", func(dir string) error {
			return os.WriteFile(filepath.Join(dir, "settings.json"), []byte(`{"extensions":["/src/pi-mcp-adapter"]}`), 0o644)
		}},
		{"extensions dir", func(dir string) error {
			return os.MkdirAll(filepath.Join(dir, "extensions", "pi-mcp-adapter"), 0o755)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			hermeticEnv(t)
			dir := sources.PiConfigDir()
			if err := os.MkdirAll(dir, 0o755); err != nil {
				t.Fatal(err)
			}
			if piMCPAdapterInstalled() {
				t.Fatal("adapter found before it was put there")
			}
			if err := tc.setup(dir); err != nil {
				t.Fatal(err)
			}
			if !piMCPAdapterInstalled() {
				t.Error("pi loads pi-mcp-adapter this way and doctor says it is missing")
			}
		})
	}
}
