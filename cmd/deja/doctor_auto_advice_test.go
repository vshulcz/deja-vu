package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The MCP install writes the same config the hooks live in, so doctor found a
// file without the hook marker and told the reader to reinstall — which writes
// the MCP entry again and no hook. The hook comes from the -auto target
// (#3313).
func TestDoctorNamesTheAutoTargetForAnMCPOnlyInstall(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	if _, err := installCrushMCP("/usr/local/bin/deja", false); err != nil {
		t.Fatalf("install crush: %v", err)
	}
	var buf bytes.Buffer
	doctorAutoRecall(&buf)
	var line string
	for _, l := range strings.Split(buf.String(), "\n") {
		if strings.HasPrefix(strings.TrimSpace(l), "crush ") {
			line = l
		}
	}
	if line == "" {
		t.Fatalf("no crush row:\n%s", buf.String())
	}
	if !strings.Contains(line, "deja install crush-auto") {
		t.Errorf("the row does not name the command that adds the hook: %q", line)
	}
	if strings.Contains(line, "reinstall)") {
		t.Errorf("the row still advises the install that cannot fix it: %q", line)
	}
	_ = os.Remove(filepath.Join(home, ".config", "crush", "crush.json"))
}
