package main

import (
	"fmt"
	"github.com/vshulcz/deja-vu/internal/index"
	"io"
	"os"
	"strings"

	"github.com/vshulcz/deja-vu/internal/usage"
)

// runStatusline prints one line for a status bar: how much memory deja served
// to agents today, and what earlier sessions decided about the file this one
// is working on. It must stay fast and quiet — no lock, no fork, no record
// read — because status bars call it constantly. Since #581 it does read the
// manifest: measured at 2 ms cold on a 1149-session store, and it is one file
// rather than the log.
func runStatusline(dir string, stdin io.Reader, stdout io.Writer) error {
	in := readStatuslineInput(stdin)
	// A first build takes a while and runs detached. The status bar is the
	// one surface the user is already looking at, so the build reports there
	// instead of leaving them to wonder why recall is quiet.
	if st := readWarmupStatus(dir); st != nil {
		fmt.Fprintf(stdout, "deja %s %s", warmupBar(st), st.progress())
		return nil
	}
	// A build asked for moments ago has not published progress yet, and until
	// it does this said "no recalls yet today" — a normal day, while the index
	// on disk is one this build cannot read (#879).
	if warmupJustRequested(dir) && index.HasManifest(dir) {
		fmt.Fprint(stdout, "deja · rebuilding the index · recall is quiet until it finishes")
		return nil
	}
	recalls, bytes, injected := usage.TodayWithInjections(dir)
	if recalls == 0 {
		if wr, wb, _, _ := usage.Week(dir); wr > 0 {
			fmt.Fprint(stdout, withFileMemory(dir, in, fmt.Sprintf("deja · quiet today · %d agent recalls, %s re-used this week", wr, humanBytes(int64(wb)))))
			return nil
		}
		fmt.Fprint(stdout, withFileMemory(dir, in, "deja · no recalls yet today · 0 B injected"))
		return nil
	}
	noun := "recalls"
	if recalls == 1 {
		noun = "recall"
	}
	line := fmt.Sprintf("deja · %d %s · %s ctx today · %s injected", recalls, noun, humanBytes(int64(bytes)), humanBytes(int64(injected)))
	if raw := usage.TodayRaw(dir); bytes > 0 && raw/int64(bytes) >= 2 {
		line += fmt.Sprintf(" · ~%d× less than replaying", raw/int64(bytes))
	}
	fmt.Fprint(stdout, withFileMemory(dir, in, line))
	return nil
}

// warmupBar draws the build as a bar rather than a bare percentage: a status
// line is read at a glance, and a moving bar reads as progress where a number
// reads as noise. Width is fixed so the line does not jitter between frames.
func warmupBar(st *warmupStatus) string {
	const width = 10
	filled := 0
	if st.Total > 0 {
		filled = width * st.Done / st.Total
		if filled > width {
			filled = width
		}
	}
	return "▕" + strings.Repeat("█", filled) + strings.Repeat("░", width-filled) + "▏"
}

// pipedStdin reports whether anything was actually piped in. An interactive
// terminal has not, and reading it would block until the user typed.
func pipedStdin(r io.Reader) bool {
	if f, ok := r.(*os.File); ok {
		if fi, err := f.Stat(); err != nil || fi.Mode()&os.ModeCharDevice != 0 {
			return false
		}
	}
	return true
}
