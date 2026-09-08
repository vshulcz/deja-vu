package main

import (
	"fmt"

	"github.com/vshulcz/deja-vu/internal/sources"
)

// harnessCount is how many agents deja reads: the registry minus deja's own
// notes source, which is the same rule the registry pages and the README count
// by. Written by hand in seven places, it stood at eighteen, twenty and
// twenty-two in different ones while harnesses landed (#3385).
func harnessCount() int {
	n := 0
	for _, h := range sources.Registry() {
		if h.Name == "deja" {
			continue
		}
		n++
	}
	return n
}

// spelledCount writes a small number the way the sentences around it do. Past
// the list it falls back to digits rather than inventing a word.
func spelledCount(n int) string {
	words := []string{"zero", "one", "two", "three", "four", "five", "six", "seven",
		"eight", "nine", "ten", "eleven", "twelve", "thirteen", "fourteen", "fifteen",
		"sixteen", "seventeen", "eighteen", "nineteen", "twenty", "twenty-one",
		"twenty-two", "twenty-three", "twenty-four", "twenty-five", "twenty-six",
		"twenty-seven", "twenty-eight", "twenty-nine", "thirty"}
	if n < 0 || n >= len(words) {
		return fmt.Sprint(n)
	}
	return words[n]
}
