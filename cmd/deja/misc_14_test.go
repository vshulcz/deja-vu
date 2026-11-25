package main

import (
	"bytes"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/search"
	"github.com/vshulcz/deja-vu/internal/sources"
	"github.com/vshulcz/deja-vu/internal/stats"
	"github.com/vshulcz/deja-vu/internal/usage"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestStarTipLine(t *testing.T) {
	dir := t.TempDir()

	// Under 15 recalls returns "" and does not write the stamp.
	if got := starTipLine(dir, 0); got != "" {
		t.Fatalf("starTipLine(0) = %q, want empty", got)
	}
	if got := starTipLine(dir, 14); got != "" {
		t.Fatalf("starTipLine(14) = %q, want empty", got)
	}
	if got := starTipLine("", 15); got != "" {
		t.Fatalf("starTipLine(empty dir) = %q, want empty", got)
	}
	stamp := filepath.Join(dir, "star-tip-shown")
	if _, err := os.Stat(stamp); err == nil {
		t.Fatalf("stamp file %s exists prematurely", stamp)
	}

	// At >= 15 recalls returns star tip on first call and writes stamp.
	const want = "  If it helps      gh repo star vshulcz/deja-vu · https://github.com/vshulcz/deja-vu"
	if got := starTipLine(dir, 15); got != want {
		t.Fatalf("starTipLine(15) = %q, want %q", got, want)
	}
	if _, err := os.Stat(stamp); err != nil {
		t.Fatalf("stamp file %s missing after first call: %v", stamp, err)
	}

	// Second call returns empty.
	if got := starTipLine(dir, 15); got != "" {
		t.Fatalf("starTipLine(15) second call = %q, want empty", got)
	}
	if got := starTipLine(dir, 50); got != "" {
		t.Fatalf("starTipLine(50) subsequent call = %q, want empty", got)
	}
}

func TestStarTipSharedGroupMode(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX group permissions are not modeled on Windows")
	}
	t.Setenv("DEJA_SHARED_GROUP", "1")
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o770); err != nil {
		t.Fatal(err)
	}
	if got := starTipLine(dir, 25); got == "" {
		t.Fatal("expected star tip line")
	}
	if n := index.UnsharedFiles(dir); n != 0 {
		t.Fatalf("index.UnsharedFiles(%s) = %d after starTipLine, want 0", dir, n)
	}
}

func TestDoctorSharedIndexClientSuppressesDeniedExternalStoreWarning(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("permission refusal requires non-root POSIX permissions")
	}
	tmp := hermeticEnv(t)
	dir := os.Getenv("DEJA_INDEX_DIR")
	claudeRoot := os.Getenv("DEJA_CLAUDE_ROOT")
	writeClaudeFixture(t, filepath.Join(claudeRoot, "proj", "s1.jsonl"), "s1", []string{
		`{"type":"user","sessionId":"s1","timestamp":"2026-09-01T10:00:00Z","cwd":"/work/app","message":{"role":"user","content":"shared index session"}}`,
	})
	if err := index.Ensure(dir, "", true, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}

	lockedDir := filepath.Join(tmp, "root-only", "opencode")
	if err := os.MkdirAll(lockedDir, 0o755); err != nil {
		t.Fatal(err)
	}
	dbPath := filepath.Join(lockedDir, "opencode.db")
	if err := os.WriteFile(dbPath, []byte("sqlite"), 0o600); err != nil {
		t.Fatal(err)
	}
	rootOnly := filepath.Join(tmp, "root-only")
	if err := os.Chmod(rootOnly, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(rootOnly, 0o755) })

	t.Setenv("DEJA_OPENCODE_DB", dbPath)
	t.Setenv("DEJA_SHARED_GROUP", "1")
	t.Setenv("DEJA_NO_AUTO_INDEX", "1")

	var out bytes.Buffer
	printDoctorText(&out, collectDoctorReport(func() (string, bool) { return "", false }, dir), nil, dir, false, true)
	text := out.String()
	if strings.Contains(text, "opencode     unplugged") {
		t.Errorf("shared-index client reported EACCES store as unplugged:\n%s", text)
	}
	if strings.Contains(text, "warning      opencode") {
		t.Errorf("shared-index client warned on external indexer's store:\n%s", text)
	}
	if !strings.Contains(text, "opencode     shared") || !strings.Contains(text, "read by the shared indexer") {
		t.Errorf("expected opencode row to say shared (read by the shared indexer):\n%s", text)
	}
}

// The repeat count comes from the corpus, so a fresh install has it; the
// week's recalls need the usage sidecar. The card leads with the figure it
// can always show, and the week moves to the supporting row.
func TestCardLeadsWithRepeatQuestions(t *testing.T) {
	value, caption := heroStat(stats.Report{RepeatQuestions: 26, WeekRecalls: 428})
	if value != "26" || !strings.Contains(caption, "more than once") {
		t.Fatalf("hero = %q %q, want the repeat count", value, caption)
	}
	// The caption sits left of the agents column at 15 px; past about forty
	// characters it runs into "WHERE IT CAME FROM".
	for _, r := range []stats.Report{{RepeatQuestions: 26}, {WeekRecalls: 428}, {TotalSessions: 12}, {PolicyWithheld: 3}} {
		if _, c := heroStat(r); len(c) > 44 {
			t.Fatalf("caption %q is %d characters, past the agents column", c, len(c))
		}
	}
	svg := renderStatsCard(stats.Report{RepeatQuestions: 26, WeekRecalls: 428, TotalSessions: 10})
	if !strings.Contains(svg, "428 recalls handed to your agents this week") {
		t.Fatalf("the week's recalls left the card:\n%s", svg)
	}
	if !strings.Contains(svg, "github.com/vshulcz/deja-vu") {
		t.Fatalf("the footer no longer points at the repository:\n%s", svg)
	}
}

// Two rows of text at the same y read as one smeared row (#3060): every
// text element in the bottom band has to sit on its own line, or on the
// footer's line only if it is the footer.
func TestCardRowsNeverShareTheFooterLine(t *testing.T) {
	r := stats.Report{RepeatQuestions: 26, WeekRecalls: 428, TotalSessions: 10}
	r.Longest.Messages = 45051
	svg := renderStatsCard(r)
	re := regexp.MustCompile(`<text[^>]*\by="(\d+)"[^>]*>([^<]*)`)
	byY := map[int][]string{}
	for _, m := range re.FindAllStringSubmatch(svg, -1) {
		y, _ := strconv.Atoi(m[1])
		byY[y] = append(byY[y], m[2])
	}
	// The footer's line carries the footer and nothing else.
	for y, texts := range byY {
		foot, other := 0, 0
		for _, tx := range texts {
			if strings.HasPrefix(tx, "$ deja") || strings.HasPrefix(tx, "github.com") {
				foot++
			} else {
				other++
			}
		}
		if foot > 0 && other > 0 {
			t.Fatalf("y=%d carries the footer and %v", y, texts)
		}
	}
	// And the supporting row is not on the footer's line.
	foot := regexp.MustCompile(`y="(\d+)"[^>]*>\$ deja stats`).FindStringSubmatch(svg)
	row := regexp.MustCompile(`y="(\d+)"[^>]*>THE LONGEST SESSION`).FindStringSubmatch(svg)
	if foot == nil || row == nil {
		t.Fatalf("footer or row missing:\n%s", svg)
	}
	fy, _ := strconv.Atoi(foot[1])
	ry, _ := strconv.Atoi(row[1])
	if fy-ry < 12 {
		t.Fatalf("row at y=%d sits on the footer at y=%d", ry, fy)
	}
}

// The card exists to be published: it prints an embed snippet and invites the
// reader to paste it into a README. What makes that safe is that it carries
// counts and nothing else — no project name, no path, no sentence out of the
// reader's own history.
//
// That property is easy to trade away for a card that feels more personal, and
// it was traded away once: the longest session's title was printed on both
// cards for a day. This is the test that would have caught it.
func TestNeitherCardPrintsContentFromTheHistory(t *testing.T) {
	const (
		title   = "продолжай рефактор виджет-сервиса"
		project = "acme-payments-internal"
	)
	r := stats.Report{
		TotalSessions:   1225,
		TotalMessages:   139970,
		RepeatQuestions: 18,
		WeekRecalls:     70,
		Longest: stats.SessionStat{
			Title: title, Project: project, Harness: "claude", Messages: 31868,
		},
		TopProjects: []stats.ProjectStats{{Project: project, Sessions: 400}},
		Harnesses:   []stats.HarnessStats{{Harness: "claude", Sessions: 62}},
	}
	r.DateRange.Start, r.DateRange.End = "2026-04-18", "2026-08-15"
	r.Heatmap.Max = 8
	r.Heatmap.Weeks = make([][7]int, 53)
	for w := 40; w < 53; w++ {
		r.Heatmap.Weeks[w][0] = 3
	}

	for name, out := range map[string]string{
		"the terminal card": strings.Join(statsCardLines(r), "\n"),
		"the SVG card":      renderStatsCard(r),
	} {
		for _, secret := range []string{title, project} {
			if strings.Contains(out, secret) {
				t.Errorf("%s prints %q, which came out of the reader's own history", name, secret)
			}
		}
		// the counts derived from those sessions are fine, and are the point
		if !strings.Contains(out, "31,868") {
			t.Errorf("%s dropped the longest session's size along with its title", name)
		}
		if !strings.Contains(out, "18") {
			t.Errorf("%s does not show how many questions were asked more than once", name)
		}
	}
}

// The card is the one thing a reader makes with deja that they might show
// somebody else, and the line printed after it said "paste into a README or
// post". The README half works. The other half does not: the card is an SVG,
// and X, Threads, Reddit, Mastodon and every chat app refuse to render one —
// the person following that sentence finds out only once the post is written.
//
// So the line has to name the conversion, and the page that does it has to
// exist in this repository rather than in a sentence.
func TestTheCardSaysHowToPostIt(t *testing.T) {
	tmp := hermeticEnv(t)
	root := os.Getenv("DEJA_CLAUDE_ROOT")
	if err := os.MkdirAll(filepath.Join(root, "-work-app"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeClaudeFixture(t, filepath.Join(root, "-work-app", "s1.jsonl"), "s1", []string{
		`{"type":"user","sessionId":"s1","cwd":"/work/app","timestamp":"2026-08-20T10:00:00Z",` +
			`"message":{"role":"user","content":"the retry budget keeps blowing up"}}`,
	})
	card := filepath.Join(tmp, "deja-stats.svg")
	out, err := captureRun(t, "stats", "--card", card)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(card); err != nil {
		t.Fatalf("no card was written: %v", err)
	}
	if !strings.Contains(out, "vshulcz.github.io/deja-vu/card/") {
		t.Errorf("the card says nothing about how to post it, and an SVG cannot be posted:\n%s", out)
	}
	if !strings.Contains(out, "[![deja](deja-stats.svg)](https://github.com/vshulcz/deja-vu)") {
		t.Errorf("the card README snippet does not link back to github.com/vshulcz/deja-vu:\n%s", out)
	}

	// The page the line sends people to.
	page := filepath.Join("..", "..", "docs", "card", "index.html")
	b, err := os.ReadFile(page)
	if err != nil {
		t.Fatalf("the line points at a page that is not in the repository: %v", err)
	}
	for _, want := range []string{"toBlob", "image/png", "never leaves your machine"} {
		if !strings.Contains(string(b), want) {
			t.Errorf("docs/card/index.html does not %q — it has to convert locally, or the promise is wrong", want)
		}
	}
}

func sampleReport() stats.Report {
	r := stats.Report{
		TotalSessions: 1225,
		TotalMessages: 139970,
		Harnesses: []stats.HarnessStats{
			{Harness: "claude", Sessions: 62},
			{Harness: "opencode", Sessions: 1013},
			{Harness: "codex", Sessions: 32},
		},
	}
	// A real report carries a date range, and the footer grows by it. Without
	// one here the straight-border test passed while the shipped card pushed
	// its right edge eight columns off the frame.
	r.DateRange.Start = "2026-04-18"
	r.DateRange.End = "2026-08-15"
	r.WeekRecalls = 70
	// Twice now a line has overflowed the border because the sample lacked the
	// field that makes it long. This is the third such field.
	r.RepeatQuestions = 21
	// A Cyrillic title is the case that broke the width maths: 22 printed
	// columns and 40 bytes. Without one here the border test measured only
	// ASCII and passed while the shipped card came out short.
	r.Longest = stats.SessionStat{Title: "продолжай рефактор виджет-сервиса", Messages: 31868}
	r.Heatmap.Max = 8
	r.Heatmap.Weeks = make([][7]int, 53)
	for w := 40; w < 53; w++ {
		for d := range 7 {
			r.Heatmap.Weeks[w][d] = (w + d) % 9
		}
	}
	r.Heatmap.Months = []stats.HeatMonth{{Col: 40, Label: "Jun"}, {Col: 48, Label: "Aug"}}
	return r
}

// Into a pipe the card is plain text, as every other stats surface is: `deja
// stats --card | cat` carried the escape codes.
func TestCardIsPlainIntoAPipe(t *testing.T) {
	var b strings.Builder
	printStatsCard(&b, sampleReport())
	if strings.Contains(b.String(), "\x1b[") {
		t.Fatalf("card written to a non-terminal carries escape codes:\n%q", b.String())
	}
	if !strings.Contains(b.String(), "WHERE IT CAME FROM") {
		t.Fatal("plain card lost its text")
	}
}

func TestCardDrawsTheFiguresAndTheAgents(t *testing.T) {
	out := strings.Join(statsCardLines(sampleReport()), "\n")
	for _, want := range []string{"deja-vu", "1,225", "139,970", "sessions", "messages",
		"agents", "WHERE IT CAME FROM", "opencode", "1,013", "ACTIVITY"} {
		if !strings.Contains(out, want) {
			t.Errorf("the card never shows %q", want)
		}
	}
}

// Every line has to be the same width or the right border walks down the page,
// and the bytes in a coloured string say nothing about how wide it prints.
func TestCardBorderIsStraight(t *testing.T) {
	lines := statsCardLines(sampleReport())
	want := visibleLen(lines[0])
	for i, line := range lines {
		if got := visibleLen(line); got != want {
			t.Errorf("line %d prints %d columns, the top border prints %d:\n%s",
				i, got, want, line)
		}
	}
}

// The agents are ranked, not printed in whatever order the report held them:
// a card that lists the smallest first says the wrong thing about the history.
func TestCardRanksAgentsBySessions(t *testing.T) {
	var order []string
	for _, line := range statsCardLines(sampleReport()) {
		for _, name := range []string{"opencode", "claude", "codex"} {
			if strings.Contains(line, name) {
				order = append(order, name)
			}
		}
	}
	want := []string{"opencode", "claude", "codex"}
	if len(order) != len(want) {
		t.Fatalf("expected each agent once, got %v", order)
	}
	for i := range want {
		if order[i] != want[i] {
			t.Fatalf("agents are in %v, want %v", order, want)
		}
	}
}

// The grid fills the card at either cell width. Sized to the data alone it took
// a third of the width and read as unfinished; padded to the full year it was
// mostly a grey rectangle standing for history that does not exist.
func TestCardHeatmapFillsTheCard(t *testing.T) {
	for _, tc := range []struct {
		name  string
		weeks int
	}{{"a short history", 13}, {"a full year", 53}} {
		r := stats.Report{}
		r.Heatmap.Max = 8
		r.Heatmap.Weeks = make([][7]int, 53)
		for w := 53 - tc.weeks; w < 53; w++ {
			r.Heatmap.Weeks[w][0] = 3
		}
		lines := heatLines(r.Heatmap)
		if len(lines) != 5 {
			t.Fatalf("%s: heatmap is %d lines, want four rows and the labels", tc.name, len(lines))
		}
		for i, line := range lines[:4] {
			if got := visibleLen(line); got != cardInner {
				t.Errorf("%s: grid row %d prints %d columns, want %d", tc.name, i, got, cardInner)
			}
		}
	}
}

// The punchline is the only sentence on the card. Cutting it at the width
// produced "deja handed your agents", which is not a shorter sentence.
func TestCardPunchlineWrapsRatherThanTruncating(t *testing.T) {
	const line = "deja handed your agents memory 70 times this week."
	got := wrapTo(line, cardInner-26)
	joined := visibleText(got[0]) + " " + visibleText(got[1])
	if !strings.HasPrefix(line, strings.TrimSpace(visibleText(got[0]))) {
		t.Fatalf("first line %q is not the start of the sentence", got[0])
	}
	if !strings.Contains(joined, "this week") {
		t.Errorf("the end of the sentence was dropped: %q", joined)
	}
}

// An empty report must still produce a card rather than a panic: a first run
// with nothing indexed is exactly when someone tries this.
func TestCardSurvivesAnEmptyReport(t *testing.T) {
	lines := statsCardLines(stats.Report{})
	out := strings.Join(lines, "\n")
	if !strings.Contains(out, "deja-vu") {
		t.Error("even an empty card should carry the mark")
	}
	want := visibleLen(lines[0])
	for i, line := range lines {
		if got := visibleLen(line); got != want {
			t.Errorf("empty card line %d prints %d columns, want %d", i, got, want)
		}
	}
}

func TestRenderStatsCardDeterministicAndEscaped(t *testing.T) {
	r := stats.Report{
		TotalSessions: 9,
		TotalMessages: 27,
		Harnesses: []stats.HarnessStats{
			{Harness: "<&>", Sessions: 4}, {Harness: "codex", Sessions: 3},
			{Harness: "a", Sessions: 2},
		},
		TopProjects: []stats.ProjectStats{{Project: "<&>", Sessions: 2}},
		Monthly:     []stats.MonthStats{{Month: "2026-07", Messages: 4}},
		DateRange:   stats.DateRangeStats{Start: "2026-01-01", End: "2026-07-01"},
	}
	one := renderStatsCard(r)
	if one != renderStatsCard(r) {
		t.Fatal("card rendering is not deterministic")
	}
	if strings.Contains(one, "<script") || !strings.Contains(one, "&lt;&amp;&gt;") {
		t.Fatalf("card escaping or script check failed: %s", one)
	}
	decoder := xml.NewDecoder(strings.NewReader(one))
	for {
		if _, err := decoder.Token(); err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			t.Fatalf("card is not XML: %v", err)
		}
	}
	if !strings.Contains(one, `viewBox="0 0 800 486"`) || !strings.Contains(one, ">9</text>") || !strings.Contains(one, ">27</text>") {
		t.Fatalf("card missing fixed layout or hero values")
	}
	// The accent is what the card leads with, and a card that has lost it is
	// a wash of one colour — which is what the terminal one looked like before
	// the hero figure took it.
	if !strings.Contains(one, `fill="#ff8700"`) {
		t.Fatalf("card has no accent on its hero figure")
	}
}

func TestRenderStatsCardHarnessAggregation(t *testing.T) {
	r := stats.Report{Harnesses: []stats.HarnessStats{
		{Harness: "z", Sessions: 1}, {Harness: "y", Sessions: 2}, {Harness: "x", Sessions: 3},
		{Harness: "w", Sessions: 4}, {Harness: "v", Sessions: 5}, {Harness: "u", Sessions: 6}, {Harness: "t", Sessions: 7},
	}}
	card := renderStatsCard(r)
	// Three agents and a count of the rest, rather than a row per harness: the
	// tail is a single fact and it was taking a line each.
	if !strings.Contains(card, "and 10 more across the rest") || !strings.Contains(card, `height="8"`) {
		t.Fatalf("aggregated harness card = %s", card)
	}
}

func TestWriteStatsCardAndStatsFlagConflict(t *testing.T) {
	path := filepath.Join(t.TempDir(), "card.svg")
	written, err := writeStatsCard(path, stats.Report{Monthly: []stats.MonthStats{{Month: "2026-01"}}})
	if err != nil || written != path {
		t.Fatalf("writeStatsCard = %q, %v", written, err)
	}
	if b, err := os.ReadFile(path); err != nil || !bytes.HasPrefix(b, []byte("<?xml")) {
		t.Fatalf("card file = %q, %v", b, err)
	}
	if err := runStats(index.DefaultDir(), []string{"--card", "--json"}); err == nil || !strings.Contains(err.Error(), "choose one output") {
		t.Fatalf("conflict error = %v", err)
	}
	if _, err := writeStatsCard(filepath.Join(path, "nested.svg"), stats.Report{}); err == nil {
		t.Fatal("expected path error")
	}
}

func TestStatsCardCommand(t *testing.T) {
	withStatsStores(t)
	path := filepath.Join(t.TempDir(), "stats.svg")
	out, err := captureRun(t, "stats", "--card", path, "--harness", "claude", "--project", "alpha", "--since", "365000d", "--role", "user")
	if err != nil {
		t.Fatal(err)
	}
	abs, _ := filepath.Abs(path)
	if !strings.Contains(out, "deja: card written to "+abs) || !strings.Contains(out, "![deja]("+filepath.Base(path)+")") {
		t.Fatalf("card output = %q, want saved %q + share snippet", out, abs)
	}
	if b, err := os.ReadFile(path); err != nil || !strings.Contains(string(b), "· agent history") {
		t.Fatalf("card contents = %q, %v", b, err)
	}
}

func TestFilterStatsSessions(t *testing.T) {
	ss := []model.Session{
		{Harness: "claude", Project: "alpha", Messages: []model.Message{{Role: "user"}, {Role: "assistant"}}},
		{Harness: "codex", Project: "beta", Messages: []model.Message{{Role: "user"}}},
	}
	got := stats.Filter(ss, search.Options{Harness: "claude", Role: "user"})
	if len(got) != 1 || len(got[0].Messages) != 1 || got[0].Messages[0].Role != "user" {
		t.Fatalf("filtered sessions = %#v", got)
	}
	if got := stats.Filter(ss, search.Options{Project: "missing", Since: 1}); len(got) != 0 {
		t.Fatalf("missing project filter = %#v", got)
	}
}

func TestStatsHeadlineAndRepeatQuestions(t *testing.T) {
	ss := []model.Session{
		{ID: "one", Messages: []model.Message{{Role: "user", Text: "How do I fix auth?"}, {Role: "user", Text: "How do I fix auth?"}}},
		{ID: "two", Messages: []model.Message{{Role: "user", Text: "how do I fix auth"}}},
		{ID: "three", Messages: []model.Message{{Role: "assistant", Text: "How do I fix auth?"}}},
	}
	if got := stats.RepeatQuestions(ss); got != 1 {
		t.Fatalf("stats.RepeatQuestions = %d, want 1", got)
	}
	if got := statsHeadline(stats.Report{TotalSessions: 1240, RepeatQuestions: 17}); got != "1,240 sessions indexed · 17 questions asked more than once" {
		t.Fatalf("headline = %q", got)
	}
	if got := statsHeadline(stats.Report{TotalSessions: 3, RepeatQuestions: 1}); got != "3 sessions indexed · 1 question asked more than once" {
		t.Fatalf("headline for one repeat = %q", got)
	}
	if got := statsHeadline(stats.Report{}); got != "" {
		t.Fatalf("empty headline = %q", got)
	}
	// Near-repeats with different wording no longer count: the metric is
	// exact-stem only so it stays linear on large corpora.
	if got := stats.RepeatQuestions([]model.Session{{Messages: []model.Message{
		{Role: "user", Text: "<local-command-caveat>"},
		{Role: "user", Text: "   "},
	}}, {Messages: []model.Message{{Role: "user", Text: "fix auth timeout in service now"}}}, {Messages: []model.Message{{Role: "user", Text: "fix auth timeout in service"}}}}); got != 0 {
		t.Fatalf("near-repeat questions = %d, want 0", got)
	}
}

func TestStatsFlagValidation(t *testing.T) {
	for _, args := range [][]string{
		{"--html", "--html"},
		{"--card", "--card"},
		{"--redaction", "--card"},
		{"--harness"},
		{"--project"},
		{"--since"},
		{"--role"},
		{"--since", "not-a-duration"},
		{"--unknown"},
	} {
		if err := runStats(index.DefaultDir(), args); err == nil {
			t.Fatalf("runStats(index.DefaultDir(), %#v) returned nil", args)
		}
	}
}

// --impact and --redaction cover the whole store as text or JSON. They printed
// text over an --html they ignored, and the same figures under any filter.
func TestStatsWholeStoreReportsRefuseWhatTheyIgnore(t *testing.T) {
	hermeticEnv(t)
	page := filepath.Join(t.TempDir(), "x.html")
	for _, report := range []string{"--impact", "--redaction"} {
		for _, extra := range [][]string{
			{"--html", page},
			{"--card"},
			{"--project", "nosuch"},
			{"--harness", "codex"},
			{"--since", "1m"},
			{"--role", "user"},
		} {
			args := append([]string{report}, extra...)
			if err := runStats(index.DefaultDir(), args); err == nil {
				t.Errorf("stats %v: accepted a flag it ignores", args)
			}
		}
	}
	if err := runStats(index.DefaultDir(), []string{"--impact", "--redaction"}); err == nil {
		t.Error("two reports at once")
	}
	if _, err := os.Stat(page); err == nil {
		t.Error("a refused run wrote the page")
	}
}

func TestHandoffsReceivedCountedFromIndex(t *testing.T) {
	ss := []model.Session{
		{ID: "h1", Messages: []model.Message{{Role: "user", Text: "You are picking up work handed off from a claude session (project x)"}}},
		{ID: "n1", Messages: []model.Message{{Role: "user", Text: "ordinary question"}}},
		{ID: "n2", Messages: []model.Message{{Role: "assistant", Text: "picking up work handed off from a"}, {Role: "user", Text: "hi"}}},
	}
	r := stats.Build(ss, time.Now())
	if r.HandoffsIn != 1 {
		t.Fatalf("HandoffsIn = %d, want 1", r.HandoffsIn)
	}
}

// A path on a disk that is not there came back as `write stats card: open
// /Volumes/…/card.png.svg: no such file or directory` — an internal path and a
// syscall, the shape #893/#907 replaced on the other writing paths (#1036).
func TestWriteStatsCardSaysWhatIsWrongWithThePath(t *testing.T) {
	gone := filepath.Join(t.TempDir(), "no-such-dir", "card.png")
	_, err := writeStatsCard(gone, stats.Report{})
	if err == nil {
		t.Fatal("writing into a directory that does not exist did not fail")
	}
	if !strings.Contains(err.Error(), "its directory does not exist") {
		t.Errorf("the error does not say what is wrong: %v", err)
	}
	if strings.Contains(err.Error(), "no such file or directory") {
		t.Errorf("the syscall is still handed back: %v", err)
	}
}

// The terminal line and the JSON both distinguish "nothing indexed" from "the
// policy withholds everything indexed". The card told the reader to run `deja
// index`, which changes nothing in that state (#2288).
func TestTheCardDoesNotBlameAnEmptyIndexForThePolicy(t *testing.T) {
	// The premise: with nothing indexed at all, the old sentence is right.
	_, caption := heroStat(stats.Report{})
	if !strings.Contains(caption, "deja index") {
		t.Fatalf("an empty store no longer points at `deja index`: %q", caption)
	}

	_, caption = heroStat(stats.Report{PolicyWithheld: 3})
	if strings.Contains(caption, "run deja index") {
		t.Errorf("the card blames an empty index while the policy holds 3 sessions: %q", caption)
	}
	if !strings.Contains(caption, "polic") {
		t.Errorf("the card does not name what is withholding the sessions: %q", caption)
	}

	// A store with sessions is unaffected either way.
	if _, caption := heroStat(stats.Report{TotalSessions: 12}); !strings.Contains(caption, "searchable") {
		t.Errorf("a normal store lost its line: %q", caption)
	}
}

// The page and the terminal made different claims from one report: "You asked
// the same thing 4 times" where the count is four questions asked more than
// once, and the busiest month where the terminal names the busiest day.
func TestStatsPageMakesTheTerminalsClaims(t *testing.T) {
	r := stats.Report{
		TotalSessions:   3,
		TotalMessages:   200,
		RepeatQuestions: 4,
		BusiestDay:      stats.DayStat{Date: "2025-12-10", Messages: 38},
		Monthly:         []stats.MonthStats{{Month: "2026-06", Messages: 152}},
	}
	path := filepath.Join(t.TempDir(), "stats.html")
	if _, err := writeStatsHTML(path, r, nil); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	page := string(b)
	for _, want := range []string{"4 questions more than once", "BUSIEST DAY 2025-12-10, 38 MESSAGES"} {
		if !strings.Contains(page, want) {
			t.Errorf("page is missing %q", want)
		}
	}
	for _, not := range []string{"the same thing 4 times", "BUSIEST 2026-06"} {
		if strings.Contains(page, not) {
			t.Errorf("page still says %q", not)
		}
	}
}

func TestStatsHTMLMetadataEscapingAndPrivacy(t *testing.T) {
	report := stats.Report{TotalSessions: 1, TotalMessages: 2, Harnesses: []stats.HarnessStats{{Harness: "claude"}}, DateRange: stats.DateRangeStats{Start: "2026-01-01", End: "2026-01-02"}, Monthly: []stats.MonthStats{{Month: "2026-01", Messages: 2}}}
	sessions := []model.Session{{Harness: "claude", Project: `<script>alert(1)</script>`, Title: "redacted title", Updated: time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC), Messages: []model.Message{{Role: "user", Text: "secret message body"}}}}
	page, err := newStatsHTMLPage(report, sessions)
	if err != nil {
		t.Fatal(err)
	}
	var rows []htmlSession
	if err := json.Unmarshal([]byte(page.SessionsJSON), &rows); err != nil || len(rows) != 1 {
		t.Fatalf("metadata JSON=%q err=%v", page.SessionsJSON, err)
	}
	if rows[0].Project != `<script>alert(1)</script>` || rows[0].Title != "redacted title" || strings.Contains(string(page.SessionsJSON), "secret message body") {
		t.Fatalf("metadata privacy failed: %q", page.SessionsJSON)
	}
	var rendered strings.Builder
	if err := statsHTMLTemplate.Execute(&rendered, page); err != nil {
		t.Fatal(err)
	}
	output := rendered.String()
	if strings.Contains(output, "<script>alert(1)</script>") || !strings.Contains(output, `\u003cscript\u003e`) || strings.Contains(output, "http://") || strings.Contains(output, "https://") {
		t.Fatalf("HTML escaping/network check failed: %s", output)
	}
	// The note says what the file holds rather than denying it holds any
	// message text, since the titles are the opening lines of sessions (#2275).
	if !strings.Contains(output, "1") || !strings.Contains(output, "2") || !strings.Contains(output, "title or its opening line") {
		t.Fatalf("HTML totals/privacy note missing: %s", output)
	}
}

func TestStatsHTMLCapAndWrite(t *testing.T) {
	sessions := make([]model.Session, statsHTMLCap+1)
	for i := range sessions {
		sessions[i] = model.Session{Harness: "h", Project: "p", Updated: time.Date(2020, 1, 1, 0, 0, i, 0, time.UTC)}
	}
	page, err := newStatsHTMLPage(stats.Report{}, sessions)
	if err != nil || !page.Truncated || page.SessionCount != statsHTMLCap {
		t.Fatalf("cap page=%#v err=%v", page, err)
	}
	path := filepath.Join(t.TempDir(), "timeline.html")
	written, err := writeStatsHTML(path, stats.Report{}, sessions[:1])
	if err != nil || written != path {
		t.Fatalf("write HTML=%q err=%v", written, err)
	}
	if b, err := os.ReadFile(path); err != nil || !strings.HasPrefix(string(b), "<!doctype html>") {
		t.Fatalf("HTML file err=%v content=%q", err, b)
	}
	if _, err := writeStatsHTML(filepath.Join(path, "nested.html"), stats.Report{}, nil); err == nil {
		t.Fatal("expected path error")
	}
}

// The ramp has four visible steps rather than a linear one: against a single
// busy day a linear scale renders an ordinary week as nothing.
func TestStatsHTMLHelpers(t *testing.T) {
	for _, c := range []struct {
		count, max int
		want       string
	}{{0, 20, "0.06"}, {1, 20, "0.22"}, {5, 20, "0.40"}, {10, 20, "0.66"}, {20, 20, "1"}, {3, 0, "1"}} {
		if got := heatOpacity(c.count, c.max); got != c.want {
			t.Errorf("heatOpacity(%d,%d)=%s want %s", c.count, c.max, got, c.want)
		}
	}
	if monthShort("2026-02") != "02" || monthShort("x") != "x" {
		t.Fatal("unexpected month label")
	}
}

func TestStatsHTMLCommandAndConflicts(t *testing.T) {
	withStatsStores(t)
	path := filepath.Join(t.TempDir(), "timeline.html")
	out, err := captureRun(t, "stats", "--html", path, "--harness", "claude")
	if err != nil {
		t.Fatal(err)
	}
	abs, _ := filepath.Abs(path)
	if strings.TrimSpace(out) != "deja: stats page written to "+abs {
		t.Fatalf("HTML output=%q want=%q", out, abs)
	}
	if err := runStats(index.DefaultDir(), []string{"--html", "--json"}); err == nil || !strings.Contains(err.Error(), "choose one output") {
		t.Fatal("expected HTML/JSON conflict")
	}
	if err := runStats(index.DefaultDir(), []string{"--html", "--card"}); err == nil || !strings.Contains(err.Error(), "choose one output") {
		t.Fatal("expected HTML/card conflict")
	}
	for _, args := range [][]string{{"--html", "--html"}, {"--unknown"}} {
		if err := runStats(index.DefaultDir(), args); err == nil {
			t.Fatalf("runStats(index.DefaultDir(), %v) accepted invalid arguments", args)
		}
	}
}

func TestStatsHTMLEdgeBranches(t *testing.T) {
	tmp := t.TempDir()
	// A session with only a start time falls back to it for date and sorting.
	started := time.Date(2026, 2, 3, 4, 5, 6, 0, time.UTC)
	sessions := []model.Session{
		{ID: "b", Harness: "claude", Started: started, Messages: []model.Message{{Role: "user", Text: "hello"}}},
		{ID: "a", Harness: "claude", Started: started},
	}
	page, err := newStatsHTMLPage(stats.Report{}, sessions)
	if err != nil {
		t.Fatal(err)
	}
	if page.SessionCount != 2 || !strings.Contains(string(page.SessionsJSON), "2026-02-03") || !strings.Contains(string(page.SessionsJSON), `"-"`) {
		t.Fatalf("page json=%s", page.SessionsJSON)
	}
	// Write failure: parent path squatted by a file.
	squat := filepath.Join(tmp, "f")
	if err := os.WriteFile(squat, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := writeStatsHTML(filepath.Join(squat, "out.html"), stats.Report{}, nil); err == nil {
		t.Fatal("expected write error")
	}
}

// `noAgentHistoryFound` reads one field of the inspection — `store.Files`,
// which is `len(check.files)` set at doctor_report.go:471 and never mutated —
// and pays a stat per path, a listing per directory, and the newest file of
// every store opened and run through that store's parser to get it. On a real
// home that measured 514 ms against 6.6 ms for the same answer (#1991).
//
// A ratio rather than a duration, so it means the same thing on a slow runner,
// and against the enumeration rather than the clock, so it does not become a
// flake when a machine is busy.
func TestTheEmptyScreenDoesNotParseEveryStore(t *testing.T) {
	if testing.Short() {
		t.Skip("timing")
	}
	hermeticEnv(t)
	claude := os.Getenv("DEJA_CLAUDE_ROOT")
	// Files big enough that parsing them is not free: the sandbox is why this
	// cost was invisible to my first measurement, so the fixture has to put it
	// back.
	long := make([]byte, 4<<20)
	for i := range long {
		long[i] = 'x'
	}
	for i := range 4 {
		seedClaude(t, claude, "app", string(rune('a'+i)), "the pgbouncer pool kept timing out "+string(long), "we retried")
	}

	// Best of three on each side. Both are well under a millisecond, and a
	// runner busy with the rest of the suite stretches such a sample by more
	// than the bar below allows — the same flake #2193 caught in the usage
	// package. The minimum is the run that was not interrupted.
	//
	// Each round times a batch rather than one call. Windows' clock ticks in
	// milliseconds, so a single enumeration measured as exactly 0 there: the
	// bar below collapsed to its own slack and `main` went red on an answer
	// that took 2.0169ms. A batch lifts both sides above the tick, and the
	// ratio then means what it says on every platform.
	const batch = 20
	enumerate, asked := time.Duration(1<<62-1), time.Duration(1<<62-1)
	var checks []doctorStoreCheck
	for range 3 {
		start := time.Now()
		for range batch {
			checks = doctorStoreChecks()
		}
		if took := time.Since(start); took < enumerate {
			enumerate = took
		}
		answer := false
		start = time.Now()
		for range batch {
			answer = noAgentHistoryFound()
		}
		if took := time.Since(start); took < asked {
			asked = took
		}
		if answer {
			t.Fatal("the fixture has history, so this measures nothing")
		}
	}
	if enumerate <= 0 {
		t.Fatalf("%d enumerations measured as %v: the clock cannot resolve this and the ratio below means nothing", batch, enumerate)
	}
	// Four times the enumeration, plus a millisecond or two of slack for a
	// loaded runner: far above the spread between runs, far below the cost of
	// reading and parsing a store's newest file.
	if asked > 4*enumerate+2*time.Millisecond {
		t.Errorf("the question took %v against %v to enumerate the stores %d times: it is parsing them",
			asked, enumerate, batch)
	}
	t.Logf("%d rounds: enumerate %v, answer %v (%d stores)", batch, enumerate, asked, len(checks))

	if _, err := os.Stat(filepath.Join(claude, "-app")); err != nil {
		t.Fatal(err)
	}
}

// "named agents + N more" is checked on the pages a reader lands on (#3356,
// TestPageTitlesCountTheRestOfTheHarnesses) and was checked nowhere in the
// files a *store* shows: the marketplace entries, the plugin manifests and the
// per-extension package descriptions. Those had drifted furthest, because
// nothing reads them on the way past — the Claude marketplace and the Codex
// plugin said "a dozen others" of thirty-four, the Gemini extension said
// twenty-nine more of thirty, and the page explaining why agents forget
// listed six formats and "a dozen others".
//
// One table, both spellings, every file that carries the phrase. `named` is
// how many agents the sentence names before it, so the arithmetic is the one a
// reader would do.
func TestEveryStoreListingCountsTheRestOfTheHarnesses(t *testing.T) {
	root := filepath.Join("..", "..")
	n := registryHarnessCount(t, root)

	for _, c := range []struct {
		file  string
		named int
		shape string // %s takes the spelled number, %d the digits
		digit bool
	}{
		{file: "plugin.json", named: 4, shape: "Claude Code, Codex, Cursor, opencode and %s more"},
		{file: ".antigravity-plugin/plugin.json", named: 4, shape: "Claude Code, Codex, Cursor, opencode and %s more"},
		{file: "antigravity-plugin/plugin.json", named: 4, shape: "Claude Code, Codex, Cursor, opencode and %s more"},
		{file: "kimi.plugin.json", named: 4, shape: "Claude Code, Codex, Cursor, opencode and %s more"},
		{file: "gemini-extension.json", named: 4, shape: "Gemini CLI, Claude Code, Codex, Cursor and %s more"},
		{file: ".claude-plugin/marketplace.json", named: 4, shape: "Claude Code, Codex, Cursor, opencode and %s more agents"},
		{file: "codex-plugin/.codex-plugin/plugin.json", named: 4, shape: "Claude Code, Codex, Cursor, opencode and %s more agents"},
		{file: "extensions/kimi/kimi.plugin.json", named: 4, shape: "Claude Code, Codex, Cursor, opencode and %s more"},
		{file: ".grok-plugin/marketplace.json", named: 4, shape: "Claude Code, Codex, Cursor, opencode and %s more"},
		{file: "extensions/grok/.grok-plugin/plugin.json", named: 4, shape: "Claude Code, Codex, Cursor, opencode and %s more"},
		{file: "extensions/grok/README.md", named: 5, shape: "Claude Code, Codex, Cursor, opencode, Zed and %s more agents"},
		{file: "extensions/dsh/index.js", named: 4, shape: "Claude Code, Codex, Cursor, opencode and %s more agents"},
		{file: "extensions/opencode/index.js", named: 4, shape: "Claude Code, Codex, Cursor, Gemini and %s more agents"},
		{file: "extensions/pi/package.json", named: 4, shape: "pi, Claude Code, Codex, Cursor and %d more", digit: true},
		{file: "extensions/openclaw/package.json", named: 4, shape: "OpenClaw, Claude Code, Codex, Cursor and %d more", digit: true},
		{file: "docs/guide/forgetting.html", named: 6, shape: "Cursor, Gemini CLI, Zed and %s more agents"},
	} {
		b, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(c.file)))
		if err != nil {
			t.Errorf("%s: %v", c.file, err)
			continue
		}
		rest := n - c.named
		var want string
		if c.digit {
			want = fmt.Sprintf(c.shape, rest)
		} else {
			want = fmt.Sprintf(c.shape, countWordForTest(rest))
		}
		if !strings.Contains(string(b), want) {
			t.Errorf("%s does not say %q — the registry has %d harnesses and the sentence names %d",
				c.file, want, n, c.named)
		}
	}
}

// A listing that stopped spelling the count at all would pass the table above
// only by losing its sentence, so the phrase itself has to still be there.
func TestNoStoreListingUndercountsWithADozen(t *testing.T) {
	root := filepath.Join("..", "..")
	for _, f := range []string{
		"plugin.json", ".antigravity-plugin/plugin.json", "antigravity-plugin/plugin.json",
		"kimi.plugin.json", "gemini-extension.json",
		".claude-plugin/marketplace.json", "codex-plugin/.codex-plugin/plugin.json",
		"docs/guide/forgetting.html",
	} {
		b, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(f)))
		if err != nil {
			t.Errorf("%s: %v", f, err)
			continue
		}
		// "a dozen" is how each of these went stale: a number that reads as
		// approximate and stops being true without looking wrong.
		if strings.Contains(string(b), "a dozen") {
			t.Errorf("%s says \"a dozen\" — spell the count so a test can keep it true", f)
		}
	}
}

// doctor keeps its own list of stores — each row carries paths and a parser —
// so narrowing the registry does not narrow the report on its own. A silenced
// store used to appear as `1 path could not be read`, which is the noise
// DEJA_STORES exists to remove, reported about a store nobody asked for.
func TestDoctorDropsTheStoresDejaStoresSilences(t *testing.T) {
	full := len(doctorStoreChecks())
	if full < 30 {
		t.Fatalf("doctor checks %d stores; this test is reading the wrong list", full)
	}

	t.Setenv(sources.StoresEnv, "claude,notes")
	checks := doctorStoreChecks()
	got := make([]string, 0, len(checks))
	for _, c := range checks {
		got = append(got, c.name)
	}
	if len(got) != 2 {
		t.Fatalf("doctor still checks %v", got)
	}
	// "notes" in the variable, "deja" in the check list: the row keeps the
	// name the rest of the code uses.
	if got[0] != "claude" || got[1] != "deja" {
		t.Errorf("doctor checks %v", got)
	}
}

// The rows a person reads are printed by a different path than the JSON, and
// filtering one left the other listing thirty-two stores as missing under a
// line that had just said deja was reading two.
func TestDoctorRowsDropTheSilencedStores(t *testing.T) {
	hermeticEnv(t)
	t.Setenv(sources.StoresEnv, "claude,notes")
	var b strings.Builder
	doctorHarnesses(&b, t.TempDir())
	out := b.String()
	if !strings.Contains(out, "reading only claude, notes ("+sources.StoresEnv+")") {
		t.Errorf("doctor does not say which stores it is reading:\n%s", out)
	}
	for _, silenced := range []string{"codex", "cursor", "zed"} {
		for line := range strings.SplitSeq(out, "\n") {
			if strings.HasPrefix(strings.TrimSpace(line), silenced+" ") {
				t.Errorf("doctor prints a row for %s, which DEJA_STORES silences: %q", silenced, line)
			}
		}
	}
	if !strings.Contains(out, "claude") {
		t.Error("doctor dropped the store it was told to read")
	}
}

// The selection is validated once, before a command runs, and every command
// carries the refusal — a hook or the MCP server is started by somebody else's
// process, and a typo there would otherwise read as "no history on this
// machine".
func TestATypoInDejaStoresStopsTheCommand(t *testing.T) {
	t.Setenv(sources.StoresEnv, "claude,opencodee")
	err := run([]string{"sources"})
	if err == nil {
		t.Fatal("deja ran with a store name that does not exist")
	}
	if !strings.Contains(err.Error(), "opencodee") {
		t.Errorf("the refusal does not name the typo: %v", err)
	}
}

// docs/benchmarks/SUBMISSION.md invites somebody to beat our row and prints
// that row: two sets, five metrics each. Nothing checked it. The headline test
// next door keys on "N.N% hit@1", which a table cell is not, so these ten
// figures could drift from the committed runs without a test noticing — and
// this is the page whose whole argument is that the numbers are checkable.
func TestTheSubmissionTableMatchesTheCommittedRuns(t *testing.T) {
	root := filepath.Join("..", "..")
	raw, err := os.ReadFile(filepath.Join(root, "docs", "benchmarks", "SUBMISSION.md"))
	if err != nil {
		t.Fatal(err)
	}
	page := string(raw)

	read := func(name string) benchArtifact {
		b, err := os.ReadFile(filepath.Join(root, "docs", "benchmarks", name))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		var a benchArtifact
		if err := json.Unmarshal(b, &a); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		return a
	}
	for _, c := range []struct {
		artifact string
		label    string
	}{
		{"longmemeval-s-cleaned.json", "470 questions"},
		{"longmemeval-s-full.json", "all 500"},
	} {
		a := read(c.artifact)
		row := submissionRow(t, page, c.label)
		for _, cell := range []struct {
			what string
			want float64
		}{
			{"hit@1", a.Total.Hit1},
			{"hit@5", a.Total.Hit5},
			{"hit@10", derefOr(a.Total.Hit10)},
			{"hit@20", derefOr(a.Total.Hit20)},
		} {
			want := fmt.Sprintf("%.1f%%", cell.want)
			if !strings.Contains(row, want) {
				t.Errorf("the %q row does not carry %s %s from %s:\n%s",
					c.label, cell.what, want, c.artifact, row)
			}
		}
		if want := fmt.Sprintf("%.3f", a.Total.MRR); !strings.Contains(row, want) {
			t.Errorf("the %q row does not carry MRR %s from %s:\n%s", c.label, want, c.artifact, row)
		}
		// The set size is the other half of the claim: 470 and 500 give
		// different numbers for the same system, which the page says itself.
		if !strings.Contains(row, strconv.Itoa(a.Total.N)) {
			t.Errorf("the %q row does not say which set it counted (%d questions)", c.label, a.Total.N)
		}
	}
}

// submissionRow is the table line whose first cell names that set.
func submissionRow(t *testing.T, page, label string) string {
	t.Helper()
	for line := range strings.SplitSeq(page, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "|") && strings.Contains(line, label) {
			return line
		}
	}
	t.Fatalf("SUBMISSION.md has no table row for %q — it is the row this page asks people to beat", label)
	return ""
}

func derefOr(v *float64) float64 {
	if v == nil {
		return 0
	}
	return *v
}

// recentImportedStore is importedStore with dates and topics a suggestion can
// actually be made from: the phrase picker looks at the last two months only,
// and takes a phrase whose two words each recur across sessions.
func recentImportedStore(t *testing.T, n int) string {
	t.Helper()
	tmp := hermeticEnv(t)
	exp := filepath.Join(tmp, "transfer")
	if err := os.MkdirAll(exp, 0o755); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	topics := []string{
		"kafka consumer rebalance keeps flapping",
		"vault rotation broke staging deploy",
		"scheduler retries twice every timeout",
		"cache eviction wiped session store",
	}
	var batch []byte
	for i := range n {
		b, err := json.Marshal(index.SyncRecord{
			Harness: "claude", SessionID: "peer" + string(rune('1'+i)), Project: "tmp/projx",
			Role: "user", Text: topics[i%len(topics)], Time: now.AddDate(0, 0, -i-1),
		})
		if err != nil {
			t.Fatal(err)
		}
		batch = append(append(batch, b...), '\n')
	}
	if err := os.WriteFile(filepath.Join(exp, "deja-sync-x.jsonl"), batch, 0o644); err != nil {
		t.Fatal(err)
	}
	dir := index.DefaultDir()
	if err := index.Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRun(t, "sync", "import", exp); err != nil {
		t.Fatal(err)
	}
	return dir
}

// The suggestion after a first index is "a phrase from your own history",
// printed to argue that the memory is worth having. On a machine whose history
// arrived by sync, it was lifting that phrase out of sessions the trust policy
// withholds from every other surface.
func TestSuggestHonoursThePolicy(t *testing.T) {
	dir := recentImportedStore(t, 8)
	if got := suggestFirstQuery(dir); got == "" {
		t.Fatal("no suggestion without a policy, so the test measures nothing")
	}
	writePolicy(t, `{"activations":{"search":{"local":true,"imported":false},"auto":{"local":true,"imported":false}}}`)
	if got := suggestFirstQuery(dir); got != "" {
		t.Errorf("suggested %q, a phrase from sessions the policy withholds", got)
	}
}

// A rule that allows the sessions leaves the suggestion in place: the screen
// is there to show someone their own history back.
func TestSuggestStillSpeaksWhenAllowed(t *testing.T) {
	dir := recentImportedStore(t, 8)
	writePolicy(t, `{"activations":{"search":{"local":true,"imported":true}}}`)
	if got := suggestFirstQuery(dir); got == "" {
		t.Error("no suggestion though the policy allows these sessions")
	}
}

func TestSuggestFirstQueryPicksDistinctiveRecentPhrase(t *testing.T) {
	tmp := hermeticEnv(t)
	root := filepath.Join(tmp, "claude", "proj-a")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "claude"))
	now := "2026-07-20T10:00:00Z"
	mk := func(id, text string) string {
		return `{"type":"user","sessionId":"` + id + `","cwd":"/w/a","timestamp":"` + now + `","message":{"role":"user","content":"` + text + `"}}` + "\n"
	}
	// "jwks rotation" recurs in two sessions; filler words dominate the rest.
	files := map[string]string{
		"s1": mk("s1", "the jwks rotation broke login again today"),
		"s2": mk("s2", "jwks rotation cache still stale after deploy"),
		"s3": mk("s3", "please update readme wording and typos"),
		"s4": mk("s4", "update readme header image"),
		"s5": mk("s5", "update readme badges"),
	}
	for id, body := range files {
		if err := os.WriteFile(filepath.Join(root, id+".jsonl"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	dir := index.DefaultDir()
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	got := suggestFirstQuery(dir)
	if !strings.Contains(got, "jwks") {
		t.Fatalf("suggestion = %q, want the distinctive recurring phrase", got)
	}
}

// Scoring two words by rarity alone picks the rarest pair, and on a store with
// prose in it that is a fragment of one sentence: on a 2,421-session store the
// pick was a verb and its object out of a single message, the subject of
// neither session that held it, while requiring the pair itself to recur moved
// what a reader running it gets back from 10 sessions to 18 (#3714).
func TestSuggestFirstQueryNeedsThePhraseToRecur(t *testing.T) {
	tmp := hermeticEnv(t)
	root := filepath.Join(tmp, "claude", "proj-a")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "claude"))
	now := "2026-07-20T10:00:00Z"
	mk := func(id, text string) string {
		return `{"type":"user","sessionId":"` + id + `","cwd":"/w/a","timestamp":"` + now + `","message":{"role":"user","content":"` + text + `"}}` + "\n"
	}
	// "quokka thimble" is the rarest pair and it is said once. Both of its
	// words recur, which is all the old rule asked of them: each appears in a
	// second session, apart. "jwks rotation" is said in two.
	files := map[string]string{
		"s1": mk("s1", "the quokka thimble stalls under load"),
		"s2": mk("s2", "quokka counters look wrong here"),
		"s3": mk("s3", "thimble metrics never reach the collector"),
		"s4": mk("s4", "jwks rotation broke login again today"),
		"s5": mk("s5", "jwks rotation cache still stale"),
	}
	for id, body := range files {
		if err := os.WriteFile(filepath.Join(root, id+".jsonl"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	dir := index.DefaultDir()
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	got := suggestFirstQuery(dir)
	if strings.Contains(got, "quokka") || strings.Contains(got, "thimble") {
		t.Fatalf("suggestion = %q, a pair said once — a reader running it gets the one session it came from", got)
	}
	if !strings.Contains(got, "jwks") {
		t.Fatalf("suggestion = %q, want the phrase that recurs", got)
	}
}

func TestSuggestFirstQueryEmptyOnThinCorpus(t *testing.T) {
	hermeticEnv(t)
	if got := suggestFirstQuery(index.DefaultDir()); got != "" {
		t.Fatalf("thin corpus suggested %q", got)
	}
}

func TestSuggestTokenRejectsCodeAndNoise(t *testing.T) {
	for _, tok := range []string{
		"map[string]any{",
		"button_result_hd_price",
		"internal/index/store.go",
		"http://example.com",
		"key=value",
	} {
		if suggestToken(tok) != "" {
			t.Errorf("%q came out of source, not a sentence", tok)
		}
	}
	for _, tok := range []string{"pgbouncer", "задачки", "compaction"} {
		if suggestToken(tok) == "" {
			t.Errorf("%q is a word someone would type", tok)
		}
	}
}

func TestSuggestPhraseTokensMarksGaps(t *testing.T) {
	// "the" is a stop word: the two content words either side of it were not
	// adjacent, and a suggestion built from them would read as a fragment.
	got := suggestPhraseTokens("retry the budget")
	if len(got) != 3 || got[0] == "" || got[1] != "" || got[2] == "" {
		t.Fatalf("got %q, want the dropped word marked", got)
	}
}

// The suggestion is scored on what people typed, not on what the agents wrote
// back. A subject the assistant repeats in every session is still distinctive
// if the person only raised it twice — and the change that made this true is
// also what took the line from 2.2 s to 0.19 s (#625).
func TestSuggestScoresUserTurnsNotAssistantEcho(t *testing.T) {
	tmp := hermeticEnv(t)
	root := filepath.Join(tmp, "claude", "proj-e")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "claude"))
	const now = "2026-07-20T10:00:00Z"
	user := func(id, text string) string {
		return `{"type":"user","sessionId":"` + id + `","cwd":"/w/e","timestamp":"` + now +
			`","message":{"role":"user","content":"` + text + `"}}` + "\n"
	}
	assistant := func(id, text string) string {
		return `{"type":"assistant","sessionId":"` + id + `","cwd":"/w/e","timestamp":"` + now +
			`","message":{"role":"assistant","content":"` + text + `"}}` + "\n"
	}
	// The agent echoes "quantum harvester" in all five sessions; the person
	// raised it in two. Counting the echo would score it as common and the
	// suggestion would fall back to filler.
	files := map[string]string{
		"e1": user("e1", "quantum harvester latency spike") + assistant("e1", "the quantum harvester is fine"),
		"e2": user("e2", "quantum harvester retries again") + assistant("e2", "the quantum harvester is fine"),
		"e3": user("e3", "please update readme wording") + assistant("e3", "the quantum harvester is fine"),
		"e4": user("e4", "update readme header image") + assistant("e4", "the quantum harvester is fine"),
		"e5": user("e5", "update readme badges please") + assistant("e5", "the quantum harvester is fine"),
	}
	for id, body := range files {
		if err := os.WriteFile(filepath.Join(root, id+".jsonl"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	dir := index.DefaultDir()
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	if got := suggestFirstQuery(dir); !strings.Contains(got, "quantum") {
		t.Fatalf("suggestion = %q, want the subject the person raised", got)
	}
}

// The suggestion is picked from what people typed as the task. Measured across
// four candidate sources on a 2,000-session store, a phrase recurring across
// session titles is the one that names a subject; a bigram out of the middle of
// a message is a fragment of whatever was being said (#3714).
func TestSuggestPicksAPhraseFromTheTitles(t *testing.T) {
	tmp := hermeticEnv(t)
	root := filepath.Join(tmp, "claude")
	proj := filepath.Join(root, "-work-app")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_CLAUDE_ROOT", root)

	now := time.Now().UTC()
	write := func(id, title string, rest ...string) {
		var b strings.Builder
		stamp := now.Add(-time.Hour).Format(time.RFC3339)
		lines := append([]string{title}, rest...)
		for _, text := range lines {
			rec := map[string]any{
				"type": "user", "sessionId": id, "cwd": "/work/app", "timestamp": stamp,
				"message": map[string]any{"role": "user", "content": text},
			}
			line, err := json.Marshal(rec)
			if err != nil {
				t.Fatal(err)
			}
			b.Write(line)
			b.WriteByte('\n')
		}
		if err := os.WriteFile(filepath.Join(proj, id+".jsonl"), []byte(b.String()), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// The task somebody asked for three times, a rarer pair buried in the
	// middle of one session — which is what the old rule would have taken —
	// and three other tasks, so the subject is distinctive among the titles
	// rather than being all of them. A phrase in every title has no rarity
	// left and the floor rejects it, which is the right answer for a store
	// that has only ever been asked one thing.
	write("s1", "the shard rebalance keeps flapping under load",
		"quokka thimble stalls whenever the collector wakes")
	write("s2", "the shard rebalance dropped a replica again")
	write("s3", "why does the shard rebalance take four minutes")
	write("s4", "update the readme wording and the badges")
	write("s5", "bump the linter and fix what it finds")
	write("s6", "write the release notes for this week")
	// An agent-run session opens with the envelope its harness wrote, and its
	// title is that envelope. On a real store the phrase recurring across
	// titles was this family — a fleet's paperwork.
	write("a1", "<teammate-message teammate_id=\"team-lead\">verify the shard rebalance report</teammate-message>")
	write("a2", "<teammate-message teammate_id=\"team-lead\">verify the replica report again</teammate-message>")
	write("a3", "<teammate-message teammate_id=\"team-lead\">verify the collector report once more</teammate-message>")

	dir := index.DefaultDir()
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}

	got := suggestFirstQuery(dir)
	if !strings.Contains(got, "shard") || !strings.Contains(got, "rebalance") {
		t.Fatalf("suggestion = %q, want the task the titles keep coming back to", got)
	}
	if strings.Contains(got, "quokka") || strings.Contains(got, "thimble") {
		t.Errorf("suggestion = %q, which is a phrase from the middle of one session", got)
	}
	if strings.Contains(strings.ToLower(got), "teammate") || strings.Contains(strings.ToLower(got), "verify") {
		t.Errorf("suggestion = %q, which comes from a harness envelope", got)
	}
}

// A store whose titles are all an agent's own reports still gets a suggestion:
// the message scan stays as the fallback.
func TestSuggestFallsBackWhenNoTitleIsAPersons(t *testing.T) {
	tmp := hermeticEnv(t)
	root := filepath.Join(tmp, "claude")
	proj := filepath.Join(root, "-work-app")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_CLAUDE_ROOT", root)
	stamp := time.Now().UTC().Add(-time.Hour).Format(time.RFC3339)
	write := func(id string, texts ...string) {
		var b strings.Builder
		for _, text := range texts {
			line, err := json.Marshal(map[string]any{
				"type": "user", "sessionId": id, "cwd": "/work/app", "timestamp": stamp,
				"message": map[string]any{"role": "user", "content": text},
			})
			if err != nil {
				t.Fatal(err)
			}
			b.Write(line)
			b.WriteByte('\n')
		}
		if err := os.WriteFile(filepath.Join(proj, id+".jsonl"), []byte(b.String()), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// Every title is a harness envelope; the bodies carry the real subject.
	write("b1", "<system-reminder>carry on</system-reminder>", "the jwks rotation broke login again")
	write("b2", "<system-reminder>carry on</system-reminder>", "jwks rotation cache is still stale")
	write("b3", "<system-reminder>carry on</system-reminder>", "please update the readme wording")
	write("b4", "<system-reminder>carry on</system-reminder>", "update readme header image")
	write("b5", "<system-reminder>carry on</system-reminder>", "update readme badges please")

	dir := index.DefaultDir()
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	if got := suggestFirstQuery(dir); !strings.Contains(got, "jwks") {
		t.Fatalf("suggestion = %q, want the fallback to find the subject in the messages", got)
	}
}

// registrySurfaces is the order the registry lists them in, and the order the
// parity table on the harnesses page prints them in.
var registrySurfaces = []string{
	"digest", "prompt", "pre_tool", "failure",
	"compaction_reset", "compaction_capture", "compaction_packet",
	"mcp", "handoff", "session_end", "rules", "statusline", "reader",
}

type surfaceClaim struct {
	Status string `json:"status"`
	Proof  string `json:"proof"`
	Note   string `json:"note"`
}

func (c surfaceClaim) claimed() bool { return c.Status == "yes" || c.Status == "partial" }

// surfaceHooks are the hook subcommands that carry each surface. A generated
// file names the subcommand it runs, so reading every file an install writes
// says which surfaces it wires without a list kept beside the installers.
// hook-antigravity is one command for every PreInvocation, and answers the
// digest, the question, a failed step and a new checkpoint (hook_antigravity.go).
// hook-codewhale is one command for message_submit, tool_call_before and
// session_end (hook_codewhale.go). hook-stop is goose's Stop block, which
// carries the edit line, the fix pair and a compaction (hook_deferred.go).
var surfaceHooks = map[string][]string{
	"digest":           {"hook-context", "hook-goose", "hook-antigravity", "hook-codewhale"},
	"prompt":           {"hook-prompt", "hook-goose-prompt", "hook-antigravity", "hook-codewhale"},
	"pre_tool":         {"hook-tool", "hook-codewhale", "hook-antigravity", "hook-stop"},
	"failure":          {"hook-tool-after", "hook-antigravity", "hook-stop"},
	"compaction_reset": {"hook-precompact", "hook-antigravity", "hook-stop"},
	"session_end":      {"hook-session-end", "hook-codewhale"},
}

// statusMarks are the calls a generated plugin makes to show something in the
// host's own UI: pi's footer, opencode 1.x and Kilo's toast, Hermes's recall
// indicator, Command Code's footer segment, Amp's status item. opencode's TUI
// plugin execs `deja statusline`.
var statusMarks = []string{"setStatus(", "showToast(", "RecallStatus", `["statusline"]`, "createStatusItem("}

// statuslineRun is a host's status line command running deja, through the
// launcher or the binary, quoted or not.
var statuslineRun = regexp.MustCompile(`deja[^\s"']*["']? statusline\b`)

// reasonixSurfaces maps the extension points deja's Reasonix extension
// subscribes to onto surfaces. The installed manifest is just "reasonix-ext",
// so the protocol's own list is what says what it does.
var reasonixSurfaces = map[string][]string{
	"session.start":      {"digest"},
	"input.receive":      {"prompt"},
	"tool.after":         {"pre_tool", "failure"},
	"compaction.prepare": {"compaction_reset"},
	"session.end":        {"session_end"},
}

var hookToken = regexp.MustCompile(`hook-[a-z]+(?:-[a-z]+)*`)

// wiredSurfaces installs a harness's targets into the test's home and reads
// back which surfaces the files they wrote reach.
func wiredSurfaces(t *testing.T, harness string) map[string]bool {
	t.Helper()
	tmp := filepath.Dir(os.Getenv("DEJA_INDEX_DIR"))
	base := harness
	if v, ok := map[string]string{"claude": "claude-code", "copilot-chat": "vscode"}[harness]; ok {
		base = v
	}
	auto := base + "-auto"
	if harness == "claude" {
		auto = "claude-auto"
	}
	// VS Code forks write their status bar extension only when the IDE's
	// directory is on disk, so seed it in the test home before installing.
	if harness == "windsurf" || harness == "kiro" {
		_ = os.MkdirAll(filepath.Join(os.Getenv("HOME"), "."+harness, "extensions"), 0o755)
	}
	for _, target := range []string{base, auto} {
		if !slices.Contains(installTargetNames(), target) {
			continue
		}
		if _, err := installTarget(target, "/bin/deja", false); err != nil {
			t.Fatalf("%s: install %s: %v", harness, target, err)
		}
	}
	var text strings.Builder
	_ = filepath.Walk(tmp, func(p string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || info.Size() > 4<<20 {
			return nil //nolint:nilerr // an unreadable entry is skipped, not a failure
		}
		b, _ := os.ReadFile(p) //nolint:gosec // reads a temp home the test built
		text.Write(b)
		text.WriteByte('\n')
		return nil
	})
	// opencode writes the plugin its installed major version loads; with
	// none installed that is 2.x, and the 1.x plugin is what a 1.x user gets.
	if harness == "opencode" {
		text.WriteString(opencodeLegacyPluginJS("/bin/deja"))
	}
	all := text.String()
	tokens := map[string]bool{}
	for _, m := range hookToken.FindAllString(all, -1) {
		tokens[m] = true
	}
	got := map[string]bool{}
	for surface, subs := range surfaceHooks {
		for _, sub := range subs {
			if tokens[sub] {
				got[surface] = true
			}
		}
	}
	for _, m := range statusMarks {
		if strings.Contains(all, m) {
			got["statusline"] = true
		}
	}
	if statuslineRun.MatchString(all) {
		got["statusline"] = true
	}
	switch harness {
	case "aider":
		// No hooks: the target puts deja's context file in aider's read:
		// list, which aider re-reads on every message and `deja aider` fills.
		// The same wrapper serves the per-message recall (aider_live.go).
		got["digest"] = strings.Contains(all, "aider-context.md")
		got["prompt"] = got["digest"]
	case "claude":
		// A target of its own, deliberately outside --auto.
		got["statusline"] = slices.Contains(installTargetNames(), "statusline")
	case "devin":
		// PostCompaction is Devin's reset event and the context hook answers
		// it — no hook-precompact enters the wiring.
		got["compaction_reset"] = strings.Contains(all, "PostCompaction") && tokens["hook-context"]
	case "crush":
		// PreToolUse is Crush's only event; `hook-tool --crush` carries the
		// digest, the newest message, the previous failure and a summary
		// read from crush.db (hook_crush.go).
		if strings.Contains(all, "hook-tool --crush") {
			for _, s := range []string{"digest", "prompt", "failure", "compaction_reset"} {
				got[s] = true
			}
		}
	case "goose":
		// The SessionStart hook prints deja's status line as goose's banner.
		got["statusline"] = tokens["hook-goose"]
	case "gemini":
		// PreCompress fires on every attempt and nothing fires after one, so
		// the prompt hook catches a compaction up from the transcript.
		got["compaction_reset"] = tokens["hook-prompt"]
	case "zcode":
		// No compaction event: the prompt and tool hooks catch one up from
		// the CLI database (hook_compaction.go catchUpZCodeCompaction).
		got["compaction_reset"] = tokens["hook-prompt"]
	case "codewhale":
		// No compaction event: hook-codewhale catches one up from the history
		// CodeWhale saves before it compacts.
		got["compaction_reset"] = tokens["hook-codewhale"]
		// tool_call_after parks the fix pair for the next tool_call_before
		// or message_submit.
		got["failure"] = strings.Contains(all, "hook-codewhale tool_call_after")
	case "junie":
		// No PostToolUse and no PreCompact: the --junie prompt and tool hooks
		// read a failed command and a compaction out of events.jsonl
		// (hook_junie.go).
		if strings.Contains(all, "--junie") {
			got["failure"] = tokens["hook-tool"] || tokens["hook-prompt"]
			got["compaction_reset"] = got["failure"]
		}
	case "commandcode":
		// No compaction shell hook: PreToolUse reads a compaction out of the
		// transcript it names (runCommandCodeTool).
		got["compaction_reset"] = tokens["hook-tool"]
	case "reasonix":
		if strings.Contains(all, "reasonix-ext") {
			for _, point := range reasonixIntercepts {
				for _, s := range reasonixSurfaces[point] {
					got[s] = true
				}
			}
			// The extension publishes its status through host/ui/publish.
			got["statusline"] = true
		}
	}
	return got
}

// The registry records each hook surface per harness, and the parity rule is
// that every one is wired or carries a proven blocker. A bool per harness
// called Cline wired with three surfaces missing, and Crush wired with one
// (#4801). This holds every surface claim to what install writes, both ways.
func TestEverySurfaceClaimMatchesWhatInstallWires(t *testing.T) {
	hermeticEnv(t)
	b, err := os.ReadFile(filepath.Join("..", "..", "docs", "registry", "registry.json"))
	if err != nil {
		t.Fatal(err)
	}
	var reg struct {
		Harnesses []struct {
			ID           string                  `json:"id"`
			ParserSource string                  `json:"parser_source"`
			Surfaces     map[string]surfaceClaim `json:"surfaces"`
			Capabilities *struct {
				MCP     bool   `json:"mcp"`
				Handoff string `json:"handoff"`
			} `json:"capabilities"`
		} `json:"harnesses"`
	}
	if err := json.Unmarshal(b, &reg); err != nil {
		t.Fatal(err)
	}
	checked := 0
	for _, h := range reg.Harnesses {
		if h.ID == "deja" {
			continue
		}
		checked++
		t.Run(h.ID, func(t *testing.T) {
			hermeticEnv(t)
			if len(h.Surfaces) != len(registrySurfaces) {
				t.Fatalf("surfaces lists %d entries, want exactly %v", len(h.Surfaces), registrySurfaces)
			}
			for _, s := range registrySurfaces {
				c, ok := h.Surfaces[s]
				if !ok {
					t.Fatalf("no %s entry", s)
				}
				checkSurfaceProof(t, s, c)
			}
			s := h.Surfaces
			wired := wiredSurfaces(t, h.ID)
			for _, surface := range []string{"digest", "prompt", "pre_tool", "failure", "compaction_reset", "session_end", "statusline"} {
				if wired[surface] && !s[surface].claimed() {
					t.Errorf("install wires %s, the registry says %s (%s)", surface, s[surface].Status, s[surface].Note)
				}
				if !wired[surface] && s[surface].claimed() {
					t.Errorf("the registry claims %s, nothing install writes reaches it", surface)
				}
			}
			// Capture and the packet hang off the reset: nothing captures a
			// compaction the host was never seen doing, and the packet needs a
			// later hook to ride.
			if s["compaction_capture"].claimed() && !s["compaction_reset"].claimed() {
				t.Errorf("compaction_capture is claimed without a compaction event")
			}
			if s["compaction_packet"].claimed() {
				if !s["compaction_reset"].claimed() {
					t.Errorf("compaction_packet is claimed without a compaction event")
				}
				if !s["digest"].claimed() && !s["prompt"].claimed() && !s["pre_tool"].claimed() {
					t.Errorf("compaction_packet is claimed with no later hook to carry it")
				}
			}
			if got := s["mcp"].claimed(); got != h.Capabilities.MCP {
				t.Errorf("mcp surface %s, capabilities.mcp %v", s["mcp"].Status, h.Capabilities.MCP)
			}
			want := map[string]string{"exec": "yes", "paste": "partial"}[h.Capabilities.Handoff]
			if s["handoff"].Status != want {
				t.Errorf("handoff is %s and the surface says %s, want %s", h.Capabilities.Handoff, s["handoff"].Status, want)
			}
			id := h.ID
			if v, ok := map[string]string{"claude": "claude-code", "copilot-chat": "vscode"}[id]; ok {
				id = v
			}
			if got := slices.Contains(rulesHarnesses, id); got != s["rules"].claimed() {
				t.Errorf("rules sync writes for this harness: %v, the registry says %s", got, s["rules"].Status)
			}
			// Every harness in the registry has a reader; it can lose
			// something, it cannot be missing.
			if h.ParserSource == "" || !s["reader"].claimed() {
				t.Errorf("reader is %s with parser %q", s["reader"].Status, h.ParserSource)
			}
		})
	}
	if checked < 30 {
		t.Fatalf("only %d harnesses read; the test is looking at the wrong field", checked)
	}
}

// checkSurfaceProof holds every cell to the rule: a claim says how it was
// shown, a gap says whether it is a proven blocker or work, and both say why.
func checkSurfaceProof(t *testing.T, surface string, c surfaceClaim) {
	t.Helper()
	note := strings.TrimSpace(c.Note)
	switch c.Status {
	case "yes", "partial":
		if c.Proof != "stand" && c.Proof != "fixture" {
			t.Errorf("%s is %s with proof %q, want stand or fixture", surface, c.Status, c.Proof)
		}
		if c.Status == "partial" && len(note) < 40 {
			t.Errorf("%s is partial and the note (%q) does not say what is missing", surface, note)
		}
	case "no":
		switch c.Proof {
		case "blocked":
			// A blocker is a claim about the host, so it names where in the
			// host it was found.
			if len(note) < 50 {
				t.Errorf("%s is blocked and the note (%q) does not say where in the host", surface, note)
			}
		case "todo":
			if !strings.Contains(note, "#") {
				t.Errorf("%s is todo and the note (%q) cites no issue", surface, note)
			}
		default:
			t.Errorf("%s is no with proof %q, want blocked or todo", surface, c.Proof)
		}
	default:
		t.Errorf("%s status %q is not yes, partial or no", surface, c.Status)
	}
}

// The parity table on the harnesses page is rendered from the registry, so a
// claim changed without re-running the generator shows up here rather than on
// the published page.
func TestTheParityTableIsRenderedFromTheRegistry(t *testing.T) {
	page, err := os.ReadFile(filepath.Join("..", "..", "docs", "guide", "harnesses.html"))
	if err != nil {
		t.Fatal(err)
	}
	i := bytes.Index(page, []byte("<!-- parity:start -->"))
	j := bytes.Index(page, []byte("<!-- parity:end -->"))
	if i < 0 || j < i {
		t.Fatal("the harnesses page has no parity markers — run `go run ./scripts/genmatrix`")
	}
	table := string(page[i:j])
	b, err := os.ReadFile(filepath.Join("..", "..", "docs", "registry", "registry.json"))
	if err != nil {
		t.Fatal(err)
	}
	var reg struct {
		Harnesses []struct {
			ID       string                  `json:"id"`
			Surfaces map[string]surfaceClaim `json:"surfaces"`
		} `json:"harnesses"`
	}
	if err := json.Unmarshal(b, &reg); err != nil {
		t.Fatal(err)
	}
	for _, h := range reg.Harnesses {
		for s, c := range h.Surfaces {
			cell := `data-surface="` + h.ID + "/" + s + `" data-status="` + c.Status + `"`
			if !strings.Contains(table, cell) {
				t.Errorf("parity table is missing %s %s=%s — run `go run ./scripts/genmatrix`", h.ID, s, c.Status)
			}
		}
	}
}

// The plist writer escapes the binary path because a home directory is a
// user-chosen string. The unit file wrote it raw, so a path holding a systemd
// specifier, a variable, a C escape or a quote ran something other than what
// deja installed — or did not parse at all (#2621).
func TestSyncTimerUnitEscapesThePath(t *testing.T) {
	cases := []struct {
		name, exe, want string
	}{
		{"percent is a specifier", "/Users/50%off/bin/deja", `ExecStart="/Users/50%%off/bin/deja" sync`},
		{"dollar is a variable", "/Users/me/$HOME/deja", `ExecStart="/Users/me/$$HOME/deja" sync`},
		{"backslash is an escape", `/Users/me/a\b/deja`, `ExecStart="/Users/me/a\\b/deja" sync`},
		{"a quote ends the string", `/Users/me/"q"/deja`, `ExecStart="/Users/me/\"q\"/deja" sync`},
		{"a space needs the quotes", "/Users/me/My Tools/deja", `ExecStart="/Users/me/My Tools/deja" sync`},
		{"an ordinary path is untouched", "/usr/local/bin/deja", `ExecStart="/usr/local/bin/deja" sync`},
		// A backslash before a quote: each is escaped once, and neither
		// escape is fed back through the other.
		{"a backslash then a quote", `/Users/me/a\"b/deja`, `ExecStart="/Users/me/a\\\"b/deja" sync`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			unit := syncAutoService(tc.exe)
			if !strings.Contains(unit, tc.want) {
				t.Fatalf("want the line\n  %s\nin\n%s", tc.want, unit)
			}
		})
	}
}

// The timer file names no path, so it has nothing to escape; this only pins
// that the two files stay in step if one of them grows one.
func TestSyncTimerFilesNameTheSameUnit(t *testing.T) {
	if !strings.Contains(syncAutoTimer(), "[Timer]") || strings.Contains(syncAutoTimer(), "ExecStart") {
		t.Fatalf("the timer file changed shape:\n%s", syncAutoTimer())
	}
}

// The path is read back out of the unit to report on the timer (#2636), so the
// two directions have to meet.
func TestSystemdEscapeRoundTrips(t *testing.T) {
	for _, p := range []string{
		"/usr/local/bin/deja",
		`/Users/me/a\b/deja`,
		`/Users/me/"q"/deja`,
		"/Users/50%off/bin/deja",
		"/Users/me/$HOME/deja",
		`/a\"b%c$d/deja`,
		"/Users/My Tools/deja",
	} {
		if got := systemdUnescape(systemdEscape(p)); got != p {
			t.Errorf("round trip changed the path:\n in  %q\n out %q\n via %q", p, got, systemdEscape(p))
		}
	}
}

// And the plist's, which is the same question for the other format.
func TestXMLEscapeRoundTrips(t *testing.T) {
	for _, p := range []string{
		"/usr/local/bin/deja",
		`/Users/John & "Jane" Smith/bin/deja`,
		"/Users/&amp;lt;/deja",
		"/Users/o'brien/bin/deja",
	} {
		if got := xmlUnescape(xmlEscape(p)); got != p {
			t.Errorf("round trip changed the path:\n in  %q\n out %q\n via %q", p, got, xmlEscape(p))
		}
	}
}

// "try fewer words" is right in direction and empty in content: the reader has
// to guess which of their words to drop, while deja read the per-term counts to
// decide there was no intersection (#826).
func TestNoMatchesNamesTheTermsThatMatchAlone(t *testing.T) {
	hermeticEnv(t)
	store := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-proj")
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	lines := []string{
		`{"type":"user","message":{"role":"user","content":"the sewage macerator showed blockage"},"timestamp":"2026-07-01T10:00:00Z","sessionId":"a1","cwd":"/proj"}`,
		`{"type":"user","message":{"role":"user","content":"the bilge pump impeller was worn"},"timestamp":"2026-07-02T10:00:00Z","sessionId":"a2","cwd":"/proj"}`,
	}
	if err := os.WriteFile(filepath.Join(store, "a.jsonl"), []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRunStderr(t, "index"); err != nil {
		t.Fatal(err)
	}

	dir := indexDirForTest()
	got := termCountLine(dir, "macerator pump")
	if !strings.Contains(got, `"macerator" in 1`) || !strings.Contains(got, `"pump" in 1`) {
		t.Errorf("the counts are not named: %q", got)
	}
	if !strings.Contains(got, "no session has them together") {
		t.Errorf("the line does not say what it means: %q", got)
	}

	// One known word, one not: name the one that can be kept.
	mixed := termCountLine(dir, "macerator zzzqqq")
	if !strings.Contains(mixed, `"macerator" in 1`) || strings.Contains(mixed, "zzzqqq") {
		t.Errorf("mixed query: %q", mixed)
	}

	// Nothing to drop: every term is unknown, and a row of zeroes is noise.
	if got := termCountLine(dir, "zzzqqq wwwqqq"); got != "" {
		t.Errorf("a query of unknown words got a line: %q", got)
	}
	// A single term has nothing to intersect with.
	if got := termCountLine(dir, "macerator"); got != "" {
		t.Errorf("a one-word query got a line: %q", got)
	}
	// And the whole no-match path still prints its own line.
	out, err := captureRunStderr(t, "macerator", "zzzqqq")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "on their own") {
		t.Errorf("the counts line did not reach the no-match output:\n%s", out)
	}
}

// drawableCharDevice returns a character device standing in for a terminal.
//
// This used to be os.DevNull, which is a character device and was the handiest
// one. It cannot be any more: the null device is excluded on purpose now,
// because taking it for a terminal made `deja index >/dev/null` paint the live
// display into the sink and print none of the plain lines on stderr. The
// subject of this test is TERM and NO_COLOR, not which device stands in, so
// the stand-in moves and the assertions stay as they were.
func drawableCharDevice(t *testing.T) *os.File {
	t.Helper()
	name := "/dev/zero"
	if runtime.GOOS == "windows" {
		// NUL is excluded there too since #1097's windows half, so the console
		// input device is the only stand-in left; a runner without a console
		// skips.
		name = "CONIN$"
	}
	f, err := os.Open(name)
	if err != nil {
		t.Skipf("no character device to stand in for a terminal: %v", err)
	}
	t.Cleanup(func() { _ = f.Close() })
	fi, err := f.Stat()
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode()&os.ModeCharDevice == 0 {
		t.Skipf("%s is not a character device here", name)
	}
	return f
}

// TERM=dumb is a terminal that can do none of this — emacs shell-mode, a CI
// shell, an editor's built-in console. NO_COLOR was honoured and this was not,
// so those readers got escape sequences as literal text (#903).
func TestDumbTerminalGetsNoDrawing(t *testing.T) {
	f := drawableCharDevice(t)

	t.Setenv("NO_COLOR", "")
	t.Setenv("TERM", "xterm-256color")
	if !defaultLogoWanted(f) {
		t.Fatal("a character device with a real TERM stopped being drawable")
	}
	t.Setenv("TERM", "dumb")
	if defaultLogoWanted(f) {
		t.Error("TERM=dumb still draws")
	}
	t.Setenv("TERM", "xterm-256color")
	t.Setenv("NO_COLOR", "1")
	if defaultLogoWanted(f) {
		t.Error("NO_COLOR stopped being honoured")
	}
}

func indexSearch(dir string, o search.Options) ([]model.Session, error) {
	r, err := index.SearchDetailed(dir, o)
	return r.Sessions, err
}

// trimBriefTitle bounds a title for the brief and strips what a terminal would
// act on rather than print. Titles are user-typed text arriving verbatim: a
// carriage return rewinds the line, an escape recolours the rest of the
// screen, a bell rings on every refresh. The `recent` lines have printed them
// raw since they existed; this is one place for all of them (#634 set the same
// rule for the status bar).
func trimBriefTitle(t string) string { return trimBriefTitleTo(t, briefTitleMax) }

// helpHidden names the commands deliberately kept out of `deja help`. They are
// wired by `deja install` and read by a harness, not typed by a person: the
// session-start and compaction hooks, goose's two, the refresh worker and the
// warmup probe. The list exists so the omission is a decision with a reason
// rather than drift — help listed five hook commands and hid five, including
// hook-context, because the text is hand-maintained and nothing compared it
// with the dispatch table (#1654). TestHelpCoversEveryCommand is that
// comparison.
var helpHidden = buildHelpHiddenCommands()

// completionHiddenCommands are the ones deja runs for itself. Leaving a command
// out is a decision recorded here rather than a name missing from three scripts.
var completionHiddenCommands = buildCompletionHiddenCommands()

func hookDigest(dir string) string {
	digest, _, _, _, _, _, _ := hookDigestResult(dir)
	return digest
}

func hookDigestResult(dir string) (string, int, int64, []string, int, []string, []string) {
	return hookDigestResultFor(dir, "", "")
}

// unplacedFiles counts transcripts under root that the harness's own filter did
// not pick up. A harness that changes its layout in a new version presents
// exactly this way: quietly fewer sessions, no error, and a directory size that
// still looks right (#701).
//
// unplacedFiles counts the transcripts under a root that deja did not read,
// split by whether it declined them on purpose. Everything was one number
// before, and on a machine that spawns subagents most of it is the deliberate
// skip: this store reported "1192 not recognised here" of which 596 were
// subagent transcripts deja is written to leave alone (#1384). A number that
// large reads as the tool failing to understand the user's own history, which
// is the one thing doctor exists to rule out.
func unplacedFiles(root string, seen []string, skipped func(string) bool) (unread, byRule int) {
	return unplacedFilesIn(root, seen, skipped, false)
}

func skillIsTheCommand(harness string) bool {
	return slices.Contains(skillIsTheCommandHarnesses(), harness)
}

// doctorHarnesses lists every store, the form `deja doctor --all` prints.
func doctorHarnesses(w io.Writer, dir string) {
	doctorHarnessStores(w, dir, true)
}

// resetEnvironmentOnce lets a test ask for a session's first block again. The
// marker file is per store and a test's store is its own; the process flag is
// shared, so it is reset under the same lock the server takes.
func resetEnvironmentOnce() {
	environmentMu.Lock()
	environmentSpent = false
	environmentMu.Unlock()
}

// printFixPairs writes the pairs in the prose form, one remedy per pair.
func printFixPairs(stdout io.Writer, pairs []index.FixPair) error {
	return printFixPairsFrom("", stdout, pairs)
}

// copySources maps each session in ss that is a fork or copy of another in ss
// to that other's position. A fork that names its source is believed; an
// unmarked one (claude --fork-session, opencode --fork) is read whole and
// judged by what it holds, which costs a read only for the pairs maybe says
// look alike (#5320).
func copySources(dir string, ss []model.Session, maybe func(src, c int, sameShape bool) bool) map[int]int {
	return copySourcesAmong(dir, ss, maybe, nil, nil)
}

// latestUserRequest returns the newest user turn in an antigravity transcript,
// or "" when the file holds none it can read.
func latestUserRequest(path string) string {
	return latestUserRequestFromSteps(transcriptTailSteps(path))
}

// latestToolFailure is the output of the newest tool step that ended badly, or
// "" when the last thing the agent did was not a failure. PostToolUse gets the
// error but its contract allows no answer at all (checked on antigravity-cli
// 1.1.13: it must return an empty object), so the moment a command fails is
// reachable only from the invocation that follows it.
//
// Only the newest tool step is read: a failure two steps back has already been
// answered or worked around, and injecting it again would be talking about
// something the agent has moved on from.
func latestToolFailure(path string) string {
	return latestToolFailureFromSteps(transcriptTailSteps(path))
}

// builtNote is what the first session start after a build says about what
// the build found: the counts, and on a second line one finding when the
// history holds one. Someone who installed deja from a marketplace
// never runs `deja install` and never sees the proof the CLI prints; for
// them the index was built in silence and recall stayed invisible until it
// happened to fire (#3073). Once per index.
func builtNote(dir string) string { return builtNoteFor(dir, true) }

// runHookPrecompact is deliberately best effort: Claude must be able to
// compact even when the input is incomplete or the index cannot start.
func runHookPrecompact(dir string) { runHookPrecompactFor(dir, "") }

// runHookContext prints session-start context. plain=false emits the Claude
// Code / Codex hook JSON envelope; plain=true prints the bare digest for
// hosts that inject raw text (the opencode plugin).
func runHookContext(dir string, plain bool) error {
	return runHookContextMode(dir, plain, false)
}

// runHookPrompt is the UserPromptSubmit hook: search the user's own prompt
// against the index (relevance, not recency) and inject a compact hint only
// when something genuinely matches. Empty output means stay silent — a hook
// that talks every turn is wallpaper. It never builds or refreshes the index:
// this path runs on every prompt and must stay ~milliseconds.
func runHookPrompt(dir string, stdin io.Reader, stdout io.Writer) error {
	return runHookPromptMode(dir, stdin, stdout, false)
}

// askedRecently reports whether this reader already had an answer to the same
// question inside the window. Scanned from the end, like the cooldowns: the
// file is append-only and the newest rows are the ones that matter.
func askedRecently(dir, sid, key string, window time.Duration) bool {
	return loadSeen(dir).asked(sid, key, window)
}

// toolHookLine is what the agent is told, or "" for the silence that is the
// default answer. It gates on the tool: a command line is worth saying only for
// the tool that runs a command, and a file's history only before the file is
// changed — Read, Glob and NotebookRead carry a file_path too, and a hook wired
// with a wide matcher would otherwise fire on every one of them.
// toolHookLine is the line for an action when nothing has been said yet.
func toolHookLine(dir, cwd string, input toolHookInput) string {
	return toolHookLineSkipping(dir, cwd, input, func(string) bool { return false })
}

// commandHookLine is the line for a command when nothing has been said yet.
func commandHookLine(dir, cwd, cmd string) string {
	return commandHookLineSkipping(dir, cwd, cmd, "", func(string) bool { return false })
}

func fileHookLine(dir, cwd, path string) string {
	return fileHookLineOutsideWithEdit(dir, cwd, path, "", nil)
}

// fileHookLineOutside leaves out the sessions self reports: the one the hook
// fires in and what counts as it. It is in the index once it has touched the
// file, and as the newest it was counted and its own last reply quoted back as
// the file's history (#4380).
func fileHookLineOutside(dir, cwd, path string, self func(index.SessionMeta, string) bool) string {
	return fileHookLineOutsideWithEdit(dir, cwd, path, "", self)
}

// promotedDecisionFor returns the newest accepted promoted note belonging to one
// of these sessions. Accepted only, for the reason projectConventions gives: a
// decision later reversed carries a non-accepted state, and LoadPromotedNotes
// keeps the latest state per source.
func promotedDecisionFor(metas []index.SessionMeta) string {
	return promotedDecisionForFile("", metas)
}

func promotedDecisionForFile(path string, metas []index.SessionMeta) string {
	return promotedDecisionForFileIn(index.DefaultDir(), path, metas)
}

// fileHookRanLine is the command the sessions that touched this file ran and
// the transcript saw finish cleanly, or "" when there is no such pattern.
//
// Reads one sidecar and the manifest rows the caller already has: this is on
// the per-action path, where the file line's own history cost 133 ms before
// #3605 moved it into the manifest.
func fileHookRanLine(dir string, metas []index.SessionMeta) string {
	return fileHookRanLineFor(dir, "", metas)
}

// weekNoteAt is that one line, or "". It goes out on the JSON path only — the
// plain path is the model's context, where a receipt to the person is noise.
//
// The first session after install starts the clock rather than reporting:
// "deja this week: 1 recall" a minute after the install would be the hook
// counting itself.
func weekNoteAt(dir string, now time.Time) string { return weekNoteFor(dir, now, true) }

// refreshAiderContext regenerates the read-only file from the same digest the
// hooks inject elsewhere, so aider users get what every other harness gets.
func refreshAiderContext(dir string) error {
	return writeAiderContext(aiderContextBody(dir))
}

func updateClaudeSessionStartHook(root map[string]any, exe string, uninstall bool) map[string]any {
	root = updateClaudeHook(root, "SessionStart", hookRun(exe, "hook-context"), "", uninstall)
	return root
}

// hermesCatalogEdits are where the copy in extensions/hermes differs from the
// one deja install writes: it comes from the Hermes plugin catalog rather than
// from deja, and finds deja on PATH.
var hermesCatalogEdits = [][2]string{
	{"Generated by deja install — safe to delete; regenerate with: deja install hermes-auto.",
		"From github.com/vshulcz/deja-vu, extensions/hermes. Installed by hermes plugins install."},
	{"the deja binary is not at %s — install it with:", "the deja binary is not on PATH — install it with:"},
	{", then run \"\n            \"deja install hermes-auto\" % DEJA", "\""},
}

// hermesCatalogPy is the provider as extensions/hermes ships it: the same
// code with deja taken from PATH, since nothing wrote the path in.
func hermesCatalogPy() string {
	py := hermesMemoryPy("deja")
	for _, r := range hermesCatalogEdits {
		if strings.Count(py, r[0]) != 1 {
			panic("hermesCatalogPy: the provider no longer holds " + r[0])
		}
		py = strings.Replace(py, r[0], r[1], 1)
	}
	return py
}

func traeIDEHooksPath() string { return traeIDEPresent()[0].hooksPath() }

func installZCodeHooks(exe string, uninstall bool) (installResult, error) {
	return installZCodeHooksWith(exe, uninstall, !uninstall && zcodeConfigIsLiveRuntimeConfig())
}

func recent(dir string, n int) ([]model.Session, error) {
	return recentMatching(dir, n, search.Options{})
}

// logoLines lays the info column beside the mark, vertically centred, and
// returns the composed lines. The live build display repaints these, so the
// layout has to be shared with printLogo rather than reimplemented.
func logoLines(info []string) []string { return logoLinesArt(catArt, info) }

// printLogo lays the info column beside the mark, vertically centred.
func printLogo(w io.Writer, info []string) { printLogoMood(w, info, moodReady) }

func loadAll(h string) []model.Session {
	var ss []model.Session
	for _, store := range sources.StoresToRead() {
		if (h == "" || store.Name == h) && store.Load != nil {
			ss = append(ss, store.Load()...)
		}
	}
	return ss
}

func recallContext(dir, q string) (string, error) {
	text, _, _, _, err := recallContextResult(dir, q, "")
	return text, err
}

// recallContextResult keeps the four-value shape its callers and tests read.
func recallContextResult(dir, q, harness string) (string, int, int64, []string, error) { //nolint:unparam // same shape as recallTextResult; changes_block_test.go calls it
	text, sessions, raw, ids, _, _, err := recallContextResultFrom(dir, q, harness)
	return text, sessions, raw, ids, err
}

// mcpContextFlag reports whether `deja mcp` was asked for the profile surface.
func mcpContextFlag(args []string) bool {
	for _, a := range args {
		if a == "--context-search" || a == "-context-search" {
			return true
		}
	}
	return false
}

func recallText(dir, q, harness string, limit, budget int) (string, error) {
	text, _, _, _, err := recallTextResult(dir, q, harness, limit, 0, budget)
	return text, err
}

// policyFilterHits drops search hits the trust policy blocks on this
// activation path. The default policy blocks nothing.
func policyFilterHits(activation string, hits []search.Hit) []search.Hit {
	kept, _ := policyFilterHitsCounted(activation, hits)
	return kept
}

// printNoMatches says how big the store actually is, not how many sessions the
// query happened to load.
//
// On the index-backed path the loaded set is empty by definition when nothing
// matched, so this printed "no matches in 0 indexed sessions" for a perfectly
// healthy index — and internal/index/manifest.go reserves that exact sentence
// as the signature of a corrupt store, on the grounds that a reader told the
// index holds zero sessions concludes the tool is broken rather than that
// their query missed. It fired on every ordinary miss, so the signature could
// not be used to recognise the failure it was written for (#637).
func printNoMatches(w io.Writer, dir, q string, regex bool) (mistypedCommand bool) {
	return printNoMatchesHinted(w, dir, q, regex, false)
}

// scrubBackupsIn is every copy this command left in one directory, for the tests
// and for the line that tells a reader where to look.
func scrubBackupsIn(dir string) []string {
	matches, err := filepath.Glob(filepath.Join(dir, "*"+scrubBackupExt+"*"))
	if err != nil {
		return nil
	}
	sort.Strings(matches)
	return matches
}

// cliSkillMeta is what a skill registry reads to know what the skill needs:
// ClawHub serves skills/deja-search as vshulcz/deja-search and checks the
// declared binary and install specs against what the skill does. It is
// OpenClaw's nested shape, which the Agent Skills spec does not allow: there
// metadata maps strings to strings, and Crush drops a skill whose metadata
// does not decode that way (#4378). So only the registry copy carries it; the
// file deja installs into ~/.agents/skills, which Crush reads, goes without.
// OpenClaw reads that directory too and loads a skill with no metadata.openclaw
// block as always eligible, which is right on a machine deja was installed on.
const cliSkillMeta = `metadata:
  openclaw:
    homepage: https://vshulcz.github.io/deja-vu/guide/memory-for-openclaw.html
    requires:
      bins:
        - deja
    install:
      - kind: brew
        formula: deja-vu
        bins: [deja]
      - kind: node
        package: "@vshulcz/deja-vu"
        bins: [deja]`

// cliSkillRegistryFile is the copy in the repository, the one ClawHub publishes.
func cliSkillRegistryFile() string {
	return "---\nname: " + cliSkillName + "\ndescription: " + cliSkillDesc + "\n" + cliSkillMeta + "\n---\n\n" + cliSkillBody + "\n"
}

// statuslineMemoryLine renders it. A count is a statistic; what the earlier
// session was about is a memory, so the title wins when there is one.
func statuslineMemoryLine(m fileMemory) string {
	return statuslineMemoryLineTo(m, statuslineMaxTitle)
}

// statuslineMemoryLineTo bounds the title to maxTitle columns — the unit
// safeForStatusline works in, since the bound exists to keep the bar inside the
// terminal. maxTitle <= 0 drops the title and falls through to the count form,
// which is what a bar too narrow to hold any of it gets.
func statuslineMemoryLineTo(m fileMemory, maxTitle int) string {
	return statuslineMemoryLineWithin(m, maxTitle, statuslineMaxName)
}

// trimStatuslineTitle makes a session title safe for a status bar. The title
// is whatever the user typed first, so it can carry a carriage return that
// rewrites the line, an ANSI escape that recolours the whole bar, or a bell.
func trimStatuslineTitle(t string) string { return safeForStatusline(t, statuslineMaxTitle) }

// emptyRecallAnswer distinguishes "there is nothing" from "there is nothing
// YET". A harness with no auto-recall wiring reaches deja over MCP and nothing
// else, so a tool result is the one place it can learn that the first index is
// still being built. Without this the agent reads a confident negative and
// tells the user they have no history.
func emptyRecallAnswer(dir, q string) string { return emptyRecallAnswerPolicy(dir, q, 0) }

// harmless are variables the code reads that cannot point it at a store.
var harmless = map[string]bool{"CI": true, "COLUMNS": true, "NO_COLOR": true, "TERM": true}

// TestScrubHelperProcess runs only as the child of the test below: it reports
// every variable that reached it with the value the parent planted.
func TestScrubHelperProcess(_ *testing.T) {
	if os.Getenv("SCRUB_PROBE") == "" {
		return
	}
	for _, kv := range os.Environ() {
		if name, value, _ := strings.Cut(kv, "="); strings.HasPrefix(value, "/leak/") {
			fmt.Println("LEAK " + name)
		}
	}
}

// Every variable deja reads is planted with a fake path in a copy of this
// binary; whatever TestMain lets through is a way for the suite to read or
// write a real store (#4178). A new variable fails here until it is scrubbed
// or named harmless.
func TestTestMainScrubsEveryVariableTheCodeReads(t *testing.T) {
	read := envNamesReadByCode(t)
	if len(read) < 50 {
		t.Fatalf("found only %d variables, the scan is broken", len(read))
	}
	cmd := exec.CommandContext(t.Context(), os.Args[0], "-test.run=^TestScrubHelperProcess$")
	cmd.Env = append(os.Environ(), "SCRUB_PROBE=1")
	for _, name := range read {
		// Runtime knobs the runtime parses before main: a path in them is a
		// fatal error, not a leak.
		if name == "PATH" || name == "GOMAXPROCS" || name == "GOGC" || name == "GOMEMLIMIT" {
			continue
		}
		cmd.Env = append(cmd.Env, name+"=/leak/"+name)
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("helper: %v\n%s", err, out)
	}
	var leaked []string
	for line := range strings.SplitSeq(string(out), "\n") {
		if name, ok := strings.CutPrefix(line, "LEAK "); ok && !harmless[name] {
			leaked = append(leaked, name)
		}
	}
	if len(leaked) > 0 {
		t.Fatalf("TestMain lets these through to the tests: %s", strings.Join(leaked, ", "))
	}
}

func envNamesReadByCode(t *testing.T) []string {
	t.Helper()
	re := regexp.MustCompile(`(?:Getenv|LookupEnv|EnvPath)\("([A-Z0-9_]+)"`)
	seen := map[string]bool{}
	for _, dir := range []string{"../../cmd", "../../internal"} {
		err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return err
			}
			src, err := os.ReadFile(path) //nolint:gosec // reads the repo's own Go sources
			if err != nil {
				return err
			}
			for _, m := range re.FindAllSubmatch(src, -1) {
				seen[string(m[1])] = true
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	names := make([]string, 0, len(seen))
	for n := range seen {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// run is one build or test invocation in a transcript: the command, then the
// output that came back.
func testRunLines(session, day, id, cmd, out string) []string {
	const cwd = "/work/app"
	ts := day + "T10:00:00Z"
	return []string{
		`{"type":"assistant","sessionId":"` + session + `","cwd":"` + cwd + `","timestamp":"` + ts +
			`","message":{"role":"assistant","content":[{"type":"tool_use","id":"` + id + `","name":"Bash",` +
			`"input":{"command":` + jsonQuoted(cmd) + `}}]}}`,
		`{"type":"user","sessionId":"` + session + `","cwd":"` + cwd + `","timestamp":"` + ts +
			`","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"` + id +
			`","content":` + jsonQuoted(out) + `}]}}`,
	}
}

// jsonQuoted writes a string the way a transcript holds it, newlines and all.
func jsonQuoted(s string) string {
	b, err := json.Marshal(s)
	if err != nil {
		panic(err)
	}
	return string(b)
}

// The band that decides whether the rest of the screen is honest. An agent
// almost always pipes its own run through a filter — `grep -E '^FAIL'` and an
// `echo DONE` after it — and the verdict never reaches the transcript. Counting
// those as passes was what made the first measurement of this say the suite
// fails one run in five when the runs that answered say two in five (#539).
func TestAFilteredRunIsCountedAsNeitherPassNorFail(t *testing.T) {
	withStatsStores(t)
	claudeRoot := os.Getenv("DEJA_CLAUDE_ROOT")
	lines := make([]string, 0, 6)
	lines = append(lines, testRunLines("r1", "2026-03-02", "c1",
		"go test ./internal/fetch -count=1", "ok  \tgithub.com/me/app/internal/fetch\t1.2s")...)
	// The same command line as the run above on purpose: a session repeating
	// one verbatim keeps a single command record and both outputs, and the
	// second run has to survive that (#539).
	lines = append(lines, testRunLines("r1", "2026-03-02", "c2",
		"go test ./internal/fetch -count=1", "--- FAIL: TestRetryBudget (0.01s)\nFAIL\tgithub.com/me/app/internal/fetch\t0.9s\nFAIL")...)
	lines = append(lines, testRunLines("r1", "2026-03-02", "c3",
		`go test ./... -count=1 2>&1 | grep -E "^FAIL" | head -3; echo DONE`, "DONE")...)
	writeClaudeFixture(t, filepath.Join(claudeRoot, "-work-app", "r1.jsonl"), "r1", lines)
	dir := index.DefaultDir()
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	h, err := index.ScanTestHistory(dir)
	if err != nil {
		t.Fatal(err)
	}
	if h.Runs != 3 {
		t.Fatalf("want 3 runs, got %d (%+v)", h.Runs, h)
	}
	if h.Passed != 1 || h.Failed != 1 || h.NoVerdict != 1 {
		t.Fatalf("want one of each band, got passed=%d failed=%d no-verdict=%d", h.Passed, h.Failed, h.NoVerdict)
	}
	var out bytes.Buffer
	if err := runTests(dir, nil, &out); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	if !strings.Contains(got, "no verdict") {
		t.Errorf("the screen does not name the third band:\n%s", got)
	}
	// The rate has to be stated over the runs it was measured on, not over
	// every run: 1 of 2, not 1 of 3.
	if !strings.Contains(got, "1 of the 2 runs that carried a verdict failed (50%)") {
		t.Errorf("the screen does not state the rate over the runs that answered:\n%s", got)
	}
}

// A test that failed twelve times in one afternoon was being fixed. One that
// failed on nine separate days is the one worth knowing about, so the list is
// cut on days and not on failures.
func TestOnlyATestThatFailedOnTwoDaysIsListed(t *testing.T) {
	withStatsStores(t)
	claudeRoot := os.Getenv("DEJA_CLAUDE_ROOT")
	fail := func(name string) string {
		return "--- FAIL: " + name + " (0.02s)\nFAIL\tgithub.com/me/app/internal/fetch\t0.4s\nFAIL"
	}
	lines := make([]string, 0, 8)
	// Twice in one day.
	lines = append(lines, testRunLines("r2", "2026-03-03", "a1",
		"go test ./internal/fetch", fail("TestQuokkabloomOnce"))...)
	lines = append(lines, testRunLines("r2", "2026-03-03", "a2",
		"go test ./internal/fetch", fail("TestQuokkabloomOnce"))...)
	// Once on each of two days.
	lines = append(lines, testRunLines("r2", "2026-03-03", "a3",
		"go test ./internal/fetch", fail("TestQuokkabloomKeepsComingBack"))...)
	lines = append(lines, testRunLines("r2", "2026-03-05", "a4",
		"go test ./internal/fetch", fail("TestQuokkabloomKeepsComingBack"))...)
	writeClaudeFixture(t, filepath.Join(claudeRoot, "-work-app", "r2.jsonl"), "r2", lines)
	dir := index.DefaultDir()
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	h, err := index.ScanTestHistory(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(h.Repeats) != 1 {
		t.Fatalf("want one repeat-failing test, got %d: %+v", len(h.Repeats), h.Repeats)
	}
	r := h.Repeats[0]
	if r.Name != "TestQuokkabloomKeepsComingBack" || r.Days != 2 || r.Failures != 2 {
		t.Fatalf("wrong row: %+v", r)
	}
	var out bytes.Buffer
	if err := runTests(dir, nil, &out); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	if !strings.Contains(got, "TestQuokkabloomKeepsComingBack") {
		t.Errorf("the screen does not name the test that kept failing:\n%s", got)
	}
	if strings.Contains(got, "TestQuokkabloomOnce") {
		t.Errorf("the screen named a test that only failed on one day:\n%s", got)
	}
}

func TestTestsJSONCarriesTheSeriesAndTheFlagsAreChecked(t *testing.T) {
	withStatsStores(t)
	claudeRoot := os.Getenv("DEJA_CLAUDE_ROOT")
	lines := make([]string, 0, 6)
	for i, day := range []string{"2026-03-09", "2026-03-10", "2026-03-17"} {
		lines = append(lines, testRunLines("r3", day, fmt.Sprintf("j%d", i),
			"go build ./...", "# github.com/me/app\ninternal/fetch/fetch.go:12:3: undefined: quokkabloom")...)
	}
	writeClaudeFixture(t, filepath.Join(claudeRoot, "-work-app", "r3.jsonl"), "r3", lines)
	dir := index.DefaultDir()
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := runTests(dir, []string{"--json"}, &out); err != nil {
		t.Fatal(err)
	}
	var got testsJSON
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, out.String())
	}
	if got.Kind != "deja.tests" || got.Schema == 0 {
		t.Errorf("no kind or schema version: %+v", got)
	}
	// A compile error is a failure: the suite did not run, which is not a pass
	// by any reading. The `#` line above it used to win as a `go build` pass.
	if got.Failed != 3 || got.Passed != 0 {
		t.Errorf("a compile error was not counted as a failure: %+v", got)
	}
	if len(got.Weeks) != 2 {
		t.Errorf("want two weeks in the series, got %d: %+v", len(got.Weeks), got.Weeks)
	}
	if got.Days != 3 {
		t.Errorf("want three distinct days, got %d", got.Days)
	}
	if err := runTests(dir, []string{"--verbose"}, &out); err == nil {
		t.Error("an unknown flag was accepted")
	}
	if err := runTests(dir, []string{"--limit"}, &out); err == nil {
		t.Error("--limit was accepted without a value")
	}
}

// Two screens that are easy to get wrong: a machine with no runs at all, and a
// list longer than the page.
func TestTestsScreenEdges(t *testing.T) {
	withStatsStores(t)
	dir := index.DefaultDir()
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := runTests(dir, nil, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "no build or test runs") {
		t.Fatalf("a store with no runs does not say so:\n%s", out.String())
	}
	claudeRoot := os.Getenv("DEJA_CLAUDE_ROOT")
	lines := make([]string, 0, 8)
	for i, name := range []string{"TestAlpha", "TestBeta"} {
		for d, day := range []string{"2026-04-01", "2026-04-02"} {
			lines = append(lines, testRunLines("r4", day, fmt.Sprintf("k%d%d", i, d),
				"go test ./internal/"+name,
				"--- FAIL: "+name+" (0.01s)\nFAIL\tgithub.com/me/app\t0.3s\nFAIL")...)
		}
	}
	writeClaudeFixture(t, filepath.Join(claudeRoot, "-work-app", "r4.jsonl"), "r4", lines)
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if err := runTests(dir, []string{"--limit", "1"}, &out); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	if !strings.Contains(got, "1 more") {
		t.Errorf("the page does not say how many rows it held back:\n%s", got)
	}
	out.Reset()
	if err := runTests(dir, []string{"--limit", "0"}, &out); err != nil {
		t.Fatal(err)
	}
	if all := out.String(); !strings.Contains(all, "TestAlpha") || !strings.Contains(all, "TestBeta") {
		t.Errorf("--limit 0 did not print every row:\n%s", all)
	}
}

// One tip at most once a day, the next in rotation each time (#4629).
func TestMaybeTipShowsOneADayInRotation(t *testing.T) {
	stamp := filepath.Join(t.TempDir(), "index.db.tip")
	day0 := time.Date(2026, 10, 8, 9, 0, 0, 0, time.UTC)

	var seen []string
	for i, at := range []time.Time{
		day0,
		day0.Add(time.Hour),      // same day: nothing
		day0.Add(25 * time.Hour), // next tip
		day0.Add(50 * time.Hour),
		day0.Add(75 * time.Hour),
		day0.Add(100 * time.Hour), // wraps to the first again
	} {
		var b bytes.Buffer
		maybeTip(&b, stamp, at)
		if i == 1 {
			if b.Len() != 0 {
				t.Fatalf("a second tip inside a day: %q", b.String())
			}
			continue
		}
		seen = append(seen, strings.TrimSpace(b.String()))
	}
	want := append(append([]string{}, dailyTips...), dailyTips[0])
	if strings.Join(seen, "\n") != strings.Join(want, "\n") {
		t.Errorf("tips shown:\n%s\nwant:\n%s", strings.Join(seen, "\n"), strings.Join(want, "\n"))
	}
}

// A stamp that cannot be read as one starts the rotation over rather than
// failing; one that cannot be written shows nothing, or it would show on
// every search.
func TestMaybeTipStampEdgeCases(t *testing.T) {
	dir := t.TempDir()
	stamp := filepath.Join(dir, "index.db.tip")
	if err := os.WriteFile(stamp, []byte("garbage"), 0o600); err != nil {
		t.Fatal(err)
	}
	var b bytes.Buffer
	maybeTip(&b, stamp, time.Now())
	if strings.TrimSpace(b.String()) != dailyTips[0] {
		t.Errorf("a corrupt stamp did not start at the first tip: %q", b.String())
	}

	b.Reset()
	maybeTip(&b, filepath.Join(dir, "missing", "deeper", "index.db.tip"), time.Now())
	if b.Len() != 0 {
		t.Errorf("a tip was shown though its stamp could not be written: %q", b.String())
	}
}

// Each tip names a command that exists, spelled as the usage spells it.
func TestDailyTipsNameRealCommands(t *testing.T) {
	usage := usageText()
	for _, tip := range dailyTips {
		cmd := strings.Fields(strings.SplitN(tip, "`", 3)[1])[:2]
		if !strings.Contains(usage, strings.Join(cmd, " ")) {
			t.Errorf("tip names %q, which the usage does not list: %s", strings.Join(cmd, " "), tip)
		}
	}
}

// A search whose output is not a terminal (a pipe, a script, a hook) gets its
// results and no tip, and leaves no stamp that would hold a tip back later.
func TestSearchIntoAPipeShowsNoTip(t *testing.T) {
	tmp := hermeticEnv(t)
	claude := filepath.Join(tmp, "claude")
	t.Setenv("DEJA_CLAUDE_ROOT", claude)
	at := time.Now().Add(-72 * time.Hour).UTC().Format(time.RFC3339)
	writeClaudeFixture(t, filepath.Join(claude, "alpha", "one.jsonl"), "c1", []string{
		`{"type":"user","sessionId":"c1","timestamp":"` + at +
			`","message":{"role":"user","content":"the frobnicator retry budget"}}`,
	})
	if _, err := captureRunStderr(t, "index"); err != nil {
		t.Fatal(err)
	}

	out, said := captureBoth(t, "search", "frobnicator")
	if !strings.Contains(out, "frobnicator") {
		t.Fatalf("the search found nothing, so this measures nothing:\n%s\n%s", out, said)
	}
	if strings.Contains(said, "tip:") || strings.Contains(out, "tip:") {
		t.Errorf("a piped search showed a tip:\n%s\n%s", out, said)
	}
	if _, err := os.Stat(index.DefaultDir() + ".tip"); err == nil {
		t.Error("a piped search left a tip stamp")
	}
}

// TOML lets a server carry sub-tables, and their headers open with `[` too.
// Removing a block by walking to the next such line stopped at the sub-table
// and left it behind, naming a server that had just gone (#2716).
func TestRemovingABlockTakesItsSubTables(t *testing.T) {
	const cfg = `model = "gpt-5"

[mcp_servers.deja]
command = "/usr/local/bin/deja"
args = ["mcp"]

[mcp_servers.deja.env]
DEJA_INDEX_DIR = "/tmp/idx"

[mcp_servers.memory]
command = "/usr/local/bin/mcp-memory"
`
	got := removeTOMLMCPBlock(cfg, "deja")
	if strings.Contains(got, "[mcp_servers.deja]") {
		t.Errorf("the block itself survived:\n%s", got)
	}
	if strings.Contains(got, "deja.env") {
		t.Errorf("a sub-table was left naming a server that is gone:\n%s", got)
	}
	if !strings.Contains(got, "[mcp_servers.memory]") {
		t.Errorf("another server went with it:\n%s", got)
	}
	if !strings.Contains(got, `model = "gpt-5"`) {
		t.Errorf("the reader's own settings went:\n%s", got)
	}
}

// The dot is what decides. A bare prefix test would take a differently named
// server with it, which is the failure this rule has to avoid while fixing the
// other one.
func TestASimilarlyNamedServerStays(t *testing.T) {
	const cfg = `[mcp_servers.deja]
command = "/usr/local/bin/deja"

[mcp_servers.deja-vu]
command = "/hand/bin/deja"

[mcp_servers.dejavu-notes]
command = "/hand/bin/notes"
`
	got := removeTOMLMCPBlock(cfg, "deja")
	for _, keep := range []string{"[mcp_servers.deja-vu]", "[mcp_servers.dejavu-notes]"} {
		if !strings.Contains(got, keep) {
			t.Errorf("%s was removed with the block that merely shares its opening:\n%s", keep, got)
		}
	}
	if strings.Contains(got, "/usr/local/bin/deja") {
		t.Errorf("the block deja owns survived:\n%s", got)
	}
}

// A sub-table of another server is not this one's to take, whichever order the
// file happens to hold them in.
func TestAnotherServersSubTableIsLeftAlone(t *testing.T) {
	const cfg = `[mcp_servers.deja]
command = "/usr/local/bin/deja"

[mcp_servers.memory.env]
KEY = "1"
`
	got := removeTOMLMCPBlock(cfg, "deja")
	if !strings.Contains(got, "[mcp_servers.memory.env]") {
		t.Errorf("another server's sub-table was taken:\n%s", got)
	}
	if !strings.Contains(got, `KEY = "1"`) {
		t.Errorf("its body went too:\n%s", got)
	}
}

// The help page says everything after `deja aider` is aider's own, --help
// included, and the same of goose — and both answered with deja's page, so the
// tool's own help was unreachable through the wrapper (#5060). The help goes to
// the tool, with the line as typed and none of the recall a real run adds.
func TestToolHelpGoesToTheTool(t *testing.T) {
	for _, tool := range []string{"aider", "goose"} {
		for _, flag := range []string{"--help", "-h"} {
			t.Run(tool+" "+flag, func(t *testing.T) {
				home := t.TempDir()
				t.Setenv("HOME", home)
				t.Setenv("USERPROFILE", home)
				t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
				t.Setenv("DEJA_INDEX_DIR", filepath.Join(home, "index"))
				t.Setenv("DEJA_WARMUP_SENTINEL", "1")
				bin := filepath.Join(home, "bin")
				if err := os.MkdirAll(bin, 0o755); err != nil {
					t.Fatal(err)
				}
				got := filepath.Join(home, "args")
				name, script := tool, "#!/bin/sh\nprintf '%s\\n' \"$@\" > '"+got+"'\n"
				if runtime.GOOS == "windows" {
					// LookPath finds it through PATHEXT, as it would aider.exe.
					name, script = tool+".cmd", "@echo %*> \""+got+"\"\r\n"
				}
				if err := os.WriteFile(filepath.Join(bin, name), []byte(script), 0o755); err != nil {
					t.Fatal(err)
				}
				t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

				out := captureStdout(t, func() {
					if err := run([]string{tool, flag}); err != nil {
						t.Errorf("deja %s %s: %v", tool, flag, err)
					}
				})
				if strings.Contains(out, "Usage:") {
					t.Errorf("deja %s %s printed deja's own page:\n%s", tool, flag, out)
				}
				b, err := os.ReadFile(got)
				if err != nil {
					t.Fatalf("%s never ran: %v", tool, err)
				}
				if args := strings.TrimSpace(string(b)); args != flag {
					t.Errorf("%s got %q, want only %q", tool, args, flag)
				}
			})
		}
	}
}

// `deja help aider` stays deja's page: it is the one way to read what the
// wrapper does once --help belongs to the tool.
func TestDejaHelpStillDescribesTheWrappers(t *testing.T) {
	for _, tool := range []string{"aider", "goose"} {
		if !toolOwnsHelp(tool) {
			t.Errorf("%s is not marked as owning its --help", tool)
		}
		if h := helpForCommand(tool); !strings.Contains(h, "deja "+tool) {
			t.Errorf("deja help %s lost its page:\n%s", tool, h)
		}
	}
	if toolOwnsHelp("search") {
		t.Error("search's --help is deja's own")
	}
}

// With the tool missing, the error says so and where deja's own page is,
// rather than falling back to that page as if it had been asked for.
func TestToolHelpWithoutTheToolSaysSo(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	err := runToolHelp("aider", []string{"--help"})
	if err == nil || !strings.Contains(err.Error(), "not on PATH") || !strings.Contains(err.Error(), "deja help aider") {
		t.Errorf("err = %v", err)
	}
}

// Every session-start and per-prompt block leaves a snapshot of its text; the
// point of action left only a count. Read from a real machine: 507 injections
// there and not one whose content could be read back, so the surface with the
// best evidence behind it was the one that could not be audited, replayed, or
// compared against what the agent did next — and `deja log --last` had nothing
// to print for it.
func TestThePointOfActionKeepsWhatItSaid(t *testing.T) {
	t.Setenv("DEJA_COMMAND_HINTS", "on")
	tmp := hermeticEnv(t)
	dir := tmp + "/index.db"
	t.Setenv("DEJA_INDEX_DIR", dir)
	commandRunWithAnOutcome(t, "go test ./... -count=1", "a", "b")
	if _, err := captureRun(t, "index"); err != nil {
		t.Fatal(err)
	}

	out := toolHookRun(t, `{"hook_event_name":"PreToolUse","tool_name":"Bash",`+
		`"tool_input":{"command":"go test ./... -count=1"},"session_id":"agent-9","cwd":"/work/app"}`)
	if out == "" {
		t.Fatal("the hook said nothing, so there is no injection to record")
	}

	b, err := os.ReadFile(usage.SnapshotPath(dir))
	if err != nil {
		t.Fatalf("no injections file: %v", err)
	}
	var kept, into string
	for line := range strings.SplitSeq(strings.TrimSpace(string(b)), "\n") {
		var row struct {
			Kind   string `json:"kind"`
			Digest string `json:"digest"`
			Text   string `json:"text"`
			Into   string `json:"into"`
		}
		if json.Unmarshal([]byte(line), &row) != nil || row.Kind != usage.KindTool {
			continue
		}
		kept = row.Digest + row.Text
		into = row.Into
	}
	if kept == "" {
		t.Fatalf("the point-of-action injection left no text:\n%s", b)
	}
	// The line itself, whatever it said: this fixture's is the command's own
	// history, and what matters is that the text reached the file.
	if !strings.Contains(kept, "has run that command") {
		t.Errorf("the snapshot does not hold what was said: %q", kept)
	}
	if into != "agent-9" {
		t.Errorf("the snapshot was recorded into %q, want the agent session", into)
	}
}

// TRAE CLI's rollouts get their own doctor row, apart from codex: live and
// archived sessions counted, and the state database and config TRAE keeps in
// the same directory left out of the unread count.
func TestDoctorReportsTheTraeStore(t *testing.T) {
	tmp := hermeticEnv(t)
	root := filepath.Join(tmp, "trae")
	write := func(path, body string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	rollout := `{"timestamp":"2026-10-01T09:00:00Z","type":"session_meta","payload":{"id":"019fbcca-0000-7000-8000-000000000001","cwd":"/w/api","originator":"codex-tui"}}` + "\n" +
		`{"timestamp":"2026-10-01T09:00:01Z","type":"event_msg","payload":{"type":"user_message","message":"why does the retry loop never back off"}}` + "\n"
	write(filepath.Join(root, "sessions", "2026", "10", "01", "rollout-2026-10-01T09-00-00-019fbcca-0000-7000-8000-000000000001.jsonl"), rollout)
	write(filepath.Join(root, "archived_sessions", "rollout-2026-09-30T09-00-00-019fbcca-0000-7000-8000-000000000002.jsonl"), rollout)
	write(filepath.Join(root, "state_5.sqlite"), "sqlite")

	var buf bytes.Buffer
	doctorHarnesses(&buf, t.TempDir())
	var row string
	for l := range strings.SplitSeq(buf.String(), "\n") {
		if strings.Contains(l, "trae") && strings.Contains(l, "file") {
			row = l
			break
		}
	}
	if row == "" {
		t.Fatalf("no trae row in the report:\n%s", buf.String())
	}
	if !strings.Contains(row, "2 files") || strings.Contains(row, "not recognised") {
		t.Errorf("row = %q, want 2 files and nothing unrecognised", row)
	}
	for l := range strings.SplitSeq(buf.String(), "\n") {
		if strings.Contains(l, "codex") && strings.Contains(l, "2 files") {
			t.Errorf("codex row counted TRAE's rollouts: %q", l)
		}
	}

	// The JSON report names the store and finds it.
	var found bool
	for _, c := range doctorStoreChecks() {
		if c.name != "trae" {
			continue
		}
		found = true
		b, err := json.Marshal(c.paths)
		if err != nil {
			t.Fatal(err)
		}
		if len(c.files) != 2 || !strings.Contains(string(b), "trae") {
			t.Errorf("trae store check = %d files at %s, want 2 under the trae root", len(c.files), b)
		}
		ss, err := c.parse(c.files[0])
		if err != nil || len(ss) != 1 || ss[0].Harness != "trae" {
			t.Errorf("trae store check parse = %v, %+v", err, ss)
		}
	}
	if !found {
		t.Fatal("doctor has no trae store check")
	}
}

// A TRAE session resumes with traex, not codex, and a history-only entry has
// nothing to reopen.
func TestResumeTraeSession(t *testing.T) {
	_, cmd, err := resumeCommand(model.Session{Harness: "trae", ID: "019fbcca-0000-7000-8000-000000000001", Project: "api"})
	if err != nil || cmd != "traex resume 019fbcca-0000-7000-8000-000000000001" {
		t.Fatalf("resume = %q, %v", cmd, err)
	}
	if _, _, err := resumeCommand(model.Session{Harness: "trae", ID: "x", Project: "history"}); err == nil {
		t.Fatal("a history.jsonl entry offered a resume command")
	}
}

// The repository ships a README per language. Two things about that set rot
// silently, and both are invisible to a reader who does not read the language.
//
// The first is the switcher: every one of these files carries the same list of
// twelve links, and it is the only way a reader lands on their own. Add a
// language and forget one file, and that language is reachable from eleven
// pages and not the twelfth; rename a file and every switcher on every page
// points at a 404.
//
// The second is the harness roster — the `Claude Code · Cline · …` block that
// each translation spells out in full. It is a count claim written thirty-four
// times over, and it went stale in Chinese before while the English pages were
// current (#3100). Comparing the roster against the registry catches that in
// any language, because the names are the same everywhere even when nothing
// around them is.
// Three of these live in the root because the outside world already links them
// by that path — the site, the plugin listings, the directory submissions — and
// the nine added later live under docs/readme so the root stays readable. The
// switcher therefore has to be written relative to whichever file carries it,
// which is one more thing to get wrong by hand and the reason this is a test.
var readmeLanguages = []struct {
	file, name string
}{
	{"README.md", "English"},
	{"docs/readme/README.zh.md", "简体中文"},
	{"docs/readme/README.zh-TW.md", "繁體中文"},
	{"docs/readme/README.ja.md", "日本語"},
	{"docs/readme/README.ko.md", "한국어"},
	{"docs/readme/README.es.md", "Español"},
	{"docs/readme/README.pt.md", "Português"},
	{"docs/readme/README.fr.md", "Français"},
	{"docs/readme/README.de.md", "Deutsch"},
	{"docs/readme/README.ru.md", "Русский"},
	{"docs/readme/README.tr.md", "Türkçe"},
	{"docs/readme/README.hi.md", "हिन्दी"},
}

// switcherFor is the line the file at current must contain: every language
// linked from where current sits, its own name plain.
func switcherFor(current string) string {
	var b strings.Builder
	b.WriteString(`<p align="center">`)
	for i, l := range readmeLanguages {
		if i > 0 {
			b.WriteString(" | ")
		}
		if l.file == current {
			b.WriteString(l.name)
			continue
		}
		b.WriteString(`<a href="` + readmeHref(current, l.file) + `">` + l.name + `</a>`)
	}
	b.WriteString("</p>")
	return b.String()
}

// readmeHref is the path from one README to another, as a browser reading the
// first one would resolve it.
func readmeHref(from, to string) string {
	up := strings.Repeat("../", strings.Count(from, "/"))
	if strings.Contains(from, "/") && filepath.Dir(from) == filepath.Dir(to) {
		return filepath.Base(to)
	}
	return up + to
}

func TestEveryTranslatedReadmeExistsAndSwitchesToAllTheOthers(t *testing.T) {
	root := filepath.Join("..", "..")
	for _, l := range readmeLanguages {
		b, err := os.ReadFile(filepath.Join(root, l.file))
		if err != nil {
			t.Errorf("%s is linked from every other README and is not here: %v", l.file, err)
			continue
		}
		if want := switcherFor(l.file); !strings.Contains(string(b), want) {
			t.Errorf("%s does not carry the language switcher every other README carries:\nwant %s", l.file, want)
		}
	}

	// The check above compares the file against a line this test builds, so it
	// only ever proves the file agrees with the formula. This one reads the
	// hrefs back out of the file and resolves each one from where that file
	// sits, which is what a reader's browser does — so a hand-edited link is
	// caught even if the formula that produced it was wrong.
	for _, l := range readmeLanguages {
		b, err := os.ReadFile(filepath.Join(root, l.file))
		if err != nil {
			continue
		}
		line := switcherLine(string(b))
		if line == "" {
			t.Errorf("%s has no line naming every language", l.file)
			continue
		}
		dir := filepath.Dir(filepath.Join(root, l.file))
		links := 0
		for _, m := range hrefPattern.FindAllStringSubmatch(line, -1) {
			if strings.HasPrefix(m[1], "http") {
				continue
			}
			links++
			if _, err := os.Stat(filepath.Join(dir, m[1])); err != nil {
				t.Errorf("the switcher in %s points at %q, which does not resolve from there: %v", l.file, m[1], err)
			}
		}
		if links != len(readmeLanguages)-1 {
			t.Errorf("the switcher in %s carries %d links; there are %d other languages", l.file, links, len(readmeLanguages)-1)
		}
	}
}

var hrefPattern = regexp.MustCompile(`href="([^"]+)"`)

// switcherLine is the one line that names the last language in the list, which
// is the switcher wherever it has been moved to in the file.
func switcherLine(text string) string {
	last := readmeLanguages[len(readmeLanguages)-1].name
	for line := range strings.SplitSeq(text, "\n") {
		if strings.Contains(line, last) && strings.Contains(line, "href=") {
			return line
		}
	}
	return ""
}

// rosterHead is where the list starts. It is the same three names in every
// language because they are product names, which is what makes this checkable
// at all.
const rosterHead = "Claude Code · Claw Code · Cline · Codex CLI"

// rosterTail closes it: "Zed" and the sentence-ending mark, which is a full
// stop in the Latin-script pages and an ideographic one in the Chinese pages.
var rosterTail = regexp.MustCompile(`Zed\s*[.。]`)

func TestTheTranslatedReadmesListEveryHarnessTheRegistryHas(t *testing.T) {
	root := filepath.Join("..", "..")
	n := registryHarnessCount(t, root)

	// English and Japanese carry the capability matrix instead of a prose
	// roster, and the two English tests already count that. Every file that
	// does spell the list out has to spell all of it.
	listed := 0
	for _, l := range readmeLanguages {
		b, err := os.ReadFile(filepath.Join(root, l.file))
		if err != nil {
			continue // the test above reports a missing file
		}
		text := string(b)
		start := strings.Index(text, rosterHead)
		if start < 0 {
			continue
		}
		end := rosterTail.FindStringIndex(text[start:])
		if end == nil {
			t.Errorf("%s starts the harness roster and never closes it on Zed", l.file)
			continue
		}
		block := strings.ReplaceAll(text[start:start+end[1]], "\n", " ")
		got := len(strings.Split(block, "·"))
		if got != n {
			t.Errorf("%s lists %d harnesses; the registry has %d", l.file, got, n)
		}
		listed++
	}
	if listed < 8 {
		t.Fatalf("only %d READMEs spell out the roster — the phrasing changed and this test stopped seeing it", listed)
	}
}

// A misspelled subcommand is searched for as a word — `deja isntall` corrects
// to "install" and returns sessions that mention installing, which is not what
// the typist wanted. Spelled correctly it would have run the command, so the
// hint only ever fires on the case where it is wanted.
func TestFuzzyNamesTheCommandBehindATypo(t *testing.T) {
	var b bytes.Buffer
	printFuzzy(&b, map[string][]string{"isntall": {"install"}})
	got := b.String()
	if !strings.Contains(got, "isntall -> install") {
		t.Fatalf("the correction itself must still be reported: %q", got)
	}
	if !strings.Contains(got, "deja install") {
		t.Fatalf("a corrected subcommand should be offered: %q", got)
	}

	// An ordinary word that happens to be misspelled gets no command hint.
	b.Reset()
	printFuzzy(&b, map[string][]string{"pgbouncr": {"pgbouncer"}})
	if strings.Contains(b.String(), "is also a command") {
		t.Fatalf("no command is named pgbouncer: %q", b.String())
	}

	// One hint is enough however many terms were corrected.
	b.Reset()
	printFuzzy(&b, map[string][]string{"isntall": {"install"}, "resotre": {"restore"}})
	if n := strings.Count(b.String(), "is also a command"); n != 1 {
		t.Fatalf("hinted %d times, want once: %q", n, b.String())
	}
}

// forget --unforget --dry-run brought the session back anyway: the destructive
// side runs a dry probe and never touches disk, but the undo went straight to
// Unforget, which lifts the tombstone and rebuilds (#1066).
func TestUnforgetDryRunRestoresNothing(t *testing.T) {
	tmp := hermeticEnv(t)
	store := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-proj")
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	rec := `{"type":"user","message":{"role":"user","content":"UNDRY dry run undo"},"timestamp":"2026-06-01T10:00:00Z","sessionId":"u","cwd":"/proj"}` + "\n"
	if err := os.WriteFile(filepath.Join(store, "u.jsonl"), []byte(rec), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(tmp, "index.db")
	if err := index.Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRunStderr(t, "forget", "--session", "claude:u"); err != nil {
		t.Fatal(err)
	}
	if n := index.TombstoneMatches("claude:u"); n != 1 {
		t.Fatalf("forgot %d sessions, want 1", n)
	}

	// The dry run names what it would restore and changes nothing.
	out, err := captureRun(t, "forget", "--unforget", "claude:u", "--dry-run")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "nothing was changed") || !strings.Contains(out, "would restore") {
		t.Errorf("dry run does not say it changed nothing: %q", out)
	}
	if n := index.TombstoneMatches("claude:u"); n != 1 {
		t.Errorf("the dry run lifted the tombstone: %d left, want 1", n)
	}

	// The real undo still restores it.
	if _, err := captureRunStderr(t, "forget", "--unforget", "claude:u"); err != nil {
		t.Fatal(err)
	}
	if n := index.TombstoneMatches("claude:u"); n != 0 {
		t.Errorf("the real undo left %d tombstones, want 0", n)
	}
}

// A restore that restored nothing exited 0, so `deja forget --unforget "$id" &&
// echo back` printed "back" for a typo, for an id that was never forgotten, and
// for a session that is still gone (#2263).
func TestUnforgetRefusesWhenItRestoredNothing(t *testing.T) {
	withTempStores(t)

	out, err := captureRun(t, "forget", "--unforget", "nothing-like-this")
	if err == nil {
		t.Errorf("a prefix matching no tombstone succeeded, printing %q", out)
	}
	if err != nil && !strings.Contains(err.Error(), "no tombstone matches") {
		t.Errorf("the refusal reads %q — it should still say what it could not find", err)
	}

	// The dry run reports the same miss and answers the same way.
	out, err = captureRun(t, "forget", "--unforget", "nothing-like-this", "--dry-run")
	if err == nil {
		t.Errorf("a dry run against no tombstone succeeded, printing %q", out)
	}
}

// An imported session lives only in the index — forget rewrote the log and the
// rebuild has nothing to re-read, so the undo lifted the tombstone and claimed
// a restore that did not happen (#967).
func TestUnforgetSaysWhenAnImportedSessionCannotComeBack(t *testing.T) {
	tmp := hermeticEnv(t)
	store := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-proj")
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	rec := `{"type":"user","message":{"role":"user","content":"local work on the ticker"},"timestamp":"2026-07-11T10:00:00Z","sessionId":"loc","cwd":"/proj"}` + "\n"
	if err := os.WriteFile(filepath.Join(store, "loc.jsonl"), []byte(rec), 0o644); err != nil {
		t.Fatal(err)
	}
	exp := filepath.Join(tmp, "transfer")
	if err := os.MkdirAll(exp, 0o755); err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(index.SyncRecord{Harness: "claude", SessionID: "p1", Project: "peer/api", Role: "user", Text: "peer text"})
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(exp, "deja-sync-x.jsonl"), append(b, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(tmp, "index.db")
	if err = index.Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	if _, err = captureRun(t, "sync", "import", exp); err != nil {
		t.Fatal(err)
	}
	var imported string
	metas, err := index.AllMeta(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range metas {
		if strings.HasPrefix(m.ID, "imported-") {
			imported = m.ID
		}
	}
	if imported == "" {
		t.Fatal("no imported session in the index")
	}

	if _, err = captureRunStderr(t, "forget", "--session", imported); err != nil {
		t.Fatal(err)
	}
	out, err := captureRun(t, "forget", "--unforget", "claude:"+imported)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "restored 1 session") {
		t.Errorf("the undo claimed a restore that did not happen: %q", out)
	}
	if !strings.Contains(out, "sync import") {
		t.Errorf("the undo does not name the way back: %q", out)
	}

	// A local session still round-trips, and still says so.
	if _, err = captureRunStderr(t, "forget", "--session", "loc"); err != nil {
		t.Fatal(err)
	}
	out, err = captureRun(t, "forget", "--unforget", "claude:loc")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "restored 1 session") {
		t.Errorf("a local session no longer reports its restore: %q", out)
	}
}

// The undo names `sync import` as the way back; this checks the way back
// actually works. Forgetting an imported session dropped it from the manifest
// but left its rows in the import dedupe ledger, so the re-import the message
// told the user to run was silently deduped away and the "only copy" was
// unrecoverable. Import now skips a ledger row only while its session still
// lives, so an unforgotten session comes back.
func TestUnforgetImportedComesBackOnReimport(t *testing.T) {
	tmp := hermeticEnv(t)
	exp := filepath.Join(tmp, "transfer")
	if err := os.MkdirAll(exp, 0o755); err != nil {
		t.Fatal(err)
	}
	// Three imported sessions so a forget is unambiguous and the count moves.
	var batch []byte
	for i := 1; i <= 3; i++ {
		b, err := json.Marshal(index.SyncRecord{
			Harness: "claude", SessionID: "pe" + string(rune('0'+i)), Project: "svc",
			Role: "user", Text: "rotate the signing key, take " + string(rune('0'+i)),
			Time: time.Date(2026, time.Month(i), 1, 10, 0, 5, 0, time.UTC),
		})
		if err != nil {
			t.Fatal(err)
		}
		batch = append(batch, append(b, '\n')...)
	}
	if err := os.WriteFile(filepath.Join(exp, "deja-sync.jsonl"), batch, 0o644); err != nil {
		t.Fatal(err)
	}
	dir := index.DefaultDir()
	if _, err := captureRun(t, "sync", "import", exp); err != nil {
		t.Fatal(err)
	}
	sessions := func() int {
		ov, err := index.Overview(dir)
		if err != nil {
			t.Fatal(err)
		}
		return ov.Sessions
	}
	if sessions() != 3 {
		t.Fatalf("import: want 3 sessions, got %d", sessions())
	}

	var id string
	metas, err := index.AllMeta(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range metas {
		if strings.HasPrefix(m.ID, "imported-") {
			id = m.ID
			break
		}
	}
	if _, err := captureRunStderr(t, "forget", "--session", id); err != nil {
		t.Fatal(err)
	}
	if sessions() != 2 {
		t.Fatalf("forget: want 2 sessions, got %d", sessions())
	}
	// While forgotten the tombstone must hold: re-import does not resurrect it.
	if _, err := captureRun(t, "sync", "import", exp); err != nil {
		t.Fatal(err)
	}
	if sessions() != 2 {
		t.Fatalf("re-import while forgotten resurrected it: want 2, got %d", sessions())
	}
	// Undo the tombstone, then re-import: now it comes back.
	if _, err := captureRun(t, "forget", "--unforget", "claude:"+id); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRun(t, "sync", "import", exp); err != nil {
		t.Fatal(err)
	}
	if got := sessions(); got != 3 {
		t.Fatalf("re-import after unforget did not bring the session back: want 3, got %d", got)
	}
}

// Lifting a tombstone restores nothing in two different situations, and they
// have different answers: a session deja only ever held in its own index came
// from another machine and `sync import` brings it back, while a transcript
// this machine wrote and then deleted is simply not on disk — pointing the
// second at sync import blames a machine the user never had (#1755).
func TestUnforgetSaysWhyNothingCameBack(t *testing.T) {
	for _, tc := range []struct {
		keys []string
		want []string
		not  []string
	}{
		{
			keys: []string{"claude:imported-ab12cd34"},
			want: []string{"another machine", "sync import"},
			not:  []string{"no longer on this machine"},
		},
		{
			keys: []string{"claude:ts-01"},
			want: []string{"no longer on this machine"},
			not:  []string{"another machine", "sync import"},
		},
		{
			keys: []string{"claude:imported-ab12cd34", "claude:ts-01"},
			want: []string{"another machine", "no longer on this machine"},
		},
	} {
		got := unforgetGoneLines(tc.keys)
		joined := strings.Join(got, "\n")
		for _, want := range tc.want {
			if !strings.Contains(joined, want) {
				t.Errorf("%v said %q, missing %q", tc.keys, joined, want)
			}
		}
		for _, no := range tc.not {
			if strings.Contains(joined, no) {
				t.Errorf("%v said %q, which should not mention %q", tc.keys, joined, no)
			}
		}
	}
	if got := unforgetGoneLines(nil); len(got) != 0 {
		t.Errorf("nothing missing still said %q", got)
	}

	// One key and several read as sentences, not as a count with a plural
	// bolted on.
	one := strings.Join(unforgetGoneLines([]string{"claude:ts-01"}), " ")
	many := strings.Join(unforgetGoneLines([]string{"claude:ts-01", "claude:ts-02"}), " ")
	if !strings.Contains(one, "claude:ts-01 is no longer") || !strings.Contains(one, "the tombstone is lifted") {
		t.Errorf("one key reads as %q", one)
	}
	if !strings.Contains(many, "claude:ts-01, claude:ts-02 are no longer") || !strings.Contains(many, "the tombstones are lifted") {
		t.Errorf("two keys read as %q", many)
	}
}

// "`deja forget --list` names them" sent the reader to a list of everything the
// machine ever forgot, where the ones the refusal meant were theirs to find
// (#1014).
func TestUnforgetRefusalNamesTheSessionsItMeans(t *testing.T) {
	tmp := hermeticEnv(t)
	store := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-proj")
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"sess0", "sess1", "sess2", "alpha", "beta"} {
		rec := `{"type":"user","message":{"role":"user","content":"session ` + id + `"},"timestamp":"2026-08-01T10:00:00Z","sessionId":"` + id + `","cwd":"/proj"}` + "\n"
		if err := os.WriteFile(filepath.Join(store, id+".jsonl"), []byte(rec), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	dir := filepath.Join(tmp, "index.db")
	if err := index.Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"sess0", "sess1", "sess2", "alpha", "beta"} {
		if _, err := captureRun(t, "forget", "--session", id); err != nil {
			t.Fatal(err)
		}
	}

	_, err := captureRun(t, "forget", "--unforget", "claude:sess")
	if err == nil {
		t.Fatal("an ambiguous selector was accepted")
	}
	for _, want := range []string{"claude:sess0", "claude:sess1", "claude:sess2"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not name %s: %v", want, err)
		}
	}
	// And not the ones it does not mean.
	if strings.Contains(err.Error(), "claude:alpha") {
		t.Errorf("the refusal named a session it would not restore: %v", err)
	}
	// Naming one still works.
	if _, err := captureRun(t, "forget", "--unforget", "claude:sess1"); err != nil {
		t.Errorf("naming one of them was refused: %v", err)
	}
}

// The restore is the moment someone checks they got back exactly what they
// lost, and it answered with a count while the ids were already in hand
// (#1095). The ids must be the ones actually restored, not every session the
// selector matches after the rebuild.
func TestTheRestoreNamesWhatCameBack(t *testing.T) {
	hermeticEnv(t)
	store := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-proj")
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	for i := range 4 {
		id := fmt.Sprintf("sess%d", i)
		rec := `{"type":"user","message":{"role":"user","content":"decision ` + id + `"},"timestamp":"2026-07-1` + strconv.Itoa(i) + `T10:00:00Z","sessionId":"` + id + `","cwd":"/proj"}` + "\n"
		if err := os.WriteFile(filepath.Join(store, id+".jsonl"), []byte(rec), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	dir := os.Getenv("DEJA_INDEX_DIR")
	if err := index.Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"claude:sess1", "claude:sess3"} {
		if _, err := captureRun(t, "forget", "--session", id); err != nil {
			t.Fatal(err)
		}
	}

	out, err := captureRun(t, "forget", "--unforget", "claude:sess", "--all-matches")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "claude:sess1") || !strings.Contains(out, "claude:sess3") {
		t.Errorf("the restore does not name what came back:\n%s", out)
	}
	// sess0 and sess2 were never forgotten; naming them would be a lie about
	// what this call did.
	for _, untouched := range []string{"claude:sess0", "claude:sess2"} {
		if strings.Contains(out, untouched) {
			t.Errorf("the restore names %s, which it did not restore:\n%s", untouched, out)
		}
	}
}

// forget refuses a prefix that reaches more than one session; the undo restored
// them all and reported the count afterwards, while the hint beside the list
// promises that it brings one back (#961).
func TestUnforgetRefusesAPrefixThatReachesSeveral(t *testing.T) {
	tmp := hermeticEnv(t)
	store := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-proj")
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	for i, id := range []string{"ab-one", "ab-two", "zz-other"} {
		rec := `{"type":"user","message":{"role":"user","content":"session ` + id + `"},"timestamp":"2026-07-1` + string(rune('1'+i)) + `T10:00:00Z","sessionId":"` + id + `","cwd":"/proj"}` + "\n"
		if err := os.WriteFile(filepath.Join(store, id+".jsonl"), []byte(rec), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	dir := filepath.Join(tmp, "index.db")
	if err := index.Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRunStderr(t, "forget", "--session", "ab", "--all-matches"); err != nil {
		t.Fatal(err)
	}
	if n := index.TombstoneMatches("ab"); n != 2 {
		t.Fatalf("forgot %d sessions, want 2", n)
	}

	// The undo refuses the same shape the delete refuses.
	if _, err := captureRun(t, "forget", "--unforget", "ab"); err == nil {
		t.Error("unforget restored several sessions from one prefix without asking")
	} else if !strings.Contains(err.Error(), "--all-matches") {
		t.Errorf("the refusal does not name the way to do it anyway: %v", err)
	}
	if n := index.TombstoneMatches("ab"); n != 2 {
		t.Errorf("the refused undo still restored something: %d tombstones left", n)
	}

	// Asked for explicitly, it does the lot.
	if _, err := captureRunStderr(t, "forget", "--unforget", "ab", "--all-matches"); err != nil {
		t.Fatal(err)
	}
	if n := index.TombstoneMatches("ab"); n != 0 {
		t.Errorf("--all-matches left %d tombstones", n)
	}

	// One id is still one command.
	if _, err := captureRunStderr(t, "forget", "--session", "ab-one"); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRunStderr(t, "forget", "--unforget", "claude:ab-one"); err != nil {
		t.Errorf("restoring a single session was refused: %v", err)
	}
}

// An empty `--unforget` fell through to the selector refusal, which names
// three ways to forget more and none to bring anything back (#1041).
func TestAnEmptyUnforgetAsksForAnId(t *testing.T) {
	hermeticEnv(t)
	err := runForget(os.Getenv("DEJA_INDEX_DIR"), []string{"--unforget", ""})
	if err == nil {
		t.Fatal("an empty --unforget was accepted")
	}
	if !strings.Contains(err.Error(), "--unforget needs an id") {
		t.Errorf("the refusal does not ask for an id: %v", err)
	}
	for _, wrong := range []string{"--project", "--before", "--dry-run"} {
		if strings.Contains(err.Error(), wrong) {
			t.Errorf("the refusal offers %s, which forgets rather than restores: %v", wrong, err)
		}
	}
	// A missing value is still its own message, and reads as a sentence.
	err = runForget(os.Getenv("DEJA_INDEX_DIR"), []string{"--unforget"})
	if err == nil || !strings.Contains(err.Error(), "--unforget needs a value") {
		t.Errorf("a missing value is not named: %v", err)
	}
}

// forget clears the title a promoted note borrowed from its source so the
// forgotten session's first line does not survive in the note (#666). unforget
// is the moment that reason stops applying, and the title stayed cleared for
// good (#969).
func TestUnforgetGivesAPromotedNoteItsBorrowedTitleBack(t *testing.T) {
	tmp := hermeticEnv(t)
	store := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-proj")
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	rec := `{"type":"user","message":{"role":"user","content":"the decision: pool cap stays at 20"},"timestamp":"2026-07-11T10:00:00Z","sessionId":"s11","cwd":"/proj"}` + "\n"
	if err := os.WriteFile(filepath.Join(store, "s11.jsonl"), []byte(rec), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(tmp, "index.db")
	if err := index.Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRunStderr(t, "promote", "s11"); err != nil {
		t.Fatal(err)
	}

	title := func(t *testing.T) string {
		t.Helper()
		b, err := os.ReadFile(os.Getenv("DEJA_NOTES_FILE"))
		if err != nil {
			t.Fatal(err)
		}
		for line := range strings.SplitSeq(strings.TrimSpace(string(b)), "\n") {
			var m map[string]any
			if json.Unmarshal([]byte(line), &m) != nil {
				continue
			}
			if kind, _ := m["kind"].(string); kind == "promoted" {
				s, _ := m["title"].(string)
				return s
			}
		}
		t.Fatal("no promoted note in the file")
		return ""
	}

	if got := title(t); !strings.Contains(got, "pool cap") {
		t.Fatalf("promote did not borrow a title: %q", got)
	}
	if _, err := captureRunStderr(t, "forget", "--session", "s11"); err != nil {
		t.Fatal(err)
	}
	if got := title(t); got != "" {
		t.Fatalf("forget left the borrowed title in place: %q", got)
	}
	if _, err := captureRunStderr(t, "forget", "--unforget", "claude:s11"); err != nil {
		t.Fatal(err)
	}
	if got := title(t); !strings.Contains(got, "pool cap") {
		t.Errorf("the note did not get its title back: %q", got)
	}
}

// A suggestion is only as good as the list it is drawn from, and the list sits
// in a different place from the parser that reads the flags. A flag added to a
// parser and not to its list costs a wrong suggestion — "did you mean --limit?"
// for a flag the command now takes — which is worse than no suggestion at all,
// and nothing else would catch it.
//
// So: whatever flag tokens a converted parser mentions, its list has to name.
func TestEachFlagListNamesEveryFlagItsParserTakes(t *testing.T) {
	files := parsePackageSources(t)

	lists := map[string][]string{}
	for _, f := range files {
		for _, d := range f.Decls {
			gen, ok := d.(*ast.GenDecl)
			if !ok || gen.Tok != token.VAR {
				continue
			}
			for _, spec := range gen.Specs {
				vs, ok := spec.(*ast.ValueSpec)
				if !ok || len(vs.Names) != 1 || len(vs.Values) != 1 {
					continue
				}
				if v, ok := stringSliceLiteral(vs.Values[0]); ok {
					lists[vs.Names[0].Name] = v
				}
			}
		}
	}

	flagToken := regexp.MustCompile(`^--[a-z][a-z0-9-]*$`)
	seen := 0
	for _, f := range files {
		for _, d := range f.Decls {
			fn, ok := d.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			listName := unknownFlagListArg(fn)
			if listName == "" {
				continue
			}
			known, ok := lists[listName]
			if !ok {
				t.Errorf("%s passes %s to unknownFlag, which is not a slice literal of flags", fn.Name.Name, listName)
				continue
			}
			cmdName := unknownFlagCmdArg(fn)
			seen++
			parsed := stringLiterals(fn.Body)
			for _, lit := range parsed {
				if !flagToken.MatchString(lit) || contains(known, lit) {
					continue
				}
				if cmdName == "last" && lit == "--limit" {
					// parseLast names --limit only to reject it with a custom hint (#3405).
					continue
				}
				t.Errorf("%s takes %s but %s does not list it — a near miss would be told to use something else",
					fn.Name.Name, lit, listName)
			}
			for _, flag := range known {
				if !flagToken.MatchString(flag) {
					continue
				}
				if cmdName == "search" && flag == "--rebuild" {
					// searchWithOptions strips --rebuild before calling parseSearch.
					continue
				}
				if !contains(parsed, flag) {
					t.Errorf("%s lists %s, which %s does not accept — a near miss could suggest a rejected flag",
						listName, flag, fn.Name.Name)
				}
				if cmdName != "search" && !contains(searchFlags, flag) && flagsOfOtherCommands[flag] == "" && nearestSearchFlag(flag) == "" {
					t.Errorf("%s (%s) is not in searchFlags or flagsOfOtherCommands — `deja search %s <query>` would silently swallow it into the query",
						flag, listName, flag)
				}
			}
			if cmdName == "search" {
				for _, row := range searchFlagRows {
					for _, m := range flagToken.FindAllString(row[0], -1) {
						if !contains(known, m) {
							t.Errorf("searchFlagRows documents %s (%q) but %s does not list it",
								m, row[0], listName)
						}
					}
				}
			} else if entry, ok := helpEntryFor(cmdName); ok {
				usageText := strings.Join(entry.usage, " ")
				for _, m := range flagToken.FindAllString(usageText, -1) {
					if !contains(known, m) {
						t.Errorf("help for %q documents %s in usage (%q) but %s does not list it",
							cmdName, m, usageText, listName)
					}
				}
			}
		}
	}
	// A rename or a deleted call site would otherwise leave this test passing
	// over nothing at all.
	if seen < 10 {
		t.Errorf("found only %d converted parsers; the helper was meant to cover eleven commands plus search", seen)
	}
}

// parsePackageSources reads this package's own non-test sources. One file at a
// time rather than parser.ParseDir, which is deprecated and which would also
// pull the tests in.
func parsePackageSources(t *testing.T) []*ast.File {
	t.Helper()
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	var files []*ast.File
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		files = append(files, f)
	}
	if len(files) == 0 {
		t.Fatal("no package sources parsed")
	}
	return files
}

// unknownFlagListArg names the flag list a function passes to unknownFlag, or
// "" when it does not call it.
func unknownFlagListArg(fn *ast.FuncDecl) string {
	name := ""
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		id, ok := call.Fun.(*ast.Ident)
		if !ok || id.Name != "unknownFlag" || len(call.Args) != 3 {
			return true
		}
		if arg, ok := call.Args[2].(*ast.Ident); ok {
			name = arg.Name
		}
		return true
	})
	return name
}

func unknownFlagCmdArg(fn *ast.FuncDecl) string {
	cmd := ""
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		id, ok := call.Fun.(*ast.Ident)
		if !ok || id.Name != "unknownFlag" || len(call.Args) != 3 {
			return true
		}
		if lit, ok := call.Args[0].(*ast.BasicLit); ok && lit.Kind == token.STRING {
			if v, err := strconv.Unquote(lit.Value); err == nil {
				cmd = v
			}
		}
		return true
	})
	return cmd
}

func stringSliceLiteral(e ast.Expr) ([]string, bool) {
	comp, ok := e.(*ast.CompositeLit)
	if !ok {
		return nil, false
	}
	arr, ok := comp.Type.(*ast.ArrayType)
	if !ok {
		return nil, false
	}
	if id, ok := arr.Elt.(*ast.Ident); !ok || id.Name != "string" {
		return nil, false
	}
	out := []string{}
	for _, el := range comp.Elts {
		lit, ok := el.(*ast.BasicLit)
		if !ok || lit.Kind != token.STRING {
			return nil, false
		}
		v, err := strconv.Unquote(lit.Value)
		if err != nil {
			return nil, false
		}
		out = append(out, v)
	}
	return out, true
}

// stringLiterals is the set of string constants written inside a node, each
// reported once: a flag a parser names in two places is one omission, not two.
func stringLiterals(n ast.Node) []string {
	seen := map[string]bool{}
	var out []string
	ast.Inspect(n, func(n ast.Node) bool {
		lit, ok := n.(*ast.BasicLit)
		if !ok || lit.Kind != token.STRING {
			return true
		}
		v, err := strconv.Unquote(lit.Value)
		if err != nil || seen[v] {
			return true
		}
		seen[v] = true
		out = append(out, v)
		return true
	})
	sort.Strings(out)
	return out
}

func contains(haystack []string, needle string) bool {
	return slices.Contains(haystack, needle)
}

// One refusal, in one shape, from every command that was converted (#1829).
// Before this, `deja search --limt x` named the flag you meant and the other
// 22 sites just said no — and search was the only one that left its own name
// out, so two refusals side by side did not say which parser had spoken.

func TestUnknownFlagNamesTheCommandAndTheNearMiss(t *testing.T) {
	for _, tc := range []struct {
		name    string
		command string
		known   []string
		typed   string
		want    string
	}{
		{"dropped letter", "show", showFlags, "--limt", `show: unknown flag "--limt" — did you mean --limit?`},
		{"doubled letter", "stats", statsFlags, "--jsson", `stats: unknown flag "--jsson" — did you mean --json?`},
		{"transposition", "log", logFlags, "--lsat", `log: unknown flag "--lsat" — did you mean --last?`},
		{"truncation", "promote", promoteFlags, "--stat", `promote: unknown flag "--stat" — did you mean --state?`},
		{"search is named too", "search", searchFlags, "--limt", `search: unknown flag "--limt" — did you mean --limit?`},
		{"nothing close", "doctor", doctorFlags, "--nope", "doctor: unknown flag \"--nope\" — `deja doctor --help` lists its flags"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := unknownFlag(tc.command, tc.typed, tc.known).Error(); got != tc.want {
				t.Errorf("unknownFlag(%q, %q) = %q, want %q", tc.command, tc.typed, got, tc.want)
			}
		})
	}
}

// A suggestion is only worth making when it is probably right. These are the
// tokens that must not collect one: a bare word is not a flag at all, a single
// dash may be a value, and a token too short to be a name would match anything
// by prefix.
func TestNoSuggestionForWhatIsNotANearMiss(t *testing.T) {
	for _, typed := range []string{"nonsense", "-x", "--", "--j", "--json"} {
		if got := nearestKnownFlag(typed, showFlags); got != "" {
			t.Errorf("nearestKnownFlag(%q) = %q, want silence", typed, got)
		}
		if got := unknownFlag("show", typed, showFlags).Error(); strings.Contains(got, "did you mean") {
			t.Errorf("unknownFlag(%q) offered a suggestion: %q", typed, got)
		}
	}
}

// The refusals as the commands themselves produce them. Each parser refuses on
// the first argument, so none of these runs reaches the work the command does.
func TestConvertedCommandsRefuseWithTheirOwnName(t *testing.T) {
	for _, tc := range []struct {
		command string
		run     func(dir string, args []string) error
		typed   string
		suggest string
	}{
		{"index", cmdIndex, "--rebuidl", "--rebuild"},
		{"forget", runForget, "--sesion", "--session"},
		{"stats", runStats, "--jsn", "--json"},
		{"view", runView, "--ou", "--out"},
		{"remember", runRemember, "--projct", "--project"},
		{"doctor", func(dir string, args []string) error {
			return runDoctor(io.Discard, args, nil, dir)
		}, "--depe", "--deep"},
		{"log", func(dir string, args []string) error {
			return runLogTo(io.Discard, dir, args)
		}, "--jsonn", "--json"},
		{"restore", func(dir string, args []string) error {
			return runRestore(dir, args, io.Discard)
		}, "--forc", "--force"},
		{"promote", func(dir string, args []string) error {
			return runPromote(dir, args, io.Discard)
		}, "--nte", "--note"},
		{"handoff", func(dir string, args []string) error {
			return runHandoff(dir, args, io.Discard)
		}, "--exe", "--exec"},
	} {
		t.Run(tc.command, func(t *testing.T) {
			err := tc.run(t.TempDir(), []string{tc.typed})
			if err == nil {
				t.Fatalf("%s accepted %q", tc.command, tc.typed)
			}
			want := tc.command + ": unknown flag"
			if !strings.Contains(err.Error(), want) {
				t.Errorf("%s said %q, want it to start with %q", tc.command, err, want)
			}
			if !strings.Contains(err.Error(), "did you mean "+tc.suggest) {
				t.Errorf("%s said %q, want it to suggest %s", tc.command, err, tc.suggest)
			}
		})
	}
}

// `deja show` takes an id, so a word is not a flag and must not be answered as
// one; the refusal it gets is about the id, not about flags.
func TestShowStillRefusesASecondIdAsAnId(t *testing.T) {
	if _, err := parseShow([]string{"abc", "def"}); err == nil ||
		strings.Contains(err.Error(), "unknown flag") {
		t.Fatalf("parseShow(two ids) = %v, want the one-id refusal", err)
	}
}

// Search is the one command where the answer also decides whether a dashed
// token is a query term: a token nowhere near a flag still searches (#755).
func TestSearchStillTakesADashedQueryTerm(t *testing.T) {
	o, err := parseSearch([]string{"--retry budget"})
	if err != nil {
		t.Fatalf("parseSearch(dashed term) = %v, want it treated as a query", err)
	}
	if !strings.Contains(o.Query, "retry budget") {
		t.Fatalf("query = %q, want the dashed term in it", o.Query)
	}
}

// Every command names an unknown flag the same way — quoted — so a reader
// scanning two refusals side by side sees one convention. Three commands
// printed it bare (`unknown flag --all-matches`) while ten quoted it.
func TestUnknownFlagIsQuotedEverywhere(t *testing.T) {
	bare := regexp.MustCompile(`unknown flag %s`)
	entries, err := os.ReadDir("./")
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") || strings.HasSuffix(e.Name(), "_test.go") {
			continue
		}
		b, err := os.ReadFile(e.Name())
		if err != nil {
			t.Fatal(err)
		}
		if loc := bare.FindIndex(b); loc != nil {
			line := 1 + strings.Count(string(b[:loc[0]]), "\n")
			t.Errorf("%s:%d prints an unknown flag unquoted; use %%q like the other commands", e.Name(), line)
		}
	}
}
