package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/termwidth"
)

// visibleWidth is what a terminal actually spends on a line: ANSI stripped and
// measured the way the renderer measures, in columns. Counting runes agreed
// with that for every ASCII fixture here and let a CJK line twice the
// terminal's width past the guards below (#2130).
func visibleWidth(line string) int { return termwidth.Columns(visibleText(line)) }

// briefStore writes three sessions whose ages decide how wide the date on the
// `recent` line is — "today" is five columns, "Jun 27 2025" is eleven.
func briefStore(t *testing.T, project string, ages ...time.Duration) string {
	t.Helper()
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
	for _, l := range strings.Split(out, "\n") {
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
			dir := briefStore(t, "tmp/projc", tc.ages...)
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
	dir := briefStore(t, "tmp/projc", 400*day, 405*day, 412*day)
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
	dir := briefStore(t, "tmp/projc", 12*day, 15*day, 19*day)

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
	dir := briefStore(t, "tmp/projc", time.Hour, 2*day, 5*day)

	for _, cols := range []int{60, 80, 120} {
		t.Run(strconv.Itoa(cols), func(t *testing.T) {
			t.Setenv("COLUMNS", strconv.Itoa(cols))
			var buf bytes.Buffer
			if err := runBrief(dir, &buf); err != nil {
				t.Fatal(err)
			}
			var wire string
			for _, l := range strings.Split(buf.String(), "\n") {
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
