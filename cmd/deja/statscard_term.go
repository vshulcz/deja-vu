package main

import (
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/vshulcz/deja-vu/internal/mark"
	"github.com/vshulcz/deja-vu/internal/search"
	"github.com/vshulcz/deja-vu/internal/stats"
)

// The card, drawn where the user already is.
//
// It was an SVG first, which means the shareable summary of someone's own work
// arrives as a file they have to go and open. The terminal is the surface the
// rest of deja lives on, so that is where this belongs; the SVG stays for the
// places a terminal cannot go, like a profile README.
//
// It is framed and fixed-width on purpose. Unframed output is a log; a border
// and a footer are what make a screenshot read as one object that came from
// somewhere, which is the whole point of a card.

// cardInner is the width inside the border. Fifty-six holds a year of weeks
// with room to breathe, and fixing it keeps the layout still: sized to the
// data, the card jumped between a third of the width and all of it depending on
// how much history someone had.
const cardInner = 56

// The heatmap ramp: four steps in the coat's own hue, because the terminal has
// no opacity and fading a colour toward the background by hand is what makes a
// grid look muddy. Empty days take a grey just above the background — visible
// as structure, never as activity.
//
// The steps have to be far apart. The first version ran 60, 61, 103, 146 with
// empty at 236, and 60 against 236 is two darks: the grid rendered as one flat
// field and none of the four steps could be told from another.
var heatRamp = [4]int{60, 103, 146, 189}

const (
	// Amber is the accent deja keeps for the one thing worth recognising. A
	// card with none of it is a flat wash of coat blue, and a card with it
	// everywhere is a fairground.
	cardAccent = mark.Accent

	heatEmpty  = 235
	cardDim    = 244
	cardFaint  = 240
	cardBright = 231
	cardRule   = 238
)

func termFG(n int) string { return "\x1b[38;5;" + strconv.Itoa(n) + "m" }

func paint(colour int, s string) string { return termFG(colour) + s + logoReset }

func termHeat(count, max int) int {
	if count <= 0 {
		return heatEmpty
	}
	ratio := 1.0
	if max > 0 {
		ratio = float64(count) / float64(max)
	}
	switch {
	case ratio <= 0.25:
		return heatRamp[0]
	case ratio <= 0.5:
		return heatRamp[1]
	case ratio <= 0.75:
		return heatRamp[2]
	default:
		return heatRamp[3]
	}
}

// cardMood picks what the cat is doing about this report. The moods exist for
// the banner already and the card was drawing "ready" unconditionally, which is
// a mascot with nothing to say.
func cardMood(r stats.Report) catMood {
	switch {
	case r.TotalSessions == 0:
		return moodAsleep
	case r.WeekRecalls > 0 && r.Recall.Recalls > 0 && r.WeekRecalls*3 > r.Recall.Recalls:
		// More than a third of every recall deja has ever served landed in the
		// last seven days.
		return moodSurprised
	default:
		return moodReady
	}
}

// streakDays counts back from the most recent week for consecutive days with
// anything in them. It is the one figure on the card that moves every day,
// which is the only real reason to run it again.
func streakDays(hm stats.HeatmapStats) int {
	var days []int
	for _, week := range hm.Weeks {
		for d := 0; d < 7; d++ {
			days = append(days, week[d])
		}
	}
	// Trailing -1 cells are days the year window has not reached yet.
	for len(days) > 0 && days[len(days)-1] < 0 {
		days = days[:len(days)-1]
	}
	streak := 0
	for i := len(days) - 1; i >= 0 && days[i] > 0; i-- {
		streak++
	}
	return streak
}

// statsCardLines returns the finished card, border included. Lines rather than
// output so a test can read what it drew without a terminal.
func statsCardLines(r stats.Report) []string {
	var body []string
	add := func(lines ...string) { body = append(body, lines...) }

	dateSpanText = dateSpan(r)
	add(headBlock(r)...)
	add("")
	activity := "ACTIVITY"
	if n := streakDays(r.Heatmap); n > 1 {
		activity += "  ·  " + formatStatNumber(n) + " day streak"
	}
	add(sectionRule(activity))
	add(heatLines(r.Heatmap)...)
	if agents := agentBlock(r); len(agents) > 0 {
		add("")
		add(sectionRule("WHERE IT CAME FROM"))
		add(agents...)
	}
	if line := longestLine(r); line != "" {
		add("")
		add(paint(cardFaint, "THE LONGEST ONE"))
		add(line)
	}
	return frame(body, footer())
}

// headBlock is the mark with the identity, the one sentence, and the figures
// beside it. Eleven lines of animal next to three lines of text left a blank
// band down the right — the same hole the demo had — so the figures live here
// rather than under it.
func headBlock(r stats.Report) []string {
	art := renderCat(cardMood(r))
	value, caption := heroStat(r)
	lines := wrapTo(caption, cardInner-26)

	right := []string{
		paint(cardBright, "deja-vu") + paint(cardFaint, "  ·  ") + paint(cardDim, "agent history"),
		"",
	}
	right = append(right, bigNumber(value, cardAccent, cardInner-26)...)
	right = append(right,
		paint(cardDim, visibleText(lines[0])),
		paint(cardDim, visibleText(lines[1])),
		"",
		figures(r),
		figureLabels(r),
	)

	var out []string
	for i, line := range art {
		text := ""
		if i < len(right) {
			text = right[i]
		}
		out = append(out, strings.TrimRight(line+"  "+text, " "))
	}
	return out
}

// wrapTo folds the punchline to two lines. The first version cut it at the
// width instead, which produced "deja handed your agents" — not a shorter
// sentence but a different one, and the sentence is the part worth reading.
func wrapTo(s string, width int) [2]string {
	if visibleLen(s) <= width {
		return [2]string{paint(cardBright, s), ""}
	}
	cut := strings.LastIndex(s[:width], " ")
	if cut <= 0 {
		cut = width
	}
	head, tail := s[:cut], strings.TrimSpace(s[cut:])
	// If the tail overflows, take a word off the head first: the sentence
	// ending is what carries the meaning, and losing it silently is what the
	// truncating version did.
	for visibleLen(tail) > width {
		c := strings.LastIndex(head, " ")
		if c <= 0 {
			break
		}
		head, tail = head[:c], strings.TrimSpace(head[c:]+" "+tail)
	}
	if visibleLen(tail) > width {
		if c := strings.LastIndex(tail[:width], " "); c > 0 {
			tail = tail[:c] + "…"
		}
	}
	return [2]string{paint(cardBright, head), paint(cardBright, tail)}
}

// heroStat is the one figure the card is about, and the sentence that says what
// it counts. Splitting them is what buys a hierarchy: the number takes the
// accent and a line to itself, the sentence becomes its caption.
//
// cardPunchline keeps the whole sentence for the SVG, where type sizes do the
// same job.
func heroStat(r stats.Report) (string, string) {
	switch {
	case r.WeekRecalls > 0:
		return formatStatNumber(r.WeekRecalls), "recalls handed to your agents this week"
	case r.RepeatQuestions > 0:
		return formatStatNumber(r.RepeatQuestions), "questions you asked more than once"
	case r.Recall.Recalls+r.Recall.Injections > 0:
		handed := r.Recall.Recalls + r.Recall.Injections
		return formatStatNumber(handed), "recalls handed to your agents"
	case r.TotalSessions > 0:
		return formatStatNumber(r.TotalSessions), "sessions of agent history, all searchable"
	default:
		return "", "nothing indexed yet — run deja index"
	}
}

func visibleText(s string) string { return ansiRE.ReplaceAllString(s, "") }

func dateSpan(r stats.Report) string {
	start, end := valueOrDash(r.DateRange.Start), valueOrDash(r.DateRange.End)
	if start == "-" && end == "-" {
		return ""
	}
	return start + "  →  " + end
}

func cardCells(r stats.Report) []struct{ value, label string } {
	// The hero falls back to the session count when nothing has been recalled
	// yet, and then the row underneath repeated it — the same number twice,
	// once as the headline and once as its own supporting figure.
	hero, _ := heroStat(r)
	cells := []struct{ value, label string }{
		{formatStatNumber(r.TotalSessions), "sessions"},
		{formatStatNumber(r.TotalMessages), "messages"},
		{strconv.Itoa(len(r.Harnesses)), "agents"},
	}
	out := cells[:0]
	for _, c := range cells {
		if c.value != hero {
			out = append(out, c)
		}
	}
	return out
}

func cellWidth(value, label string) int {
	if visibleLen(label) > visibleLen(value) {
		return visibleLen(label)
	}
	return visibleLen(value)
}

func figures(r stats.Report) string {
	var parts []string
	for _, c := range cardCells(r) {
		parts = append(parts, paint(cardBright, pad(c.value, cellWidth(c.value, c.label))))
	}
	return strings.Join(parts, "  ")
}

func figureLabels(r stats.Report) string {
	var parts []string
	for _, c := range cardCells(r) {
		parts = append(parts, paint(cardFaint, pad(c.label, cellWidth(c.value, c.label))))
	}
	return strings.Join(parts, "  ")
}

func pad(s string, w int) string {
	if n := visibleLen(s); n < w {
		return s + strings.Repeat(" ", w-n)
	}
	return s
}

func leftPad(s string, w int) string {
	if n := visibleLen(s); n < w {
		return strings.Repeat(" ", w-n) + s
	}
	return s
}

// sectionRule is a heading with a hairline running out to the card's edge, so
// the eye finds the blocks without either of them shouting.
func sectionRule(title string) string {
	rule := cardInner - visibleLen(title) - 1
	if rule < 0 {
		rule = 0
	}
	return paint(cardDim, title) + " " + paint(cardRule, strings.Repeat("─", rule))
}

// heatLines draws the year as four rows of half blocks: one cell carries two
// days, the upper one as the foreground and the lower one as the background.
//
// Seven rows of one cell per day was the obvious shape and the wrong one. It
// wanted a small-square glyph to leave gaps between cells, and the width of ▪
// varies by font — in something people screenshot, a character that is narrow
// in one terminal and full-width in another breaks the layout with nothing to
// warn you. A full block is the one shape every monospace font agrees on.
func heatLines(hm stats.HeatmapStats) []string {
	if len(hm.Weeks) == 0 {
		return []string{paint(cardFaint, "nothing indexed yet — run deja index")}
	}
	// Always the last twenty-eight weeks, ending today. The earlier version
	// trimmed the empty run at the front because it drew as one grey slab —
	// but that was the missing gaps, not the empty days. With a column of air
	// after each week an empty cell reads as a quiet day, the way it does in
	// any contribution grid, and a fixed window keeps the card the same shape
	// for everyone.
	const cell = 2
	weeks, first := hm.Weeks, 0
	if room := cardInner / cell; len(weeks) > room {
		first = len(weeks) - room
	}
	weeks = weeks[first:]

	// A year is 53 weeks and the card is 56 columns, so even a full history
	// leaves a remainder. Pad on the left: the right edge is today, which is
	// the end worth anchoring, and the gap lands before the history began.
	lead := cardInner - len(weeks)*cell
	if lead < 0 {
		lead = 0
	}

	var grid []string
	// Seven days is odd, so the last row carries one day over an empty one.
	for d := 0; d < 7; d += 2 {
		var b strings.Builder
		// Plain spaces rather than empty cells: a drawn grey slab standing
		// for the time before someone's history began is the same dead
		// rectangle this trimming was meant to remove.
		if lead > 0 {
			b.WriteString(strings.Repeat(" ", lead))
		}
		for _, week := range weeks {
			top := termHeat(week[d], hm.Max)
			bottom := heatEmpty
			if d+1 < 7 {
				bottom = termHeat(week[d+1], hm.Max)
			}
			b.WriteString(fgColour(top) + bgColour(bottom) + "▀" + logoReset + " ")
		}
		grid = append(grid, b.String())
	}

	months := make([]byte, cardInner+8)
	for i := range months {
		months[i] = ' '
	}
	for _, mt := range hm.Months {
		col := lead + (mt.Col-first)*cell
		if col >= 0 && col+len(mt.Label) < len(months) {
			copy(months[col:], mt.Label)
		}
	}
	// The labels go under the grid: above it they sat between the heading and
	// the data and read as part of the heading.
	return append(grid, paint(cardFaint, strings.TrimRight(string(months), " ")))
}

// agentBlock lists where the history came from, longest first. The bar is drawn
// against the largest entry rather than the total: one agent usually holds most
// of it, and shares of a whole would print every other one as nothing.
func agentBlock(r stats.Report) []string {
	if len(r.Harnesses) == 0 {
		return nil
	}
	ranked := append([]stats.HarnessStats(nil), r.Harnesses...)
	for i := 1; i < len(ranked); i++ {
		for j := i; j > 0 && ranked[j].Sessions > ranked[j-1].Sessions; j-- {
			ranked[j], ranked[j-1] = ranked[j-1], ranked[j]
		}
	}
	// Six rows where five carry a single cell of bar is one fact stretched
	// down the card. Three, and a count of what is left.
	rest := 0
	if len(ranked) > 3 {
		for _, h := range ranked[3:] {
			rest += h.Sessions
		}
		ranked = ranked[:3:3]
	}

	name, count := 0, 0
	for _, h := range ranked {
		if len(h.Harness) > name {
			name = len(h.Harness)
		}
		if n := len(formatStatNumber(h.Sessions)); n > count {
			count = n
		}
	}
	bar := cardInner - name - count - 3
	max := ranked[0].Sessions

	var out []string
	for _, h := range ranked {
		width := 0
		if max > 0 {
			width = h.Sessions * bar / max
		}
		if width < 1 && h.Sessions > 0 {
			width = 1
		}
		// The unfilled part is drawn rather than left blank: without it the
		// rows have no common length, so a short bar reads as a short row and
		// not as a small share.
		out = append(out, fmt.Sprintf("%s %s%s %s",
			paint(cardDim, pad(h.Harness, name)),
			paint(mark.Coat, strings.Repeat("▄", width)),
			paint(heatEmpty, strings.Repeat("▄", bar-width)),
			paint(cardBright, leftPad(formatStatNumber(h.Sessions), count))))
	}
	if rest > 0 {
		out = append(out, paint(cardFaint, fmt.Sprintf("and %s more across the rest",
			formatStatNumber(rest))))
	}
	return out
}

// dateSpanText is set by the card before the footer is drawn. A package-level
// value rather than a parameter because footer() is called from frame(), which
// has no report.
var dateSpanText string

// longestLine is the one piece of the card that is not a count: the title of
// the longest session, which is a sentence someone wrote on this machine. It is
// what makes the card theirs rather than a shape two people with the same
// totals would both get.
//
// The title comes from the index, so it is already redacted; SafeLine is what
// stops a control character in it from moving the cursor out of the card.
func longestLine(r stats.Report) string {
	title := strings.TrimSpace(r.Longest.Title)
	if title == "" || r.Longest.Messages == 0 {
		return ""
	}
	count := formatStatNumber(r.Longest.Messages) + " messages"
	room := cardInner - len(count) - 2
	title = search.SafeLine(title)
	// Runes, not bytes. This is the one string on the card that comes from a
	// user, so it is the one that is not ASCII, and cutting a multi-byte
	// character in half prints a replacement box in the middle of their own
	// words.
	if runes := []rune(title); len(runes) > room {
		cut := room - 1
		for i := cut; i > 0; i-- {
			if runes[i] == ' ' {
				cut = i
				break
			}
		}
		title = string(runes[:cut]) + "…"
	}
	gap := cardInner - visibleLen(title) - visibleLen(count)
	if gap < 1 {
		gap = 1
	}
	return paint(cardDim, title) + strings.Repeat(" ", gap) + paint(cardFaint, count)
}

// footer is what makes a screenshot say where it came from. Dim enough not to
// compete with the figures, and the last thing inside the border.
func footer() string {
	const right = "vshulcz.github.io/deja-vu"
	left := "deja stats --card"
	// A build with no version stamped calls itself "dev", and "vdev" in a
	// footer reads as a typo rather than as a local build.
	if version != "" && version != "dev" {
		left += " · v" + version
	}
	// The date range lives here rather than in the head: the head ran one line
	// longer than the mark beside it and dropped its last entry with nothing to
	// say so. It reads better next to the provenance anyway.
	//
	// It is the first thing to go when the line will not fit. A footer that
	// overflows pushes the right border off the card, and the border is the
	// thing that makes this a card at all.
	if span := dateSpanText; span != "" &&
		visibleLen(left)+visibleLen(span)+visibleLen(right)+6 <= cardInner {
		left += "  ·  " + span
	}
	gap := cardInner - visibleLen(left) - visibleLen(right)
	if gap < 1 {
		gap = 1
	}
	return paint(cardFaint, left) + strings.Repeat(" ", gap) + paint(mark.Coat, right)
}

// frame wraps the body in a rounded border, padding every line to one width.
// The padding is why visibleWidth exists: measuring a coloured string by its
// bytes puts the right border somewhere different on every line.
func frame(body []string, foot string) []string {
	top := paint(cardRule, "╭"+strings.Repeat("─", cardInner+2)+"╮")
	bottom := paint(cardRule, "╰"+strings.Repeat("─", cardInner+2)+"╯")
	edge := paint(cardRule, "│")

	out := []string{top, edge + strings.Repeat(" ", cardInner+2) + edge}
	for _, line := range body {
		out = append(out, edge+" "+pad(line, cardInner)+" "+edge)
	}
	out = append(out,
		edge+strings.Repeat(" ", cardInner+2)+edge,
		edge+" "+pad(foot, cardInner)+" "+edge,
		bottom)
	return out
}

func printStatsCard(w io.Writer, r stats.Report) {
	for _, line := range statsCardLines(r) {
		fmt.Fprintln(w, line)
	}
}
