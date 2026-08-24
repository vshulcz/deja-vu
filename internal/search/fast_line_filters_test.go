package search

import (
	"math/rand"
	"strings"
	"testing"
)

// The fast paths in front of lineNumberRE and toolDumpRE have to answer exactly
// what the engine would: a line the pattern drops and the fast path keeps is a
// tool dump printed into a digest, and the other way round loses a real line.
func TestFastLineFiltersMatchTheirPatterns(t *testing.T) {
	alphabet := []string{" ", "\t", "\r", "\v", "\f", "0", "5", "9", ":", "|", "a", "Z", "é", "٣", "tool_use", "TOOL_RESULT", "npm ERR!", "npm err!", "NPM Err!", "goroutine 5", "GOROUTINE 12", "goroutine x", "panic:", "PANIC:", "netcat", "<local-command", "12345:", "123456:", "1:", " 12| ", " "}
	r := rand.New(rand.NewSource(7))
	bad := 0
	for range 400000 {
		n := 1 + r.Intn(4)
		var b strings.Builder
		for range n {
			b.WriteString(alphabet[r.Intn(len(alphabet))])
		}
		line := b.String()
		if looksNumbered(line) != lineNumberRE.MatchString(line) {
			if bad < 5 {
				t.Errorf("numbered mismatch on %q: fast=%v regex=%v", line, looksNumbered(line), lineNumberRE.MatchString(line))
			}
			bad++
		}
		if looksToolDump(line) != toolDumpRE.MatchString(line) {
			if bad < 5 {
				t.Errorf("tooldump mismatch on %q: fast=%v regex=%v", line, looksToolDump(line), toolDumpRE.MatchString(line))
			}
			bad++
		}
	}
	if bad > 0 {
		t.Errorf("%d mismatches between the fast paths and their patterns", bad)
	}
}
