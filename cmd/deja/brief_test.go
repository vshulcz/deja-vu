package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/search"
	"github.com/vshulcz/deja-vu/internal/sources"
	"github.com/vshulcz/deja-vu/internal/termwidth"
	"github.com/vshulcz/deja-vu/internal/usage"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

// brief threw its arguments away at the dispatch table, so `deja brief --json`
// printed the human screen and exited 0 — the shape #2253 fixed for friction
// and restore (#2265).
func TestBriefRefusesArgumentsItDoesNotTake(t *testing.T) {
	withTempStores(t)

	// The premise: it still prints the screen when asked for nothing.
	out, err := captureRun(t, "brief")
	if err != nil || !strings.Contains(out, "deja dev") {
		t.Fatalf("bare brief: %q err=%v", out, err)
	}

	for _, arg := range []string{"--json", "extra"} {
		out, err := captureRun(t, "brief", arg)
		if err == nil {
			t.Errorf("brief %s was accepted and printed %q", arg, out)
			continue
		}
		if !strings.Contains(err.Error(), arg) {
			t.Errorf("brief %s said %v — the message should name what it could not take", arg, err)
		}
	}
}

// One block, one project: the lines differ only by their date, and a date
// carrying its year used to cut the project further on the lines that had one
// — or take it away entirely — because each line budgeted on its own (#2128).
func TestBriefRecentBlockShowsOneProjectSpelling(t *testing.T) {
	dir := briefWideProjectAgesStore(t)
	t.Setenv("COLUMNS", "60")

	var buf bytes.Buffer
	if err := runBrief(dir, &buf); err != nil {
		t.Fatal(err)
	}
	lines := recentLines(buf.String())
	if len(lines) < 3 {
		t.Fatalf("want three recent lines, got %d:\n%s", len(lines), buf.String())
	}
	spellings := make([]string, 0, len(lines))
	dates := make([]string, 0, len(lines))
	for _, l := range lines {
		project, date := briefLineFields(t, visibleText(l))
		spellings = append(spellings, project)
		dates = append(dates, date)
	}
	// The premise: the dates are not all the same width, so a per-line budget
	// has something to differ about. Without this the test passes on any day
	// the fixture's ages happen to render alike.
	if termwidth.Columns(dates[0]) == termwidth.Columns(dates[len(dates)-1]) {
		t.Fatalf("every date is the same width %q, so this measures nothing:\n%s", dates, buf.String())
	}
	for _, s := range spellings[1:] {
		if s != spellings[0] {
			t.Errorf("one project, %d spellings in one block: %q\n%s", len(spellings), spellings, buf.String())
			break
		}
	}
	// The block still fits, and shortening did not take the project with it —
	// the budget that binds here leaves room for a tail (#1592).
	for _, l := range lines {
		if w := termwidth.Columns(visibleText(l)); w > 60 {
			t.Errorf("%d columns: %q", w, visibleText(l))
		}
	}
	if spellings[0] == "" {
		t.Errorf("every line dropped the project, so this measures nothing:\n%s", buf.String())
	}
}

// briefLineFields returns what a recent line prints for the project and the
// date. The project is empty on a line that dropped it, and the date is the
// field the title follows.
func briefLineFields(t *testing.T, text string) (project, date string) {
	t.Helper()
	_, after, ok := strings.Cut(text, "] ")
	if !ok {
		t.Fatalf("no harness on a recent line: %q", text)
	}
	// Harness, project and date are separated by " · ", and the title follows
	// the date. Two fields before the title mean a project is there. The title
	// may hold a separator of its own, so only the first two are split off.
	parts := strings.SplitN(after, " · ", 3)
	switch len(parts) {
	case 0, 1:
		t.Fatalf("no date on a recent line: %q", text)
		return "", ""
	case 2:
		return "", parts[0]
	default:
		return parts[0], parts[1]
	}
}

// A wide project and three ages that render three ways whatever day it is: a
// session from today, and two old enough to carry a year.
func briefWideProjectAgesStore(t *testing.T) string {
	t.Helper()
	tmp := hermeticEnv(t)
	root := filepath.Join(tmp, "claude", "-work-数据平台-消费者重平衡")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "claude"))
	const question = "为什么 kafka 消费者在预发环境不断重新平衡，日志里全是 rebalance 超时"
	day := 24 * time.Hour
	for i, age := range []time.Duration{2 * time.Hour, 400 * day, 380 * day} {
		sid := string(rune('a'+i)) + "1b2c3d4"
		body, err := json.Marshal(map[string]any{
			"type": "user", "sessionId": sid, "cwd": "/work/数据平台/消费者重平衡",
			"timestamp": time.Now().Add(-age).UTC().Format(time.RFC3339),
			"message":   map[string]any{"role": "user", "content": question},
		})
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, sid+".jsonl"), append(body, '\n'), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	dir := index.DefaultDir()
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	return dir
}

// `deja` with nothing indexed builds the index first, and a build it cannot run
// reported the syscall: `open /…/index.db.lock: permission denied` — an internal
// lock file nobody can act on, on the first screen a new install shows. Every
// other command routes the same failure through ensureError.
func TestBriefNamesWhyABuildCouldNotRun(t *testing.T) {
	// The refusal is produced by a directory mode, and a mode is not how
	// windows refuses a write: 0500 there leaves the directory writable and the
	// build succeeds, so the test measured the platform rather than the message.
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("directory modes do not refuse writes on windows or as root")
	}
	tmp := hermeticEnv(t)
	ro := filepath.Join(tmp, "ro")
	if err := os.MkdirAll(ro, 0o755); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(ro, "index.db")
	t.Setenv("DEJA_INDEX_DIR", dir)
	if err := os.Chmod(ro, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(ro, 0o700) })

	err := runBrief(dir, io.Discard)
	if err == nil {
		t.Fatal("a build that could not run was reported as a success")
	}
	msg := err.Error()
	if strings.Contains(msg, ".lock") || strings.Contains(msg, "permission denied") {
		t.Errorf("the refusal is still the syscall: %v", err)
	}
	if !strings.Contains(msg, "permissions") || !strings.Contains(msg, "DEJA_INDEX_DIR") {
		t.Errorf("the refusal does not say what to change: %v", err)
	}
}

// A project name is the part of a `recent` prefix that grows without bound, and
// a CJK one is two columns per character. At COLUMNS=60 the prefix alone was 58
// columns, the title budget fell to its 12-column floor, and the line ended at
// 71 — a wrapped row, not a ragged end (#1592).
func TestBriefRecentLinesShortenAWideProject(t *testing.T) {
	dir := briefCJKProjectStore(t)

	for _, width := range []int{60, 80} {
		t.Setenv("COLUMNS", strconv.Itoa(width))
		var buf bytes.Buffer
		if err := runBrief(dir, &buf); err != nil {
			t.Fatal(err)
		}
		lines := recentLines(buf.String())
		if len(lines) == 0 {
			t.Fatalf("COLUMNS=%d: no recent lines:\n%s", width, buf.String())
		}
		for _, l := range lines {
			text := visibleText(l)
			if w := termwidth.Columns(text); w > width {
				t.Errorf("COLUMNS=%d: %d columns: %q", width, w, text)
			}
			// Shortening must not take the whole title with it: the line is
			// there to name the work, and an empty column names nothing.
			if i := strings.LastIndex(text, " · "); i < 0 || termwidth.Columns(text[i+3:]) < 8 {
				t.Errorf("COLUMNS=%d: nothing readable left of the title: %q", width, text)
			}
		}
	}
}

func briefCJKProjectStore(t *testing.T) string {
	t.Helper()
	tmp := hermeticEnv(t)
	root := filepath.Join(tmp, "claude", "-work-数据平台-消费者重平衡")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "claude"))
	const question = "为什么 kafka 消费者在预发环境不断重新平衡，日志里全是 rebalance 超时"
	for i, age := range []time.Duration{400 * 24 * time.Hour, 380 * 24 * time.Hour, 12 * 24 * time.Hour} {
		sid := string(rune('a'+i)) + "1b2c3d4"
		body, err := json.Marshal(map[string]any{
			"type": "user", "sessionId": sid, "cwd": "/work/数据平台/消费者重平衡",
			"timestamp": time.Now().Add(-age).UTC().Format(time.RFC3339),
			"message":   map[string]any{"role": "user", "content": question},
		})
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, sid+".jsonl"), append(body, '\n'), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	dir := index.DefaultDir()
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	return dir
}

// The longest harness name and a date carrying its year leave 42 columns of a
// 60-column pane, and the two floors — eight for a path tail, twelve for a
// title — do not fit in the remaining eighteen. Review found that combination
// still printing 62–63 columns, so the project goes rather than the line
// running past the edge with an unreadable stub of a path on it (#1592).
func TestBriefProjectGoesWhenTheFloorsCannotFit(t *testing.T) {
	t.Setenv("COLUMNS", "60")

	// `recent    ` + ` [antigravity] ` + ` · ` + `Jul 20 2025` + ` · `
	const restWithLongHarness = 42
	if got := fitBriefProject("work/数据平台/消费者重平衡", restWithLongHarness); got != "" {
		t.Errorf("project = %q, want it dropped: 60 - 42 - 12 - 1 leaves five columns", got)
	}

	// With an ordinary harness there is room, and the tail is what survives.
	const restWithClaude = 36
	got := fitBriefProject("work/数据平台/消费者重平衡", restWithClaude)
	if got == "" {
		t.Fatal("project dropped when there was room for it")
	}
	if !strings.HasPrefix(got, "…") || !strings.HasSuffix(got, "重平衡") {
		t.Errorf("project = %q, want the tail of the path", got)
	}
	if w := termwidth.Columns(got); restWithClaude+w+briefRecentTitleFloor+1 > 60 {
		t.Errorf("project = %q (%d columns): the line would still overflow", got, w)
	}

	// A project that already fits is untouched, whatever the floors say.
	if got := fitBriefProject("api", restWithLongHarness); got != "api" {
		t.Errorf("project = %q, want it left alone", got)
	}
}

// briefCJKStore is briefStore with Chinese first messages, so the titles the
// `recent` lines carry are twice as wide as they are long.
func briefCJKStore(t *testing.T, ages ...time.Duration) string {
	t.Helper()
	tmp := hermeticEnv(t)
	root := filepath.Join(tmp, "claude", "-w-支付服务")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "claude"))
	titles := []string{
		"重试队列在预发环境卡住了工作进程同时醒来需要加抖动才能错开彼此的唤醒时间",
		"支付回调重复投递导致订单状态机反复回退需要幂等键来兜底整个链路的写入",
		"消费者重平衡在预发环境反复抖动需要把会话超时和心跳间隔一起调大才稳定",
	}
	for i, age := range ages {
		sid := string(rune('a'+i)) + "1b2c3d4"
		at := time.Now().Add(-age).UTC().Format(time.RFC3339)
		body := `{"type":"user","sessionId":"` + sid + `","cwd":"/w/支付服务","timestamp":"` + at +
			`","message":{"role":"user","content":"` + titles[i%len(titles)] + `"}}` + "\n"
		if err := os.WriteFile(filepath.Join(root, sid+".jsonl"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := index.Ensure(index.DefaultDir(), "", true, nil); err != nil {
		t.Fatal(err)
	}
	return index.DefaultDir()
}

// #1073 budgeted the title against what is already on the line so the `recent`
// lines stop wrapping. The budget is columns and the cut counted runes, so a
// Chinese title of 35 characters passed a 44-rune cap and printed 110 columns
// on an 80-column terminal — and did not shrink when the terminal did, because
// the rune count was under the cap at every width.
func TestBriefRecentLinesFitWithCJKTitles(t *testing.T) {
	day := 24 * time.Hour
	dir := briefCJKStore(t, 12*day, 15*day, 19*day)

	for _, width := range []int{60, 80, 100, 120} {
		t.Setenv("COLUMNS", strconv.Itoa(width))
		var buf bytes.Buffer
		if err := runBrief(dir, &buf); err != nil {
			t.Fatal(err)
		}
		lines := recentLines(buf.String())
		if len(lines) < 2 {
			t.Fatalf("want several recent lines, got %d:\n%s", len(lines), buf.String())
		}
		for _, l := range lines {
			if w := termwidth.Columns(visibleText(l)); w > width {
				t.Errorf("COLUMNS=%d: line is %d columns: %q", width, w, visibleText(l))
			}
		}
	}
}

// The lines still grow with the terminal — a fixed conservative cap would pass
// the test above and show the same stub at every width.
func TestBriefCJKTitlesGrowWithTheTerminal(t *testing.T) {
	day := 24 * time.Hour
	dir := briefCJKStore(t, 12*day, 15*day, 19*day)

	width := func(cols string) int {
		t.Setenv("COLUMNS", cols)
		var buf bytes.Buffer
		if err := runBrief(dir, &buf); err != nil {
			t.Fatal(err)
		}
		lines := recentLines(buf.String())
		if len(lines) == 0 {
			t.Fatalf("no recent lines at COLUMNS=%s:\n%s", cols, buf.String())
		}
		return termwidth.Columns(visibleText(lines[0]))
	}
	narrow, wide := width("60"), width("120")
	if wide <= narrow {
		t.Errorf("the line did not grow with the terminal: 60 gave %d, 120 gave %d", narrow, wide)
	}
}

// The cut itself, at the three shapes a title takes.
func TestTrimBriefTitleToBoundsColumns(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   string
		max  int
	}{
		{"latin", "the retry queue stalls on staging and all the workers wake together", 44},
		{"chinese", "重试队列在预发环境卡住了工作进程同时醒来需要加抖动才能错开彼此的唤醒时间", 44},
		{"mixed", "重试队列 stalls on staging 工作进程同时醒来 needs jitter to spread them", 30},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := trimBriefTitleTo(tc.in, tc.max)
			// The ellipsis is added past the budget, as it always was.
			if w := termwidth.Columns(strings.TrimSuffix(got, "…")); w > tc.max {
				t.Errorf("trimBriefTitleTo(%q, %d) prints %d columns: %q", tc.in, tc.max, w, got)
			}
			if !strings.HasPrefix(tc.in, strings.TrimSuffix(got, "…")) {
				t.Errorf("the cut is not a prefix of the title: %q", got)
			}
		})
	}
	// A title inside the budget is returned whole, without a mark claiming
	// something was dropped.
	short := "重试队列卡住了"
	if got := trimBriefTitleTo(short, 44); got != short {
		t.Errorf("a title inside the budget was cut: %q", got)
	}
}

// The first screen printed only when stdout was a terminal, so `deja | less`
// and `deja > file` got the usage screen and there was no way to ask for it on
// purpose — the obvious guess was a search for the word (#2108).
func TestBriefIsACommandAndPrintsIntoAPipe(t *testing.T) {
	tmp := hermeticEnv(t)
	root := filepath.Join(tmp, "claude", "projects", "-tmp-app")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	rec := claudeRecord(t, map[string]any{
		"type": "user", "sessionId": "s1", "cwd": "/tmp/app",
		"timestamp": time.Now().Add(-2 * time.Hour).UTC().Format(time.RFC3339),
		"message":   map[string]any{"role": "user", "content": "the pool was exhausted while the migration held the lock"},
	})
	if err := os.WriteFile(filepath.Join(root, "s1.jsonl"), []byte(rec), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRun(t, "index"); err != nil {
		t.Fatal(err)
	}
	// captureRun's stdout is a pipe, which is the case this is about.
	out, err := captureRun(t, "brief")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"across 1 agent", "recent", "the pool was exhausted"} {
		if !strings.Contains(out, want) {
			t.Errorf("the first screen does not carry %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "no matches") || strings.Contains(out, "Usage:") {
		t.Errorf("`deja brief` did not print the first screen:\n%s", out)
	}
}

// The brief is the owner's own screen, and browsing is what the `search` rule
// governs — `recent` honours it. Two lines below it, `asked` and `hit` were
// filtered by `auto` instead, so a policy that keeps work out of casual greps
// while leaving auto-recall on printed "all N sessions are withheld from your
// own searches" and the text of those sessions underneath (#1312).
func TestBriefHonoursOneRuleAcrossTheWholeScreen(t *testing.T) {
	dir := seedAgedSessions(t, map[string]agedSession{
		"a1": {"why does the docker build fail on arm64", 40 * 24 * time.Hour},
		"a2": {"why does the docker build fail on arm64", 6 * 24 * time.Hour},
	})
	writePolicy(t, `{"activations":{"search":{"local":false},"auto":{"local":true}}}`)
	var buf bytes.Buffer
	if err := runBrief(dir, &buf); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if !strings.Contains(out, "search rule keeps them out of your own searches") {
		t.Fatalf("the screen does not report the rule under test:\n%s", out)
	}
	if strings.Contains(out, "docker build fail on arm64") {
		t.Errorf("session text printed under a line saying it is all withheld:\n%s", out)
	}
}

// The reverse policy — browsing allowed, auto-recall denied — must keep those
// lines, since nothing withholds them from the reader.
func TestBriefKeepsItsInsightsWhenOnlyTheAgentIsDenied(t *testing.T) {
	dir := seedAgedSessions(t, map[string]agedSession{
		"a1": {"why does the docker build fail on arm64", 40 * 24 * time.Hour},
		"a2": {"why does the docker build fail on arm64", 6 * 24 * time.Hour},
	})
	writePolicy(t, `{"activations":{"search":{"local":true},"auto":{"local":false}}}`)
	var buf bytes.Buffer
	if err := runBrief(dir, &buf); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if !strings.Contains(out, "asked      ") {
		t.Errorf("a rule that only stops the agent emptied the reader's own screen:\n%s", out)
	}
}

// The withheld line itself names whichever rule withholds everything, and it
// prefers auto; on this policy the rule that emptied the screen is search.
func TestBriefNamesTheRuleThatEmptiedTheScreen(t *testing.T) {
	storeWith(t, false)
	writePolicy(t, `{"activations":{"search":{"local":false},"auto":{"local":true}}}`)
	var buf bytes.Buffer
	if err := runBrief(index.DefaultDir(), &buf); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if !strings.Contains(out, "withheld") {
		t.Fatalf("no withheld line on a store the policy withholds:\n%s", out)
	}
	if !strings.Contains(out, "search rule") {
		t.Errorf("the line names a rule other than the one that emptied the screen:\n%s", out)
	}
	if strings.Contains(out, "recent ") {
		t.Errorf("sessions listed under a line saying they are all withheld:\n%s", out)
	}
}

// When two rules withhold everything, the reader is told the one that emptied
// the screen in front of them rather than the one they never see fail.
func TestBriefPrefersTheRuleItObeys(t *testing.T) {
	storeWith(t, false)
	writePolicy(t, `{"activations":{"search":{"local":false},"auto":{"local":false}}}`)
	var buf bytes.Buffer
	if err := runBrief(index.DefaultDir(), &buf); err != nil {
		t.Fatal(err)
	}
	if out := buf.String(); !strings.Contains(out, "search rule keeps them out of your own searches") {
		t.Errorf("with both rules withholding, the screen names the one it does not obey:\n%s", out)
	}
}

// seedAgedSessions writes one Claude session per entry at the given age and
// builds the index, so a brief can be measured against a store shaped on
// purpose rather than against whatever the developer's own machine holds.
func seedAgedSessions(t *testing.T, sessions map[string]struct {
	title string
	age   time.Duration
}) string {
	t.Helper()
	tmp := hermeticEnv(t)
	root := filepath.Join(tmp, "claude", "proj-d")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "claude"))
	now := time.Now()
	for sid, s := range sessions {
		at := now.Add(-s.age)
		// A session meant to be "today" (a sub-day age) lands on the previous
		// calendar day when the test runs just after midnight, and the brief's
		// today/week counts — which reckon the day in local time and have no
		// injectable now — then read one line short. The premise is gone, not
		// the code, so skip rather than fail a green build at 00:1x. Compare in
		// local time, the frame the brief uses.
		if s.age < 24*time.Hour && at.YearDay() != now.YearDay() {
			t.Skip("seeded session crosses the midnight boundary; brief today/week semantics are clock-relative")
		}
		when := at.UTC().Format("2006-01-02T15:04:05Z")
		body := fmt.Sprintf(`{"type":"user","sessionId":%q,"cwd":"/w/d","timestamp":%q,"message":{"role":"user","content":%q}}`,
			sid, when, s.title) + "\n"
		if err := os.WriteFile(filepath.Join(root, sid+".jsonl"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	dir := index.DefaultDir()
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	return dir
}

type agedSession = struct {
	title string
	age   time.Duration
}

// A store whose only sessions are today's makes "this week" repeat "today"
// verbatim, which on a one-session store put the same number on three of the
// four lines (#842).
func TestBriefDropsWeekLineThatRestatesToday(t *testing.T) {
	dir := seedAgedSessions(t, map[string]agedSession{
		"d1": {"fix the flaky auth test in the login handler", 20 * time.Minute},
	})
	var out bytes.Buffer
	if err := runBrief(dir, &out); err != nil {
		t.Fatal(err)
	}
	s := out.String()
	if !strings.Contains(s, "today      1 session") {
		t.Fatalf("brief lost the today line:\n%s", s)
	}
	if strings.Contains(s, "this week") {
		t.Fatalf("week line restates today with weaker numbers:\n%s", s)
	}
}

// The line still has to appear when the week holds sessions today does not,
// or the fix above has simply deleted a fact.
func TestBriefKeepsWeekLineWhenItAddsSessions(t *testing.T) {
	dir := seedAgedSessions(t, map[string]agedSession{
		"d1": {"fix the flaky auth test in the login handler", 20 * time.Minute},
		"d2": {"why does the docker build fail on arm64", 3 * 24 * time.Hour},
	})
	var out bytes.Buffer
	if err := runBrief(dir, &out); err != nil {
		t.Fatal(err)
	}
	s := out.String()
	if !strings.Contains(s, "this week  2 sessions") {
		t.Fatalf("week line dropped though the week holds more than today:\n%s", s)
	}
}

// Recalls the week has and today does not are the other reason to keep it.
func TestBriefKeepsWeekLineWhenItAddsRecalls(t *testing.T) {
	dir := seedAgedSessions(t, map[string]agedSession{
		"d1": {"fix the flaky auth test in the login handler", 20 * time.Minute},
	})
	usage.RecordDigest(dir, usage.KindDejaVu, "digest", 1, 2048)
	var out bytes.Buffer
	if err := runBrief(dir, &out); err != nil {
		t.Fatal(err)
	}
	s := out.String()
	if !strings.Contains(s, "déjà vu moment") {
		t.Fatalf("week line dropped a déjà vu moment today's line never carried:\n%s", s)
	}
}

// seedAskedAndReused builds a store where the same question was asked in
// sessions far enough apart to earn the "asked" line, and where recalls land
// on that same work — the normal case, since what you repeat is what gets
// recalled.
func seedAskedAndReused(t *testing.T, reusedTitle string) string {
	t.Helper()
	dir := seedAgedSessions(t, map[string]agedSession{
		"a1": {"why does the docker build fail on arm64", 40 * 24 * time.Hour},
		"a2": {"why does the docker build fail on arm64", 6 * 24 * time.Hour},
		"a3": {reusedTitle, 5 * 24 * time.Hour},
	})
	for range 4 {
		usage.RecordServedSessions(dir, usage.KindRecall, 100, 1, false, 1000, []string{"a3"})
	}
	return dir
}

// #843: the question asked twice and the memory kept being recalled are
// usually the same work, and the screen printed its title once as "asked" and
// again as "reused".
func TestBriefDoesNotPrintTheSameTitleTwice(t *testing.T) {
	dir := seedAskedAndReused(t, "why does the docker build fail on arm64")
	var out bytes.Buffer
	if err := runBrief(dir, &out); err != nil {
		t.Fatal(err)
	}
	s := out.String()
	if !strings.Contains(s, "asked      ") {
		t.Fatalf("no asked line to judge:\n%s", s)
	}
	// Only the two insight labels are judged here: `recent` lists sessions and
	// will legitimately show a repeated question three times.
	n := 0
	for ln := range strings.SplitSeq(s, "\n") {
		if !strings.HasPrefix(ln, "asked ") && !strings.HasPrefix(ln, "reused ") {
			continue
		}
		if strings.Contains(ln, "why does the docker build fail on arm64") {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("title carries %d of the insight labels, want 1:\n%s", n, s)
	}
	if !strings.Contains(s, "4× re-used recently") {
		t.Fatalf("dropping the second title also dropped the reuse count:\n%s", s)
	}
	if strings.Contains(s, "reused     ") {
		t.Fatalf("reused block kept its own line for a title already shown:\n%s", s)
	}
}

// Different work under each label is two facts, and both keep their line.
func TestBriefKeepsReusedLineWhenItNamesOtherWork(t *testing.T) {
	dir := seedAskedAndReused(t, "pgbouncer prepared statements blow up")
	var out bytes.Buffer
	if err := runBrief(dir, &out); err != nil {
		t.Fatal(err)
	}
	s := out.String()
	if !strings.Contains(s, "asked      why does the docker build fail on arm64") {
		t.Fatalf("asked line missing:\n%s", s)
	}
	if !strings.Contains(s, "reused     pgbouncer prepared statements blow up") {
		t.Fatalf("reused line collapsed into a different question:\n%s", s)
	}
	if !strings.Contains(s, "last worked ") {
		t.Fatalf("reused block lost the date it was last worked:\n%s", s)
	}
}

// Two questions can agree for the 44 columns the screen has room for and still
// be different work. Judging sameness on the cut text deleted the second one
// (#843).
func TestBriefKeepsReusedLineWhenTitlesOnlyShareTheirOpening(t *testing.T) {
	const asked = "why does the docker build keep failing on the arm64 runner"
	const other = "why does the docker build keep failing on the raspberry pi"
	dir := seedAgedSessions(t, map[string]agedSession{
		"a1": {asked, 40 * 24 * time.Hour},
		"a2": {asked, 6 * 24 * time.Hour},
		"a3": {other, 5 * 24 * time.Hour},
	})
	for range 4 {
		usage.RecordServedSessions(dir, usage.KindRecall, 100, 1, false, 1000, []string{"a3"})
	}
	var out bytes.Buffer
	if err := runBrief(dir, &out); err != nil {
		t.Fatal(err)
	}
	s := out.String()
	if !strings.Contains(s, "asked      ") {
		t.Fatalf("no asked line to judge:\n%s", s)
	}
	if !strings.Contains(s, "reused     ") {
		t.Fatalf("a different question was folded away because its first 44 columns matched:\n%s", s)
	}
}

// The asked span ends at the newest asking; the memory agents keep recalling
// is often an older one. Folding the reuse count into the asked line must not
// carry away the date that says which (#843).
func TestBriefKeepsLastWorkedWhenTheReusedMemoryIsOlderThanTheNewestAsking(t *testing.T) {
	const q = "why does the docker build fail on arm64"
	dir := seedAgedSessions(t, map[string]agedSession{
		"a1": {q, 40 * 24 * time.Hour},
		"a2": {q, 2 * 24 * time.Hour},
	})
	for range 4 {
		usage.RecordServedSessions(dir, usage.KindRecall, 100, 1, false, 1000, []string{"a1"})
	}
	var out bytes.Buffer
	if err := runBrief(dir, &out); err != nil {
		t.Fatal(err)
	}
	s := out.String()
	if !strings.Contains(s, "4× re-used recently") {
		t.Fatalf("reuse count missing:\n%s", s)
	}
	want := "last worked " + search.RelativeDate(time.Now().Add(-40*24*time.Hour))
	if !strings.Contains(s, want) {
		t.Fatalf("the recalled memory is 40 days old, not 2 — %q missing:\n%s", want, s)
	}
}

// The other half: when the reused memory is the newest asking, its date is the
// right end of the span already and printing it twice is the repetition #843
// removed.
func TestBriefDropsLastWorkedWhenItRepeatsTheSpan(t *testing.T) {
	dir := seedAskedAndReused(t, "why does the docker build fail on arm64")
	var out bytes.Buffer
	if err := runBrief(dir, &out); err != nil {
		t.Fatal(err)
	}
	if s := out.String(); strings.Contains(s, "last worked") {
		t.Fatalf("last worked repeats the span's right end:\n%s", s)
	}
}

// A week that served recalls on days that are not today carries a number the
// today line never had, so it is not an echo (#842).
func TestBriefKeepsWeekLineForRecallsServedEarlierInTheWeek(t *testing.T) {
	dir := seedAgedSessions(t, map[string]agedSession{
		"d1": {"fix the flaky auth test in the login handler", 20 * time.Minute},
	})
	writeUsageEvents(t, dir, 2, 60*time.Hour)
	var out bytes.Buffer
	if err := runBrief(dir, &out); err != nil {
		t.Fatal(err)
	}
	if s := out.String(); !strings.Contains(s, "this week  1 session · 2 recalls") {
		t.Fatalf("week line dropped two recalls today never served:\n%s", s)
	}
}

// writeUsageEvents backdates recall events, which the usage recorders cannot
// do: they stamp time.Now().
func writeUsageEvents(t *testing.T, dir string, n int, age time.Duration) {
	t.Helper()
	var b strings.Builder
	for range n {
		e := usage.Event{Time: time.Now().Add(-age).UTC(), Kind: usage.KindRecall, Bytes: 100, Sessions: 1}
		raw, err := json.Marshal(e)
		if err != nil {
			t.Fatal(err)
		}
		b.Write(raw)
		b.WriteByte('\n')
	}
	if err := os.WriteFile(usage.Path(dir), []byte(b.String()), 0o600); err != nil {
		t.Fatal(err)
	}
}

// The first screen a new user sees, on a machine whose store deja is not
// allowed to read: it told them no agent had run here, which sends them looking
// for a store that is not missing. Every other surface answers through
// `emptyIndexHint` and says a store could not be read; this one has no branch
// of its own, which is #1020's fix having missed a caller (#1979).
func TestTheEmptyScreenSaysAStoreCouldNotBeRead(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("directory permissions do not deny reads here")
	}
	hermeticEnv(t)
	claude := os.Getenv("DEJA_CLAUDE_ROOT")
	seedClaude(t, claude, "app", "s1", "the pgbouncer pool kept timing out", "we fixed the retry")

	proj := filepath.Join(claude, "-app")
	if err := os.Chmod(proj, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(proj, 0o755) })

	var b bytes.Buffer
	printNoHistory(&b, false)
	// Newlines collapsed: this is wrapped copy, and an assertion that breaks
	// when a line wraps differently is about the wrapping, not the claim.
	out := strings.Join(strings.Fields(b.String()), " ")

	if !strings.Contains(out, "could not be read") {
		t.Errorf("the empty screen does not say a store was unreadable:\n%s", out)
	}
	if !strings.Contains(out, "deja doctor") {
		t.Errorf("the empty screen does not point at what names it:\n%s", out)
	}
	if strings.Contains(out, "no agent has") {
		t.Errorf("the empty screen still says no agent ran here:\n%s", out)
	}
}

// And on a machine that genuinely has nothing, the introduction is unchanged —
// it is the one screen written to explain deja rather than to answer.
func TestTheEmptyScreenStillIntroducesDejaOnAQuietMachine(t *testing.T) {
	hermeticEnv(t)

	var b bytes.Buffer
	printNoHistory(&b, false)
	out := strings.Join(strings.Fields(b.String()), " ")

	if !strings.Contains(out, "no agent has") {
		t.Errorf("the introduction changed on an empty machine:\n%s", out)
	}
	if strings.Contains(out, "could not be read") {
		t.Errorf("an empty machine was told a store is unreadable:\n%s", out)
	}
}

// Two locked stores are named as two, and the pronoun follows the noun. The
// helper beside this one has said "them" since the wording was written; this
// screen's own copy hardcoded "it" (#1979).
func TestTheEmptyScreenCountsLockedStoresCorrectly(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("directory permissions do not deny reads here")
	}
	hermeticEnv(t)
	claude := os.Getenv("DEJA_CLAUDE_ROOT")
	codex := os.Getenv("DEJA_CODEX_ROOT")
	seedClaude(t, claude, "app", "s1", "the pgbouncer pool kept timing out", "we fixed the retry")
	if err := os.MkdirAll(filepath.Join(codex, "sessions"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(codex, "sessions", "s.jsonl"), []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{filepath.Join(claude, "-app"), filepath.Join(codex, "sessions")} {
		if err := os.Chmod(p, 0o000); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.Chmod(p, 0o755) })
	}

	var b bytes.Buffer
	printNoHistory(&b, false)
	out := strings.Join(strings.Fields(b.String()), " ")
	if !strings.Contains(out, "2 stores") {
		t.Errorf("two locked stores are not counted:\n%s", out)
	}
	if strings.Contains(out, "names it") {
		t.Errorf("two stores, one pronoun:\n%s", out)
	}
}

// A store excluded with DEJA_EXCLUDE_HARNESSES was said to be excluded in the
// exclude file, which the reader never wrote (#5506 follow-up).
func TestTheEmptyScreenNamesTheEnvironmentForAnEnvExclusion(t *testing.T) {
	hermeticEnv(t)
	t.Setenv("DEJA_EXCLUDE_HARNESSES", "claude")

	var b bytes.Buffer
	printNoHistory(&b, false)
	out := strings.Join(strings.Fields(b.String()), " ")
	if !strings.Contains(out, "excluded in DEJA_EXCLUDE_HARNESSES") {
		t.Errorf("the empty screen does not name the environment:\n%s", out)
	}
	if strings.Contains(out, reportPath(sources.ExcludePath())) {
		t.Errorf("the empty screen names an exclude file nobody wrote:\n%s", out)
	}
}

// The counters deliberately leave the future out, so the screen has to say
// those sessions exist — otherwise "recent" shows dates that have not happened
// above counters that do not mention them (#696).
func TestBriefNamesSessionsAheadOfTheClock(t *testing.T) {
	tmp := hermeticEnv(t)
	root := filepath.Join(tmp, "claude", "proj-p")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "claude"))
	now := time.Now()
	write := func(sid string, at time.Time) {
		body := `{"type":"user","sessionId":"` + sid + `","cwd":"/w/p","timestamp":"` +
			at.UTC().Format(time.RFC3339) + `","message":{"role":"user","content":"work in ` + sid + `"}}` + "\n"
		if err := os.WriteFile(filepath.Join(root, sid+".jsonl"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("now", now.Add(-time.Hour))
	write("ahead", now.AddDate(0, 0, 6))
	dir := index.DefaultDir()
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if err := runBrief(dir, &buf); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "stamped later than this machine's clock") {
		t.Errorf("brief does not mention the future session:\n%s", buf.String())
	}
	// A store with none of them says nothing extra. `deja forget` is what
	// takes a session out: deleting the transcript no longer does, because a
	// rebuild keeps what the client's cleanup deleted (#3529).
	if err := os.Remove(filepath.Join(root, "ahead.jsonl")); err != nil {
		t.Fatal(err)
	}
	if _, err := index.Forget(dir, index.ForgetOptions{Session: "ahead"}); err != nil {
		t.Fatal(err)
	}
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	buf.Reset()
	if err := runBrief(dir, &buf); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(buf.String(), "ahead") {
		t.Errorf("clean store mentions the future:\n%s", buf.String())
	}
}

// The brief is gated on "can this reader take a screen", not on "does this
// reader want colour". They were the same predicate, so NO_COLOR and TERM=dumb
// printed the usage screen to someone sitting at a terminal with an index
// (#1596). The drawing predicate keeps refusing both — TestDumbTerminalGetsNoDrawing
// pins that — because a logo and a live display are escapes, and the brief is
// text.
func TestBriefIsNotGatedOnColour(t *testing.T) {
	f := drawableCharDevice(t)

	t.Setenv("NO_COLOR", "")
	t.Setenv("TERM", "xterm-256color")
	if !briefWanted(f) {
		t.Fatal("a character device with a real TERM stopped taking the brief")
	}

	t.Setenv("NO_COLOR", "1")
	if !briefWanted(f) {
		t.Error("NO_COLOR replaced the brief with the usage screen")
	}
	t.Setenv("NO_COLOR", "")
	t.Setenv("TERM", "dumb")
	if !briefWanted(f) {
		t.Error("TERM=dumb replaced the brief with the usage screen")
	}
	// Drawing still refuses both: that is a separate question and #903's
	// answer to it stands.
	if defaultLogoWanted(f) {
		t.Error("TERM=dumb started drawing again")
	}
}

// The width budget asks the same question as the gate. It used to ask whether
// colour was wanted, so the readers the gate newly admits were told "do not
// cut" and their narrow lines wrapped (#1596).
func TestPrintableWidthFollowsTheScreenNotTheColour(t *testing.T) {
	f := drawableCharDevice(t)
	t.Setenv("COLUMNS", "")
	t.Setenv("TERM", "xterm-256color")
	t.Setenv("NO_COLOR", "1")
	if got := printableWidth(f); got == 0 {
		t.Error("NO_COLOR reader was told not to cut, so the brief wraps instead of fitting")
	}
	t.Setenv("NO_COLOR", "")
	t.Setenv("TERM", "dumb")
	if got := printableWidth(f); got == 0 {
		t.Error("TERM=dumb reader was told not to cut")
	}
	// A pipe still reads as "do not cut": a script wants the text whole.
	r, wpipe, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	defer wpipe.Close()
	if got := printableWidth(wpipe); got != 0 {
		t.Errorf("a pipe got a width of %d, want 0", got)
	}
}

// Every line the brief prints has to fit the width it laid out for. The
// exception is the `recent` block, where a prefix that already fills the line
// keeps a 12-column title rather than an empty one — that floor is deliberate
// and TestBriefRecentLinesHonourColumns pins it.
//
// Measured before the fix on a 1548-session store: 5 lines over at COLUMNS=60,
// 9 at 50, 12 at 40. `today`, `before` and `try` carried no budget at all, and
// the fixed-prefix lines were cut to a constant 44 columns — 56 with the label,
// wider than the pane (#1588).
// briefNarrowStore seeds the lines the width contract is about: one question
// asked in three sessions (which is what puts `asked` and `before` on the
// screen), a project path of the length people actually have, a span that
// carries a year, and recalls served today so the `today` line grows its
// sizes. The first fixture for this test had none of those and passed while
// `before` was 64 columns wide (#1588).
func briefNarrowStore(t *testing.T) string {
	t.Helper()
	tmp := hermeticEnv(t)
	root := filepath.Join(tmp, "claude", "-work-platform-services-kafka-consumer-rebalance")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "claude"))
	const question = "why does the kafka consumer rebalance keep flapping in staging"
	for i, age := range []time.Duration{400 * 24 * time.Hour, 380 * 24 * time.Hour, 12 * 24 * time.Hour} {
		sid := string(rune('a'+i)) + "1b2c3d4"
		at := time.Now().Add(-age).UTC().Format(time.RFC3339)
		body := fmt.Sprintf(`{"type":"user","sessionId":%q,"cwd":"/work/platform/services/kafka-consumer-rebalance","timestamp":%q,"message":{"role":"user","content":%q}}`,
			sid, at, question) + "\n"
		if err := os.WriteFile(filepath.Join(root, sid+".jsonl"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	dir := index.DefaultDir()
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	usage.RecordDigest(dir, usage.KindRecall, strings.Repeat("x", 4096), 3, 262144)
	usage.RecordDigest(dir, usage.KindDejaVu, "dv digest", 2, 4096)
	return dir
}

// The three lines this fix budgets have to fit the width the brief laid out
// for: `today`, `try`, and `before` in the shape where shortening the project
// keeps every fact.
//
// Not every line on this screen fits. `withheld` is 103 columns of fixed
// wording, `ahead` passes 60 once the count reaches three digits, `before`
// carrying the re-use suffix is 110, and the `recent` floor keeps a 12-column
// title rather than an empty one on purpose. Those need either a wording
// change, which is not a layout decision, or one choke point every line goes
// through; #1588 carries the reproductions.
func TestBriefTodayTryAndBeforeFitTheirPane(t *testing.T) {
	dir := briefNarrowStore(t)

	// 60 is the width the code lays out for — briefTitleBudget names the
	// 60-column split pane #604 fixed — and 80 is the default. Below 60 the
	// screen still overflows on the header, the suggestion and the `recent`
	// floor; those are separate items, not this one.
	for _, width := range []int{60, 80} {
		t.Setenv("COLUMNS", strconv.Itoa(width))
		var buf bytes.Buffer
		if err := runBrief(dir, &buf); err != nil {
			t.Fatal(err)
		}
		var seen int
		for line := range strings.SplitSeq(strings.TrimRight(buf.String(), "\n"), "\n") {
			text := visibleText(line)
			if !strings.HasPrefix(text, "today ") && !strings.HasPrefix(text, "try ") &&
				!strings.HasPrefix(text, "before ") {
				continue
			}
			seen++
			if w := termwidth.Columns(text); w > width {
				t.Errorf("COLUMNS=%d: %d columns: %q", width, w, text)
			}
		}
		if seen < 2 {
			t.Fatalf("COLUMNS=%d: the fixture printed %d of the three budgeted lines:\n%s", width, seen, buf.String())
		}
	}
}

// fitBriefWhen cuts the project out of a line and pastes the result back
// between byte offsets taken from the original string. Since #1825 the cut
// returns composed text, so the middle of that splice is not the bytes it
// replaced — the offsets belong to the original, which is what keeps it safe,
// and this says so (#1842).
func TestTheBriefSpliceSurvivesADecomposedProjectName(t *testing.T) {
	const acute, diaeresis = "́", "̈"
	for _, project := range []string{
		"u" + diaeresis + "ber-server-project-with-a-long-name",
		"über-server-project-with-a-long-name",
		"cafe" + acute + "-deploy-pipeline-project-name-here",
	} {
		line := "worked in " + project + " · last worked 3 days ago"
		// Below 55 the guard keeps the line whole (there would be nothing
		// readable left of the name); 55 and up are the widths that actually
		// splice, which is what this test is about.
		for _, room := range []int{30, 55, 60, 65, 70, 75, 80} {
			got := fitBriefWhen(line, room)
			if !utf8.ValidString(got) {
				t.Errorf("the splice produced invalid UTF-8 at room=%d: %q", room, got)
			}
			// The facts after the name are what the line exists for, so the
			// splice must hand them back whole however the middle was cut.
			if !strings.HasSuffix(got, " · last worked 3 days ago") {
				t.Errorf("room=%d: the splice damaged the tail of the line: %q", room, got)
			}
			if !strings.HasPrefix(got, "worked in ") {
				t.Errorf("room=%d: the splice damaged the head of the line: %q", room, got)
			}
			if strings.Contains(got, "�") {
				t.Errorf("the splice produced a replacement character at room=%d: %q", room, got)
			}
			// One spelling at every width: a line that fits used to come back
			// as stored while a cut one came back composed, so the same
			// project appeared two ways in one screen (#1844).
			if strings.Contains(got, "u\u0308") || strings.Contains(got, "e\u0301") {
				t.Errorf("room=%d: the decomposed spelling reached the screen: %q", room, got)
			}
		}
	}
}

// The other two brief helpers that cut had the same split: a value that fits
// kept its stored spelling, a cut one came back composed. All three compose
// now, so one screen shows one spelling (#1844).
func TestEveryBriefHelperShowsOneSpelling(t *testing.T) {
	const diaeresis, acute = "̈", "́"
	decomposed := "u" + diaeresis + "ber-server-project"
	long := decomposed + strings.Repeat("-and-more", 8)

	for _, tc := range []struct {
		name string
		got  string
	}{
		{"a title that fits", trimBriefTitleTo("cafe"+acute+" deploy", 60)},
		{"a title that is cut", trimBriefTitleTo("cafe"+acute+" deploy "+strings.Repeat("x", 200), 20)},
		{"a project that fits", fitBriefProject(decomposed, 10)},
		{"a project that is cut", fitBriefProject(long, 40)},
	} {
		if strings.Contains(tc.got, diaeresis) || strings.Contains(tc.got, acute) {
			t.Errorf("%s: the decomposed spelling reached the screen: %q", tc.name, tc.got)
		}
		if tc.got == "" {
			t.Errorf("%s: nothing came back, so this proves nothing", tc.name)
		}
	}
}

// importedStore builds a machine on the receiving end of `deja sync import`:
// three sessions, every one of them arrived from somewhere else.
func importedStore(t *testing.T) string {
	t.Helper()
	tmp := hermeticEnv(t)
	exp := filepath.Join(tmp, "transfer")
	if err := os.MkdirAll(exp, 0o755); err != nil {
		t.Fatal(err)
	}
	var batch []byte
	for i := range 3 {
		b, err := json.Marshal(index.SyncRecord{
			Harness: "claude", SessionID: "peer" + string(rune('1'+i)), Project: "tmp/projx",
			Role: "user", Text: "the kafka consumer rebalance keeps flapping, take " + string(rune('1'+i)),
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

func writePolicy(t *testing.T, body string) {
	t.Helper()
	p := filepath.Join(t.TempDir(), "policy.json")
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_POLICY_FILE", p)
}

// Someone who sets `auto: local-only` on a machine where everything arrived by
// sync gets silence from their agent, while search and the listing still
// answer — so the rule is the last thing they suspect. The first screen counted
// the sessions as the memory and said nothing about the rule; doctor had the
// number all along, five screens later (#1067).
func TestBriefSaysWhenThePolicyWithholdsEverything(t *testing.T) {
	dir := importedStore(t)
	writePolicy(t, `{"activations":{"auto":{"local":true,"imported":false}}}`)

	var buf bytes.Buffer
	if err := runBrief(dir, &buf); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if !strings.Contains(out, "all 3 sessions") || !strings.Contains(out, "keeps them out of every agent") {
		t.Errorf("brief does not say the rule withholds everything:\n%s", out)
	}
	// The number has to agree with doctor's, which is where the reader goes next.
	withheld, total := policyWithheldCounts(dir)
	if withheld["auto"] != 3 || total != 3 {
		t.Fatalf("fixture wrong: withheld=%d total=%d", withheld["auto"], total)
	}
}

// The line is about the rule emptying the agent's memory, not about having a
// rule: a store the rule lets through says nothing extra, and neither does one
// with no rule at all.
func TestBriefStaysQuietWhenThePolicyWithholdsNothing(t *testing.T) {
	dir := importedStore(t)

	var buf bytes.Buffer
	if err := runBrief(dir, &buf); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(buf.String(), "keeps them out of every agent") {
		t.Errorf("no policy file, yet the brief claims memory is withheld:\n%s", buf.String())
	}

	// A rule that names this import group allows it through again.
	writePolicy(t, `{"activations":{"auto":{"local":true,"imported":false,"imported:tmp":true}}}`)
	buf.Reset()
	if err := runBrief(dir, &buf); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(buf.String(), "keeps them out of every agent") {
		t.Errorf("rule withholds nothing, yet the brief says it does:\n%s", buf.String())
	}

	// Partial withholding stays quiet: the counters above are still broadly
	// true, and a caveat on every line is wallpaper.
	local := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-proj")
	if err := os.MkdirAll(local, 0o755); err != nil {
		t.Fatal(err)
	}
	rec := `{"type":"user","sessionId":"loc","cwd":"/proj","timestamp":"2026-07-11T10:00:00Z","message":{"role":"user","content":"local work on the ticker window"}}` + "\n"
	if err := os.WriteFile(filepath.Join(local, "loc.jsonl"), []byte(rec), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	writePolicy(t, `{"activations":{"auto":{"local":true,"imported":false}}}`)
	if withheld, total := policyWithheldCounts(dir); withheld["auto"] == 0 || withheld["auto"] == total {
		t.Fatalf("fixture is not partial: withheld=%d total=%d", withheld["auto"], total)
	}
	buf.Reset()
	if err := runBrief(dir, &buf); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(buf.String(), "keeps them out of every agent") {
		t.Errorf("partial withholding reported as total:\n%s", buf.String())
	}

	// A rule on the search path does not empty the agent's memory.
	writePolicy(t, `{"activations":{"search":{"local":true,"imported":false}}}`)
	buf.Reset()
	if err := runBrief(dir, &buf); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(buf.String(), "keeps them out of every agent") {
		t.Errorf("search-path rule reported as withholding from agents:\n%s", buf.String())
	}
}

// askedLine returns the "asked" row of the brief, or "" if there is none.
func askedLine(out string) string {
	for ln := range strings.SplitSeq(out, "\n") {
		if strings.HasPrefix(ln, "asked ") {
			return ln
		}
	}
	return ""
}

// A question asked locally and again by a peer whose session arrived by sync is
// still the same question asked twice — the one line the brief exists to show.
// Imported sessions used to carry no asked hashes, so the repeat crossed a sync
// boundary unseen; stats' RepeatQuestions (from records) counted it and the two
// screens disagreed. It stays gated: a machine that withholds imported memory
// from this screen must not surface an all-imported repeat. The rule is the
// reader's own — `search`, like the rest of the brief — since a rule aimed at
// agents leaves work the reader can still grep for themselves (#1312).
func TestBriefAskedTwiceCountsImportedAndHonoursPolicy(t *testing.T) {
	q := "how do I configure the retry queue for delivery?"

	// A local session asks it in March.
	tmp := hermeticEnv(t)
	local := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-proj")
	if err := os.MkdirAll(local, 0o755); err != nil {
		t.Fatal(err)
	}
	rec := `{"type":"user","sessionId":"locz","cwd":"/proj","timestamp":"2026-03-01T10:00:00Z","message":{"role":"user","content":"` + q + `"}}` + "\n"
	if err := os.WriteFile(filepath.Join(local, "l.jsonl"), []byte(rec), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRun(t, "index"); err != nil {
		t.Fatal(err)
	}

	// A peer asks the same thing in July; it arrives by sync.
	exp := filepath.Join(tmp, "transfer")
	if err := os.MkdirAll(exp, 0o755); err != nil {
		t.Fatal(err)
	}
	peer := `{"harness":"claude","session_id":"peerz","project":"proj","role":"user","text":"` + q + `","time":"2026-07-01T10:00:00Z"}` + "\n"
	if err := os.WriteFile(filepath.Join(exp, "deja-sync-p.jsonl"), []byte(peer), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := index.DefaultDir()
	if _, err := captureRun(t, "sync", "import", exp); err != nil {
		t.Fatal(err)
	}

	// Default policy (local+imported): the repeat surfaces.
	var buf bytes.Buffer
	if err := runBrief(dir, &buf); err != nil {
		t.Fatal(err)
	}
	if ln := askedLine(buf.String()); !strings.Contains(ln, "retry queue") {
		t.Fatalf("asked-twice did not count the imported repeat; asked line = %q\n%s", ln, buf.String())
	}

	// A rule that keeps imported memory off the reader's own screen must drop
	// the line: the only thing keeping the count at two is a session they chose
	// to hide. The whole brief reads `search` — a rule aimed at agents alone
	// leaves this screen intact, since the reader can still grep the same
	// session (#1312).
	writePolicy(t, `{"activations":{"search":{"local":true,"imported":false}}}`)
	buf.Reset()
	if err := runBrief(dir, &buf); err != nil {
		t.Fatal(err)
	}
	if ln := askedLine(buf.String()); ln != "" {
		t.Errorf("asked-twice surfaced an all-imported repeat under search:local-only:\n%s", ln)
	}
	writePolicy(t, `{"activations":{"auto":{"local":true,"imported":false}}}`)
	buf.Reset()
	if err := runBrief(dir, &buf); err != nil {
		t.Fatal(err)
	}
	if ln := askedLine(buf.String()); ln == "" {
		t.Errorf("a rule aimed at agents emptied the reader's own screen:\n%s", buf.String())
	}
}

// hitLine returns the "hit" (friction) row of the brief, or "" if there is none.
func hitLine(out string) string {
	for ln := range strings.SplitSeq(out, "\n") {
		if strings.HasPrefix(ln, "hit ") {
			return ln
		}
	}
	return ""
}

// A wall a peer kept hitting, arriving by sync, is a wall — `deja friction` and
// stats both count it, and the brief's one friction line used to be the lone
// holdout because imported sessions carried no Hit hashes. Populate meta.Hit on
// import and gate the brief's lookup on the search rule, the same shape as the
// asked-twice test above.
func TestBriefFrictionCountsImportedAndHonoursPolicy(t *testing.T) {
	tmp := hermeticEnv(t)
	exp := filepath.Join(tmp, "transfer")
	if err := os.MkdirAll(exp, 0o755); err != nil {
		t.Fatal(err)
	}
	var batch []byte
	for i := 1; i <= index.FrictionMinSessions; i++ {
		rec := `{"harness":"claude","session_id":"peer` + string(rune('0'+i)) + `","project":"svc","role":"tool-output","text":"npm ERR! code ELIFECYCLE","time":"2026-0` + string(rune('0'+i)) + `-01T10:00:00Z"}` + "\n"
		batch = append(batch, rec...)
	}
	if err := os.WriteFile(filepath.Join(exp, "deja-sync-p.jsonl"), batch, 0o644); err != nil {
		t.Fatal(err)
	}
	dir := index.DefaultDir()
	if _, err := captureRun(t, "sync", "import", exp); err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	if err := runBrief(dir, &buf); err != nil {
		t.Fatal(err)
	}
	if ln := hitLine(buf.String()); !strings.Contains(ln, "ELIFECYCLE") {
		t.Fatalf("friction line did not count the imported wall; hit line = %q\n%s", ln, buf.String())
	}

	writePolicy(t, `{"activations":{"search":{"local":true,"imported":false}}}`)
	buf.Reset()
	if err := runBrief(dir, &buf); err != nil {
		t.Fatal(err)
	}
	if ln := hitLine(buf.String()); ln != "" {
		t.Errorf("friction surfaced an all-imported wall under search:local-only:\n%s", ln)
	}
	// And a rule that only stops agents leaves the reader's own screen alone.
	writePolicy(t, `{"activations":{"auto":{"local":true,"imported":false}}}`)
	buf.Reset()
	if err := runBrief(dir, &buf); err != nil {
		t.Fatal(err)
	}
	if ln := hitLine(buf.String()); ln == "" {
		t.Errorf("a rule aimed at agents emptied the reader's own screen:\n%s", buf.String())
	}
}

// The brief's recent block names a project and the first line of a session.
// Search, `last`, blame and the status line all refuse a session a trust rule
// withholds; this screen named three of them.
func TestBriefRecentHonoursThePolicy(t *testing.T) {
	dir := importedStore(t)
	writePolicy(t, `{"activations":{"search":{"local":true,"imported":false},"auto":{"local":true,"imported":false}}}`)

	var buf bytes.Buffer
	if err := runBrief(dir, &buf); err != nil {
		t.Fatal(err)
	}
	if out := buf.String(); strings.Contains(out, "projx") || strings.Contains(out, "kafka") {
		t.Errorf("the brief names a session the policy withholds:\n%s", out)
	}
}

// With no rule in the way the block still fills: a fix that simply dropped it
// would pass the test above.
func TestBriefRecentStillNamesWhatIsAllowed(t *testing.T) {
	dir := importedStore(t)

	var buf bytes.Buffer
	if err := runBrief(dir, &buf); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if !strings.Contains(out, "recent") || !strings.Contains(out, "projx") {
		t.Errorf("the brief stopped naming recent sessions:\n%s", out)
	}
	if n := strings.Count(out, "projx"); n != 3 {
		t.Errorf("the brief names %d recent sessions, want 3:\n%s", n, out)
	}
}

// A rule that withholds only some sessions leaves the block full rather than
// short: the brief asks for more than it shows for exactly this reason.
func TestBriefRecentFillsAroundWithheldSessions(t *testing.T) {
	dir := importedStore(t)
	// A local session alongside the imported ones, newer than none of them:
	// the block should fill with it rather than come back short.
	store := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-proj")
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	line := `{"type":"user","message":{"role":"user","content":"the local scheduler question"},"timestamp":"2026-08-02T10:00:00Z","sessionId":"solo","cwd":"/proj"}` + "\n"
	if err := os.WriteFile(filepath.Join(store, "solo.jsonl"), []byte(line), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := index.Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	// The withheld one is the NEWEST, so a brief that asks for only as many as
	// it shows comes back short.
	writePolicy(t, `{"activations":{"search":{"local":false,"imported":true}}}`)

	var buf bytes.Buffer
	if err := runBrief(dir, &buf); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if strings.Contains(out, "scheduler") {
		t.Errorf("the brief names a withheld session:\n%s", out)
	}
	if n := strings.Count(out, "projx"); n != 3 {
		t.Errorf("the brief names %d recent sessions, want the block filled with the 3 allowed:\n%s", n, out)
	}
}

// The order anyone trying a new tool follows: run it once on an empty machine,
// work for a day, run it again. The first run leaves an empty index, and the
// second kept reporting a machine with no history while the sessions were on
// disk and the very next command indexed them (#1313).
func TestBriefSaysHistoryArrivedAfterAnEmptyFirstRun(t *testing.T) {
	hermeticEnv(t)
	dir := index.DefaultDir()
	var first bytes.Buffer
	if err := runBrief(dir, &first); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(first.String(), "no agent history found yet") {
		t.Fatalf("an empty machine did not get the empty-state screen:\n%s", first.String())
	}

	// A day of work lands in the store deja already knows about.
	proj := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-work")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	line := `{"type":"user","message":{"role":"user","content":"why does the retry queue stall"},"timestamp":"2026-08-02T10:00:00Z","sessionId":"aaa","cwd":"/work"}` + "\n"
	if err := os.WriteFile(filepath.Join(proj, "a.jsonl"), []byte(line), 0o644); err != nil {
		t.Fatal(err)
	}

	var second bytes.Buffer
	if err := runBrief(dir, &second); err != nil {
		t.Fatal(err)
	}
	out := second.String()
	if strings.Contains(out, "no agent history found yet") {
		t.Errorf("the screen still reports an empty machine after the store filled up:\n%s", out)
	}
	if !strings.Contains(out, "deja index") {
		t.Errorf("the screen does not say what to run:\n%s", out)
	}
}

// And it must not build, take the lock, or narrate: that rule is why the
// obvious fix was rejected. The store is left untouched by the brief, so the
// session count stays at zero until something else indexes it.
func TestBriefStillIndexesNothingWhenItSaysSo(t *testing.T) {
	hermeticEnv(t)
	dir := index.DefaultDir()
	var buf bytes.Buffer
	if err := runBrief(dir, &buf); err != nil {
		t.Fatal(err)
	}
	proj := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-work")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	line := `{"type":"user","message":{"role":"user","content":"why does the retry queue stall"},"timestamp":"2026-08-02T10:00:00Z","sessionId":"aaa","cwd":"/work"}` + "\n"
	if err := os.WriteFile(filepath.Join(proj, "a.jsonl"), []byte(line), 0o644); err != nil {
		t.Fatal(err)
	}
	buf.Reset()
	if err := runBrief(dir, &buf); err != nil {
		t.Fatal(err)
	}
	if ov, err := index.Overview(dir); err != nil || ov.Sessions != 0 {
		t.Errorf("the brief indexed on its own: %d sessions, err %v", ov.Sessions, err)
	}
	for _, narration := range []string{"incremental index", "indexing sessions into"} {
		if strings.Contains(buf.String(), narration) {
			t.Errorf("indexing narration landed on the brief:\n%s", buf.String())
		}
	}
}

// A machine that genuinely has nothing still gets the original screen — the
// one that explains what deja reads and where to look.
func TestBriefKeepsTheEmptyStateWhenNothingArrived(t *testing.T) {
	hermeticEnv(t)
	dir := index.DefaultDir()
	var buf bytes.Buffer
	if err := runBrief(dir, &buf); err != nil {
		t.Fatal(err)
	}
	buf.Reset()
	if err := runBrief(dir, &buf); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "no agent history found yet") {
		t.Errorf("a machine with no history was told to index something:\n%s", buf.String())
	}
}

// A machine that excludes every project has files in the stores that will
// never be indexed. Telling that reader to run `deja index` on every screen
// would be a nag they can do nothing about.
func TestBriefDoesNotNagAboutExcludedProjects(t *testing.T) {
	hermeticEnv(t)
	t.Setenv("DEJA_EXCLUDE_PROJECTS", "work")
	dir := index.DefaultDir()
	var buf bytes.Buffer
	if err := runBrief(dir, &buf); err != nil {
		t.Fatal(err)
	}
	proj := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-work")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	line := `{"type":"user","message":{"role":"user","content":"why does the retry queue stall"},"timestamp":"2026-08-02T10:00:00Z","sessionId":"aaa","cwd":"/work"}` + "\n"
	if err := os.WriteFile(filepath.Join(proj, "a.jsonl"), []byte(line), 0o644); err != nil {
		t.Fatal(err)
	}
	// Index once: the excluded work is read and produces nothing, which is the
	// state the reader is left in.
	if err := index.Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	buf.Reset()
	if err := runBrief(dir, &buf); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(buf.String(), "history found, not indexed yet") {
		t.Errorf("the screen asks for an index that would change nothing:\n%s", buf.String())
	}
}

func seedBriefIndex(t *testing.T) string {
	t.Helper()
	tmp := hermeticEnv(t)
	root := filepath.Join(tmp, "claude", "proj-a")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "claude"))
	body := `{"type":"user","sessionId":"b1","cwd":"/w/a","timestamp":"2026-07-20T10:00:00Z","message":{"role":"user","content":"jwks rotation cache stale problem"}}` + "\n"
	if err := os.WriteFile(filepath.Join(root, "b1.jsonl"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := index.DefaultDir()
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestBriefShowsMemoryAlive(t *testing.T) {
	dir := seedBriefIndex(t)
	usage.RecordDigest(dir, usage.KindRecall, strings.Repeat("x", 512), 1, 4096)
	usage.RecordDigest(dir, usage.KindDejaVu, "dv digest", 1, 2048)
	var out bytes.Buffer
	if err := runBrief(dir, &out); err != nil {
		t.Fatal(err)
	}
	s := out.String()
	// "session across" rather than "sessions across": the count is pluralised,
	// and a fixture with one session used to read "1 sessions across 1 agents".
	for _, want := range []string{"session", "across", "recent", "déjà vu moment", "deja log"} {
		if !strings.Contains(s, want) {
			t.Fatalf("brief missing %q:\n%s", want, s)
		}
	}
	if strings.Contains(s, "Usage:") {
		t.Fatal("brief fell back to usage text")
	}
}

func TestBriefFallsBackToUsageWithoutIndex(t *testing.T) {
	hermeticEnv(t)
	var out bytes.Buffer
	if err := runBrief(index.DefaultDir(), &out); err != nil {
		t.Fatal(err)
	}
	// printUsage writes to stdout, not our buffer — the contract is simply
	// that runBrief does not error and prints nothing misleading.
	if strings.Contains(out.String(), "sessions across") {
		t.Fatalf("brief invented an index: %q", out.String())
	}
}

func TestDejaVuCountersFlow(t *testing.T) {
	hermeticEnv(t)
	dir := index.DefaultDir()
	usage.RecordDigest(dir, usage.KindDejaVu, "digest", 2, 1024)
	usage.RecordDigest(dir, usage.KindHook, "start digest", 1, 512)
	if got := usage.DejaVuWeek(dir); got != 1 {
		t.Fatalf("DejaVuWeek = %d, want 1", got)
	}
	tot := usage.Totals(dir)
	if tot.DejaVuMoments != 1 || tot.Injections != 2 {
		t.Fatalf("totals = %+v", tot)
	}
}

func TestDejaVuLineShape(t *testing.T) {
	s := model.Session{Title: "jwks cache rotation broke login on the gateway and it hurt", Updated: time.Now().AddDate(0, 0, -21)}
	line := dejaVuLine(s)
	if !strings.Contains(line, "deja-vu — ") || !strings.Contains(line, "jwks") {
		t.Fatalf("dejaVuLine = %q", line)
	}
}

// A store whose work is all older than a week opened with two zero lines —
// "today 0 sessions" and "this week 0 sessions · 0 recalls" — which is the
// worst possible first screen for someone whose history is real but not recent.
func TestBriefReplacesAQuietWeekWithTheSpanItHolds(t *testing.T) {
	tmp := hermeticEnv(t)
	proj := filepath.Join(tmp, "claude", "proj")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	for i, day := range []string{"11", "13", "15"} {
		line := `{"type":"user","sessionId":"s` + day + `","cwd":"/w","timestamp":"2026-04-` + day +
			`T03:04:05Z","message":{"role":"user","content":"why does the pool exhaust under load ` +
			string(rune('a'+i)) + `?"}}`
		if err := os.WriteFile(filepath.Join(proj, "s"+day+".jsonl"), []byte(line+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	dir := index.DefaultDir()
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := runBrief(dir, &out); err != nil {
		t.Fatal(err)
	}
	// The brief renders the span in the reader's local timezone. Derive the
	// expected endpoints from the same servable overview it describes; a fixed
	// UTC date made this assertion fail west of Greenwich despite the correct
	// span being rendered.
	overview, err := index.OverviewServable(dir)
	if err != nil {
		t.Fatal(err)
	}
	got := out.String()
	if !strings.Contains(got, "covering") ||
		!strings.Contains(got, overview.Oldest.Local().Format("Jan 2 2006")) ||
		!strings.Contains(got, overview.Newest.Local().Format("Jan 2 2006")) {
		t.Fatalf("want the span the index holds:\n%s", got)
	}
	for _, unwanted := range []string{"today      0", "this week  0"} {
		if strings.Contains(got, unwanted) {
			t.Errorf("a zero line survived: %q\n%s", unwanted, got)
		}
	}
}

// The first screen names a wall and the count beside it; running the command
// it points at must produce the same number, or the tool reads as broken.
func TestBriefNamesTheWallAndAgreesWithTheCommand(t *testing.T) {
	tmp := hermeticEnv(t)
	root := filepath.Join(tmp, "claude", "proj-f")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "claude"))
	for i := range index.FrictionMinSessions {
		sid := fmt.Sprintf("wf%02d", i)
		body := `{"type":"user","sessionId":"` + sid + `","cwd":"/w/f","timestamp":"2026-07-2` +
			strconv.Itoa(i) + `T10:00:00Z","message":{"role":"user","content":"run the linters"}}` + "\n" +
			`{"type":"user","sessionId":"` + sid + `","cwd":"/w/f","timestamp":"2026-07-2` +
			strconv.Itoa(i) + `T10:05:00Z","message":{"role":"user","content":[{"type":"tool_result",` +
			`"content":"zsh:` + strconv.Itoa(i+1) + `: command not found: shellcheck"}]}}` + "\n"
		if err := os.WriteFile(filepath.Join(root, sid+".jsonl"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	dir := index.DefaultDir()
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}

	var brief bytes.Buffer
	if err := runBrief(dir, &brief); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(brief.String(), "command not found: shellcheck") {
		t.Fatalf("the wall is missing from the first screen:\n%s", brief.String())
	}
	want := fmt.Sprintf("%d sessions", index.FrictionMinSessions)
	if !strings.Contains(brief.String(), want) {
		t.Fatalf("want %q on the screen:\n%s", want, brief.String())
	}
	var cmd bytes.Buffer
	if err := runFriction(dir, nil, &cmd); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(cmd.String(), want) {
		t.Fatalf("the command disagrees with the screen:\n%s", cmd.String())
	}
}

// visibleWidth is what a terminal actually spends on a line: ANSI stripped and
// measured the way the renderer measures, in columns. Counting runes agreed
// with that for every ASCII fixture here and let a CJK line twice the
// terminal's width past the guards below (#2130).
func visibleWidth(line string) int { return termwidth.Columns(visibleText(line)) }

// briefStore writes three sessions whose ages decide how wide the date on the
// `recent` line is — "today" is five columns, "Jun 27 2025" is eleven.
func briefStore(t *testing.T, ages ...time.Duration) string {
	t.Helper()
	const project = "tmp/projc"
	tmp := hermeticEnv(t)
	root := filepath.Join(tmp, "claude", "-"+strings.ReplaceAll(project, "/", "-"))
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "claude"))
	for i, age := range ages {
		sid := string(rune('a'+i)) + "1b2c3d4"
		at := time.Now().Add(-age).UTC().Format(time.RFC3339)
		body := `{"type":"user","sessionId":"` + sid + `","cwd":"/` + project + `","timestamp":"` + at +
			`","message":{"role":"user","content":"we need to fix the kafka consumer rebalance that keeps flapping in staging, attempt ` + string(rune('1'+i)) + `"}}` + "\n"
		if err := os.WriteFile(filepath.Join(root, sid+".jsonl"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	dir := index.DefaultDir()
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	return dir
}

func recentLines(out string) []string {
	var got []string
	for l := range strings.SplitSeq(out, "\n") {
		if strings.Contains(l, "[claude]") {
			got = append(got, l)
		}
	}
	return got
}

// The `recent` lines were cut to a fixed 44-rune title regardless of what was
// already on the line, so they ran to 85–91 columns and wrapped on the 80-column
// terminal that is the common case — three orphan fragments, and the aligned
// layout gone (#1073).
func TestBriefRecentLinesFitEightyColumns(t *testing.T) {
	t.Setenv("COLUMNS", "")
	day := 24 * time.Hour
	for _, tc := range []struct {
		name string
		ages []time.Duration
	}{
		{"ordinary day", []time.Duration{time.Hour, 2 * time.Hour, 3 * time.Hour}},
		{"quiet week", []time.Duration{12 * day, 15 * day, 19 * day}},
		{"old store", []time.Duration{400 * day, 405 * day, 412 * day}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := briefStore(t, tc.ages...)
			var buf bytes.Buffer
			if err := runBrief(dir, &buf); err != nil {
				t.Fatal(err)
			}
			lines := recentLines(buf.String())
			if len(lines) == 0 {
				t.Fatalf("no recent lines:\n%s", buf.String())
			}
			for _, l := range lines {
				if w := visibleWidth(l); w > 80 {
					t.Errorf("recent line is %d columns wide, wraps on an 80-column terminal: %q", w, l)
				}
			}
		})
	}
}

// The old store is the case that made the raggedness visible: three lines of
// three different lengths, because the date in front of the title varies and
// the budget did not.
func TestBriefRecentLinesDoNotVaryWithTheDateWidth(t *testing.T) {
	t.Setenv("COLUMNS", "")
	day := 24 * time.Hour
	dir := briefStore(t, 400*day, 405*day, 412*day)
	var buf bytes.Buffer
	if err := runBrief(dir, &buf); err != nil {
		t.Fatal(err)
	}
	lines := recentLines(buf.String())
	if len(lines) < 2 {
		t.Fatalf("want several recent lines, got %d:\n%s", len(lines), buf.String())
	}
	first := visibleWidth(lines[0])
	for _, l := range lines[1:] {
		if w := visibleWidth(l); w != first {
			t.Errorf("recent lines are ragged: %d vs %d — the title budget still ignores the date\n%s", first, w, buf.String())
		}
	}
}

// COLUMNS is honoured when the reader exports it, and a narrow terminal must
// not cut the title away entirely.
func TestBriefRecentLinesHonourColumns(t *testing.T) {
	day := 24 * time.Hour
	dir := briefStore(t, 12*day, 15*day, 19*day)

	t.Setenv("COLUMNS", "60")
	var buf bytes.Buffer
	if err := runBrief(dir, &buf); err != nil {
		t.Fatal(err)
	}
	for _, l := range recentLines(buf.String()) {
		if w := visibleWidth(l); w > 60 {
			t.Errorf("COLUMNS=60: line is %d columns: %q", w, l)
		}
		// The floor keeps a readable fragment rather than an empty column.
		if i := strings.LastIndex(l, " · "); i < 0 || visibleWidth(l[i+3:]) < 12 {
			t.Errorf("COLUMNS=60 cut the title down to nothing: %q", l)
		}
	}

	// Narrow enough that the prefix alone fills the line: the budget goes
	// negative, and without a floor trimBriefTitleTo slices a rune array by a
	// negative length. The line overflows a 40-column terminal — a little
	// overflow beats a panic or an empty column.
	t.Setenv("COLUMNS", "40")
	buf.Reset()
	if err := runBrief(dir, &buf); err != nil {
		t.Fatal(err)
	}
	for _, l := range recentLines(buf.String()) {
		if i := strings.LastIndex(l, " · "); i < 0 || visibleWidth(l[i+3:]) < 12 {
			t.Errorf("COLUMNS=40 cut the title down to nothing: %q", l)
		}
	}

	// A wide terminal keeps the layout it always had: the 44-rune cap stands.
	t.Setenv("COLUMNS", "200")
	buf.Reset()
	if err := runBrief(dir, &buf); err != nil {
		t.Fatal(err)
	}
	for _, l := range recentLines(buf.String()) {
		if w := visibleWidth(l); w > 100 {
			t.Errorf("COLUMNS=200: title budget grew past the 44 cap: %d columns %q", w, l)
		}
	}

	// Nonsense values fall back to the 80-column target rather than to no bound.
	t.Setenv("COLUMNS", "not-a-number")
	buf.Reset()
	if err := runBrief(dir, &buf); err != nil {
		t.Fatal(err)
	}
	for _, l := range recentLines(buf.String()) {
		if w := visibleWidth(l); w > 80 {
			t.Errorf("COLUMNS=not-a-number: line is %d columns: %q", w, l)
		}
	}
}

// #604 budgeted the brief's transcript lines against the terminal, but deja's
// own copy was not budgeted: the wire line ran to 64 columns and wrapped
// `--auto` alone onto a second line of the first screen a fresh install shows
// (#1411). Every line of the brief fits a 60-column pane, this one included.
func TestBriefWireLineFitsSixtyColumns(t *testing.T) {
	day := 24 * time.Hour
	dir := briefStore(t, time.Hour, 2*day, 5*day)

	for _, cols := range []int{60, 80, 120} {
		t.Run(strconv.Itoa(cols), func(t *testing.T) {
			t.Setenv("COLUMNS", strconv.Itoa(cols))
			var buf bytes.Buffer
			if err := runBrief(dir, &buf); err != nil {
				t.Fatal(err)
			}
			var wire string
			for l := range strings.SplitSeq(buf.String(), "\n") {
				if strings.Contains(l, "install --auto") {
					wire = l
				}
			}
			if wire == "" {
				t.Fatalf("no wire line on an unwired store:\n%s", buf.String())
			}
			if w := visibleWidth(wire); w > 60 {
				t.Errorf("COLUMNS=%d: wire line is %d columns, wraps on a 60-column pane: %q", cols, w, wire)
			}
		})
	}
}

// The width guards on the recent block ask this helper how much of the
// terminal a line took. A CJK character is one rune and two columns, so a
// helper counting runes would let a line twice the terminal's width past the
// guards that exist to stop exactly that (#2130).
func TestVisibleWidthCountsColumnsNotRunes(t *testing.T) {
	for _, tc := range []struct {
		line string
		want int
	}{
		{"recent     [claude] tmp/projc · today · a title", 47},
		{"\x1b[2m[claude]\x1b[0m tmp/projc", 18},
		{"消费者重平衡", 12},
		{"recent     [claude] …者重平衡 · today", 37},
		// The numbers are written out rather than derived: a table that asks
		// the helper's own body what to expect agrees with it whatever it says.
	} {
		if got := visibleWidth(tc.line); got != tc.want {
			t.Errorf("visibleWidth(%q) = %d, want %d columns", tc.line, got, tc.want)
		}
	}
}
