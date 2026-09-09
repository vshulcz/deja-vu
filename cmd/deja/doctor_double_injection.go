package main

import (
	"fmt"
	"io"

	"github.com/vshulcz/deja-vu/internal/usage"
)

// doctorDoubleInjections reports a session that was handed the same injection
// several times in one second.
//
// Every other check reads a config and answers "is deja's hook here". This one
// reads what actually happened: the usage log holds the injections deja served
// and the session each went to, and one prompt is one injection — the hooks
// dedupe per session and per prompt. Several inside a second are several hooks
// running for one event, which is what a config that collected deja's entry
// more than once does, and what a machine did for a whole day while every
// check above it said "wired" (#3421).
func doctorDoubleInjections(w io.Writer, dir string) {
	repeats := usage.RepeatedInjections(dir)
	if len(repeats) == 0 {
		return
	}
	worst := repeats[0]
	for _, r := range repeats {
		if r.Count > worst.Count {
			worst = r
		}
	}
	fmt.Fprintf(w, "  %-12s %s\n", "repeated",
		fmt.Sprintf("one agent session was served %d %s injections inside a second (%s) — the hooks are wired more than once; `deja install --auto` leaves one of each",
			worst.Count, worst.Kind, worst.At.Local().Format("2006-01-02 15:04:05")))
}
