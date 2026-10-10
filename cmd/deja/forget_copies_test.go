package main

import (
	"strings"
	"testing"
)

// The line names the copy and the command that takes it, which is the whole
// session: the reader decides, since the fork's own work goes too.
func TestForgetCopiesLine(t *testing.T) {
	if got := forgetCopiesLine(nil); got != "" {
		t.Fatalf("no copies printed %q", got)
	}
	one := forgetCopiesLine([]string{"claude:s9"})
	for _, want := range []string{"1 other session still holds", "claude:s9", "`deja forget --session s9`", "with its own work"} {
		if !strings.Contains(one, want) {
			t.Errorf("one copy: %q lacks %q", one, want)
		}
	}
	two := forgetCopiesLine([]string{"claude:s1", "claude:s9"})
	if !strings.Contains(two, "2 other sessions still hold") || !strings.Contains(two, "--session <id>` drops each") {
		t.Errorf("two copies: %q", two)
	}
}
