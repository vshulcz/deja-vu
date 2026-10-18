package search

import (
	"strings"
	"testing"
)

// The search that found a line treats ё and е as one letter, so the
// highlight marks the word in the spelling the line has.
func TestHighlightFoldsYo(t *testing.T) {
	cases := []struct{ line, q, want string }{
		{"отчет зеленый готов", "зелёный", "зеленый"},
		{"Отчёт зелёный готов", "зеленый отчет", "Отчёт"},
		{"всё ЗЕЛЁНОЕ", "все", "всё"},
	}
	for _, c := range cases {
		if got := highlight(c.line, c.q, false, true); !strings.Contains(got, cMatch+c.want+cReset) {
			t.Errorf("highlight(%q, %q) = %q, want %q marked", c.line, c.q, got, c.want)
		}
	}
}
