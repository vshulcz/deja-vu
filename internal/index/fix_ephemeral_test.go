package index

import (
	"testing"

	"github.com/vshulcz/deja-vu/internal/model"
)

// A remedy that names a file under a temp directory cannot be run by anyone,
// including the session that ran it: the file is gone by the time deja serves
// the pair. On this machine 38 of 186 confirmed pairs stored one — an agent
// scripts an edit into a scratch file, runs it, then re-runs the suite, and
// the whole line is mined as the fix for the failing test.
//
// Handing that back at the moment an agent is stuck is worse than silence,
// which is the bar the miner already holds itself to for a command that
// failed (#2163).
func TestARemedyUnderATempDirectoryIsNotMined(t *testing.T) {
	gone := []string{
		"$ python3 /Users/x/.claude/jobs/9f0aa059/tmp/edit38.py; go test ./internal/index/",
		"$ sh /tmp/patch.sh",
		"$ bash /var/folders/2b/T/scratch/apply.sh && go build ./...",
		"$ python3 $TMPDIR/fixture.py",
	}
	for _, cmd := range gone {
		if !namesAnEphemeralPath(cmd) {
			t.Errorf("kept as a remedy, and the file it names is gone: %s", cmd)
		}
	}

	kept := []string{
		"$ go mod tidy",
		"$ python3 scripts/gen.py",
		"$ brew install pgbouncer",
		// The word appears, the path does not: a flag or a package name that
		// merely spells it is not a scratch file.
		"$ go test ./internal/tmp/",
		"$ kubectl get pods --request-timeout=20s",
		"$ git commit -m 'drop the /tmp fallback'",
	}
	for _, cmd := range kept {
		if namesAnEphemeralPath(cmd) {
			t.Errorf("dropped a runnable remedy: %s", cmd)
		}
	}
}

// End to end through the miner, so the rule is wired and not merely present.
func TestTheMinerSkipsAScratchScriptAndTakesTheRealRemedy(t *testing.T) {
	ms := []model.Message{
		{Role: roleToolOutput, Text: "--- FAIL: TestPoolTimeout"},
		{Role: roleCommand, Text: "$ python3 /tmp/edit.py; go test ./..."},
		{Role: roleToolOutput, Text: "ok"},
		{Role: roleCommand, Text: "$ go mod tidy"},
		{Role: roleToolOutput, Text: "ok"},
	}
	pairs := fixPairsIn(ms, "k", "proj")
	if len(pairs) != 1 {
		t.Fatalf("want one pair, got %d: %#v", len(pairs), pairs)
	}
	if pairs[0].Command != "$ go mod tidy" {
		t.Errorf("remedy = %q, want the command that can still be run", pairs[0].Command)
	}
}
