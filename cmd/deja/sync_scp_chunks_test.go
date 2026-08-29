package main

import (
	"fmt"
	"strings"
	"testing"
)

// Export writes a file per source transcript, so a machine with tens of
// thousands of records hands scp thousands of paths. Windows refuses a command
// line over 32,767 characters outright — and it refuses it after the export has
// run, which is the worst moment to find out (#2002).
func TestScpSplitsPathsUnderTheCommandLineBound(t *testing.T) {
	var paths []string
	for i := 0; i < 23336; i++ {
		paths = append(paths, fmt.Sprintf(`C:\Users\someone\AppData\Local\Temp\deja-sync-9f3a12bc-17761031960%05d.jsonl`, i))
	}
	budget := sshArgsBudget([]string{"-o", "BatchMode=yes"}, "root@example.com", "/tmp/deja-sync-XXXX")
	chunks := scpChunks(paths, budget)
	if len(chunks) < 2 {
		t.Fatalf("23336 paths went out in %d command line(s)", len(chunks))
	}
	seen := 0
	for i, chunk := range chunks {
		seen += len(chunk)
		if n := len(strings.Join(chunk, " ")); n > budget {
			t.Errorf("chunk %d is %d characters, the budget is %d", i, n, budget)
		}
		if len(chunk) == 0 {
			t.Errorf("chunk %d is empty", i)
		}
	}
	if seen != len(paths) {
		t.Errorf("sent %d paths of %d — the rest would never reach the peer", seen, len(paths))
	}
}

// One path longer than the whole budget still goes: refusing to send it is
// worse than letting the platform complain about that one file.
func TestAPathBiggerThanTheBudgetStillGoes(t *testing.T) {
	long := strings.Repeat("x", 40000) + ".jsonl"
	chunks := scpChunks([]string{long, "b.jsonl"}, 1000)
	if len(chunks) != 2 {
		t.Fatalf("chunks = %d, want the long path on its own and the short one after", len(chunks))
	}
	if chunks[0][0] != long {
		t.Error("the long path was dropped")
	}
}

// The budget counts what the flags and the destination already spend, so a long
// host or a long remote directory cannot push the line over on its own.
func TestTheBudgetLeavesRoomForFlagsAndDestination(t *testing.T) {
	plain := sshArgsBudget(nil, "h", "/tmp/x")
	loaded := sshArgsBudget([]string{"-o", strings.Repeat("K", 400)}, strings.Repeat("h", 200), strings.Repeat("/d", 100))
	if loaded >= plain {
		t.Errorf("a heavier fixed part left as much room for paths: %d vs %d", loaded, plain)
	}
	if loaded < 512 {
		t.Errorf("budget collapsed to %d; one path at a time is the floor", loaded)
	}
}
