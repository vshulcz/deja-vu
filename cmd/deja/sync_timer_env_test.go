package main

import (
	"encoding/xml"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// clearDejaEnv unsets every DEJA_* and XDG_* variable the test process
// inherited, so what a unit carries is only what the test sets.
func clearDejaEnv(t *testing.T) {
	t.Helper()
	for _, kv := range os.Environ() {
		name, _, _ := strings.Cut(kv, "=")
		if strings.HasPrefix(name, "DEJA_") || strings.HasPrefix(name, "XDG_") {
			t.Setenv(name, "")
		}
	}
}

// launchd and systemd start the timer with their own environment, not the
// shell's, so `deja sync` from the timer imported into another index under
// other switches than the same command typed by hand (#5436).
func TestSyncTimerUnitsCarryTheShellsDejaSettings(t *testing.T) {
	home := syncTimerHome(t)
	clearDejaEnv(t)
	cache := filepath.Join(home, "elsewhere", `50%off "cache"`)
	t.Setenv("XDG_CACHE_HOME", cache)
	t.Setenv("DEJA_INDEX_TOOL_OUTPUT", "0")
	t.Setenv("DEJA_EXCLUDE_HARNESSES", "opencode")

	plist := syncAutoPlist("/usr/local/bin/deja")
	var doc struct{}
	if err := xml.Unmarshal([]byte(plist), &doc); err != nil {
		t.Fatalf("the plist does not parse: %v\n%s", err, plist)
	}
	for _, want := range []string{
		"<key>EnvironmentVariables</key>",
		"<key>DEJA_INDEX_TOOL_OUTPUT</key><string>0</string>",
		"<key>DEJA_EXCLUDE_HARNESSES</key><string>opencode</string>",
		"<key>XDG_CACHE_HOME</key><string>" + xmlEscape(cache) + "</string>",
	} {
		if !strings.Contains(plist, want) {
			t.Errorf("the plist lacks %s:\n%s", want, plist)
		}
	}

	service := syncAutoService("/usr/local/bin/deja")
	for _, want := range []string{
		`Environment="DEJA_INDEX_TOOL_OUTPUT=0"`,
		`Environment="DEJA_EXCLUDE_HARNESSES=opencode"`,
		`Environment="XDG_CACHE_HOME=` + strings.NewReplacer(`\`, `\\`, "%", "%%", `"`, `\"`).Replace(cache) + `"`,
	} {
		if !strings.Contains(service, want) {
			t.Errorf("the service lacks %s:\n%s", want, service)
		}
	}
	if !strings.Contains(service, "ExecStart=\"/usr/local/bin/deja\" sync\n") {
		t.Errorf("ExecStart changed:\n%s", service)
	}
}

// Control: a default setup writes the units it always wrote.
func TestSyncTimerUnitsCarryNothingOnADefaultSetup(t *testing.T) {
	syncTimerHome(t)
	clearDejaEnv(t)
	if p := syncAutoPlist("/usr/local/bin/deja"); strings.Contains(p, "EnvironmentVariables") {
		t.Errorf("a default setup carries an environment:\n%s", p)
	}
	if s := syncAutoService("/usr/local/bin/deja"); strings.Contains(s, "Environment=") {
		t.Errorf("a default setup carries an environment:\n%s", s)
	}
}
