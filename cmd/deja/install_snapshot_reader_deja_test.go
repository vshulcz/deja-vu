package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/vshulcz/deja-vu/internal/sources"
)

// A reader who lists "deja" in plugins.allow, as doctor tells them to
// (#5256), wrote the name into their own config. The first uninstall write
// took that for deja's wiring and deleted the snapshot of it, so the writes
// after it had nothing to hand back and the reader's one-line blocks came
// back split (#5281).
func TestOpenClawUninstallKeepsASnapshotThatOnlyAllowsDeja(t *testing.T) {
	hermeticEnv(t)
	path := filepath.Join(sources.OpenClawStateDir(), "openclaw.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	seed := "// user settings\n{\n  // top comment\n" +
		"  \"agents\": {\"defaults\": {\"model\": \"anthropic/x\"}},\n" +
		"  \"plugins\": {\"allow\": [\"my-plugin\", \"deja\"], \"entries\": {\"my-plugin\": {\"enabled\": true}}, \"load\": {\"paths\": [\"/opt/my-plugin\"]}},\n" +
		"  \"hooks\": {\"internal\": {\"enabled\": true, \"entries\": {\"session-memory\": {\"enabled\": true}}}},\n" +
		"  \"mcp\": {\"servers\": {\"zz-mine\": {\"command\": \"my-server\"}}}\n}\n"
	if err := os.WriteFile(path, []byte(seed), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, err := captureRun(t, "install", "openclaw-auto", "--no-index"); err != nil {
		t.Fatalf("install: %v\n%s", err, out)
	}
	if out, err := captureRun(t, "uninstall", "openclaw-auto"); err != nil {
		t.Fatalf("uninstall: %v\n%s", err, out)
	}
	if got := readFile(t, path); got != seed {
		t.Errorf("uninstall left:\n%s\nwant:\n%s", got, seed)
	}
	if got := readFile(t, path+".bak"); got != seed {
		t.Errorf("the reader's snapshot is gone or changed:\n%s", got)
	}
}

// The name as a value of the reader's is theirs; the name as a key, or a
// command of deja's, is deja's wiring and its snapshot still goes.
func TestDropOwnBackupTellsTheReadersDejaFromDejas(t *testing.T) {
	for _, tc := range []struct {
		name, bak string
		kept      bool
	}{
		{"allow list naming deja", `{"plugins": {"allow": ["deja"]}}`, true},
		{"commented allow list naming deja", "{\n  // \"deja\" goes here\n  \"plugins\": {\"allow\": [ \"deja\" ]}\n}", true},
		{"deja's entry", `{"mcpServers": {"deja": {"command": "/opt/x/deja", "args": ["mcp"]}}}`, false},
		{"deja's entry beside the reader's allow", `{"allow": ["deja"], "mcp": {"servers": {"deja": {"command": "/opt/x/deja", "args": ["mcp"]}}}}`, false},
		{"deja's hook behind the reader's &&", `{"allow": ["deja"], "statusLine": {"command": "cd /x && /opt/x/deja statusline"}}`, false},
		{"deja's hook", `{"hooks": [{"command": "/opt/x/deja-hook hook-context --x"}]}`, false},
		{"not JSON, deja's TOML table", "[mcp_servers.deja]\ncommand = \"/opt/x/deja\"\nargs = [\"mcp\"]\n", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "c.json")
			if err := os.WriteFile(path+".bak", []byte(tc.bak), 0o644); err != nil {
				t.Fatal(err)
			}
			dropOwnBackup(path)
			_, err := os.Stat(path + ".bak")
			if kept := err == nil; kept != tc.kept {
				t.Errorf("kept = %v, want %v", kept, tc.kept)
			}
		})
	}
}
