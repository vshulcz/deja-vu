package main

import (
	"fmt"
	"io"
	"os"
	"strings"
	"unicode"

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
	weekRecalls, _, _, _ := usage.Week(dir)
	dejaVu := usage.DejaVuWeek(dir)
	// Only when the week has nothing at all to report. Recalls and déjà vu
	// moments are counted from usage, not from session age, so a store whose
	// sessions are old can still have served memory this week — and hiding that
	// was exactly the wrong trade.
	quietWeek := ov.SessionsToday == 0 && ov.SessionsWeek == 0 &&
		recalls == 0 && weekRecalls == 0 && dejaVu == 0 && !ov.Oldest.IsZero()
	line := fmt.Sprintf("today      %d session%s", ov.SessionsToday, pluralS(ov.SessionsToday))
	if recalls > 0 {
		line += fmt.Sprintf(" · %d recall%s served (%s", recalls, pluralS(recalls), humanBytes(int64(bytes)))
		if raw := usage.TodayRaw(dir); bytes > 0 && raw/int64(bytes) >= 2 {
			line += " from " + humanBytes(raw)
		}
		line += ")"
	}
	if !quietWeek {
		fmt.Fprintln(w, line)
	}

	wr := weekRecalls
	if quietWeek {
		// Nothing this week. Two zero lines is the worst possible opening for
		// someone whose agent history is real but older — and the interesting
		// fact is right there: how far back the memory goes.
		fmt.Fprintf(w, "covering   %s%s → %s%s\n", bold,
			ov.Oldest.Local().Format("Jan 2 2006"), ov.Newest.Local().Format("Jan 2 2006"), reset)
	} else {
		week := fmt.Sprintf("this week  %d session%s · %d recall%s", ov.SessionsWeek, pluralS(ov.SessionsWeek), wr, pluralS(wr))
		if dejaVu > 0 {
			week += fmt.Sprintf(" · %s%d déjà vu moment%s%s", bold, dejaVu, pluralS(dejaVu), reset)
		}
		fmt.Fprintln(w, week)
	}

	// Sessions ahead of the clock sit at the top of "recent" with dates that
	// have not happened, and the counters above deliberately leave them out —
	// so the screen has to say they exist, or the two disagree with no
	// explanation (#696).
	if ov.Future > 0 {
		fmt.Fprintf(w, "ahead      %d session%s stamped later than this machine's clock\n", ov.Future, pluralS(ov.Future))
	}

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

	// What the counters above cannot say: which memory kept being worth
	// recalling. "63 recalls" is a rate; a named piece of work is the thing a
	// person repeats to a colleague (#579).
	if r, ok := findReusedMemory(dir); ok {
		fmt.Fprintf(w, "reused     %s%s%s\n", bold, trimBriefTitle(r.Title), reset)
		// Not "by agents": the count includes the déjà vu events written when
		// the user's own prompt returns to the same ground, which is a person
		// coming back rather than an agent pulling. And not "so far": the
		// usage log keeps a rolling window, so the count is recent history.
		fmt.Fprintf(w, "           %s%d× re-used recently · last worked %s%s\n", dim, r.Times, search.RelativeDate(r.Age), reset)
	}

	// The other line drawn from the reader's own data rather than from a
	// counter: a wall this machine keeps running into. Manifest-only, like the
	// asked line above it — the command that reports the full list reads the
	// record log, which is a hundred times this screen's budget.
	if f, ok := index.FindFriction(dir); ok {
		fmt.Fprintf(w, "hit        %s%s%s\n", bold, trimBriefTitle(f.Text), reset)
		fmt.Fprintf(w, "again      %s%d sessions · last %s · deja friction%s\n",
			dim, len(f.Sessions), search.RelativeDate(f.Last), reset)
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

// pluralY is pluralS for words ending in -y: one query, two queries.
func pluralY(n int) string {
	if n == 1 {
		return "y"
	}
	return "ies"
}

func pluralS(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

// trimBriefTitle bounds a title for the brief and strips what a terminal would
// act on rather than print. Titles are user-typed text arriving verbatim: a
// carriage return rewinds the line, an escape recolours the rest of the
// screen, a bell rings on every refresh. The `recent` lines have printed them
// raw since they existed; this is one place for all of them (#634 set the same
// rule for the status bar).
func trimBriefTitle(t string) string {
	t = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) {
			return ' '
		}
		return r
	}, t)
	t = strings.Join(strings.Fields(t), " ")
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
