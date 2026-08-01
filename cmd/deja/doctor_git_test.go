package main

import (
	"os/exec"
	"strings"
	"testing"
)

// Without git a hit loses the line saying its files changed since, project
// names lose worktree identity, and the session-start hook loses the task
// signal — all in silence, with nowhere to ask why (#796).
func TestDoctorToolsReportsGit(t *testing.T) {
	var out strings.Builder
	doctorTools(&out)
	got := out.String()
	if !strings.Contains(got, "git ") {
		t.Errorf("git is not listed:\n%s", got)
	}
	if !strings.Contains(got, "sqlite3") {
		t.Errorf("sqlite3 lost its row:\n%s", got)
	}

	_, err := exec.LookPath("git")
	wantFound := err == nil
	line := ""
	for _, l := range strings.Split(got, "\n") {
		if strings.Contains(l, "git ") {
			line = l
		}
	}
	// "not found" contains "found", so a substring test passes for either
	// answer: read the status field itself.
	status := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "git"))
	status = strings.TrimSpace(strings.SplitN(status, "(", 2)[0])
	if wantFound && status != "found" {
		t.Errorf("git is on PATH but the row says %q", status)
	}
	if !wantFound && status != "not found" {
		t.Errorf("git is not on PATH but the row says %q", status)
	}
	if !strings.Contains(line, "changed-file notes") {
		t.Errorf("the row does not say what git is for: %q", line)
	}
}
