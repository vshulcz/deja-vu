package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// onlyTheIgnoredTreeKnows builds a store where a word, an error, a command and
// a file exist solely in the tree the ignore rule keeps out. Anything that
// prints "zonko" is reading from it.
func onlyTheIgnoredTreeKnows(t *testing.T) {
	t.Helper()
	tmp := hermeticEnv(t)
	t.Setenv("DEJA_INDEX_DIR", filepath.Join(tmp, "index.db"))
	policyFile := filepath.Join(tmp, "policy.json")
	t.Setenv("DEJA_POLICY_FILE", policyFile)
	if err := os.WriteFile(policyFile, []byte(`{"ignore":["*scratch*"]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	root := os.Getenv("DEJA_CLAUDE_ROOT")
	for _, id := range []string{"s1", "s2", "s3"} {
		writeClaudeFixture(t, filepath.Join(root, "-w-scratch", id+".jsonl"), id, []string{
			`{"type":"user","sessionId":"` + id + `","cwd":"/w/scratch","timestamp":"2026-07-01T10:00:00Z","message":{"role":"user","content":"the zonkobuffer keeps overflowing"}}`,
			`{"type":"user","sessionId":"` + id + `","cwd":"/w/scratch","timestamp":"2026-07-01T10:01:00Z","message":{"role":"user","content":[{"type":"tool_result","content":"panic: zonkobuffer overflow at shard 7"}]}}`,
			`{"type":"assistant","sessionId":"` + id + `","cwd":"/w/scratch","timestamp":"2026-07-01T10:02:00Z","message":{"role":"assistant","content":[{"type":"tool_use","name":"Bash","input":{"command":"terraform apply --zonkoshard 7"}}]}}`,
			`{"type":"assistant","sessionId":"` + id + `","cwd":"/w/scratch","timestamp":"2026-07-01T10:03:00Z","message":{"role":"assistant","content":[{"type":"tool_use","name":"Edit","input":{"file_path":"/w/scratch/zonko.go","old_string":"a","new_string":"b"}}]}}`,
		})
	}
	writeClaudeFixture(t, filepath.Join(root, "-w-keep", "k1.jsonl"), "k1", []string{
		`{"type":"user","sessionId":"k1","cwd":"/w/keep","timestamp":"2026-07-01T10:00:00Z","message":{"role":"user","content":"morning notes about nothing"}}`,
	})
	if _, err := captureRun(t, "index"); err != nil {
		t.Fatal(err)
	}
}

// Five fixes found one surface each — how, restore and friction (#2630), the
// per-command hook (#2652), sync export (#2654), the friction walls behind the
// brief and the session-start block (#2658) — and each is pinned on its own.
// Nothing pinned the set, so the next reader of sessions would be found the
// same way: by accident (#2660).
//
// A command that legitimately echoes the query back — `no command on this
// machine mentions "zonkoshard"` — must not read as a leak, so the marker is
// looked for outside the reader's own words.
//
// The command in the fixture is a `terraform apply` because the reader only
// keeps commands it recognises as work (worthIndexing): an invented binary
// name is never indexed as a command at all, and a sweep built on one would
// pass without ever asking `how` anything.
func TestNoSurfaceServesTheIgnoredTree(t *testing.T) {
	for _, tc := range []struct {
		name  string
		args  []string
		stdin string
	}{
		{name: "search", args: []string{"zonkobuffer"}},
		{name: "search by error paste", args: []string{"panic: zonkobuffer overflow at shard 7"}},
		{name: "fix", args: []string{"fix", "panic: zonkobuffer overflow at shard 7"}},
		{name: "how", args: []string{"how", "zonkoshard"}},
		{name: "friction", args: []string{"friction"}},
		{name: "last", args: []string{"last"}},
		{name: "files", args: []string{"files", "zonkobuffer"}},
		{name: "blame", args: []string{"blame", "zonko.go"}},
		{name: "brief", args: []string{"brief"}},
		{name: "stats", args: []string{"stats"}},
		{name: "restore", args: []string{"restore", "/w/scratch/zonko.go"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			onlyTheIgnoredTreeKnows(t)
			out, err := captureRun(t, tc.args...)
			if err != nil {
				t.Fatalf("%v: %v", tc.args, err)
			}
			errOut, err := captureRunStderr(t, tc.args...)
			if err != nil {
				t.Fatalf("%v: %v", tc.args, err)
			}
			for _, line := range strings.Split(out+"\n"+errOut, "\n") {
				if !strings.Contains(line, "zonko") {
					continue
				}
				// The query, echoed inside a refusal, is the reader's own word
				// coming back — not a session being served.
				if echoesTheQuery(line, tc.args) {
					continue
				}
				t.Fatalf("a session the ignore rule keeps out reached %v:\n%s", tc.args, line)
			}
		})
	}
}

// echoesTheQuery reports whether a line only carries the marker because the
// command repeated what it was asked.
func echoesTheQuery(line string, args []string) bool {
	for _, a := range args {
		if strings.Contains(a, "zonko") && strings.Contains(line, a) {
			return true
		}
	}
	return false
}

// The store really does hold what the sweep is looking for, or every case above
// passes for the wrong reason.
func TestTheIgnoredTreeIsReachableWhenTheRuleIsLifted(t *testing.T) {
	onlyTheIgnoredTreeKnows(t)
	if err := os.WriteFile(os.Getenv("DEJA_POLICY_FILE"), []byte(`{"ignore":["*nothing-here*"]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	out, err := captureRun(t, "how", "zonkoshard")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "terraform apply --zonkoshard 7") {
		t.Fatalf("with the rule lifted the command should be there:\n%s", out)
	}
}
