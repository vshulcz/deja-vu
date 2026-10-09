package termwidth

import (
	"strings"
	"testing"
)

// An indent wider than the terminal still leaves twenty columns of text, so a
// narrow pane does not get one word per line.
func TestIndentKeepsTwentyColumnsUnderAWideIndent(t *testing.T) {
	got := Indent("one two three four five six seven eight", 24, strings.Repeat(" ", 20), strings.Repeat(" ", 20))
	first := strings.Split(got, "\n")[0]
	if Columns(strings.TrimLeft(first, " ")) < 15 {
		t.Errorf("first line kept too little text: %q", first)
	}
}

// Nothing but spaces has no words to wrap; it comes back as it was.
func TestWrapOfBlankTextIsTheText(t *testing.T) {
	if got := Wrap("     ", 2); len(got) != 1 || got[0] != "     " {
		t.Errorf("Wrap(blank) = %q", got)
	}
}

// A line indented with a tab is left alone: its column is the terminal's call.
func TestWrapTextLeavesTabIndentedLines(t *testing.T) {
	line := "\t" + strings.Repeat("word ", 30)
	if got := WrapText(line, 40); got != line {
		t.Errorf("tab-indented line was rewrapped:\n%q", got)
	}
}
