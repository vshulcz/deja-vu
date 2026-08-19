package sources

import (
	"path/filepath"
	"testing"
)

// Where Zed keeps settings.json differs by platform, and the installer writes
// to whatever this returns. A wrong answer here does not fail loudly: install
// reports success and Zed reads a file deja never touched.
func TestZedConfigDirPerPlatform(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	// Pointed away from the profile on purpose: a roaming profile is not
	// always under the home directory, and reading APPDATA is the only way to
	// find it. Same answer either way proves nothing.
	roaming := filepath.Join(t.TempDir(), "Roaming")
	t.Setenv("APPDATA", roaming)
	if got, want := zedConfigDir("windows"), filepath.Join(roaming, "Zed"); got != want {
		t.Errorf("windows config dir = %q, want %q", got, want)
	}
	// A windows without APPDATA set still has a profile to fall back on.
	t.Setenv("APPDATA", "")
	if got, want := zedConfigDir("windows"), filepath.Join(home, "AppData", "Roaming", "Zed"); got != want {
		t.Errorf("windows config dir without APPDATA = %q, want %q", got, want)
	}

	t.Setenv("XDG_CONFIG_HOME", "")
	if got, want := zedConfigDir("linux"), filepath.Join(home, ".config", "zed"); got != want {
		t.Errorf("posix config dir = %q, want %q", got, want)
	}
	xdg := filepath.Join(home, "elsewhere")
	t.Setenv("XDG_CONFIG_HOME", xdg)
	if got, want := zedConfigDir("linux"), filepath.Join(xdg, "zed"); got != want {
		t.Errorf("XDG_CONFIG_HOME ignored: %q, want %q", got, want)
	}
}

// The override is what lets anyone whose layout differs point deja at the real
// file, and it is how this is tested without a Zed install.
func TestZedSettingsPathHonoursTheOverride(t *testing.T) {
	want := filepath.Join(t.TempDir(), "settings.json")
	t.Setenv("DEJA_ZED_CONFIG", want)
	if got := ZedSettingsPath(); got != want {
		t.Errorf("ZedSettingsPath = %q, want %q", got, want)
	}
	t.Setenv("DEJA_ZED_CONFIG", "")
	if got := ZedSettingsPath(); got == want {
		t.Errorf("the override outlived its variable: %q", got)
	}
}
