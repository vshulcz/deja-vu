package index

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sync/atomic"
	"testing"
	"time"
)

// A full build left no command-fail state, so the first replacing pass after
// it walked the whole log for the table: 15 of 21 s for 20 sessions of a
// 40,000 session store (#5465).
func TestTheFirstReplacingPassAfterABuildReadsOnlyTheChangedSession(t *testing.T) {
	tmp := parityEnv(t, map[string]string{"DEJA_CLAUDE_ROOT": "claude"})
	defer func(l *time.Location) { time.Local = l }(time.Local)
	time.Local = time.UTC
	proj := filepath.Join(tmp, "claude", "-tmp-proj")
	const n = 12
	session := func(i int, ask string) {
		id := fmt.Sprintf("s%02d", i)
		var body string
		for turn := 0; turn < 4; turn++ {
			ts := fmt.Sprintf("%02d:%02d", i*4+turn, 0)[:3]
			tid := fmt.Sprintf("t%d", turn)
			body += parityClaudeLine(id, ts+"00Z", "user", fmt.Sprintf("%q", ask)) +
				parityClaudeLine(id, ts+"01Z", "assistant", `[{"type":"tool_use","id":"`+tid+`","name":"Bash","input":{"command":"make migrate"}}]`) +
				parityClaudeLine(id, ts+"02Z", "user", `[{"type":"tool_result","tool_use_id":"`+tid+`","content":"dial tcp 127.0.0.1:5432: connect: connection refused\nmake: *** [migrate] Error 1"}]`)
		}
		parityWrite(t, filepath.Join(proj, id+".jsonl"), body, false)
		later := time.Now().Add(time.Duration(i+len(ask)) * time.Second)
		_ = os.Chtimes(filepath.Join(proj, id+".jsonl"), later, later)
	}
	for i := 0; i < n; i++ {
		session(i, "why does the migration hang")
	}
	inc := filepath.Join(tmp, "inc")
	parityPass(t, inc, false)

	seen := atomic.LoadInt64(&commandFailRecordsSeen)
	session(7, "why does the migration still hang")
	parityPass(t, inc, false)
	seen = atomic.LoadInt64(&commandFailRecordsSeen) - seen

	if seen > 12 {
		t.Errorf("the command-fail walk looked at %d records, want the changed session's 12", seen)
	}
	if len(ReadCommandFails(inc)) == 0 {
		t.Fatal("no command failure on file, so this measures nothing")
	}
	sameAsRebuild(t, inc)

	// The control: without the state on file the walk is whole again.
	if err := os.Remove(commandFailStatePath(inc)); err != nil {
		t.Fatal(err)
	}
	seen = atomic.LoadInt64(&commandFailRecordsSeen)
	session(8, "why does the migration still hang")
	parityPass(t, inc, false)
	if seen = atomic.LoadInt64(&commandFailRecordsSeen) - seen; seen < n*12 {
		t.Errorf("with no state the walk looked at %d records, want all %d, so the counter misses the walk", seen, n*12)
	}
}

// The state a build leaves is the walk of the log it wrote. A copy that ends
// on the command its owner has a prompt after holds that command once in the
// log, under the owner, so the run is over; fed the copy's turns again, the
// state kept it pending and blamed an output appended to the owner on it.
func TestTheBuildsCommandFailStateIsTheWalkOfItsLog(t *testing.T) {
	tmp := parityEnv(t, map[string]string{"DEJA_CLAUDE_ROOT": "claude"})
	defer func(l *time.Location) { time.Local = l }(time.Local)
	time.Local = time.UTC
	proj := filepath.Join(tmp, "claude", "-tmp-proj")
	cmd := func(id, ts string) string {
		return parityClaudeLine(id, ts, "assistant", `[{"type":"tool_use","id":"t1","name":"Bash","input":{"command":"make migrate"}}]`)
	}
	out := func(id, ts, tid string) string {
		return parityClaudeLine(id, ts, "user", `[{"type":"tool_result","tool_use_id":"`+tid+`","content":"dial tcp 127.0.0.1:5432: connect: connection refused\nmake: *** [migrate] Error 1"}]`)
	}
	parityWrite(t, filepath.Join(proj, "z-other.jsonl"),
		parityClaudeLine("other", "00:00Z", "user", `"run the migration"`)+cmd("other", "00:01Z")+out("other", "00:02Z", "t1"), false)
	head := parityClaudeLine("s1", "01:00Z", "user", `"run the migration"`) + cmd("s1", "01:01Z")
	a := filepath.Join(proj, "a.jsonl")
	parityWrite(t, a, head+parityClaudeLine("s1", "01:02Z", "user", `"never mind, look at the docs"`), false)
	parityWrite(t, filepath.Join(proj, "b.jsonl"), head, false)
	inc := filepath.Join(tmp, "inc")
	parityPass(t, inc, false)
	st := readCommandFailState(inc)
	if st == nil {
		t.Fatal("the build left no command-fail state")
	}
	if s := st.Acc.Sessions["claude:s1"]; s != nil && s.Pending != "" {
		t.Errorf("the build left %q pending in s1, which a walk of its log ends with nothing pending", s.Pending)
	}

	parityWrite(t, a, out("s1", "01:03Z", "t9"), true)
	later := time.Now().Add(5 * time.Second)
	_ = os.Chtimes(a, later, later)
	parityPass(t, inc, false)
	if got, want := ReadCommandFails(inc), commandFailsByFullWalk(t, inc); len(got)+len(want) > 0 && !reflect.DeepEqual(got, want) {
		t.Errorf("after an append the table is\n%+v\na walk of the same index gives\n%+v", got, want)
	}
}
