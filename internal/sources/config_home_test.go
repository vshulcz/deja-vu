package sources

import (
	"path/filepath"
	"testing"
)

// deja's own files follow an absolute XDG_CONFIG_HOME and ignore a relative
// one, as the XDG spec says; the exclude list and aider's project list read
// a relative one from the working directory (#5052).
func TestConfigDirIgnoresARelativeXDGConfigHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", "relcfg")
	want := filepath.Join(home, ".config", "deja")
	for name, got := range map[string]string{
		"ConfigDir":         ConfigDir(),
		"ExcludePath":       filepath.Dir(ExcludePath()),
		"AiderProjectsPath": filepath.Dir(AiderProjectsPath()),
	} {
		if got != want {
			t.Errorf("%s under %q, want %q", name, got, want)
		}
	}
	abs := filepath.Join(home, "cfg")
	t.Setenv("XDG_CONFIG_HOME", abs)
	if got := ConfigDir(); got != filepath.Join(abs, "deja") {
		t.Errorf("an absolute XDG_CONFIG_HOME was not followed: %q", got)
	}
}
