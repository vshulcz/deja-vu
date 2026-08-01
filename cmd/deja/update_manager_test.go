package main

import (
	"bytes"
	"errors"
	"path/filepath"
	"strings"
	"testing"
)

// Writing into a package manager's tree succeeds and then comes undone: the
// manager still records the old version and its next upgrade overwrites the
// file, so the user sees an update that did not stick (#775).
var errStopUpdateForTest = errors.New("stop")

func TestUpdateDefersToThePackageManagerThatOwnsTheBinary(t *testing.T) {
	cases := []struct {
		exe     string
		manager string
		command string
	}{
		{"/opt/homebrew/Cellar/deja-vu/0.16.5/bin/deja", "Homebrew", "brew upgrade deja-vu"},
		{"/home/linuxbrew/.linuxbrew/bin/deja", "Homebrew", "brew upgrade deja-vu"},
		{`C:\Users\v\scoop\apps\deja\current\deja.exe`, "scoop", "scoop update deja"},
		{"/nix/store/abc123-deja-0.16.5/bin/deja", "Nix", "nix profile upgrade deja"},
		// README has promised this since the npm package shipped: "deja update
		// is for standalone installs. Homebrew and npm installs update through
		// the package manager." Nothing enforced it.
		{"/usr/lib/node_modules/deja-vu/bin/deja", "npm", "npm update -g deja-vu"},
	}
	for _, tc := range cases {
		var out bytes.Buffer
		err := performUpdate(updateConfig{
			currentVersion: "0.1.0",
			goos:           "linux",
			goarch:         "amd64",
			executable:     tc.exe,
			download: func(string, int64, string) ([]byte, error) {
				t.Errorf("%s: downloaded a release for a managed binary", tc.exe)
				return nil, nil
			},
		}, &out)
		if err != nil {
			t.Errorf("%s: %v", tc.exe, err)
		}
		if !strings.Contains(out.String(), tc.manager) || !strings.Contains(out.String(), tc.command) {
			t.Errorf("%s: want %q and %q, got %q", tc.exe, tc.manager, tc.command, out.String())
		}
	}

	// --force is the documented escape hatch, so it must still reach the
	// download; and an ordinary path must not be mistaken for a managed one.
	for _, tc := range []struct {
		name string
		cfg  updateConfig
	}{
		{"force", updateConfig{force: true, currentVersion: "0.1.0", goos: "linux", goarch: "amd64", executable: "/opt/homebrew/Cellar/deja-vu/0.16.5/bin/deja"}},
		{"plain path", updateConfig{currentVersion: "0.1.0", goos: "linux", goarch: "amd64", executable: filepath.Join("/usr", "local", "bin", "deja")}},
	} {
		reached := false
		var out bytes.Buffer
		_ = performUpdate(updateConfig{
			force: tc.cfg.force, currentVersion: tc.cfg.currentVersion,
			goos: tc.cfg.goos, goarch: tc.cfg.goarch, executable: tc.cfg.executable,
			download: func(string, int64, string) ([]byte, error) {
				reached = true
				return nil, errStopUpdateForTest
			},
		}, &out)
		if !reached {
			t.Errorf("%s: stopped before the download: %q", tc.name, out.String())
		}
	}
}
