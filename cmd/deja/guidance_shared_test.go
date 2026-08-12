package main

import (
	"os"
	"testing"
)

// Eight harnesses read one file. Uninstalling any one of them must not take it
// away from the other seven — the failure would be silent, since nothing errors
// and the remaining harnesses simply stop finding the skill.
func TestSharedSkillSurvivesUninstallOfOneReader(t *testing.T) {
	hermeticEnv(t)
	if _, err := installGuidance("cursor", false); err != nil {
		t.Fatal(err)
	}
	path := guidancePath("cursor")
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("shared skill not written: %v", err)
	}
	if got := guidancePath("gemini"); got != path {
		t.Fatalf("gemini reads %q, cursor wrote %q", got, path)
	}

	recordWiring([]string{"cursor", "gemini"}, false)
	r, err := installGuidance("cursor", true)
	if err != nil {
		t.Fatal(err)
	}
	if r.Action != "kept" {
		t.Fatalf("uninstalling one reader = %q, want kept", r.Action)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("shared skill removed while gemini still reads it: %v", err)
	}

	// With the last reader gone there is nothing left to serve.
	recordWiring([]string{"gemini"}, true)
	if r, err = installGuidance("cursor", true); err != nil {
		t.Fatal(err)
	}
	if r.Action != "removed" {
		t.Fatalf("uninstalling the last reader = %q, want removed", r.Action)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("shared skill survived the last reader: %v", err)
	}
}
