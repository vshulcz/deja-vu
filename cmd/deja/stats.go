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
	"github.com/vshulcz/deja-vu/internal/search"
	"github.com/vshulcz/deja-vu/internal/stats"
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

type redactionReport struct {
	Total       int                       `json:"total"`
	ByHarness   map[string]map[string]int `json:"by_harness"`
	SidecarSize int64                     `json:"sidecar_size,omitempty"`
	Tombstones  int                       `json:"tombstones"`
}

func runStats(dir string, args []string) error {
	jsonOut := false
	impact := false
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
		case "--impact":
			impact = true
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
	if impact {
		return runStatsImpact(os.Stdout, dir, jsonOut)
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
	if err := index.Ensure(dir, "", false, progress); err != nil {
		return err
	}
	if redaction {
		return printRedactionReport(dir, jsonOut)
	}
	ss, err := index.SearchWithRecovery(dir, search.Options{All: true}, progress)
	if err != nil {
		return err
	}
	report := stats.Build(stats.Filter(ss, options), time.Now())
	sshTip := sshSyncTip(dir, ss)
	report.Recall = usage.Totals(dir)
	report.WeekRecalls, report.WeekBytes, report.WeekInjected, _ = usage.Week(dir)
	if fi, e := os.Stat(embed.Path(dir)); e == nil {
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
		path, err := writeStatsHTML(htmlPath, report, stats.Filter(ss, options))
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
	if sshTip != "" {
		fmt.Fprintln(os.Stdout, sshTip)
	}
	return nil
}

func printRedactionReport(dir string, jsonOut bool) error {
	report, err := index.RedactionReport(dir)
	if err != nil {
		return err
	}
	r := redactionReport{Total: report.Total, ByHarness: report.Rules, Tombstones: len(index.Tombstones())}
	if fi, e := os.Stat(embed.Path(dir)); e == nil {
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

func printStats(w io.Writer, r stats.Report) {
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
		fmt.Fprintf(w, "  %-18s %s %d\n", stats.TrimRunes(p.Project, 18), strings.Repeat(barGlyph, stats.ScaledBar(p.Sessions, maxProject, 18)), p.Sessions)
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
	if r.Recall.RawBytes > 0 && r.Recall.Bytes > 0 {
		ratio := r.Recall.RawBytes / int64(r.Recall.Bytes)
		if ratio >= 2 {
			fmt.Fprintf(w, "  Distilled        %s served from %s of transcripts — ~%d× less context\n", humanBytes(int64(r.Recall.Bytes)), humanBytes(r.Recall.RawBytes), ratio)
		}
	}
	fmt.Fprintf(w, "  This week        %d recalls by your agents · %s re-used (plus %d auto-injections)\n", r.WeekRecalls, humanBytes(int64(r.WeekBytes)), r.WeekInjected)
	if r.Recall.DejaVuMoments > 0 {
		fmt.Fprintf(w, "  Déjà vu          %d prompt%s your own history already answered\n", r.Recall.DejaVuMoments, pluralS(r.Recall.DejaVuMoments))
	}
	if r.AgentCredits > 0 {
		fmt.Fprintf(w, "  Credited aloud   agents said \"deja-vu recalled\" %d times (%d this week)\n", r.AgentCredits, r.WeekCredits)
	}
	if r.HandoffsIn > 0 {
		fmt.Fprintf(w, "  Handoffs         %d sessions started from a handoff\n", r.HandoffsIn)
	}
	fmt.Fprintf(w, "  Injections       %d · %d sessions · %s\n", r.Recall.Injections, r.Recall.InjectedSessions, humanBytes(int64(r.Recall.InjectedBytes)))
	fmt.Fprintf(w, "  Empty results    %.1f%%\n", r.Recall.EmptyResultRate*100)
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
