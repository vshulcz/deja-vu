package main

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// The repair rewrites the wiring. The /deja command holds the same absolute
// path and was written by the install command rather than by installTarget, so
// a move left it running a binary that is gone — and `deja install` for the
// same target, which the repair exists to save the reader, fixed it (#2693).
func TestTheRepairRewritesTheCommandFile(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("CURSOR_CONFIG_DIR", filepath.Join(home, ".cursor"))

	old := filepath.Join(home, "old", "deja")
	if _, err := installTarget("cursor-auto", old, false); err != nil {
		t.Fatal(err)
	}
	if _, err := installCommandFile(guidanceHarness("cursor-auto"), old, false); err != nil {
		t.Fatal(err)
	}
	command := commandFilePath(guidanceHarness("cursor-auto"))
	before, err := os.ReadFile(command)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(before), old) {
		t.Fatalf("the command file does not name the binary it was written for:\n%s", before)
	}

	// The state by hand, the way the other wiring tests seed it: what install
	// records depends on which harnesses the machine running the test has.
	if err := os.MkdirAll(filepath.Dir(wiringStatePath()), 0o755); err != nil {
		t.Fatal(err)
	}
	state := `{"version":"0.0.1","targets":["cursor-auto"],"exe":` + strconv.Quote(old) +
		`,"home":` + strconv.Quote(homeDir()) + `}`
	if err := os.WriteFile(wiringStatePath(), []byte(state), 0o644); err != nil {
		t.Fatal(err)
	}
	restore := exeIsTemporary
	t.Cleanup(func() { exeIsTemporary = restore })
	exeIsTemporary = func(string) bool { return false }

	if rewired := refreshWiringAfterUpgrade(); len(rewired) == 0 {
		t.Fatalf("nothing was rewired (stuck: %v)", stuckWiring)
	}
	after, err := os.ReadFile(command)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(after), old) {
		t.Errorf("the /deja command still runs the binary that is gone:\n%s", after)
	}

	// And a machine that asked for the plumbing without the text keeps it that
	// way: the repair rewrites a command file, it does not hand out one.
	if err := os.Remove(command); err != nil {
		t.Fatal(err)
	}
	state = `{"version":"0.0.2","targets":["cursor-auto"],"exe":` + strconv.Quote(old) +
		`,"home":` + strconv.Quote(homeDir()) + `}`
	if err := os.WriteFile(wiringStatePath(), []byte(state), 0o644); err != nil {
		t.Fatal(err)
	}
	// The repair still runs — otherwise this proves only that it bailed. It
	// reports nothing changed, which is the point: the wiring already names
	// this binary and the command file is not there to rewrite.
	refreshWiringAfterUpgrade()
	if len(stuckWiring) != 0 {
		t.Fatalf("the second repair could not write: %v", stuckWiring)
	}
	if _, err := os.Stat(command); err == nil {
		t.Errorf("the repair wrote a command file the machine had declined: %s", command)
	}
}

// Goose keeps the command in config.yaml and the workflow in a recipe beside
// it. Going by the recipe alone would put back a slash command someone took
// out of the config by hand (#2693).
func TestTheRepairLeavesAGooseCommandTakenOutByHand(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))

	old := filepath.Join(home, "old", "deja")
	if _, err := installTarget("goose-auto", old, false); err != nil {
		t.Fatal(err)
	}
	if _, err := installCommandFile("goose", old, false); err != nil {
		t.Fatal(err)
	}
	config := filepath.Join(gooseConfigDir(), "config.yaml")
	b, err := os.ReadFile(config)
	if err != nil {
		t.Fatal(err)
	}
	if !gooseSlashCommandPresent() {
		t.Fatalf("the command was never written:\n%s", b)
	}
	// Taken out by hand, recipe left behind.
	if err := os.WriteFile(config, []byte(removeGooseSlashCommand(string(b))), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := os.MkdirAll(filepath.Dir(wiringStatePath()), 0o755); err != nil {
		t.Fatal(err)
	}
	state := `{"version":"0.0.1","targets":["goose-auto"],"exe":` + strconv.Quote(old) +
		`,"home":` + strconv.Quote(homeDir()) + `}`
	if err := os.WriteFile(wiringStatePath(), []byte(state), 0o644); err != nil {
		t.Fatal(err)
	}
	restore := exeIsTemporary
	t.Cleanup(func() { exeIsTemporary = restore })
	exeIsTemporary = func(string) bool { return false }

	if rewired := refreshWiringAfterUpgrade(); len(rewired) == 0 {
		t.Fatalf("nothing was rewired (stuck: %v)", stuckWiring)
	}
	if gooseSlashCommandPresent() {
		after, _ := os.ReadFile(config)
		t.Errorf("the repair put the slash command back:\n%s", after)
	}
}
