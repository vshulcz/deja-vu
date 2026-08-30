package main

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/sources"
)

// The writers read the old config with `old, _ := os.ReadFile`, so a file that
// exists but cannot be read looked exactly like one that is not there and deja
// wrote a fresh config over it. backupOnce caught that by accident — until a
// `.bak` was already beside the file, which is the ordinary state after any
// earlier install, and then the user's config went with nothing left of it
// (#2751).
func TestAConfigThatCannotBeReadIsNotOverwritten(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("chmod does not take a file's read away here")
	}
	hermeticEnv(t)
	path := filepath.Join(sources.CursorCLIHome(), "mcp.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	mine := "{\"mcpServers\":{\"mine\":{\"command\":\"x\"}}}\n"
	if err := os.WriteFile(path, []byte(mine), 0o644); err != nil {
		t.Fatal(err)
	}
	// A snapshot from an earlier install: backupOnce takes one per file per
	// run, so with this here it never reads the config at all.
	if err := os.WriteFile(path+".bak", []byte("older\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(path, 0o644) })

	_, err := captureRun(t, "install", "cursor", "--no-index")
	if err == nil {
		t.Error("a config deja could not read was reported as installed")
	} else if !strings.Contains(err.Error(), path) {
		t.Errorf("the refusal does not name the config: %v", err)
	}
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
	b, rerr := os.ReadFile(path)
	if rerr != nil {
		t.Fatal(rerr)
	}
	if string(b) != mine {
		t.Errorf("the config deja could not read was written over:\n%s", b)
	}
}

// And the same on the way out: what cannot be read cannot be edited down to
// "the file without deja in it" either.
func TestUninstallDoesNotWriteOverAConfigItCannotRead(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("chmod does not take a file's read away here")
	}
	hermeticEnv(t)
	if _, err := captureRun(t, "install", "cursor", "--no-index"); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(sources.CursorCLIHome(), "mcp.json")
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path+".bak", []byte("older\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(path, 0o644) })

	if _, err := captureRun(t, "uninstall", "cursor"); err == nil {
		t.Error("a config deja could not read was reported as unwired")
	} else if !strings.Contains(err.Error(), path) {
		t.Errorf("the refusal does not name the config: %v", err)
	}
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
	after, rerr := os.ReadFile(path)
	if rerr != nil {
		t.Fatal(rerr)
	}
	if string(after) != string(before) {
		t.Errorf("the config was written over on the way out:\n%s", after)
	}
}

// A target that wires more than one file has to be refused on the file that
// cannot be read, not on whichever one it happens to report: `uninstall
// claude-auto` read no entry to remove from an unreadable ~/.claude.json and
// said the target was unwired while the file still named deja.
func TestAMultiFileTargetIsRefusedOnTheFileItCannotRead(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("chmod does not take a file's read away here")
	}
	hermeticEnv(t)
	if _, err := captureRun(t, "install", "claude-auto", "--no-index"); err != nil {
		t.Fatal(err)
	}
	path := sources.ClaudeJSONPath()
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(path, 0o644) })

	if _, err := captureRun(t, "uninstall", "claude-auto"); err == nil {
		t.Error("a target was reported unwired while a config it could not read still names deja")
	}
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
	after, rerr := os.ReadFile(path)
	if rerr != nil {
		t.Fatal(rerr)
	}
	if string(after) != string(before) {
		t.Errorf("the config was written over:\n%s", after)
	}
}

// The sync timer's unit is read only to decide whether the job is loaded; the
// removal itself is by path. Refusing to read it left a launchd job running
// against a binary the user was removing.
func TestUninstallTakesOutASyncUnitItCannotRead(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("chmod does not take a file's read away here")
	}
	hermeticEnv(t)
	if _, err := captureRun(t, "install", "sync-timer", "--no-index"); err != nil {
		t.Fatal(err)
	}
	unit := syncAutoPlistPath()
	if runtime.GOOS != "darwin" {
		unit = filepath.Join(syncAutoUnitDir(), "deja-sync.timer")
	}
	if _, err := os.Stat(unit); err != nil {
		t.Skipf("no unit written here: %v", err)
	}
	if err := os.Chmod(unit, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(unit, 0o644) })

	if _, err := captureRun(t, "uninstall", "sync-timer"); err != nil {
		t.Fatalf("uninstall refused a unit it only had to remove: %v", err)
	}
	if _, err := os.Stat(unit); !os.IsNotExist(err) {
		t.Errorf("the unit survived the uninstall")
	}
}
