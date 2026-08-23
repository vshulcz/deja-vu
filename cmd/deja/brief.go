package main

import (
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"unicode"

	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/policy"
	"github.com/vshulcz/deja-vu/internal/search"
	"github.com/vshulcz/deja-vu/internal/termwidth"
	"github.com/vshulcz/deja-vu/internal/usage"
)

// briefWithholdingReach says what the rule actually keeps memory out of. The
// auto rule stops injection, the search rule empties the reader's own queries,
// and the mcp rule stops the agent asking — one sentence for all three read
// wrong for two of them (#1103).
func briefWithholdingReach(activation string) string {
	switch activation {
	case policy.ActivationSearch:
		return "search rule keeps them out of your own searches"
	case policy.ActivationMCP:
		return "mcp rule keeps them out of every agent that asks"
	default:
		return "auto rule keeps them out of every agent"
	}
}

// briefWithholdingRule names the activation whose rule withholds the whole
// store, preferring search: that is the rule this screen itself obeys, so when
// two rules withhold everything the reader is told the one that emptied what
// they are looking at (#1312). Auto comes next — the path they never see fail —
// and is also the fallback when nothing withholds everything, so the caller's
// comparison simply fails and the line stays off.
func briefWithholdingRule(withheld map[string]int, total int) string {
	for _, a := range []string{policy.ActivationSearch, policy.ActivationAuto, policy.ActivationMCP} {
		if withheld[a] == total {
			return a
		}
	}
	return policy.ActivationAuto
}

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
			printNoHistory(w, staleEmptyIndex(dir))
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

	// An index and no agent reading it is the state a fresh install lands in,
	// and nothing said so: install.sh drops a binary, `deja` builds the memory
	// and reports it, and the part that makes any of it arrive on its own is a
	// second command nobody has been told about. It sits directly under the
	// header because that is the only line a first-time reader is certain to
	// read.
	if nothingWired() {
		// 53 columns: every line of the brief fits a 60-column pane (#604),
		// and the longer wording left `--auto` wrapped alone on its own line
		// of the first screen a fresh install shows (#1411).
		fmt.Fprintf(w, "wire       %sno agent wired yet%s — `deja install --auto`\n", bold, reset)
	}

	recalls, bytes, _ := usage.TodayDemand(dir)
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
		// Three widths of the same fact, longest first: a narrow pane gives up
		// the raw total, then the served size, rather than wrapping them onto a
		// line of their own (#1588). Dropping straight from both to neither
		// loses a figure that had nine columns to spare at 60.
		line += fmt.Sprintf(" · %d recall%s served", recalls, pluralS(recalls))
		served := " (" + humanBytes(int64(bytes)) + ")"
		full := served
		if raw := usage.TodayRaw(dir); bytes > 0 && raw/int64(bytes) >= 2 {
			full = " (" + humanBytes(int64(bytes)) + " from " + humanBytes(raw) + ")"
		}
		// printableWidth, not briefWidth: a pipe reads as "do not cut", and a
		// script that redirects the brief wants the figures, not the layout.
		room := printableWidth(w)
		switch {
		case room == 0 || barColumns(line+full) <= room:
			line += full
		case barColumns(line+served) <= room:
			line += served
		}
	}
	if !quietWeek {
		fmt.Fprintln(w, line)
	}

	// The week line earns its space only when the week holds something today
	// does not. Equal session counts, equal recall counts and no déjà vu means
	// it restates the line above with weaker numbers — on a one-session store
	// that put the same number on three of the four lines (#842).
	weekEchoesToday := ov.SessionsWeek == ov.SessionsToday && weekRecalls == recalls && dejaVu == 0

	wr := weekRecalls
	if quietWeek {
		// Nothing this week. Two zero lines is the worst possible opening for
		// someone whose agent history is real but older — and the interesting
		// fact is right there: how far back the memory goes.
		fmt.Fprintf(w, "covering   %s%s → %s%s\n", bold,
			ov.Oldest.Local().Format("Jan 2 2006"), ov.Newest.Local().Format("Jan 2 2006"), reset)
	} else if !weekEchoesToday {
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

	// The top line counts what is indexed; the auto rule decides what an agent
	// actually gets. When it withholds every session the two disagree
	// completely — the screen says the memory is there and no agent will ever
	// see any of it — and the reader who set the rule has no reason to suspect
	// it, because search and the listing below still answer. doctor has the
	// number (#978); it is the fifth screen someone reaches for, not the first
	// (#1067). Partial withholding stays quiet: the counters are still broadly
	// true, and a caveat on every line is wallpaper.
	// Every activation, not just auto: a rule on `search` withholds the
	// reader's own queries, search says so on its own screen, and this one
	// stayed silent (#1103, the shape #1102 fixed on the status line).
	if withheld, total := policyWithheldCounts(dir); total > 0 && withheld[briefWithholdingRule(withheld, total)] == total {
		fmt.Fprintf(w, "withheld   %sall %d session%s%s — your trust policy's "+briefWithholdingReach(briefWithholdingRule(withheld, total))+" (`deja doctor`)\n",
			bold, total, pluralS(total), reset)
	}

	// Read the index as-is: the brief must never trigger a rebuild or let
	// indexing narration tear through its layout.
	// Filtered like every other browsing surface: `last`, search and the status
	// line all refuse a session a rule withholds, and this screen named the
	// project and the first line of it three times over (#1350, the shape #1026
	// fixed on the status line). Asked for more than three so the block still
	// fills when some are withheld.
	//
	// The search activation, while the asked-twice line below uses auto: the two
	// serve different readers. This block is what the person is looking at, and
	// browsing is what the search rule governs; that line is about what an agent
	// would be handed, which is the auto rule's business.
	recent, err := index.RecentMatching(dir, briefRecentLines*8, search.Options{})
	if err == nil {
		recent, _ = policyFilterSessionsCounted(policy.ActivationSearch, recent)
		if len(recent) > briefRecentLines {
			recent = recent[:briefRecentLines]
		}
	}
	if err == nil && len(recent) > 0 {
		label := "recent    "
		for _, s := range recent {
			title := s.Title
			if title == "" {
				title = firstUserTitle(s)
			}
			// Budgeted against what is already on the line, not against a
			// constant. Every other line on this screen has a fixed 11-column
			// prefix; this one carries the harness, the project and a date,
			// and a fixed 44-rune title on top of that overflowed 80 columns
			// in every store state — worst on an old store, where "Jun 27
			// 2025" is six columns longer than "today" and the three lines
			// came out three different lengths (#1073).
			head := fmt.Sprintf("%s [%s] %s · %s · ", label, s.Harness, s.Project, search.RelativeDate(s.Updated))
			title = trimBriefTitleTo(title, briefTitleBudget(barColumns(head)))
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
	briefPol := policy.Load()
	// The search rule, like `recent` above and every other terminal surface.
	// These lines print the reader's own session text on the reader's own
	// screen; filtering them by auto meant a policy that keeps work out of
	// casual greps printed "all N sessions are withheld from your own
	// searches" and quoted three of them underneath (#1312).
	briefAllows := func(project string) bool {
		return briefPol.Allows(policy.ActivationSearch, project)
	}
	asked, haveAsked := index.FindAskedTwice(dir, briefAllows)
	askedText := ""
	if haveAsked {
		askedText = trimBriefTitle(asked.Text)
	}

	// What the counters above cannot say: which memory kept being worth
	// recalling. "63 recalls" is a rate; a named piece of work is the thing a
	// person repeats to a colleague (#579).
	r, haveReused := findReusedMemory(dir, briefAllows)

	// What you keep asking is what keeps getting recalled, so these two land on
	// the same work more often than not — and then the screen printed one title
	// twice and made the reader diff two lines to notice (#843). The two facts
	// differ, so both are kept; the second copy of the title is not.
	sameWork := haveAsked && haveReused && sameBriefWork(r.Title, asked.Text)

	if haveAsked {
		fmt.Fprintf(w, "asked      %s%s%s\n", bold, askedText, reset)
		when := askedWhen(asked)
		if sameWork {
			when += fmt.Sprintf(" · %d× re-used recently", r.Times)
			// The span ends at the newest asking, and the memory being recalled
			// is often an older one — the answer people keep coming back to,
			// not the last time they asked. Then this date is a second fact and
			// folding it away loses it (#843).
			if last := search.RelativeDate(r.Age); last != search.RelativeDate(asked.Sessions[0].Updated) {
				when += " · last worked " + last
			}
		}
		fmt.Fprintf(w, "before     %s%s%s\n", dim, fitBriefWhen(when, printableWidth(w)), reset)
	}

	if haveReused && !sameWork {
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
	if f, ok := index.FindFriction(dir, briefAllows); ok {
		fmt.Fprintf(w, "hit        %s%s%s\n", bold, trimBriefTitle(f.Text), reset)
		fmt.Fprintf(w, "again      %s%d sessions · last %s · deja friction%s\n",
			dim, len(f.Sessions), search.RelativeDate(f.Last), reset)
	}

	// The greeting printed on a first build already ends with this exact
	// suggestion. Printing it twice on the one screen that has to be legible
	// is worse than not printing it at all.
	if q := suggestFirstQuery(dir); q != "" && !justGreeted {
		// The line is a command to copy, so it is never cut: an ellipsis inside
		// it searches for a truncated word and a literal "…". What goes first
		// is the note, and if the command alone still does not fit, the whole
		// suggestion — the screen has other lines that say more (#1588).
		room := printableWidth(w)
		command := fmt.Sprintf("try        deja %q", q)
		const note = " (from your own history)"
		switch {
		case room == 0 || barColumns(command+note) <= room:
			fmt.Fprintf(w, "try        %sdeja %q%s %s(from your own history)%s\n", bold, q, reset, dim, reset)
		case barColumns(command) <= room:
			fmt.Fprintf(w, "try        %sdeja %q%s\n", bold, q, reset)
		}
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
func trimBriefTitle(t string) string { return trimBriefTitleTo(t, briefTitleMax) }

// briefLabelColumns is the fixed prefix every line but `recent` carries: a
// name, padded to the column the values line up on.
const briefLabelColumns = 11

// briefTitleMax is the width the fixed-prefix lines (`asked`, `reused`, `hit`)
// are laid out to: an 11-column label plus 44 plus the ellipsis is 56, which
// fits a 60-column pane. Narrower than that they overflow, and widening the
// rule here would also cut `hook-context` output, which is not a terminal line
// at all — that is a separate item (#1588).
const briefTitleMax = 44

func trimBriefTitleTo(t string, max int) string {
	t = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) {
			return ' '
		}
		return r
	}, t)
	t = strings.Join(strings.Fields(t), " ")
	// Columns rather than runes: the budget is what is left of the terminal,
	// and a Chinese title is one rune and two columns per character, so a
	// 44-rune cap printed 88 columns and the line the budget exists to fit
	// ran off the edge (#1073 fixed the same overflow for Latin text).
	if termwidth.Columns(t) > max {
		return termwidth.Cut(t, max) + "…"
	}
	return t
}

// briefRecentLines is how many recent sessions the brief names.
const briefRecentLines = 3

// briefTitleBudget is how much title fits after a prefix of prefixLen columns.
//
// The width is the terminal's when it can be read, COLUMNS when the reader
// exports it, and 80 otherwise — a 60-column split pane wrapped every line of
// the first screen a new user sees, and 80 was a guess that is wrong there
// (#604). The 44 cap keeps a wide terminal looking as it always has, and the
// floor stops a deep project path from cutting titles to nothing: a line that
// overflows by a little beats one that says nothing.
func briefTitleBudget(prefixLen int) int {
	room := briefWidth() - prefixLen - 1 // the ellipsis
	if room > briefTitleMax {
		return briefTitleMax
	}
	if room < 12 {
		return 12
	}
	return room
}

// printableWidth is briefWidth for a writer that may not be a terminal at all.
// A pipe gets zero, which every caller reads as "do not cut": a script wants
// the text, not the layout (#604).
//
// COLUMNS still counts on a pipe, because bash and zsh set it without
// exporting it — a child process only sees it when someone exported it on
// purpose, which is the same override briefWidth already honours.
func printableWidth(w io.Writer) int {
	if os.Getenv("COLUMNS") == "" && !statColorOK(w) {
		return 0
	}
	return briefWidth()
}

func briefWidth() int {
	// COLUMNS first: a reader who exports it is overriding the terminal on
	// purpose, and scripts set it to pin the layout.
	if n, err := strconv.Atoi(os.Getenv("COLUMNS")); err == nil && n >= 20 && n <= 400 {
		return n
	}
	if n, ok := terminalWidth(); ok && n >= 20 && n <= 400 {
		return n
	}
	return 80
}

// sameBriefWork reports whether the reused memory and the repeated question
// name the same work.
//
// Judging that on the 44 columns the screen shows is judging the wrong text:
// two questions about the same build that end "on the arm64 runner" and "on the
// raspberry pi" are identical for the first 44 characters, and the reused one
// then vanished from the screen instead of being printed (#843). The manifest
// cuts a title at 60 characters while the question is stored whole, so neither
// side is guaranteed complete — prefix agreement over everything both sides
// kept is the strongest test available here.
func sameBriefWork(title, asked string) bool {
	t, a := briefWorkKey(title), briefWorkKey(asked)
	if t == "" || a == "" {
		return false
	}
	return strings.HasPrefix(a, t) || strings.HasPrefix(t, a)
}

func briefWorkKey(s string) string {
	s = strings.Join(strings.Fields(strings.ToLower(s)), " ")
	return strings.TrimSpace(strings.TrimSuffix(s, "…"))
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

// staleEmptyIndex reports whether the empty index is out of date — the stores
// have changed since it was written.
//
// The order anyone trying a new tool follows: install it, run `deja` to see
// what it does (nothing yet), work for a day, run it again. The first run wrote
// an empty index, and the refresh above only fires when there is no manifest at
// all, so the second run kept saying the machine had no history while twelve
// sessions sat on disk (#1313). Answering by rebuilding is not open to this
// screen — measured at 7.0s behind another index's lock, exit 1 on a read-only
// index directory, and indexing narration through the layout — so it says the
// true thing cheaply instead. UpToDate reads the manifest and stats the stores;
// it builds nothing and takes no lock, and it is only asked when the index
// holds no sessions at all. Measured at 3.4ms over 1552 files in a 950 MB
// store, which is a quarter of what this screen already spends.
func staleEmptyIndex(dir string) bool {
	if !index.HasManifest(dir) {
		return false
	}
	fresh, _ := index.UpToDate(dir, "")
	return !fresh
}

// printNoHistory is the empty state, in its two honest forms: the index built
// and found nothing, or it found nothing because it has not looked since the
// stores changed. The first names where deja looked, because the usual cause is
// that the agent stores live somewhere this machine does not have.
func printNoHistory(w io.Writer, stale bool) {
	if stale {
		fmt.Fprintln(w, "deja-vu "+version+" · history found, not indexed yet")
		fmt.Fprintln(w)
		fmt.Fprintln(w, "your agents have written since deja last looked — run `deja index`")
		fmt.Fprintln(w)
		fmt.Fprintln(w, "  deja index       read what is new")
		fmt.Fprintln(w, "  deja sources     what was looked for, and where")
		fmt.Fprintln(w, "  deja help        every command")
		return
	}
	fmt.Fprintln(w, "deja-vu "+version+" · no agent history found yet")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "deja reads the session stores your agents already write —")
	fmt.Fprintln(w, "Claude Code, Codex, opencode, Cursor, Gemini, Copilot and twelve more.")
	fmt.Fprintln(w, "Nothing was found on this machine, which usually means no agent has")
	fmt.Fprintln(w, "run here yet, or its store lives somewhere else.")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "  deja sources     what was looked for, and where")
	fmt.Fprintln(w, "  deja doctor      check the setup")
	fmt.Fprintln(w, "  deja help        every command")
}

// fitBriefWhen makes the `before` line fit by shortening the project path and
// nothing else. Cutting the end instead drops "last worked <date>" and the
// reuse count — the two facts #843 added because they are not restatements of
// the span. A project name is the only part of this line that grows without
// bound (#1588).
func fitBriefWhen(when string, room int) string {
	room -= briefLabelColumns
	if room <= 0 || barColumns(when) <= room {
		return when
	}
	const in = " in "
	start := strings.Index(when, in)
	end := strings.Index(when, " · ")
	if start < 0 || end < 0 || end <= start {
		return when
	}
	project := when[start+len(in) : end]
	keep := barColumns(project) - (barColumns(when) - room) - 1 // the ellipsis
	if keep < 6 {
		// Nothing readable would be left of the path, so the facts stay whole
		// and the line overflows — the trade the `recent` floor already makes.
		return when
	}
	return when[:start+len(in)] + termwidth.Cut(project, keep) + "…" + when[end:]
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
