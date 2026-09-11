package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// seedMissingProgram writes two sessions that ran a wrapped command, were told
// the wrapper does not exist, and then ran the same command without it.
//
// The wrapper is `timeout` because that is what the mining recognises as a
// wrapper (`commandProgram` skips timeout, gtimeout, stdbuf and nice), and it is
// the program this machine is missing most.
func seedMissingProgram(t *testing.T, remedy string) string {
	t.Helper()
	tmp := hermeticEnv(t)
	dir := filepath.Join(tmp, "index.db")
	t.Setenv("DEJA_INDEX_DIR", dir)
	root := os.Getenv("DEJA_CLAUDE_ROOT")
	for _, id := range []string{"m1", "m2"} {
		writeClaudeFixture(t, filepath.Join(root, "-work-app", id+".jsonl"), id, []string{
			`{"type":"user","sessionId":"` + id + `","cwd":"/work/app","timestamp":"2026-02-02T03:04:05Z","message":{"role":"user","content":"restart the worker"}}`,
			`{"type":"assistant","sessionId":"` + id + `","cwd":"/work/app","timestamp":"2026-02-02T03:04:06Z","message":{"role":"assistant","content":[{"type":"tool_use","name":"Bash","input":{"command":"timeout 12 launchctl kickstart -k system/app.worker"}}]}}`,
			`{"type":"user","sessionId":"` + id + `","cwd":"/work/app","timestamp":"2026-02-02T03:04:07Z","message":{"role":"user","content":[{"type":"tool_result","content":"zsh:1: command not found: timeout"}]}}`,
			`{"type":"assistant","sessionId":"` + id + `","cwd":"/work/app","timestamp":"2026-02-02T03:04:08Z","message":{"role":"assistant","content":[{"type":"tool_use","name":"Bash","input":{"command":"` + remedy + `"}}]}}`,
			`{"type":"user","sessionId":"` + id + `","cwd":"/work/app","timestamp":"2026-02-02T03:04:09Z","message":{"role":"user","content":[{"type":"tool_result","content":"done"}]}}`,
		})
	}
	if _, err := captureRun(t, "index"); err != nil {
		t.Fatal(err)
	}
	return dir
}

// The line states a fact and recommends nothing, and the note on missing_program.go
// says what that is worth: of the ten sessions told at session start that
// `timeout` was missing, nine ran it anyway. When the store holds the remedy —
// and for `timeout` on a real store both recorded pairs are the same command with
// the wrapper taken out — the line can say so.
func TestTheMissingProgramLineSaysTheCommandRanWithoutIt(t *testing.T) {
	dir := seedMissingProgram(t, "launchctl kickstart -k system/app.worker")
	got := missingProgramLine(dir, "timeout 30 launchctl kickstart -k system/app.worker")
	if !strings.Contains(got, "is not on this machine") {
		t.Fatalf("the missing program was not named at all: %q", got)
	}
	if !strings.Contains(got, "ran without it") {
		t.Errorf("the recorded remedy did not reach the line: %q", got)
	}
}

// A remedy that is a different command is a claim one recorded run does not
// support, and a diagnostic is not a remedy at all: the line stays a fact.
func TestADifferentCommandIsNotOfferedAsTheRemedy(t *testing.T) {
	dir := seedMissingProgram(t, "launchctl print system/app.worker | grep state")
	got := missingProgramLine(dir, "timeout 30 launchctl kickstart -k system/app.worker")
	if !strings.Contains(got, "is not on this machine") {
		t.Fatalf("the missing program was not named at all: %q", got)
	}
	if strings.Contains(got, "ran without it") {
		t.Errorf("a different command was offered as the same one: %q", got)
	}
}
