package index

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func roleStore(t *testing.T, withTool bool) string {
	t.Helper()
	tmp := t.TempDir()
	claudeRoot := filepath.Join(tmp, "claude")
	setHome(t, filepath.Join(tmp, "home"))
	t.Setenv("DEJA_CLAUDE_ROOT", claudeRoot)
	dir := filepath.Join(tmp, "index.db")
	t.Setenv("DEJA_INDEX_DIR", dir)
	proj := filepath.Join(claudeRoot, "-w-app")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	lines := `{"type":"user","sessionId":"a","timestamp":"2026-01-02T03:04:05Z","message":{"role":"user","content":"why does the build fail"}}` + "\n"
	if withTool {
		lines += `{"type":"user","sessionId":"a","timestamp":"2026-01-02T03:04:06Z","message":{"role":"user","content":[{"type":"tool_result","content":"go build ./... failed"}]}}` + "\n"
	}
	if err := os.WriteFile(filepath.Join(proj, "a.jsonl"), []byte(lines), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	return dir
}

// bigRoleStore is a store whose second record carries the tool role and whose
// remaining thousands do not, so asking for that role can stop at the front
// while asking for one nothing carries has to reach the end.
func bigRoleStore(t *testing.T) string {
	t.Helper()
	tmp := t.TempDir()
	claudeRoot := filepath.Join(tmp, "claude")
	setHome(t, filepath.Join(tmp, "home"))
	t.Setenv("DEJA_CLAUDE_ROOT", claudeRoot)
	dir := filepath.Join(tmp, "index.db")
	t.Setenv("DEJA_INDEX_DIR", dir)
	t.Setenv("DEJA_INDEX_COMMANDS", "1")
	proj := filepath.Join(claudeRoot, "-w-app")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	b.WriteString(`{"type":"user","sessionId":"a","timestamp":"2026-01-02T03:04:05Z","message":{"role":"user","content":"why does the build fail"}}` + "\n")
	b.WriteString(`{"type":"user","sessionId":"a","timestamp":"2026-01-02T03:04:06Z","message":{"role":"user","content":[{"type":"tool_result","content":"go build ./... failed"}]}}` + "\n")
	filler := strings.Repeat("the pgbouncer pool timed out and the retry took another second ", 12)
	for i := 0; i < 6000; i++ {
		fmt.Fprintf(&b, `{"type":"assistant","sessionId":"a","timestamp":"2026-01-02T03:04:07Z","message":{"role":"assistant","content":%q}}`+"\n", fmt.Sprintf("%s %d", filler, i))
	}
	// One record of a second role at the very end: an implementation that gives
	// up on an absent role partway through the log answers "absent" for this
	// one, and a test that only measures cost would let it through.
	b.WriteString(`{"type":"user","sessionId":"a","timestamp":"2026-01-02T03:04:08Z","message":{"role":"user","content":[{"type":"tool_use","name":"Bash","input":{"command":"psql -c 'show pool'"}}]}}` + "\n")
	if err := os.WriteFile(filepath.Join(proj, "a.jsonl"), []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	return dir
}

// The question behind "this index holds no tool records at all": absent means
// absent, present means present, and an index that cannot be read says nothing
// rather than claiming absence.
func TestHasRecordOfRole(t *testing.T) {
	if dir := roleStore(t, true); !HasRecordOfRole(dir, roleToolOutput) {
		t.Error("a store with a tool result reports none")
	}
	if dir := roleStore(t, false); HasRecordOfRole(dir, roleToolOutput) {
		t.Error("a talk-only store reports tool records")
	}
	if !HasRecordOfRole(filepath.Join(t.TempDir(), "no-index"), roleToolOutput) {
		t.Error("an unreadable index claimed the role is absent")
	}
}

// It stops at the first match rather than reading the whole log: the caller
// asks on every empty role search, and most stores that have the records have
// them near the front.
//
// Against a two-record store this measured nothing — fifty full scans of two
// records are nowhere near any wall-clock bound, so the case passed with the
// early exit deleted (#2003). A ratio against the scan that cannot stop early,
// over a store where a full pass costs something.
func TestHasRecordOfRoleStopsEarly(t *testing.T) {
	if testing.Short() {
		t.Skip("timing")
	}
	dir := bigRoleStore(t)

	// The role is in the second record of the log; the absent one is nowhere,
	// so answering it has to reach the end.
	// Best of three for the cheap side: a call costs ~120 microseconds, almost
	// all of it fixed, so one scheduler stall on a loaded runner is a multiple
	// of the whole measurement while the full scan absorbs the same stall.
	early := time.Hour
	for round := 0; round < 3; round++ {
		started := time.Now()
		for i := 0; i < 20; i++ {
			if !HasRecordOfRole(dir, roleToolOutput) {
				t.Fatal("lost the record it just found")
			}
		}
		if took := time.Since(started); took < early {
			early = took
		}
	}

	started := time.Now()
	for i := 0; i < 20; i++ {
		if HasRecordOfRole(dir, "no-such-role") {
			t.Fatal("a role nothing carries was reported present")
		}
	}
	full := time.Since(started)

	// Cost is not correctness: an implementation that gives up on an absent
	// role partway through the log measures exactly like one that reads to the
	// end, and the last record is the one it would miss.
	if !HasRecordOfRole(dir, roleCommand) {
		t.Error("a role carried only by the last record was reported absent")
	}

	// The fixture has to make a full pass expensive, or the comparison below
	// compares two numbers that are both noise.
	if full < 20*time.Millisecond {
		t.Fatalf("a full scan of the fixture took %v for twenty passes, which is too cheap to measure against", full)
	}
	// Half the full scan. Deleting the early exit puts the ratio at 1.0, so
	// this still fails it by a factor of two, and it survives the contended
	// runs where a quarter did not.
	if early > full/2 {
		t.Errorf("finding the role in the second record took %v against %v to scan the whole log: it is not stopping early", early, full)
	}
	t.Logf("early %v, full %v", early, full)
}
