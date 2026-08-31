package main

import (
	"strings"
	"testing"
)

// With no home directory there is no policy file to point at, and the lines
// that pointed at one ended in "see " and nothing — including doctor, which is
// the screen somebody opens to find out where the file goes (#2785).
func TestTheLinesAboutThePolicyDoNotPointAtNothing(t *testing.T) {
	for _, k := range []string{"HOME", "USERPROFILE", "XDG_CONFIG_HOME", "DEJA_POLICY_FILE"} {
		t.Setenv(k, "")
	}
	if got := seePolicyFile(); got != "" {
		t.Errorf("a sentence still points at a file: %q", got)
	}
	line := noPolicyFileLine()
	if strings.HasSuffix(strings.TrimSpace(line), "at") || strings.Contains(line, "at  ") {
		t.Errorf("doctor names nowhere as somewhere: %q", line)
	}
	if !strings.Contains(line, "home directory") {
		t.Errorf("doctor does not say why there is no file: %q", line)
	}
}

// With one, both still name it.
func TestTheLinesAboutThePolicyNameItWhenThereIsOne(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("DEJA_POLICY_FILE", dir+"/policy.json")
	if got := seePolicyFile(); !strings.Contains(got, "policy.json") {
		t.Errorf("the sentence lost the path: %q", got)
	}
	if got := noPolicyFileLine(); !strings.Contains(got, "policy.json") {
		t.Errorf("doctor lost the path: %q", got)
	}
}
