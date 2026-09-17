package main

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/policy"
)

// `deja how` is the surface whose answer gets run, and it answered from the
// whole machine: asked inside one repository it returned another's wrapper —
// forty runs of `rtk go test ./...` above this repository's own command
// (#3705). The scope is the project of the working directory now.
func TestHowAnswersFromTheProjectItWasAskedIn(t *testing.T) {
	hermeticEnv(t)
	root := os.Getenv("DEJA_CLAUDE_ROOT")
	// Two projects, and the busier one is not the one being asked about.
	writeClaudeFixture(t, filepath.Join(root, "-work-alpha", "a.jsonl"), "alpha-1", []string{
		`{"type":"user","sessionId":"alpha-1","cwd":"/work/alpha","timestamp":"2026-09-10T10:00:00Z",` +
			`"message":{"role":"user","content":"how do we run the tests"}}`,
		`{"type":"assistant","sessionId":"alpha-1","cwd":"/work/alpha","timestamp":"2026-09-10T10:00:01Z",` +
			`"message":{"role":"assistant","content":[{"type":"tool_use","name":"Bash","input":{"command":"go test ./cmd/... -count=1"}}]}}`,
	})
	for i, id := range []string{"beta-1", "beta-2", "beta-3"} {
		writeClaudeFixture(t, filepath.Join(root, "-work-beta", id+".jsonl"), id, []string{
			`{"type":"user","sessionId":"` + id + `","cwd":"/work/beta","timestamp":"2026-09-1` + string(rune('1'+i)) + `T10:00:00Z",` +
				`"message":{"role":"user","content":"run the tests"}}`,
			`{"type":"assistant","sessionId":"` + id + `","cwd":"/work/beta","timestamp":"2026-09-1` + string(rune('1'+i)) + `T10:00:01Z",` +
				`"message":{"role":"assistant","content":[{"type":"tool_use","name":"Bash","input":{"command":"rtk go test ./..."}}]}}`,
		})
	}

	dir := os.Getenv("DEJA_INDEX_DIR")
	if err := index.Ensure(dir, "", false, io.Discard); err != nil {
		t.Fatal(err)
	}
	// Asked in alpha: alpha's command, though beta ran three times as many
	// sessions.
	entries, _, _, err := howEntries(dir, []string{"test"}, howScope("/work/alpha", "", false), policy.ActivationSearch)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) == 0 {
		t.Fatal("no command matched in the project that ran one")
	}
	if !strings.Contains(entries[0].Command, "go test ./cmd/...") {
		t.Errorf("asked in alpha, answered %q", entries[0].Command)
	}
	for _, e := range entries {
		if strings.Contains(e.Command, "rtk") {
			t.Errorf("the other project's command reached an answer scoped to alpha: %q", e.Command)
		}
	}

	// The machine still answers when asked for it, and there the busier
	// project wins as before.
	wide, _, _, err := howEntries(dir, []string{"test"}, howScope("/work/alpha", "", true), policy.ActivationSearch)
	if err != nil {
		t.Fatal(err)
	}
	if len(wide) == 0 || !strings.Contains(wide[0].Command, "rtk") {
		t.Errorf("--all-projects did not answer from the machine: %+v", wide)
	}
}

// A directory that belongs to no project deja knows still gets an answer: the
// scope is a default, not a filter the caller cannot see past.
func TestHowWidensWhenTheProjectHasNothing(t *testing.T) {
	hermeticEnv(t)
	root := os.Getenv("DEJA_CLAUDE_ROOT")
	writeClaudeFixture(t, filepath.Join(root, "-work-beta", "b.jsonl"), "beta-1", []string{
		`{"type":"user","sessionId":"beta-1","cwd":"/work/beta","timestamp":"2026-09-11T10:00:00Z",` +
			`"message":{"role":"user","content":"run the tests"}}`,
		`{"type":"assistant","sessionId":"beta-1","cwd":"/work/beta","timestamp":"2026-09-11T10:00:01Z",` +
			`"message":{"role":"assistant","content":[{"type":"tool_use","name":"Bash","input":{"command":"rtk go test ./..."}}]}}`,
	})
	// A directory of its own, belonging to no project in the store.
	elsewhere := filepath.Join(os.Getenv("HOME"), "elsewhere")
	if err := os.MkdirAll(elsewhere, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(elsewhere)
	out, err := captureRun(t, "how", "test")
	if err != nil {
		t.Fatalf("how: %v", err)
	}
	if !strings.Contains(out, "rtk go test") {
		t.Errorf("a directory with no project of its own got no answer:\n%s", out)
	}
	if !strings.Contains(out, "ran elsewhere on this machine") {
		t.Errorf("the widening was not said:\n%s", out)
	}
}
