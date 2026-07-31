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
	justGreeted := false
	ov, err := index.Overview(dir)
	if err != nil || ov.Sessions == 0 {
		// Nothing indexed yet. Printing usage here is what a fresh install used
		// to do, and it is the wrong answer to `deja`: the reader has just
		// installed the thing and gets forty lines of command syntax instead of
		// one fact about their own work. Build the index — the progress display
		// and the per-harness summary already exist — and show the brief.
		if !index.HasManifest(dir) {
			greeted, err := buildForFirstRun(dir)
			if err != nil {
				return err
			}
			justGreeted = greeted
			if ov, err = index.Overview(dir); err != nil {
				return err
			}
		}
		if ov.Sessions == 0 {
			printNoHistory(w)
			return nil
		}
	}
	color := statColorOK(os.Stdout)
	bold, dim, reset := "", "", ""
	if color {
		bold, dim, reset = logoBold, logoDim, logoReset
	}
	fmt.Fprintf(w, "%sdeja-vu%s %s · %s%d%s session%s across %s%d%s agent%s\n",
		bold, reset, version, bold, ov.Sessions, reset, pluralS(ov.Sessions),
		bold, ov.Harnesses, reset, pluralS(ov.Harnesses))

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
	week := fmt.Sprintf("this week  %d session%s · %d recall%s", ov.SessionsWeek, pluralS(ov.SessionsWeek), wr, pluralS(wr))
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

	// The one line on this screen that says something a person could not have
	// noticed themselves: a question they asked in more than one session. A
	// count of sessions is reporting; this is the thing the tool is for.
	if a, ok := index.FindAskedTwice(dir); ok {
		fmt.Fprintf(w, "asked      %s%s%s\n", bold, trimBriefTitle(a.Text), reset)
		fmt.Fprintf(w, "before     %s%s%s\n", dim, askedWhen(a), reset)
	}

	// The greeting printed on a first build already ends with this exact
	// suggestion. Printing it twice on the one screen that has to be legible
	// is worse than not printing it at all.
	if q := suggestFirstQuery(dir); q != "" && !justGreeted {
		fmt.Fprintf(w, "try        %sdeja \"%s\"%s %s(from your own history)%s\n", bold, q, reset, dim, reset)
	}
	fmt.Fprintf(w, "%smore       deja log · deja stats · deja help%s\n", dim, reset)
	return nil
}

// plural and verbS keep "1 session mentions" from reading as "1 sessions
// mention" — the noun and the verb disagree in opposite directions.
func plural(n int) string { return pluralS(n) }

func verbS(n int) string {
	if n == 1 {
		return "s"
	}
	return ""
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

// buildForFirstRun indexes with the same narration `deja warmup` uses, so the
// wait is legible and ends in the per-harness summary rather than in silence.
// It reports whether the greeting was actually printed, so the brief does not
// repeat the suggestion the greeting already ends with.
func buildForFirstRun(dir string) (bool, error) {
	prepareFirstIndexGreeting(dir)
	if err := withBuildProgress(func() error { return index.Ensure(dir, "", false, os.Stderr) }); err != nil {
		return false, err
	}
	before := index.LastBuild
	maybeFirstIndexGreeting(dir)
	return before.Initial && before.Messages > 0 && logoWanted(os.Stdout), nil
}

// printNoHistory is the honest empty state: the index built and found nothing.
// It names where deja looked, because the usual cause is that the agent stores
// live somewhere this machine does not have.
func printNoHistory(w io.Writer) {
	fmt.Fprintln(w, "deja-vu "+version+" · no agent history found yet")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "deja reads the session stores your agents already write —")
	fmt.Fprintln(w, "Claude Code, Codex, opencode, Cursor, Gemini, Copilot and ten more.")
	fmt.Fprintln(w, "Nothing was found on this machine, which usually means no agent has")
	fmt.Fprintln(w, "run here yet, or its store lives somewhere else.")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "  deja sources     what was looked for, and where")
	fmt.Fprintln(w, "  deja doctor      check the setup")
	fmt.Fprintln(w, "  deja help        every command")
}

// askedWhen says how far apart the askings were, which is the point of the
// line: the same question in May and again in June is worth a reader's
// attention in a way that "asked 4 times" is not.
func askedWhen(a index.AskedTwice) string {
	n := len(a.Sessions)
	newest := a.Sessions[0].Updated
	oldest := a.Sessions[n-1].Updated
	span := fmt.Sprintf("%s → %s", oldest.Format("Jan 2"), search.RelativeDate(newest))
	project := a.Sessions[0].Project
	for _, m := range a.Sessions {
		if m.Project != project {
			project = ""
			break
		}
	}
	times := fmt.Sprintf("%d session%s", n, pluralS(n))
	if project != "" && project != "-" {
		times += " in " + project
	}
	return times + " · " + span
}
