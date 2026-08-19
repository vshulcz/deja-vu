package search

import (
	"bytes"
	"github.com/vshulcz/deja-vu/internal/termwidth"
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/query"
)

func widthHit(project, snippet string) Hit {
	return Hit{
		Session: model.Session{
			Harness: "claude", Project: project, ID: "abc123def456",
			Updated: time.Date(2026, 8, 2, 10, 0, 0, 0, time.UTC),
		},
		Count:    3,
		Snippets: []string{snippet},
	}
}

// A 60-column pane is what a split editor gives you, and there every hit
// wrapped mid-word — on the first screen a new user sees (#604).
func TestHitsFitTheTerminal(t *testing.T) {
	long := "we decided the retry queue should back off with full jitter rather than a fixed delay, because the fixed one synchronised every worker"
	for _, width := range []int{40, 60, 80, 100} {
		var buf bytes.Buffer
		Print(&buf, []Hit{widthHit("app", long)}, query.Options{Width: width})
		for _, line := range strings.Split(strings.TrimRight(buf.String(), "\n"), "\n") {
			if strings.HasPrefix(line, "  ") && len([]rune(line)) > width {
				t.Errorf("width %d: a snippet ran to %d runes: %q", width, len([]rune(line)), line)
			}
		}
	}
}

// A pipe gets the whole line: a script reading deja wants the text, and cutting
// it there would lose data rather than layout.
func TestAPipeIsNotTruncated(t *testing.T) {
	long := strings.Repeat("alpha beta ", 30)
	var buf bytes.Buffer
	Print(&buf, []Hit{widthHit("app", long)}, query.Options{})
	if !strings.Contains(buf.String(), strings.TrimSpace(long)) {
		t.Errorf("piped output was cut:\n%s", buf.String())
	}
}

// The project column gives way first: cutting the line at its end takes the
// match count with it, and that is what a reader scans.
func TestTheProjectGivesWayBeforeTheCount(t *testing.T) {
	var buf bytes.Buffer
	Print(&buf, []Hit{widthHit("a-very-long-project-directory-name", "short")}, query.Options{Width: 60})
	line := strings.Split(buf.String(), "\n")[0]
	if !strings.Contains(line, "3 matches") {
		t.Errorf("the count was cut off the header: %q", line)
	}
	if !strings.Contains(line, "…") {
		t.Errorf("the project was not shortened: %q", line)
	}
	if !strings.Contains(line, "abc123") {
		t.Errorf("the id a reader copies is gone: %q", line)
	}
}

// And a wide terminal keeps everything.
func TestAWideTerminalCutsNothing(t *testing.T) {
	var buf bytes.Buffer
	Print(&buf, []Hit{widthHit("app", "the retry queue stalls")}, query.Options{Width: 200})
	if strings.Contains(buf.String(), "…") {
		t.Errorf("a wide terminal still elided:\n%s", buf.String())
	}
}

// A CJK line is one rune per two columns, so counting runes cut it to twice the
// terminal's width — worse than the wrap this exists to prevent, because the
// text is gone rather than reflowed.
func TestAWideScriptLineFitsToo(t *testing.T) {
	cjk := strings.Repeat("调度器的重试队列在预发环境卡住", 6)
	var buf bytes.Buffer
	Print(&buf, []Hit{widthHit("app", cjk)}, query.Options{Width: 60})
	for _, line := range strings.Split(strings.TrimRight(buf.String(), "\n"), "\n") {
		if !strings.HasPrefix(line, "  ") {
			continue
		}
		if got := termwidth.Columns(line); got > 60 {
			t.Errorf("a CJK snippet printed %d columns wide: %q", got, line)
		}
	}
}

// And the measure itself, since every budget above depends on it.
func TestColumnsCountsWideRunes(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want int
	}{
		{"abc", 3},
		{"调度", 4},
		{"a调b", 4},
		{"", 0},
	} {
		if got := termwidth.Columns(tc.in); got != tc.want {
			t.Errorf("termwidth.Columns(%q) = %d, want %d", tc.in, got, tc.want)
		}
	}
}
