package main

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/policy"
	"github.com/vshulcz/deja-vu/internal/usage"
)

// policyStatusLine names which paths a trust rule switches off, for the one
// line that is always on screen. Silence is wrong when the reader's own
// searches are withheld, and "activates nothing" is wrong when only the
// automatic path is (#1012, #1102).
func policyStatusLine() string {
	pol := policy.Load()
	off := make([]string, 0, 3)
	for _, a := range []string{policy.ActivationAuto, policy.ActivationSearch, policy.ActivationMCP} {
		if pol.Describe(a) == "nothing activates" {
			off = append(off, a)
		}
	}
	switch len(off) {
	case 0:
		return ""
	case 3:
		return "deja · off here · the trust policy activates nothing (`deja doctor`)"
	default:
		return "deja · " + strings.Join(off, "+") + " off · the trust policy (`deja doctor`)"
	}
}

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
	if warmupJustRequested(dir) {
		// Requiring a manifest here left the very minute after install — the
		// first build, no index yet — reading "no recalls yet today", a normal
		// quiet day (#925). The same guard #909 took off the hook.
		if index.HasManifest(dir) {
			fmt.Fprint(stdout, "deja · rebuilding the index · recall is quiet until it finishes")
		} else {
			fmt.Fprint(stdout, "deja · indexing your history · recall comes online in a few seconds")
		}
		return nil
	}
	// A rule that activates nothing turns memory off everywhere, and the line
	// people always have on screen read the same as an ordinary quiet day:
	// "no recalls yet today" is true and says nothing about why it will stay
	// true (#1012).
	// Name the path that is off rather than generalising one rule to all of
	// them: an `auto` rule said "activates nothing" while search and MCP still
	// answered, and a `search` rule said nothing at all (#1102).
	if line := policyStatusLine(); line != "" {
		fmt.Fprint(stdout, withFileMemory(dir, in, line))
		return nil
	}
	// One read for the whole line: it renders on every prompt, and two passes
	// over the log can also straddle a write and print numbers that were never
	// true together (#2224).
	n := usage.StatusCounters(dir)
	recalls, bytes, injected := n.Recalls, n.Bytes, n.Injected
	if recalls == 0 {
		// Injections are not recalls, but they are the whole day for someone
		// who lives on auto-recall: saying "0 B injected" while memory has
		// been arriving since morning is the kind of untrue line #1403 is
		// about.
		if injected > 0 {
			fmt.Fprint(stdout, withFileMemory(dir, in, fmt.Sprintf("deja · no agent recalls today · %s injected", humanBytes(int64(injected)))))
			return nil
		}
		if wr, wb := n.WeekRecalls, n.WeekBytes; wr > 0 {
			// The line below this one already branches at one; this one said
			// "1 agent recalls" all day, on the surface the reader looks at
			// most (#1600).
			fmt.Fprint(stdout, withFileMemory(dir, in, fmt.Sprintf("deja · quiet today · %d agent recall%s, %s re-used this week",
				wr, pluralS(wr), humanBytes(int64(wb)))))
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
	if raw := n.RawToday; bytes > 0 && raw/int64(bytes) >= 2 {
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
