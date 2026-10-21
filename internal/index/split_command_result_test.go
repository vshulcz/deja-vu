package index

import (
	"path/filepath"
	"strings"
	"testing"
)

// A pass that runs while a command is still running reads its call without
// the result. The result lands by the next pass, in the tail it appends, and
// the command stored already never got the exit 0 a rebuild gives it: `deja`
// said it ran, never that it passed. A clean answer to a call that ran no
// command is still appended.
func TestACommandWhoseResultLandsAPassLaterGetsItsExit(t *testing.T) {
	for _, tc := range []struct {
		name, call string
	}{
		{"command", hBash("app", "s1", 10, 7, "x", "go build ./...", "ok", false)},
		{"edit", hEdit("app", "s1", 10, 7, "x", "main.go", "a()", "b()")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newTwoWayEnv(t)
			a := filepath.Join(h.tmp, "a.db")
			h.put("app", "s1", hPrompt("app", "s1", 10, 0, "why does the build fail"))
			if err := Ensure(a, "", false, nil); err != nil {
				t.Fatal(err)
			}
			call, result, _ := strings.Cut(tc.call, "\n")
			h.add("app", "s1", call+"\n")
			if err := Ensure(a, "", false, nil); err != nil {
				t.Fatal(err)
			}
			h.add("app", "s1", result)
			if err := Ensure(a, "", false, nil); err != nil {
				t.Fatal(err)
			}
			b := filepath.Join(h.tmp, "b.db")
			copyDir(t, a, b)
			if err := Ensure(b, "", true, nil); err != nil {
				t.Fatal(err)
			}
			diffSnaps(t, tc.name, takeSnap(t, a), takeSnap(t, b))
		})
	}
}
