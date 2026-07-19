package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/vshulcz/deja-vu/internal/embed"
	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/search"
	"github.com/vshulcz/deja-vu/internal/usage"
)

const (
	statReset  = "\x1b[0m"
	statDim    = "\x1b[2m"
	statBold   = "\x1b[1m"
	statOrange = "\x1b[38;5;208m"
	statGreen  = "\x1b[32m"
	statBlue   = "\x1b[34m"
)

type statsReport struct {
	TotalSessions   int            `json:"total_sessions"`
	TotalMessages   int            `json:"total_messages"`
	RepeatQuestions int            `json:"repeat_questions,omitempty"`
	Harnesses       []harnessStats `json:"harnesses"`
	TopProjects     []projectStats `json:"top_projects"`
	Monthly         []monthStats   `json:"monthly"`
	Heatmap         heatmapStats   `json:"-"` // card-only presentation data; kept out of the stable --json schema
	Sparkline       string         `json:"sparkline"`
	DateRange       dateRangeStats `json:"date_range"`
	Longest         sessionStat    `json:"longest_session"`
	BusiestDay      dayStat        `json:"busiest_day"`
	Recall          usage.Summary  `json:"recall"`
	SidecarSize     int64          `json:"sidecar_size,omitempty"`
}

type redactionReport struct {
	Total       int                       `json:"total"`
	ByHarness   map[string]map[string]int `json:"by_harness"`
	SidecarSize int64                     `json:"sidecar_size,omitempty"`
	Tombstones  int                       `json:"tombstones"`
}

type harnessStats struct {
	Harness  string `json:"harness"`
	Sessions int    `json:"sessions"`
	Messages int    `json:"messages"`
}

type projectStats struct {
	Project  string `json:"project"`
	Sessions int    `json:"sessions"`
}

type monthStats struct {
	Month    string `json:"month"`
	Messages int    `json:"messages"`
}

// heatmapStats is a GitHub-style trailing-year grid: one column per week,
// seven rows (Sun–Sat). A day count of -1 means the cell falls outside the
// covered range and is not drawn.
type heatmapStats struct {
	Weeks  [][7]int    `json:"weeks"`
	Max    int         `json:"max"`
	Months []heatMonth `json:"months"`
}

type heatMonth struct {
	Col   int    `json:"col"`
	Label string `json:"label"`
}

type dateRangeStats struct {
	Start string `json:"start,omitempty"`
	End   string `json:"end,omitempty"`
}

type sessionStat struct {
	ID       string `json:"id,omitempty"`
	Harness  string `json:"harness,omitempty"`
	Project  string `json:"project,omitempty"`
	Title    string `json:"title,omitempty"`
	Messages int    `json:"messages"`
}

type dayStat struct {
	Date     string `json:"date,omitempty"`
	Messages int    `json:"messages"`
}

func runStats(args []string) error {
	jsonOut := false
	cardPath := ""
	card := false
	htmlPath := ""
	html := false
	redaction := false
	var options search.Options
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--json":
			jsonOut = true
		case "--html":
			if html {
				return fmt.Errorf("stats: --html specified twice")
			}
			html = true
			htmlPath = "deja-stats.html"
			if i+1 < len(args) && !strings.HasPrefix(args[i+1], "-") {
				htmlPath = args[i+1]
				i++
			}
		case "--redaction":
			redaction = true
		case "--card":
			if card {
				return fmt.Errorf("stats: --card specified twice")
			}
			card = true
			cardPath = "deja-stats.svg"
			if i+1 < len(args) && !strings.HasPrefix(args[i+1], "-") {
				cardPath = args[i+1]
				i++
			}
		case "--harness", "--project", "--since", "--role":
			if i+1 >= len(args) {
				return fmt.Errorf("stats: %s needs value", args[i])
			}
			v := args[i+1]
			i++
			switch args[i-1] {
			case "--harness":
				options.Harness = v
			case "--project":
				options.Project = v
			case "--role":
				options.Role = v
			case "--since":
				d, err := parseDur(v)
				if err != nil {
					return err
				}
				options.Since = d
			}
		default:
			return fmt.Errorf("stats: unknown flag %s", args[i])
		}
	}
	if (jsonOut && card) || (jsonOut && html) || (card && html) {
		return fmt.Errorf("stats: choose one output")
	}
	if redaction && card {
		return fmt.Errorf("stats: --redaction cannot combine with --card")
	}
	// A card is a shareable artifact, not a build log: keep the per-harness
	// indexing chatter off stdout/stderr and show one quiet status line instead.
	progress := io.Writer(os.Stderr)
	if cardPath != "" {
		fmt.Fprintln(os.Stderr, "deja: preparing your stats card …")
		progress = io.Discard
	}
	if err := index.Ensure(index.DefaultDir(), "", false, progress); err != nil {
		return err
	}
	if redaction {
		return printRedactionReport(jsonOut)
	}
	ss, err := index.SearchWithRecovery(index.DefaultDir(), search.Options{All: true}, progress)
	if err != nil {
		return err
	}
	report := buildStats(filterStatsSessions(ss, options), time.Now())
	report.Recall = usage.Totals(index.DefaultDir())
	if fi, e := os.Stat(embed.Path(index.DefaultDir())); e == nil {
		report.SidecarSize = fi.Size()
	}
	if cardPath != "" {
		path, err := writeStatsCard(cardPath, report)
		if err != nil {
			return err
		}
		base := filepath.Base(path)
		fmt.Fprintf(os.Stdout, "saved %s\n\nshare it — paste into a README or post:\n  ![deja](%s)\n", path, base)
		return nil
	}
	if htmlPath != "" {
		path, err := writeStatsHTML(htmlPath, report, filterStatsSessions(ss, options))
		if err != nil {
			return err
		}
		fmt.Fprintln(os.Stdout, path)
		return nil
	}
	if jsonOut {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(report)
	}
	printStats(os.Stdout, report)
	return nil
}

func printRedactionReport(jsonOut bool) error {
	stats, err := index.RedactionReport(index.DefaultDir())
	if err != nil {
		return err
	}
	r := redactionReport{Total: stats.Total, ByHarness: stats.Rules, Tombstones: len(index.Tombstones())}
	if fi, e := os.Stat(embed.Path(index.DefaultDir())); e == nil {
		r.SidecarSize = fi.Size()
	}
	if jsonOut {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(r)
	}
	fmt.Fprintf(os.Stdout, "Redactions\n  Total       %d\n  Tombstones  %d\n", r.Total, r.Tombstones)
	if r.SidecarSize > 0 {
		fmt.Fprintf(os.Stdout, "  Sidecar     %s\n", humanBytes(r.SidecarSize))
	}
	harnesses := make([]string, 0, len(r.ByHarness))
	for h := range r.ByHarness {
		harnesses = append(harnesses, h)
	}
	sort.Strings(harnesses)
	for _, h := range harnesses {
		fmt.Fprintf(os.Stdout, "  %s\n", h)
		rules := make([]string, 0, len(r.ByHarness[h]))
		for rule := range r.ByHarness[h] {
			rules = append(rules, rule)
		}
		sort.Strings(rules)
		for _, rule := range rules {
			fmt.Fprintf(os.Stdout, "    %-20s %d\n", rule, r.ByHarness[h][rule])
		}
	}
	return nil
}

func filterStatsSessions(ss []model.Session, o search.Options) []model.Session {
	if o.Harness == "" && o.Project == "" && o.Since <= 0 && o.Role == "" {
		return ss
	}
	cut := time.Time{}
	if o.Since > 0 {
		cut = time.Now().Add(-o.Since)
	}
	out := make([]model.Session, 0, len(ss))
	for _, s := range ss {
		if o.Harness != "" && s.Harness != o.Harness {
			continue
		}
		if o.Project != "" && !strings.Contains(strings.ToLower(s.Project), strings.ToLower(o.Project)) {
			continue
		}
		if !cut.IsZero() && s.Updated.Before(cut) {
			continue
		}
		if o.Role != "" {
			cp := s
			cp.Messages = nil
			for _, m := range s.Messages {
				if m.Role == o.Role {
					cp.Messages = append(cp.Messages, m)
				}
			}
			if len(cp.Messages) == 0 {
				continue
			}
			s = cp
		}
		out = append(out, s)
	}
	return out
}

func buildStats(ss []model.Session, now time.Time) statsReport {
	byHarness := map[string]*harnessStats{}
	byProject := map[string]int{}
	byDay := map[string]int{}
	monthStart := firstMonth(now).AddDate(0, -11, 0)
	months := make([]monthStats, 12)
	monthIndex := map[string]int{}
	for i := range months {
		m := monthStart.AddDate(0, i, 0)
		label := m.Format("2006-01")
		months[i] = monthStats{Month: label}
		monthIndex[label] = i
	}

	var out statsReport
	var minT, maxT time.Time
	for _, s := range ss {
		out.TotalSessions++
		msgCount := len(s.Messages)
		out.TotalMessages += msgCount
		hs := byHarness[s.Harness]
		if hs == nil {
			hs = &harnessStats{Harness: s.Harness}
			byHarness[s.Harness] = hs
		}
		hs.Sessions++
		hs.Messages += msgCount
		project := s.Project
		if project == "" {
			project = "-"
		}
		byProject[project]++
		if msgCount > out.Longest.Messages {
			out.Longest = sessionStat{ID: s.ID, Harness: s.Harness, Project: project, Title: statTitle(s), Messages: msgCount}
		}
		considerTime(&minT, &maxT, s.Started)
		considerTime(&minT, &maxT, s.Updated)
		for _, m := range s.Messages {
			t := m.Time
			if t.IsZero() {
				t = s.Updated
			}
			considerTime(&minT, &maxT, t)
			if !t.IsZero() {
				day := t.Format("2006-01-02")
				byDay[day]++
				if i, ok := monthIndex[firstMonth(t).Format("2006-01")]; ok {
					months[i].Messages++
				}
			}
		}
	}
	out.Harnesses = make([]harnessStats, 0, len(byHarness))
	for _, hs := range byHarness {
		out.Harnesses = append(out.Harnesses, *hs)
	}
	sort.Slice(out.Harnesses, func(i, j int) bool { return out.Harnesses[i].Harness < out.Harnesses[j].Harness })
	for project, count := range byProject {
		out.TopProjects = append(out.TopProjects, projectStats{Project: project, Sessions: count})
	}
	sort.Slice(out.TopProjects, func(i, j int) bool {
		if out.TopProjects[i].Sessions == out.TopProjects[j].Sessions {
			return out.TopProjects[i].Project < out.TopProjects[j].Project
		}
		return out.TopProjects[i].Sessions > out.TopProjects[j].Sessions
	})
	if len(out.TopProjects) > 5 {
		out.TopProjects = out.TopProjects[:5]
	}
	for day, count := range byDay {
		if count > out.BusiestDay.Messages || (count == out.BusiestDay.Messages && (out.BusiestDay.Date == "" || day < out.BusiestDay.Date)) {
			out.BusiestDay = dayStat{Date: day, Messages: count}
		}
	}
	out.Monthly = months
	out.Heatmap = buildHeatmap(byDay, now)
	out.Sparkline = sparkline(months)
	if !minT.IsZero() {
		out.DateRange.Start = minT.Format("2006-01-02")
		out.DateRange.End = maxT.Format("2006-01-02")
	}
	out.RepeatQuestions = repeatQuestions(ss)
	return out
}

// buildHeatmap turns per-day message counts into a Sunday-aligned trailing-year
// grid (~53 week columns) with month ticks, for the shareable stats card.
func buildHeatmap(byDay map[string]int, now time.Time) heatmapStats {
	end := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	start := end.AddDate(0, 0, -370)
	for start.Weekday() != time.Sunday {
		start = start.AddDate(0, 0, -1)
	}
	var hm heatmapStats
	lastMonth := ""
	for cur := start; !cur.After(end); {
		var week [7]int
		colDate := cur
		for d := 0; d < 7; d++ {
			if cur.After(end) {
				week[d] = -1
			} else {
				c := byDay[cur.Format("2006-01-02")]
				week[d] = c
				if c > hm.Max {
					hm.Max = c
				}
			}
			cur = cur.AddDate(0, 0, 1)
		}
		if mon := colDate.Format("Jan"); mon != lastMonth {
			hm.Months = append(hm.Months, heatMonth{Col: len(hm.Weeks), Label: mon})
			lastMonth = mon
		}
		hm.Weeks = append(hm.Weeks, week)
	}
	return hm
}

func printStats(w io.Writer, r statsReport) {
	color := statColorOK(w)
	barGlyph := "#"
	if color {
		barGlyph = "█"
	}
	faint, bold, reset := "", "", ""
	if color {
		faint, bold, reset = statDim, statBold, statReset
	}
	fmt.Fprintf(w, "%sdeja stats%s\n", bold, reset)
	fmt.Fprintf(w, "%sindexed agent work, wrapped for sharing%s\n\n", faint, reset)
	if headline := statsHeadline(r); headline != "" {
		fmt.Fprintf(w, "%s%s%s\n\n", bold, headline, reset)
	}
	fmt.Fprintf(w, "Sessions  %s%d%s\n", bold, r.TotalSessions, reset)
	fmt.Fprintf(w, "Messages  %s%d%s\n", bold, r.TotalMessages, reset)
	fmt.Fprintf(w, "Range     %s → %s\n\n", valueOrDash(r.DateRange.Start), valueOrDash(r.DateRange.End))
	if r.SidecarSize > 0 {
		fmt.Fprintf(w, "Semantic  sidecar %s\n\n", humanBytes(r.SidecarSize))
	}

	fmt.Fprintf(w, "%sBy harness%s\n", bold, reset)
	for _, h := range r.Harnesses {
		tag := statHarnessTag(h.Harness, color)
		pad := 14 - len(h.Harness) - 2 // visible width: [name]
		if pad < 1 {
			pad = 1
		}
		fmt.Fprintf(w, "  %s%s %4d sessions  %5d messages\n", tag, strings.Repeat(" ", pad), h.Sessions, h.Messages)
	}
	fmt.Fprintln(w)

	fmt.Fprintf(w, "%sTop projects%s\n", bold, reset)
	maxProject := 0
	for _, p := range r.TopProjects {
		if p.Sessions > maxProject {
			maxProject = p.Sessions
		}
	}
	for _, p := range r.TopProjects {
		fmt.Fprintf(w, "  %-18s %s %d\n", trimRunes(p.Project, 18), strings.Repeat(barGlyph, scaledBar(p.Sessions, maxProject, 18)), p.Sessions)
	}
	fmt.Fprintln(w)

	fmt.Fprintf(w, "%sLast 12 months%s\n", bold, reset)
	fmt.Fprintf(w, "  %s  %s\n", r.Sparkline, monthLabels(r.Monthly))
	fmt.Fprintln(w)

	fmt.Fprintf(w, "%sHighlights%s\n", bold, reset)
	fmt.Fprintf(w, "  Longest session  %d messages · %s · %s\n", r.Longest.Messages, statHarnessTag(r.Longest.Harness, color), valueOrDash(r.Longest.Title))
	fmt.Fprintf(w, "  Busiest day      %s · %d messages\n", valueOrDash(r.BusiestDay.Date), r.BusiestDay.Messages)
	fmt.Fprintln(w)
	fmt.Fprintf(w, "%sRecall%s\n", bold, reset)
	fmt.Fprintf(w, "  Recalls served   %d\n", r.Recall.Recalls)
	fmt.Fprintf(w, "  Injections       %d · %d sessions · %s\n", r.Recall.Injections, r.Recall.InjectedSessions, humanBytes(int64(r.Recall.InjectedBytes)))
	fmt.Fprintf(w, "  Empty results    %.1f%%\n", r.Recall.EmptyResultRate*100)
}

func statTitle(s model.Session) string {
	if s.Title != "" && !statNoise(s.Title) {
		return s.Title
	}
	for _, m := range s.Messages {
		if m.Role == "user" && !statNoise(m.Text) {
			return trimRunes(strings.Join(strings.Fields(m.Text), " "), 60)
		}
	}
	return ""
}

func statNoise(s string) bool {
	t := strings.TrimSpace(s)
	for _, p := range []string{"<local-command", "<command-", "<task-notification", "<teammate-message", "<bash-", "Caveat:", "<system-reminder"} {
		if strings.HasPrefix(t, p) {
			return true
		}
	}
	return false
}

func statColorOK(w io.Writer) bool {
	if os.Getenv("NO_COLOR") != "" {
		return false
	}
	f, ok := w.(*os.File)
	if !ok {
		return false
	}
	st, err := f.Stat()
	if err != nil {
		return false
	}
	return st.Mode()&os.ModeCharDevice != 0
}

func statHarnessTag(h string, color bool) string {
	tag := "[" + h + "]"
	if !color {
		return tag
	}
	switch h {
	case "claude":
		return statOrange + tag + statReset + statBold
	case "codex":
		return statGreen + tag + statReset + statBold
	case "opencode":
		return statBlue + tag + statReset + statBold
	case "cursor":
		return "\x1b[36m" + tag + statReset + statBold
	case "gemini":
		return "\x1b[35m" + tag + statReset + statBold
	case "aider":
		return "\x1b[33m" + tag + statReset + statBold
	case "antigravity":
		return "\x1b[94m" + tag + statReset + statBold
	}
	return tag
}
