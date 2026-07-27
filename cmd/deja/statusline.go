package main

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/vshulcz/deja-vu/internal/usage"
)

// runStatusline prints one line for a status bar: how much memory deja served
// to agents today. It must stay fast and quiet — no index access, no locks —
// because status bars call it constantly.
func runStatusline(dir string, stdin io.Reader, stdout io.Writer) error {
	drainStdin(stdin)
	// A first build takes a while and runs detached. The status bar is the
	// one surface the user is already looking at, so the build reports there
	// instead of leaving them to wonder why recall is quiet.
	if st := readWarmupStatus(dir); st != nil {
		fmt.Fprintf(stdout, "deja %s %s", warmupBar(st), st.progress())
		return nil
	}
	recalls, bytes, injected := usage.TodayWithInjections(dir)
	if recalls == 0 {
		if wr, wb, _, _ := usage.Week(dir); wr > 0 {
			fmt.Fprintf(stdout, "deja · quiet today · %d agent recalls, %s re-used this week", wr, humanBytes(int64(wb)))
			return nil
		}
		fmt.Fprint(stdout, "deja · no recalls yet today · 0 B injected")
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
	fmt.Fprint(stdout, line)
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

// Claude Code pipes session JSON to statusline commands. We don't need it,
// but leaving the pipe unread can block the caller on some platforms.
func drainStdin(r io.Reader) {
	if f, ok := r.(*os.File); ok {
		if fi, err := f.Stat(); err != nil || fi.Mode()&os.ModeCharDevice != 0 {
			return // interactive terminal: nothing piped, don't block
		}
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(r, 1<<20))
}
