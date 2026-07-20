// Package stats computes the corpus statistics behind `deja stats`:
// per-harness/project counts, activity heatmap, and repeat-question metric.
package stats

import (
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/search"
	"github.com/vshulcz/deja-vu/internal/usage"
)

type Report struct {
	TotalSessions   int            `json:"total_sessions"`
	TotalMessages   int            `json:"total_messages"`
	RepeatQuestions int            `json:"repeat_questions,omitempty"`
	Harnesses       []HarnessStats `json:"harnesses"`
	TopProjects     []ProjectStats `json:"top_projects"`
	Monthly         []MonthStats   `json:"monthly"`
	Heatmap         HeatmapStats   `json:"-"` // card-only presentation data; kept out of the stable --json schema
	Sparkline       string         `json:"sparkline"`
	DateRange       DateRangeStats `json:"date_range"`
	Longest         SessionStat    `json:"longest_session"`
	BusiestDay      DayStat        `json:"busiest_day"`
	Recall          usage.Summary  `json:"recall"`
	WeekRecalls     int            `json:"week_recalls"`
	WeekBytes       int            `json:"week_bytes"`
	WeekInjected    int            `json:"week_injected"`
	HandoffsIn      int            `json:"handoffs_received"`
	AgentCredits    int            `json:"agent_credits"`
	WeekCredits     int            `json:"week_agent_credits"`
	SidecarSize     int64          `json:"sidecar_size,omitempty"`
}

type HarnessStats struct {
	Harness  string `json:"harness"`
	Sessions int    `json:"sessions"`
	Messages int    `json:"messages"`
}

type ProjectStats struct {
	Project  string `json:"project"`
	Sessions int    `json:"sessions"`
}

type MonthStats struct {
	Month    string `json:"month"`
	Messages int    `json:"messages"`
}

// HeatmapStats is a GitHub-style trailing-year grid: one column per week,
// seven rows (Sun–Sat). A day count of -1 means the cell falls outside the
// covered range and is not drawn.
type HeatmapStats struct {
	Weeks  [][7]int    `json:"weeks"`
	Max    int         `json:"max"`
	Months []HeatMonth `json:"months"`
}

type HeatMonth struct {
	Col   int    `json:"col"`
	Label string `json:"label"`
}

type DateRangeStats struct {
	Start string `json:"start,omitempty"`
	End   string `json:"end,omitempty"`
}

type SessionStat struct {
	ID       string `json:"id,omitempty"`
	Harness  string `json:"harness,omitempty"`
	Project  string `json:"project,omitempty"`
	Title    string `json:"title,omitempty"`
	Messages int    `json:"messages"`
}

type DayStat struct {
	Date     string `json:"date,omitempty"`
	Messages int    `json:"messages"`
}

func Filter(ss []model.Session, o search.Options) []model.Session {
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

func Build(ss []model.Session, now time.Time) Report {
	byHarness := map[string]*HarnessStats{}
	byProject := map[string]int{}
	byDay := map[string]int{}
	monthStart := firstMonth(now).AddDate(0, -11, 0)
	months := make([]MonthStats, 12)
	monthIndex := map[string]int{}
	for i := range months {
		m := monthStart.AddDate(0, i, 0)
		label := m.Format("2006-01")
		months[i] = MonthStats{Month: label}
		monthIndex[label] = i
	}

	var out Report
	var minT, maxT time.Time
	for _, s := range ss {
		out.TotalSessions++
		msgCount := len(s.Messages)
		out.TotalMessages += msgCount
		hs := byHarness[s.Harness]
		if hs == nil {
			hs = &HarnessStats{Harness: s.Harness}
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
			out.Longest = SessionStat{ID: s.ID, Harness: s.Harness, Project: project, Title: Title(s), Messages: msgCount}
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
	out.Harnesses = make([]HarnessStats, 0, len(byHarness))
	for _, hs := range byHarness {
		out.Harnesses = append(out.Harnesses, *hs)
	}
	sort.Slice(out.Harnesses, func(i, j int) bool { return out.Harnesses[i].Harness < out.Harnesses[j].Harness })
	for project, count := range byProject {
		out.TopProjects = append(out.TopProjects, ProjectStats{Project: project, Sessions: count})
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
			out.BusiestDay = DayStat{Date: day, Messages: count}
		}
	}
	out.Monthly = months
	out.Heatmap = buildHeatmap(byDay, now)
	out.Sparkline = sparkline(months)
	if !minT.IsZero() {
		out.DateRange.Start = minT.Format("2006-01-02")
		out.DateRange.End = maxT.Format("2006-01-02")
	}
	out.RepeatQuestions = RepeatQuestions(ss)
	weekCut := now.Add(-7 * 24 * time.Hour)
	for _, s := range ss {
		for _, msg := range s.Messages {
			// The attribution loop: agents saying "deja-vu recalled" end up in
			// the very transcripts deja indexes, so the next pass can count how
			// often memory was credited out loud — a measured magic metric with
			// zero telemetry.
			if msg.Role == "assistant" && strings.Contains(msg.Text, "deja-vu recalled") {
				out.AgentCredits++
				if !msg.Time.IsZero() && msg.Time.After(weekCut) {
					out.WeekCredits++
				}
			}
		}
		for _, msg := range s.Messages {
			if msg.Role != "user" {
				continue
			}
			if strings.Contains(msg.Text, "picking up work handed off from a") {
				out.HandoffsIn++
			}
			break // only the opening user turn marks a handoff-seeded session
		}
	}
	return out
}

// buildHeatmap turns per-day message counts into a Sunday-aligned trailing-year
// grid (~53 week columns) with month ticks, for the shareable stats card.
func buildHeatmap(byDay map[string]int, now time.Time) HeatmapStats {
	end := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	start := end.AddDate(0, 0, -370)
	for start.Weekday() != time.Sunday {
		start = start.AddDate(0, 0, -1)
	}
	var hm HeatmapStats
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
			hm.Months = append(hm.Months, HeatMonth{Col: len(hm.Weeks), Label: mon})
			lastMonth = mon
		}
		hm.Weeks = append(hm.Weeks, week)
	}
	return hm
}

func Title(s model.Session) string {
	if s.Title != "" && !Noise(s.Title) {
		return s.Title
	}
	for _, m := range s.Messages {
		if m.Role == "user" && !Noise(m.Text) {
			return TrimRunes(strings.Join(strings.Fields(m.Text), " "), 60)
		}
	}
	return ""
}

func Noise(s string) bool {
	t := strings.TrimSpace(s)
	for _, p := range []string{"<local-command", "<command-", "<task-notification", "<teammate-message", "<bash-", "Caveat:", "<system-reminder"} {
		if strings.HasPrefix(t, p) {
			return true
		}
	}
	return false
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

func sparkline(months []MonthStats) string {
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

func ScaledBar(n, maxN, width int) int {
	if n <= 0 || maxN <= 0 {
		return 0
	}
	scaled := n * width / maxN
	if scaled == 0 {
		return 1
	}
	return scaled
}

func TrimRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	if n <= 1 {
		return string(r[:n])
	}
	return string(r[:n-1]) + "…"
}

// RepeatQuestions is a corpus proxy because the usage sidecar does not store query text.
func RepeatQuestions(ss []model.Session) int {
	// Exact stem match only: questionStemFor already folds case and
	// punctuation, and a pairwise similarity pass is quadratic in corpora
	// with tens of thousands of user messages.
	counts := map[string]int{}
	for _, s := range ss {
		seen := map[string]bool{}
		for _, m := range s.Messages {
			if m.Role != "user" {
				continue
			}
			stem := questionStemFor(m.Text)
			// Short acknowledgements ("ok", "continue") repeat across every
			// session; only substantial messages count as questions.
			if stem == "" || len(strings.Fields(stem)) < 4 || seen[stem] {
				continue
			}
			seen[stem] = true
			counts[stem]++
		}
	}
	count := 0
	for _, n := range counts {
		if n > 1 {
			count++
		}
	}
	return count
}

func questionStemFor(text string) string {
	if Noise(text) {
		return ""
	}
	var b strings.Builder
	for _, r := range strings.ToLower(text) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' || r == '-' {
			b.WriteRune(r)
		} else {
			b.WriteByte(' ')
		}
	}
	return strings.Join(strings.Fields(b.String()), " ")
}
