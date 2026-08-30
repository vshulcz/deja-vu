package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// handWiredMachine is a machine where the harnesses were configured by the
// reader before deja arrived, so every config deja edits gets a snapshot.
func handWiredMachine(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	for path, body := range map[string]string{
		".claude.json":          "{\"mine\":\"MINE\"}\n",
		".claude/settings.json": "{\"theme\":\"dark\"}\n",
		".codex/config.toml":    "[tools]\nweb = true\n",
		".cursor/mcp.json":      "{\"mcpServers\":{\"theirs\":{\"command\":\"x\"}}}\n",
	} {
		full := filepath.Join(home, filepath.FromSlash(path))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return home
}

// An uninstall keeps the snapshot it took of a config the reader already had —
// #840 settled that neither the config nor its snapshot is deja's to delete.
// It said nothing about them, on a screen that otherwise names what it keeps:
// #2487 added gemini's line for exactly that reason, and nine files on disk are
// the same shape (#2676).
func TestUninstallNamesTheSnapshotsItLeaves(t *testing.T) {
	home := handWiredMachine(t)
	if _, err := captureRun(t, "install", "--all", "--no-index"); err != nil {
		t.Fatalf("install: %v", err)
	}
	out, err := captureRunStderr(t, "uninstall", "--all")
	if err != nil {
		t.Fatalf("uninstall: %v", err)
	}
	left := 0
	_ = filepath.Walk(home, func(p string, fi os.FileInfo, err error) error {
		if err == nil && !fi.IsDir() && strings.HasSuffix(p, ".bak") {
			left++
		}
		return nil
	})
	if left == 0 {
		t.Fatal("nothing was backed up, so this pins nothing")
	}
	if !strings.Contains(out, ".bak") {
		t.Fatalf("%d snapshots left on disk and the uninstall names none of them:\n%s", left, out)
	}
}

// A machine deja set up from nothing has no snapshots to name, and a line about
// none of them is noise.
func TestUninstallSaysNothingWhenItLeftNoSnapshots(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	for _, d := range []string{".claude", ".codex", ".cursor"} {
		if err := os.MkdirAll(filepath.Join(home, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := captureRun(t, "install", "--all", "--no-index"); err != nil {
		t.Fatalf("install: %v", err)
	}
	out, err := captureRunStderr(t, "uninstall", "--all")
	if err != nil {
		t.Fatalf("uninstall: %v", err)
	}
	if strings.Contains(out, ".bak") {
		t.Fatalf("nothing was snapshotted here, and the uninstall talked about snapshots:\n%s", out)
	}
}
