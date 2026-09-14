package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/index"
)

// A filter that matches nothing is not an unbuilt index. `deja stats --since
// 30d` over a store whose sessions are all older answered "nothing indexed yet
// — run `deja index`", which is advice for a state deja is not in: indexing
// changes nothing and doctor reports the stores as found. `last` learned to say
// what emptied the result in #637; stats was the screen still guessing.
func TestStatsNamesTheFilterThatEmptiedIt(t *testing.T) {
	tmp := hermeticEnv(t)
	writeClaudeFixture(t, filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "work", "one.jsonl"), "narrow", []string{
		`{"type":"user","sessionId":"narrow","timestamp":"2026-01-05T10:00:00Z",` +
			`"message":{"role":"user","content":"the migration ran twice"}}`,
	})
	dir := filepath.Join(tmp, "index.db")
	if err := index.Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}

	for _, c := range []struct {
		args []string
		want string
	}{
		{[]string{"stats", "--since", "30d"}, "since 30d"},
		{[]string{"stats", "--harness", "codex"}, `harness "codex"`},
		{[]string{"stats", "--project", "nowhere"}, `project "nowhere"`},
	} {
		out, err := captureRun(t, c.args...)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(out, "run `deja index`") {
			t.Errorf("%v sent the reader after a build that changes nothing:\n%s", c.args, out)
		}
		if !strings.Contains(out, "no sessions match "+c.want) {
			t.Errorf("%v does not name what emptied it:\n%s", c.args, out)
		}
	}

	// --since earns the extra line `last` prints: every session is older, and
	// the newest one dates the window the reader would have to widen to.
	out, err := captureRun(t, "stats", "--since", "30d")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "2026-01-05") {
		t.Errorf("the window note does not date the newest session:\n%s", out)
	}

	// And an index with nothing in it keeps the advice that fits it, filter or
	// no filter — there the build is exactly what is missing.
	hermeticEnv(t)
	if err := index.Ensure(filepath.Join(t.TempDir(), "index.db"), "", false, nil); err != nil {
		t.Fatal(err)
	}
	out, err = captureRun(t, "stats", "--since", "30d")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "nothing indexed yet") {
		t.Errorf("an empty index lost the advice that fits it:\n%s", out)
	}
}
