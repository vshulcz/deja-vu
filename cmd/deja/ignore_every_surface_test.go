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
// everySurface is what a reader can ask, with a marker only the withheld
// sessions know. One list, used by both sweeps, so a rule that covers one and
// not the other is visible rather than assumed.
func everySurface(scratchPath string) []struct {
	name string
	args []string
} {
	return []struct {
		name string
		args []string
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
		{name: "restore", args: []string{"restore", scratchPath}},
	}
}

// servesTheMarker runs one command and fails if a withheld session reached it.
func servesTheMarker(t *testing.T, args []string) {
	t.Helper()
	out, err := captureRun(t, args...)
	if err != nil {
		t.Fatalf("%v: %v", args, err)
	}
	errOut, err := captureRunStderr(t, args...)
	if err != nil {
		t.Fatalf("%v: %v", args, err)
	}
	for _, line := range strings.Split(out+"\n"+errOut, "\n") {
		if !strings.Contains(line, "zonko") || echoesTheQuery(line, args) {
			continue
		}
		t.Fatalf("a withheld session reached %v:\n%s", args, line)
	}
}

func TestNoSurfaceServesTheIgnoredTree(t *testing.T) {
	for _, tc := range everySurface("/w/scratch/zonko.go") {
		t.Run(tc.name, func(t *testing.T) {
			onlyTheIgnoredTreeKnows(t)
			// The query, echoed inside a refusal, is the reader's own word
			// coming back — not a session being served.
			servesTheMarker(t, tc.args)
		})
	}
}

// The other rule that withholds sessions, over the same surfaces. The trust
// policy has been taught one screen at a time too — #937 for the listing,
// #1026 for files and blame, #1120 for friction, #2399 for the MCP block — so
// the set deserves the same pin the ignore rule got (#2660).
//
// The sessions have to be genuinely imported, because Origin reads the
// "imported:" prefix the import writes; a fixture cannot fake it from a cwd.
// So this store is built the way a second machine really is: index here,
// export, then index and import as the other one.
func TestNoSurfaceServesMemoryTheTrustPolicyWithholds(t *testing.T) {
	for _, tc := range everySurface("/w/alpha/zonko.go") {
		t.Run(tc.name, func(t *testing.T) {
			importedAndWithheld(t)
			servesTheMarker(t, tc.args)
		})
	}
}

// importedAndWithheld leaves the caller on a machine holding three imported
// sessions and a policy that withholds imported memory from every activation.
func importedAndWithheld(t *testing.T) {
	t.Helper()
	tmp := hermeticEnv(t)
	exchange := filepath.Join(tmp, "exchange")
	if err := os.MkdirAll(exchange, 0o755); err != nil {
		t.Fatal(err)
	}

	// The machine the work happened on.
	t.Setenv("DEJA_INDEX_DIR", filepath.Join(tmp, "a.db"))
	rootA := filepath.Join(tmp, "claude-a")
	t.Setenv("DEJA_CLAUDE_ROOT", rootA)
	for _, id := range []string{"a1", "a2", "a3"} {
		writeClaudeFixture(t, filepath.Join(rootA, "-w-alpha", id+".jsonl"), id, []string{
			`{"type":"user","sessionId":"` + id + `","cwd":"/w/alpha","timestamp":"2026-07-01T10:00:00Z","message":{"role":"user","content":"the zonkobuffer keeps overflowing"}}`,
			`{"type":"user","sessionId":"` + id + `","cwd":"/w/alpha","timestamp":"2026-07-01T10:01:00Z","message":{"role":"user","content":[{"type":"tool_result","content":"panic: zonkobuffer overflow at shard 7"}]}}`,
			`{"type":"assistant","sessionId":"` + id + `","cwd":"/w/alpha","timestamp":"2026-07-01T10:02:00Z","message":{"role":"assistant","content":[{"type":"tool_use","name":"Bash","input":{"command":"terraform apply --zonkoshard 7"}}]}}`,
			`{"type":"assistant","sessionId":"` + id + `","cwd":"/w/alpha","timestamp":"2026-07-01T10:03:00Z","message":{"role":"assistant","content":[{"type":"tool_use","name":"Edit","input":{"file_path":"/w/alpha/zonko.go","old_string":"a","new_string":"b"}}]}}`,
		})
	}
	if _, err := captureRun(t, "index"); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRun(t, "sync", "export", exchange); err != nil {
		t.Fatal(err)
	}

	// The machine reading it back.
	t.Setenv("DEJA_INDEX_DIR", filepath.Join(tmp, "b.db"))
	rootB := filepath.Join(tmp, "claude-b")
	t.Setenv("DEJA_CLAUDE_ROOT", rootB)
	writeClaudeFixture(t, filepath.Join(rootB, "-w-beta", "b1.jsonl"), "b1", []string{
		`{"type":"user","sessionId":"b1","cwd":"/w/beta","timestamp":"2026-07-01T10:00:00Z","message":{"role":"user","content":"morning notes about nothing"}}`,
	})
	if _, err := captureRun(t, "index"); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRun(t, "sync", "import", exchange); err != nil {
		t.Fatal(err)
	}
	policyFile := filepath.Join(tmp, "policy-b.json")
	t.Setenv("DEJA_POLICY_FILE", policyFile)
	if err := os.WriteFile(policyFile, []byte(`{"activations":{"search":{"imported":false},"mcp":{"imported":false},"auto":{"imported":false}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
}

// And the control: with the policy allowing imported memory, the same store
// answers — or every case above passes for the wrong reason.
func TestTheImportedMemoryIsReachableWhenThePolicyAllowsIt(t *testing.T) {
	importedAndWithheld(t)
	if err := os.WriteFile(os.Getenv("DEJA_POLICY_FILE"), []byte(`{}`), 0o600); err != nil {
		t.Fatal(err)
	}
	out, err := captureRun(t, "how", "zonkoshard")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "terraform apply --zonkoshard 7") {
		t.Fatalf("with imported memory allowed the command should be there:\n%s", out)
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
