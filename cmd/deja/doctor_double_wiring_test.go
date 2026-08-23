package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func zedSettings(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "settings.json")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// Installing the extension after `deja install zed` leaves both entries, and
// neither side can see the other: the install skip only fires the other way
// round, and the extension reads no settings.
func TestZedWiredTwiceOnlyWhenBothAreThere(t *testing.T) {
	both := zedSettings(t, `{
  "context_servers": {
    "deja": {"command": "deja", "args": ["mcp"]},
    "deja-context-server": {"enabled": true}
  }
}`)
	if !zedWiredTwice(both) {
		t.Error("both entries present, reported as fine")
	}

	for name, body := range map[string]string{
		"only ours":               `{"context_servers": {"deja": {"command": "deja"}}}`,
		"only the extension":      `{"context_servers": {"deja-context-server": {"enabled": true}}}`,
		"another server entirely": `{"context_servers": {"other": {}}}`,
		"no block at all":         `{"theme": "One Dark"}`,
	} {
		if zedWiredTwice(zedSettings(t, body)) {
			t.Errorf("%s: reported as wired twice", name)
		}
	}

	if zedWiredTwice(filepath.Join(t.TempDir(), "nothing.json")) {
		t.Error("a machine with no settings file is not wired twice")
	}
}

// The line has to name the fix, not just the problem: a warning nobody can act
// on is noise in the one command people run when memory misbehaves.
func TestDoctorDoubleWiringNamesTheFix(t *testing.T) {
	t.Setenv("DEJA_ZED_CONFIG", zedSettings(t, `{
  "context_servers": {
    "deja": {"command": "deja"},
    "deja-context-server": {"enabled": true}
  }
}`))
	var out bytes.Buffer
	doctorDoubleWiring(&out)
	got := out.String()
	if !strings.Contains(got, "zed") || !strings.Contains(got, "deja uninstall zed") {
		t.Fatalf("doctor line does not say what to do:\n%s", got)
	}

	t.Setenv("DEJA_ZED_CONFIG", zedSettings(t, `{"context_servers": {"deja": {"command": "deja"}}}`))
	out.Reset()
	doctorDoubleWiring(&out)
	if out.Len() != 0 {
		t.Fatalf("a single entry printed a warning:\n%s", out.String())
	}
}
