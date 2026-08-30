package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/sources"
)

func writeClaudeSettings(t *testing.T, events ...string) {
	t.Helper()
	// A binary that is there. doctor also reports wiring that names a binary
	// which has been moved away, and a hard-coded path nobody installed made
	// every one of these fixtures look like that case.
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	var entries []string
	for _, e := range events {
		entries = append(entries, `"`+e+`":[{"matcher":"","hooks":[{"type":"command","command":"`+exe+` `+
			subFor(e)+`"}]}]`)
	}
	body := `{"hooks":{` + strings.Join(entries, ",") + `}}`
	path := filepath.Join(sources.ClaudeConfigDir(), "settings.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func subFor(event string) string {
	for _, h := range claudeHookWiring {
		if h.Event == event {
			return h.Sub
		}
	}
	return "hook-unknown"
}

// A settings.json written by an older deja keeps working and quietly lacks
// everything added since. It was reported as "wired" on the strength of one
// event out of five, so the features that arrived with the others reached
// nobody until they happened to reinstall.
func TestDoctorNamesTheHookEventsAnOlderInstallLacks(t *testing.T) {
	withStatsStores(t)
	writeClaudeSettings(t, "SessionStart", "PreCompact", "UserPromptSubmit")

	var out bytes.Buffer
	doctorHooks(&out)
	got := out.String()
	if !strings.Contains(got, "out of date") {
		t.Errorf("a three-of-five wiring was not called out:\n%s", got)
	}
	for _, want := range []string{"PreToolUse", "PostToolUse", "deja install"} {
		if !strings.Contains(got, want) {
			t.Errorf("the report does not name %q:\n%s", want, got)
		}
	}
}

// Everything the installer writes is there: no complaint, and no advice to run
// a command that would change nothing.
func TestDoctorIsQuietWhenEveryHookIsWired(t *testing.T) {
	withStatsStores(t)
	var all []string
	for _, h := range claudeHookWiring {
		all = append(all, h.Event)
	}
	writeClaudeSettings(t, all...)

	var out bytes.Buffer
	doctorHooks(&out)
	got := out.String()
	if strings.Contains(got, "out of date") || strings.Contains(got, "deja install") {
		t.Errorf("a complete wiring was reported as stale:\n%s", got)
	}
}
