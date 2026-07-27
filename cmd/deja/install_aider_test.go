package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func aiderConf(t *testing.T, home string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(home, ".aider.conf.yml"))
	if err != nil {
		t.Fatalf("conf missing: %v", err)
	}
	return string(b)
}

func TestInstallAiderWritesTheReadKey(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "cfg"))
	// hook-context is run as a subprocess; a path that is not a deja binary
	// must not stop the config from being written.
	if _, err := installAider("/bin/echo", false); err != nil {
		t.Fatalf("install: %v", err)
	}
	conf := aiderConf(t, home)
	if !strings.Contains(conf, "read:") || !strings.Contains(conf, "aider-context.md") {
		t.Fatalf("conf does not point aider at the context file:\n%s", conf)
	}
	// aider refuses to start when a configured read file is missing, so the
	// installer has to leave one behind.
	if _, err := os.Stat(filepath.Join(home, "cfg", "deja", "aider-context.md")); err != nil {
		t.Fatalf("context file not created: %v", err)
	}
}

// The read: list is where users keep CONVENTIONS.md. Losing it would be a
// silent behaviour change in their sessions.
func TestInstallAiderKeepsExistingReadEntries(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "cfg"))
	path := filepath.Join(home, ".aider.conf.yml")
	if err := os.WriteFile(path, []byte("read:\n  - ~/CONVENTIONS.md\nauto-commits: false\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := installAider("/bin/echo", false); err != nil {
		t.Fatalf("install: %v", err)
	}
	conf := aiderConf(t, home)
	for _, want := range []string{"~/CONVENTIONS.md", "aider-context.md", "auto-commits: false"} {
		if !strings.Contains(conf, want) {
			t.Fatalf("install dropped %q:\n%s", want, conf)
		}
	}
	if _, err := installAider("/bin/echo", true); err != nil {
		t.Fatalf("uninstall: %v", err)
	}
	conf = aiderConf(t, home)
	if strings.Contains(conf, "aider-context.md") {
		t.Fatalf("uninstall left our entry behind:\n%s", conf)
	}
	if !strings.Contains(conf, "~/CONVENTIONS.md") || !strings.Contains(conf, "auto-commits: false") {
		t.Fatalf("uninstall took the user's settings with it:\n%s", conf)
	}
}

// Uninstalling from a config we created must not leave a read: key with an
// empty list under it — aider reads that as a null and errors on startup.
func TestUninstallAiderDropsTheEmptyReadKey(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "cfg"))
	if _, err := installAider("/bin/echo", false); err != nil {
		t.Fatalf("install: %v", err)
	}
	if _, err := installAider("/bin/echo", true); err != nil {
		t.Fatalf("uninstall: %v", err)
	}
	if conf := aiderConf(t, home); strings.Contains(conf, "read:") {
		t.Fatalf("dangling read: key with nothing under it:\n%q", conf)
	}
	if _, err := os.Stat(filepath.Join(home, "cfg", "deja", "aider-context.md")); !os.IsNotExist(err) {
		t.Fatalf("context file survived uninstall: %v", err)
	}
}

// A scalar read: is the documented single-file form; promoting it to a list
// is the only way to add ours without dropping theirs.
func TestInstallAiderPromotesAScalarRead(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "cfg"))
	path := filepath.Join(home, ".aider.conf.yml")
	if err := os.WriteFile(path, []byte("read: ~/CONVENTIONS.md\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := installAider("/bin/echo", false); err != nil {
		t.Fatalf("install: %v", err)
	}
	conf := aiderConf(t, home)
	if strings.Contains(conf, "read: ~/CONVENTIONS.md") {
		t.Fatalf("scalar form left in place, our entry cannot coexist with it:\n%s", conf)
	}
	if !strings.Contains(conf, "  - ~/CONVENTIONS.md") || !strings.Contains(conf, "aider-context.md") {
		t.Fatalf("both files must end up in the list:\n%s", conf)
	}
}
