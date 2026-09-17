package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// #3659 asked for three things about an MCP entry naming a deja build under
// another basename: that it be recognised, reported, and **removable**. The
// first two have tests beside the reader (doctor_renamed_binary_test.go); this
// is the third, and it is the one an uninstall gets wrong quietly — removal
// matches entries by what they name, so an entry naming `deja-probe` outlived
// every uninstall and went on calling a binary nobody has.
func TestInstallAdoptsAndUninstallRemovesAnEntryNamingAnotherBuild(t *testing.T) {
	hermeticEnv(t)
	home := os.Getenv("HOME")
	hermes := filepath.Join(home, ".hermes")
	if err := os.MkdirAll(hermes, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HERMES_HOME", hermes)
	// The record is what would otherwise recognise the path, so it is emptied:
	// the name has to answer on its own (#3681 made that true for hooks).
	forgetWrittenExes()
	t.Cleanup(forgetWrittenExes)

	path := filepath.Join(hermes, "config.yaml")
	theirs := "provider: openai\nmcp_servers:\n  deja:\n    command: \"" +
		filepath.Join(home, "gone", "deja-probe") + "\"\n    args: [\"mcp\"]\n"
	if err := os.WriteFile(path, []byte(theirs), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := captureRun(t, "install", "hermes", "--no-index"); err != nil {
		t.Fatalf("install hermes: %v", err)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(after), "deja-probe") {
		t.Errorf("the install left the entry naming another build:\n%s", after)
	}
	if n := strings.Count(string(after), "deja:"); n != 1 {
		t.Errorf("the install stacked a second entry beside it (%d):\n%s", n, after)
	}

	if _, err := captureRun(t, "uninstall", "hermes"); err != nil {
		t.Fatalf("uninstall hermes: %v", err)
	}
	final, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(final), "deja") {
		t.Errorf("the uninstall left deja in the config:\n%s", final)
	}
	// The reader's own key and their other settings stay: emptied of deja and
	// of nothing else (#2583). An `mcp_servers:` with nothing under it is what
	// hermes reads as `{}`.
	if !strings.Contains(string(final), "provider: openai") {
		t.Errorf("the uninstall took the reader's own settings:\n%s", final)
	}
}
