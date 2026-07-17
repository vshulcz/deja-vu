package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"time"

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
	TotalSessions int            `json:"total_sessions"`
	TotalMessages int            `json:"total_messages"`
	Harnesses     []harnessStats `json:"harnesses"`
	TopProjects   []projectStats `json:"top_projects"`
	Monthly       []monthStats   `json:"monthly"`
	Sparkline     string         `json:"sparkline"`
	DateRange     dateRangeStats `json:"date_range"`
	Longest       sessionStat    `json:"longest_session"`
	BusiestDay    dayStat        `json:"busiest_day"`
	Recall        usage.Summary  `json:"recall"`
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
	for _, a := range args {
		switch a {
		case "--json":
			jsonOut = true
		default:
			return fmt.Errorf("stats: unknown flag %s", a)
		}
	}
	if err := index.Ensure(index.DefaultDir(), "", false, os.Stderr); err != nil {
		return err
	}
	ss, err := index.SearchWithRecovery(index.DefaultDir(), search.Options{All: true}, os.Stderr)
	if err != nil {
		return err
	}
	report := buildStats(ss, time.Now())
	report.Recall = usage.Totals(index.DefaultDir())
	if jsonOut {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(report)
	}
	printStats(os.Stdout, report)
	return nil
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
	out.Sparkline = sparkline(months)
	if !minT.IsZero() {
		out.DateRange.Start = minT.Format("2006-01-02")
		out.DateRange.End = maxT.Format("2006-01-02")
	}
	return out
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
	fmt.Fprintf(w, "Sessions  %s%d%s\n", bold, r.TotalSessions, reset)
	fmt.Fprintf(w, "Messages  %s%d%s\n", bold, r.TotalMessages, reset)
	fmt.Fprintf(w, "Range     %s → %s\n\n", valueOrDash(r.DateRange.Start), valueOrDash(r.DateRange.End))

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

func considerTime(minT, maxT *time.Time, t time.Time) {
	if t.IsZero() {
		return
	}
	if minT.IsZero() || t.Before(*minT) {
		*minT = t
	}
	if maxT.IsZero() || t.After(*maxT) {
		*maxT = t
	}
}

func firstMonth(t time.Time) time.Time {
	y, m, _ := t.Date()
	return time.Date(y, m, 1, 0, 0, 0, 0, t.Location())
}

func sparkline(months []monthStats) string {
	blocks := []rune("▁▂▃▄▅▆▇█")
	maxMessages := 0
	for _, m := range months {
		if m.Messages > maxMessages {
			maxMessages = m.Messages
		}
	}
	var b strings.Builder
	for _, m := range months {
		idx := 0
		if maxMessages > 0 && m.Messages > 0 {
			idx = ((m.Messages - 1) * (len(blocks) - 1) / maxMessages) + 1
		}
		b.WriteRune(blocks[idx])
	}
	return b.String()
}

func monthLabels(months []monthStats) string {
	labels := make([]string, 0, len(months))
	for _, m := range months {
		if t, err := time.Parse("2006-01", m.Month); err == nil {
			labels = append(labels, t.Format("Jan"))
		}
	}
	return strings.Join(labels, " ")
}

func scaledBar(n, maxN, width int) int {
	if n <= 0 || maxN <= 0 {
		return 0
	}
	scaled := n * width / maxN
	if scaled == 0 {
		return 1
	}
	return scaled
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

func valueOrDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

func trimRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	if n <= 1 {
		return string(r[:n])
	}
	return string(r[:n-1]) + "…"
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
