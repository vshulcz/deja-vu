package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// An uninstall takes back the snapshot it made of a config that already had
// deja in it — "so the reader does not find the binary they just removed back
// in their config directory", as the comment on that path puts it. Ownership is
// read from the content, and the marker list knew every spelling except the two
// that do not write the word on its own: zed's context-server id and the dsh
// patch block (#2575).
func TestUninstallTakesBackItsOwnBackupForEveryHarness(t *testing.T) {
	for _, tc := range []struct {
		name    string
		content string
	}{
		{"zed", `{"context_servers":{"` + zedServerID + `":{"command":"/opt/bin/dj","args":["mcp"]}}}`},
		{"deepseek", dshBlockStart + "\n- insert:\n    - id: mcp-deja\n      config:\n        serverName: deja\n"},
		{"claude", `{"mcpServers":{"deja":{"command":"/opt/bin/dj"}}}`},
		{"a config of the reader's own", "theme: One Dark\nfont: 15\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "settings.json")
			if err := os.WriteFile(path, []byte(tc.content), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path+".bak", []byte(tc.content), 0o600); err != nil {
				t.Fatal(err)
			}
			old := removingWiring
			removingWiring = true
			t.Cleanup(func() { removingWiring = old })
			if _, err := writeIfChanged(path, []byte(tc.content), []byte("{}\n")); err != nil {
				t.Fatal(err)
			}
			_, err := os.Stat(path + ".bak")
			mine := !strings.Contains(tc.name, "reader's own")
			if mine && !os.IsNotExist(err) {
				t.Errorf("a snapshot of deja's own wiring survived the uninstall: %v", err)
			}
			if !mine && err != nil {
				t.Errorf("the reader's own backup was taken away: %v", err)
			}
		})
	}
}
