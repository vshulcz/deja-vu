package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/sources"
)

// codexHome writes a hooks.json holding the given events and the trust entry
// codex needs before it runs any of them.
func codexHome(t *testing.T, events ...string) {
	t.Helper()
	home := sources.CodexHome()
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	sub := func(event string) string {
		for _, h := range codexHookWiring {
			if h.Event == event {
				return h.Sub
			}
		}
		return "hook-unknown"
	}
	var entries []string
	for _, e := range events {
		entries = append(entries, `"`+e+`":[{"matcher":"","hooks":[{"type":"command","command":"/usr/local/bin/deja `+
			sub(e)+`"}]}]`)
	}
	if err := os.WriteFile(filepath.Join(home, "hooks.json"),
		[]byte(`{"hooks":{`+strings.Join(entries, ",")+`}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "config.toml"),
		[]byte("[hooks.json:session_start]\ntrusted_hash = \"sha256:abc\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
}

// Codex trusts a hooks.json by its hash, and the one it approved may be the one
// an older deja wrote. Trusted said "wired" whatever was in it, so the events
// added since ran nowhere and nothing said so.
func TestDoctorNamesTheCodexEventsAnOlderInstallLacks(t *testing.T) {
	withStatsStores(t)
	codexHome(t, "SessionStart")

	var out bytes.Buffer
	doctorCodexHook(&out)
	got := out.String()
	if !strings.Contains(got, "out of date") {
		t.Errorf("a one-of-three wiring was not called out:\n%s", got)
	}
	for _, want := range []string{"PreToolUse", "PostToolUse", "deja install"} {
		if !strings.Contains(got, want) {
			t.Errorf("the report does not name %q:\n%s", want, got)
		}
	}
}

func TestDoctorIsQuietWhenCodexHasEveryEvent(t *testing.T) {
	withStatsStores(t)
	var all []string
	for _, h := range codexHookWiring {
		all = append(all, h.Event)
	}
	codexHome(t, all...)

	var out bytes.Buffer
	doctorCodexHook(&out)
	if got := out.String(); strings.Contains(got, "out of date") {
		t.Errorf("a complete wiring was reported as stale:\n%s", got)
	}
}
