package index

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// A pass that writes the log again used to read all of it back three more
// times: for the offset table, to check every friction entry, and to walk the
// command failures (#5402). It knows which sessions it changed, and the rest
// are copied record for record, so each of the three reads only those.
func TestReplacementPassSidecarsReadOnlyTheChangedSessions(t *testing.T) {
	tmp := parityEnv(t, map[string]string{"DEJA_CLAUDE_ROOT": "claude"})
	// The command table's last run comes back in the local zone from the
	// records and in UTC from a full build: the same instant, printed apart.
	defer func(l *time.Location) { time.Local = l }(time.Local)
	time.Local = time.UTC
	proj := filepath.Join(tmp, "claude", "-tmp-proj")
	const n = 12
	session := func(i int, ask string) {
		id := fmt.Sprintf("s%02d", i)
		var body string
		var bodySb27 strings.Builder
		for turn := range 4 {
			ts := fmt.Sprintf("%02d:%02d", i*4+turn, 0)[:3]
			tid := fmt.Sprintf("t%d", turn)
			bodySb27.WriteString(parityClaudeLine(id, ts+"00Z", "user", fmt.Sprintf("%q", ask)) +
				parityClaudeLine(id, ts+"01Z", "assistant", `[{"type":"tool_use","id":"`+tid+`","name":"Bash","input":{"command":"make migrate"}}]`) +
				parityClaudeLine(id, ts+"02Z", "user", `[{"type":"tool_result","tool_use_id":"`+tid+`","content":"dial tcp 127.0.0.1:5432: connect: connection refused\nmake: *** [migrate] Error 1"}]`))
		}
		body += bodySb27.String()
		parityWrite(t, filepath.Join(proj, id+".jsonl"), body, false)
		later := time.Now().Add(time.Duration(i+len(ask)) * time.Second)
		_ = os.Chtimes(filepath.Join(proj, id+".jsonl"), later, later)
	}
	for i := range n {
		session(i, "why does the migration hang")
	}
	inc := filepath.Join(tmp, "inc")
	parityPass(t, inc, false)
	// One rewrite first, so the measured pass starts from what a store that
	// was updated before holds: a command-fail state and friction texts.
	session(3, "why does the migration hang again")
	parityPass(t, inc, false)

	scanned, outputs, seen := atomic.LoadInt64(&recordOffsetsScanned), atomic.LoadInt64(&frictionOutputsRead), atomic.LoadInt64(&commandFailRecordsSeen)
	session(7, "why does the migration still hang")
	parityPass(t, inc, false)
	scanned, outputs, seen = atomic.LoadInt64(&recordOffsetsScanned)-scanned, atomic.LoadInt64(&frictionOutputsRead)-outputs, atomic.LoadInt64(&commandFailRecordsSeen)-seen

	// One session of twelve changed: 12 records of it, 4 of them outputs.
	if scanned != 0 {
		t.Errorf("the offset table walked %d records, want none: the pass knows where it wrote each", scanned)
	}
	if outputs > 4 {
		t.Errorf("friction texts read %d tool outputs back, want the changed session's 4", outputs)
	}
	if seen > 12 {
		t.Errorf("the command-fail walk looked at %d records, want the changed session's 12", seen)
	}
	m, err := readManifest(inc)
	if err != nil {
		t.Fatal(err)
	}
	if len(frictionWalls(m)) == 0 || len(ReadCommandFails(inc)) == 0 {
		t.Fatal("no wall or no command failure on file, so this measures nothing")
	}
	checkFrictionTexts(t, inc, m)
	offsetTableMatchesAWalk(t, inc, true)
	sameAsRebuild(t, inc)
}

// offsetTableMatchesAWalk holds the offset table in dir to a walk of its log.
// Unless whole is set, a table that covers only part of the log, or none, is
// left alone: readers walk the rest.
func offsetTableMatchesAWalk(t *testing.T, dir string, whole bool) {
	t.Helper()
	f, err := os.Open(filepath.Join(dir, "records.bin"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		t.Fatal(err)
	}
	ro, err := readRecordOffsetsFor(dir, f, nil)
	if !whole && (err != nil || ro.covered != fi.Size()) {
		return
	}
	if err != nil {
		t.Fatalf("no offset table that fits the log: %v", err)
	}
	m, err := readManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	walked := map[string][]int64{}
	n, err := scanRecordOffsets(f, tablesFromManifest(m), 0, fi.Size(), walked)
	if err != nil {
		t.Fatal(err)
	}
	if ro.covered != fi.Size() || ro.records != n || fmt.Sprint(ro.byKey) != fmt.Sprint(walked) {
		t.Errorf("offset table covers %d bytes and %d records, the log %d and %d; same offsets: %v",
			ro.covered, ro.records, fi.Size(), n, fmt.Sprint(ro.byKey) == fmt.Sprint(walked))
	}
}
