package main

import (
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/jsonout"
	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/policy"
	"github.com/vshulcz/deja-vu/internal/redact"
	"github.com/vshulcz/deja-vu/internal/search"
	"github.com/vshulcz/deja-vu/internal/stats"
)

// `deja stats --year` is the twelve months of somebody's own work with agents,
// in one screen (#578).
//
// Counts describe a store; this describes a person's year, and deja is the
// only place that material exists in one piece because it spans every harness
// they used. Two rules it has to keep:
//
//   - Every number says the arithmetic that produced it, the way `--impact`
//     does. "7% redone" is a figure that falls apart under the first question;
//     "91 of 1,229 questions were asked in more than one session" does not.
//   - It is written to be shown to other people, so every quoted line takes
//     the outbound redaction pass on the way out and the screen says what that
//     masked — the same rule `deja recap` follows, and for the same reason.
const yearWindow = 365 * 24 * time.Hour

// yearFrictionShown is how many recurring errors the screen names. Three is
// what fits under a heading without becoming a log; `deja friction` is the
// whole list.
const yearFrictionShown = 3

const yearJSONKind = "deja.stats.year"

type yearJSON struct {
	Kind     string         `json:"kind"`
	Schema   int            `json:"schema_version"`
	From     string         `json:"from"`
	To       string         `json:"to"`
	Sessions int            `json:"sessions"`
	Messages int            `json:"messages"`
	Harness  []harnessYear  `json:"harnesses"`
	Projects []projectYear  `json:"projects"`
	Work     index.YearWork `json:"work"`
	// Questions is the redone-work figure with its denominator.
	Questions     questionYear   `json:"questions"`
	Friction      []frictionYear `json:"friction"`
	BusiestDay    string         `json:"busiest_day,omitempty"`
	BusiestCount  int            `json:"busiest_day_turns,omitempty"`
	LongestTitle  string         `json:"longest_session,omitempty"`
	LongestTurns  int            `json:"longest_session_messages,omitempty"`
	Masked        map[string]int `json:"masked,omitempty"`
	OutsideWindow int            `json:"sessions_outside_window,omitempty"`
}

type harnessYear struct {
	Harness  string `json:"harness"`
	Sessions int    `json:"sessions"`
}

type projectYear struct {
	Project  string `json:"project"`
	Sessions int    `json:"sessions"`
}

type questionYear struct {
	Distinct int `json:"distinct"`
	Repeated int `json:"repeated"`
}

type frictionYear struct {
	Line     string `json:"line"`
	Sessions int    `json:"sessions"`
}

// yearReport is what the screen and the JSON both render.
type yearReport struct {
	from, to      time.Time
	sessions      int
	messages      int
	harnesses     []harnessYear
	projects      []projectYear
	work          index.YearWork
	questions     questionYear
	friction      []frictionYear
	busiestDay    string
	busiestCount  int
	longestTitle  string
	longestTurns  int
	masked        map[string]int
	outsideWindow int
}

func runStatsYear(w io.Writer, dir string, asJSON bool) error {
	now := time.Now()
	from := now.Add(-yearWindow)
	r, err := buildYearReport(dir, from, now)
	if err != nil {
		return err
	}
	if asJSON {
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		out := yearJSON{
			Kind: yearJSONKind, Schema: jsonout.Version,
			From: r.from.Format("2006-01-02"), To: r.to.Format("2006-01-02"),
			Sessions: r.sessions, Messages: r.messages, Harness: r.harnesses,
			Projects: r.projects, Work: r.work, Questions: r.questions,
			Friction: r.friction, BusiestDay: r.busiestDay, BusiestCount: r.busiestCount,
			LongestTitle: r.longestTitle, LongestTurns: r.longestTurns,
			Masked: r.masked, OutsideWindow: r.outsideWindow,
		}
		if out.Harness == nil {
			out.Harness = []harnessYear{}
		}
		if out.Projects == nil {
			out.Projects = []projectYear{}
		}
		if out.Friction == nil {
			out.Friction = []frictionYear{}
		}
		return enc.Encode(out)
	}
	printYear(w, r)
	return nil
}

func buildYearReport(dir string, from, now time.Time) (yearReport, error) {
	r := yearReport{from: from, to: now, masked: map[string]int{}}
	ss, err := index.SearchWithRecovery(dir, search.Options{All: true}, io.Discard)
	if err != nil {
		return r, err
	}
	// The same trust policy every shareable surface applies: another machine's
	// project stays off a screen written to be shown (#966).
	kept, _ := policyFilterSessionsSplit(policy.ActivationSearch, ss)
	inWindow := make([]model.Session, 0, len(kept))
	for _, s := range kept {
		when := s.Updated
		if when.IsZero() {
			when = s.Started
		}
		if when.IsZero() || when.Before(from) {
			r.outsideWindow++
			continue
		}
		inWindow = append(inWindow, s)
	}
	report := stats.Build(inWindow, now)
	r.sessions, r.messages = report.TotalSessions, report.TotalMessages
	for _, h := range report.Harnesses {
		r.harnesses = append(r.harnesses, harnessYear{Harness: h.Harness, Sessions: h.Sessions})
	}
	for _, p := range report.TopProjects {
		name := recapProjectName(p.Project)
		masked, c := redact.Outbound(name)
		addMasked(r.masked, c)
		r.projects = append(r.projects, projectYear{Project: masked, Sessions: p.Sessions})
	}
	if report.BusiestDay.Messages > 0 {
		r.busiestDay, r.busiestCount = report.BusiestDay.Date, report.BusiestDay.Messages
	}
	if report.Longest.Messages > 0 {
		title, c := redact.Outbound(report.Longest.Title)
		addMasked(r.masked, c)
		r.longestTitle, r.longestTurns = title, report.Longest.Messages
	}
	r.questions.Distinct, r.questions.Repeated = stats.QuestionSpread(inWindow)

	work, err := index.ScanYearWork(dir, from)
	if err != nil {
		return r, err
	}
	r.work = work

	// The same gate the session-start block uses: TopFriction applies the
	// ignore rule itself (it matches on paths, which a project callback cannot
	// see), so this callback is only the trust activation.
	pol := policy.Load()
	allow := func(project string) bool { return pol.Allows(policy.ActivationSearch, project) }
	for _, f := range index.TopFriction(dir, yearFrictionShown, allow) {
		line, c := redact.Outbound(f.Text)
		addMasked(r.masked, c)
		r.friction = append(r.friction, frictionYear{Line: line, Sessions: len(f.Sessions)})
	}
	return r, nil
}

func addMasked(into map[string]int, c redact.Counts) {
	for k, v := range c {
		into[k] += v
	}
}

func printYear(w io.Writer, r yearReport) {
	fmt.Fprintf(w, "your year with coding agents\n%s to %s, from the sessions on this machine\n\n",
		r.from.Format("2006-01-02"), r.to.Format("2006-01-02"))
	if r.sessions == 0 {
		fmt.Fprintln(w, "no sessions in the last twelve months — `deja index` reads what is on disk, and `deja sources` says where it looked")
		return
	}
	fmt.Fprintf(w, "  %s session%s, %s turns in them, across %s\n",
		plainCount(r.sessions), pluralS(r.sessions), plainCount(r.messages), yearAgentList(r.harnesses))
	if len(r.projects) > 0 {
		fmt.Fprintf(w, "  busiest project        %s, %s sessions\n", r.projects[0].Project, plainCount(r.projects[0].Sessions))
	}
	if r.busiestCount > 0 {
		fmt.Fprintf(w, "  busiest day            %s, %s turns\n", r.busiestDay, plainCount(r.busiestCount))
	}
	if r.longestTurns > 0 {
		fmt.Fprintf(w, "  longest session        %s turns — %s\n", plainCount(r.longestTurns), r.longestTitle)
	}

	fmt.Fprintf(w, "\nwhat the agents did\n")
	fmt.Fprintf(w, "  %s file%s opened or written  (%s records naming a file, deduplicated by path)\n",
		plainCount(r.work.Files), pluralS(r.work.Files), plainCount(r.work.Records["files"]))
	fmt.Fprintf(w, "  %s distinct command%s run    (%s command records, deduplicated by the command line)\n",
		plainCount(r.work.Commands), pluralS(r.work.Commands), plainCount(r.work.Records["command"]))
	fmt.Fprintf(w, "  %s replaced span%s kept      in %s file%s — the exact bytes an edit overwrote, which `deja restore` hands back\n",
		plainCount(r.work.Spans), pluralS(r.work.Spans), plainCount(r.work.SpanFiles), pluralS(r.work.SpanFiles))
	if r.work.Undated > 0 {
		fmt.Fprintf(w, "  %s work records carry no timestamp, so they are in none of the three figures above\n",
			plainCount(r.work.Undated))
	}

	fmt.Fprintf(w, "\nwhat came round twice\n")
	if r.questions.Distinct > 0 {
		fmt.Fprintf(w, "  %s of %s questions were asked in more than one session (%s)\n",
			plainCount(r.questions.Repeated), plainCount(r.questions.Distinct),
			yearPercent(r.questions.Repeated, r.questions.Distinct))
	}
	for _, f := range r.friction {
		fmt.Fprintf(w, "  %s session%s hit  %s\n", plainCount(f.Sessions), pluralS(f.Sessions), yearOneLine(f.Line))
	}
	if len(r.friction) == yearFrictionShown {
		fmt.Fprintln(w, "  the rest of them: `deja friction`")
	}

	if n := recapMaskedTotal(r.masked); n > 0 {
		fmt.Fprintf(w, "\nmasked for outbound use: %s — this screen is written to be shown to other people, so addresses, internal hostnames, emails and home paths are removed\n",
			recapMaskedSummary(r.masked))
	}
	if r.outsideWindow > 0 {
		fmt.Fprintf(w, "%s indexed sessions are older than this window, or carry no date at all\n", plainCount(r.outsideWindow))
	}
}

// plainCount keeps the numbers unformatted: the rest of deja prints them
// without separators, and a screen that groups its digits one way and the
// stats card another reads as two tools.
func plainCount(n int) string { return strconv.Itoa(n) }

// yearAgentList names the agents, biggest first, and stops naming them where a
// list becomes a table: "12 agents" is the honest short form.
func yearAgentList(hs []harnessYear) string {
	if len(hs) == 0 {
		return "no agents"
	}
	sorted := make([]harnessYear, len(hs))
	copy(sorted, hs)
	sort.Slice(sorted, func(i, j int) bool {
		if sorted[i].Sessions != sorted[j].Sessions {
			return sorted[i].Sessions > sorted[j].Sessions
		}
		return sorted[i].Harness < sorted[j].Harness
	})
	if len(sorted) == 1 {
		return sorted[0].Harness
	}
	named := sorted
	if len(named) > 3 {
		named = named[:3]
	}
	parts := make([]string, 0, len(named))
	for _, h := range named {
		parts = append(parts, fmt.Sprintf("%s (%s)", h.Harness, plainCount(h.Sessions)))
	}
	out := fmt.Sprintf("%d agents — %s", len(sorted), strings.Join(parts, ", "))
	if n := len(sorted) - len(named); n > 0 {
		out += fmt.Sprintf(" and %d more", n)
	}
	return out
}

// yearOneLine keeps a quoted error to one line of the screen: a stack trace
// pasted into a session would otherwise take the whole report.
func yearOneLine(s string) string {
	s = strings.ReplaceAll(strings.ReplaceAll(s, "\r", " "), "\n", " ")
	return stats.TrimRunes(strings.Join(strings.Fields(s), " "), 88)
}

func yearPercent(n, total int) string {
	if total <= 0 {
		return "0%"
	}
	return fmt.Sprintf("%.1f%%", 100*float64(n)/float64(total))
}
