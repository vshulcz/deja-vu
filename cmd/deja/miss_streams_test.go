package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Where a command says it found nothing, per surface.
//
// Two rules coexist in the tree and neither was written down. `search` and
// `blame` follow the one #2566 stated for `check`: stdout carries the result,
// the sentence about why there is none goes to stderr, so a run that parses
// stdout reads a miss as an empty answer rather than as prose. `how`, `files`,
// `fix` and `restore` say it on stdout, where their results would be.
//
// Both are defensible — moving `how`'s miss to stderr would hand an agent an
// empty stdout where #2634 deliberately put a pointer to `deja blame` — so this
// pins the split rather than picking a side. A new surface has to choose one
// on purpose, and a surface that changes sides has to say why (#2670).
func TestWhereEachSurfaceSaysItFoundNothing(t *testing.T) {
	for _, tc := range []struct {
		name   string
		args   []string
		stderr bool // the miss is said on stderr, and stdout stays empty
	}{
		{name: "search", args: []string{"zonkobuffer"}, stderr: true},
		{name: "blame", args: []string{"blame", "zonko.go"}, stderr: true},
		{name: "how", args: []string{"how", "zonkoctl"}},
		{name: "files", args: []string{"files", "zonkobuffer"}},
		{name: "fix", args: []string{"fix", "panic: zonkobuffer overflow"}},
		{name: "restore", args: []string{"restore", "/w/app/zonko.go"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			missStore(t)
			out, err := captureRun(t, tc.args...)
			if err != nil {
				t.Fatalf("a question with no answer is not an error: %v", err)
			}
			errOut, err := captureRunStderr(t, tc.args...)
			if err != nil {
				t.Fatal(err)
			}
			said := strings.TrimSpace(out)
			toldOnStderr := strings.TrimSpace(errOut)
			if tc.stderr {
				if said != "" {
					t.Fatalf("stdout should stay empty for a miss here, got:\n%s", said)
				}
				if toldOnStderr == "" {
					t.Fatalf("nothing on stderr says why there is no answer")
				}
				return
			}
			if said == "" {
				t.Fatalf("this surface says its miss on stdout, and said nothing:\nstderr was:\n%s", toldOnStderr)
			}
		})
	}
}

// missStore holds work that matches none of the questions above, so every case
// is a genuine miss rather than a filtered one.
func missStore(t *testing.T) {
	t.Helper()
	tmp := hermeticEnv(t)
	t.Setenv("DEJA_INDEX_DIR", filepath.Join(tmp, "index.db"))
	root := os.Getenv("DEJA_CLAUDE_ROOT")
	for _, id := range []string{"s1", "s2", "s3"} {
		writeClaudeFixture(t, filepath.Join(root, "-w-app", id+".jsonl"), id, []string{
			`{"type":"user","sessionId":"` + id + `","cwd":"/w/app","timestamp":"2026-07-01T10:00:00Z","message":{"role":"user","content":"the widget pipeline keeps stalling"}}`,
			`{"type":"assistant","sessionId":"` + id + `","cwd":"/w/app","timestamp":"2026-07-01T10:02:00Z","message":{"role":"assistant","content":[{"type":"tool_use","name":"Bash","input":{"command":"terraform apply --widget 7"}}]}}`,
		})
	}
	if _, err := captureRun(t, "index"); err != nil {
		t.Fatal(err)
	}
}

// And the other half of the same fact: none of them treats a miss as an error.
// #333 and #396 settled where an exit code does belong — a refusal deja cannot
// carry out — and a question with no answer is not one.
func TestAMissIsNotAnError(t *testing.T) {
	for _, args := range [][]string{
		{"zonkobuffer"},
		{"blame", "zonko.go"},
		{"how", "zonkoctl"},
		{"files", "zonkobuffer"},
		{"fix", "panic: zonkobuffer overflow"},
		{"restore", "/w/app/zonko.go"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			missStore(t)
			if _, err := captureRun(t, args...); err != nil {
				t.Fatalf("%v exited with an error for a question it simply could not answer: %v", args, err)
			}
		})
	}
}
