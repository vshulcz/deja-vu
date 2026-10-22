package index

import (
	"fmt"
	"path/filepath"
	"testing"
	"time"
)

// A pass read the command times from the record log in local time and a build
// from the transcripts in UTC, so the day `deja how` and orient printed for
// "last" depended on which one wrote the table.
func TestCommandTimesReadBackInLocalTimeWhicheverPathWrote(t *testing.T) {
	tmp := parityEnv(t, map[string]string{"DEJA_CLAUDE_ROOT": "claude"})
	loc := time.Local
	t.Cleanup(func() { time.Local = loc })
	time.Local = time.FixedZone("east", 5*3600)
	proj := filepath.Join(tmp, "claude", "-tmp-proj")
	run := func(id string, n int) string {
		tid := fmt.Sprintf("t%d", n)
		return parityClaudeLine(id, fmt.Sprintf("%02d:01Z", n), "assistant", `[{"type":"tool_use","id":"`+tid+`","name":"Bash","input":{"command":"make zzlocal"}}]`) +
			parityClaudeLine(id, fmt.Sprintf("%02d:02Z", n), "user", `[{"type":"tool_result","tool_use_id":"`+tid+`","content":"ok"}]`)
	}
	for _, id := range []string{"a", "b"} {
		parityWrite(t, filepath.Join(proj, id+".jsonl"), parityClaudeLine(id, "00:00Z", "user", `"ship it"`)+run(id, 1), false)
	}
	inc := filepath.Join(tmp, "inc")
	parityPass(t, inc, true)
	parityWrite(t, filepath.Join(proj, "a.jsonl"), run("a", 2), true)
	parityPass(t, inc, false)
	fresh := inc + "-fresh"
	parityPass(t, fresh, true)
	for _, dir := range []string{inc, fresh} {
		cmds := ReadCommands(dir)
		if len(cmds) == 0 {
			t.Fatalf("%s: no command table, so this measures nothing", filepath.Base(dir))
		}
		for _, c := range cmds {
			if c.Last.Location() != time.Local {
				t.Errorf("%s: %q last ran %v, not in local time", filepath.Base(dir), c.Command, c.Last)
			}
			for p, pu := range c.ByProject {
				if pu.Last.Location() != time.Local {
					t.Errorf("%s: %q in %s last ran %v, not in local time", filepath.Base(dir), c.Command, p, pu.Last)
				}
			}
		}
	}
	if a, b := fmt.Sprint(ReadCommands(inc)), fmt.Sprint(ReadCommands(fresh)); a != b {
		t.Errorf("command table after the pass:\n  %s\nafter a rebuild:\n  %s", a, b)
	}
}
