package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/sources"
)

// Every hook check answered "is deja's hook here", and a file holding eight
// copies of it answers yes. What the machine did instead was start eight
// processes per prompt and inject the same memory eight times, with doctor
// printing "wired" over it (#3421).
func TestDoctorCountsRepeatedHookEntries(t *testing.T) {
	withStatsStores(t)
	// A file really called deja, and really there: the count keys on the
	// binary's name, and a missing one would set the row's other note off.
	exe := filepath.Join(t.TempDir(), "deja")
	if err := os.WriteFile(exe, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	var events []string
	for _, h := range claudeHookWiring {
		one := `{"hooks":[{"type":"command","command":"` + jsonEscaped(t, exe) + ` ` + h.Sub + `"}]}`
		copies := []string{one}
		if h.Event == "UserPromptSubmit" {
			copies = []string{one, one, one}
		}
		events = append(events, `"`+h.Event+`":[`+strings.Join(copies, ",")+`]`)
	}
	path := filepath.Join(sources.ClaudeConfigDir(), "settings.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"hooks":{`+strings.Join(events, ",")+`}}`), 0o600); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	doctorHooks(&out)
	got := out.String()
	if !strings.Contains(got, "UserPromptSubmit ×3") {
		t.Errorf("doctor did not count the repeated hook:\n%s", got)
	}
	if !strings.Contains(got, "deja install claude-auto") {
		t.Errorf("doctor named no way out of it:\n%s", got)
	}
	// The events wired once are not the reader's problem and must not be named.
	if strings.Contains(got, "SessionStart ×") {
		t.Errorf("doctor reported an event that runs once:\n%s", got)
	}
}

// A config wired the way an install leaves it says nothing about repeats.
func TestDoctorSaysNothingAboutASingleEntry(t *testing.T) {
	withStatsStores(t)
	writeClaudeSettings(t, "SessionStart", "UserPromptSubmit")

	var out bytes.Buffer
	doctorHooks(&out)
	if got := out.String(); strings.Contains(got, "more than once per event") {
		t.Errorf("doctor invented a repeat:\n%s", got)
	}
}
