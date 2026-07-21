package main

import (
	"fmt"
	"io"
	"os"

	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/search"
	"github.com/vshulcz/deja-vu/internal/usage"
)

// runBrief is what a bare `deja` on a terminal shows: the memory, alive.
// Manifest metadata and the usage sidecar only — it must feel instant.
// Pipes and scripts still get the usage text; `deja help` always works.
func runBrief(dir string, w io.Writer) error {
	ov, err := index.Overview(dir)
	if err != nil || ov.Sessions == 0 {
		printUsage()
		return nil
	}
	color := statColorOK(os.Stdout)
	bold, dim, reset := "", "", ""
	if color {
		bold, dim, reset = logoBold, logoDim, logoReset
	}
	fmt.Fprintf(w, "%sdeja-vu%s %s · %s%d%s sessions across %s%d%s agents\n",
		bold, reset, version, bold, ov.Sessions, reset, bold, ov.Harnesses, reset)

	recalls, bytes, _ := usage.TodayWithInjections(dir)
	line := fmt.Sprintf("today      %d session%s", ov.SessionsToday, pluralS(ov.SessionsToday))
	if recalls > 0 {
		line += fmt.Sprintf(" · %d recall%s served (%s", recalls, pluralS(recalls), humanBytes(int64(bytes)))
		if raw := usage.TodayRaw(dir); bytes > 0 && raw/int64(bytes) >= 2 {
			line += " from " + humanBytes(raw)
		}
		line += ")"
	}
	fmt.Fprintln(w, line)

	wr, _, _, _ := usage.Week(dir)
	week := fmt.Sprintf("this week  %d sessions · %d recalls", ov.SessionsWeek, wr)
	if dv := usage.DejaVuWeek(dir); dv > 0 {
		week += fmt.Sprintf(" · %s%d déjà vu moment%s%s", bold, dv, pluralS(dv), reset)
	}
	fmt.Fprintln(w, week)

	// Read the index as-is: the brief must never trigger a rebuild or let
	// indexing narration tear through its layout.
	if recent, err := index.RecentMatching(dir, 3, search.Options{}); err == nil && len(recent) > 0 {
		label := "recent    "
		for _, s := range recent {
			title := s.Title
			if title == "" {
				title = firstUserTitle(s)
			}
			title = trimBriefTitle(title)
			fmt.Fprintf(w, "%s %s[%s]%s %s · %s%s%s", label, dim, s.Harness, reset, s.Project, dim, search.RelativeDate(s.Updated), reset)
			if title != "" {
				fmt.Fprintf(w, " · %s", title)
			}
			fmt.Fprintln(w)
			label = "          "
		}
	}

	if q := suggestFirstQuery(dir); q != "" {
		fmt.Fprintf(w, "try        %sdeja \"%s\"%s %s(from your own history)%s\n", bold, q, reset, dim, reset)
	}
	fmt.Fprintf(w, "%smore       deja log · deja stats · deja help%s\n", dim, reset)
	return nil
}

func pluralS(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

func trimBriefTitle(t string) string {
	r := []rune(t)
	if len(r) > 44 {
		return string(r[:44]) + "…"
	}
	return t
}
