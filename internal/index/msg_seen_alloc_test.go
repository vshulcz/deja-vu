package index

import (
	"runtime"
	"strings"
	"testing"
	"time"
)

// The rebuild's dedupe set holds an entry per message of the corpus, so its
// key has to stay a fixed size: a joined string with the time formatted and
// the hash in hex was a fresh allocation per message, 316 MB live at the peak
// of a 3.7M-message build.
func TestMsgSeenEntryIsFixedSize(t *testing.T) {
	const msgs = 2000
	at := time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)
	texts := make([]string, msgs)
	for i := range texts {
		texts[i] = strings.Repeat("x", 4096) + string(rune('a'+i%26)) + strings.Repeat("y", i)
	}
	key := "claude:" + strings.Repeat("0", 36)
	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	seen := msgSeen{}
	for i, text := range texts {
		if seen.dup(key, "user", at.Add(time.Duration(i)*time.Millisecond), text) {
			t.Fatalf("message %d reported as a repeat", i)
		}
	}
	runtime.ReadMemStats(&after)
	if !seen.dup(key, "user", at.Add(7*time.Millisecond), texts[7]) {
		t.Fatal("a repeated message was not caught")
	}
	if seen.dup(key, "user", at.Add(7*time.Millisecond+time.Nanosecond), texts[7]) {
		t.Fatal("the same text at another time was taken for a repeat")
	}
	if seen.dup(key, "user", at.Add(7*time.Millisecond).In(time.FixedZone("x", 3600)), texts[7]) == false {
		t.Fatal("the same instant in another zone was not taken for a repeat")
	}
	// The map itself, and nothing per entry beside it.
	perEntry := float64(after.Mallocs-before.Mallocs) / msgs
	if perEntry > 0.5 {
		t.Fatalf("%.2f allocations per message in the dedupe set; want the key stored inline", perEntry)
	}
}
